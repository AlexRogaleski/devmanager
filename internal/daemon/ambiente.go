package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
	"github.com/AlexRogaleski/devmanager/internal/supervisor"
)

// ambiente é um projeto em execução sob o daemon.
//
// O estado fica aqui em memória, e é a ÚNICA fonte de verdade sobre o que
// está rodando. Não persistimos isso em disco de propósito: um daemon que
// caiu não tem processos rodando, e um arquivo dizendo o contrário seria
// pior que nenhuma informação.
type ambiente struct {
	nome    string
	caminho string
	php     string
	porta   int
	dominio string

	// portasExtras são as portas nomeadas ({{port:vite}}) deste ambiente.
	// Ficam na API para que um cliente — a CLI hoje, uma interface gráfica
	// depois — possa mostrar onde cada processo atende.
	portasExtras map[string]int
	anel         *Anel
	desdeQue     time.Time

	// servicos são os serviços que o projeto declara. Guardados aqui para
	// responder "alguém ainda usa este contêiner?" quando outro ambiente cai.
	servicos []services.Spec

	cancelar  context.CancelFunc
	encerrado chan struct{} // fechado quando o supervisor retorna

	mu        sync.Mutex
	processos []Processo
	erro      string
}

// snapshot devolve a visão do ambiente para a API.
func (a *ambiente) snapshot() Ambiente {
	a.mu.Lock()
	defer a.mu.Unlock()

	procs := make([]Processo, len(a.processos))
	copy(procs, a.processos)

	return Ambiente{
		Projeto:      a.nome,
		PortasExtras: a.portasExtras,
		Caminho:      a.caminho,
		PHP:          a.php,
		Porta:        a.porta,
		Dominio:      a.dominio,
		Processos:    procs,
		DesdeQue:     a.desdeQue,
		Erro:         a.erro,
	}
}

// finalizar registra o resultado da execução.
func (a *ambiente) finalizar(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	estado := ProcParado
	if err != nil {
		estado = ProcFalhou
		a.erro = err.Error()
	}
	for i := range a.processos {
		a.processos[i].Estado = estado
	}
}

// subir monta e inicia um ambiente para o projeto.
//
// A função é longa porque o caminho é longo: achar o projeto, escolher o PHP,
// garantir os serviços, decidir os processos e supervisionar. Quebrá-la em
// pedaços pequenos espalharia uma sequência que só faz sentido inteira — e
// cada etapa precisa do resultado da anterior.
func (s *Servidor) subir(ctx context.Context, caminho string, nome string, pedido PedidoStart) (*ambiente, error) {
	p, err := project.Detect(caminho)
	if err != nil {
		return nil, err
	}

	rt, err := environment.ResolverPHP(ctx, p)
	if err != nil {
		return nil, err
	}

	r, err := environment.NovoRunner(p, rt)
	if err != nil {
		return nil, err
	}

	anel := NovoAnel(LinhasPadrao)

	// O anel recebe também as mensagens do próprio daemon sobre este
	// ambiente, sob o nome "devm": assim `devm logs` mostra "subindo
	// postgres:17" junto com a saída dos processos, na ordem em que
	// aconteceram.
	registrar := func(formato string, args ...any) {
		anel.Escrever("devm", fmt.Sprintf(formato, args...), time.Now())
	}

	specs, err := s.garantirServicos(ctx, p, r, anel, registrar)
	if err != nil {
		return nil, err
	}

	porta := pedido.Porta
	if porta == 0 {
		if porta, err = environment.PortaLivre(); err != nil {
			return nil, err
		}
	}

	exec, err := environment.Processos(p, porta)
	if err != nil {
		return nil, err
	}
	if exec, err = exec.Filtrar(pedido.Apenas); err != nil {
		return nil, err
	}
	if pedido.SemNode {
		exec = exec.SemFrontend()
	}

	ctxAmb, cancelar := context.WithCancel(context.Background())

	amb := &ambiente{
		nome:      nome,
		caminho:   p.Path,
		servicos:  specs,
		php:       rt.Version.String(),
		anel:      anel,
		desdeQue:  time.Now(),
		cancelar:  cancelar,
		encerrado: make(chan struct{}),
	}

	// A porta vem da Execucao, e não da variável local: um projeto que
	// declarou `processes` com --port fixo escuta onde ELE disse, e é essa
	// porta que o proxy precisa publicar.
	if exec.Porta != 0 {
		amb.porta = exec.Porta
		amb.dominio = p.Domain()
	}
	amb.portasExtras = exec.Extras

	amb.processos = make([]Processo, 0, len(exec.Processos))
	for _, proc := range exec.Processos {
		amb.processos = append(amb.processos, Processo{
			Nome:   proc.Nome,
			Linha:  proc.Linha,
			Estado: ProcRodando,
		})
	}

	sup := &supervisor.Supervisor{
		Processos: exec.Processos,
		Ambiente:  exec.Ambiente(),
		Runner:    r,
		// Saida fica nil: o daemon não tem terminal. SaidaDe manda cada
		// processo para o anel, com o nome preservado.
		SaidaDe: anel.Escritor,
	}

	// O supervisor bloqueia até algum processo terminar. Numa goroutine, o
	// pedido HTTP pode responder na hora e o ambiente continua vivo.
	go func() {
		defer close(amb.encerrado)
		defer cancelar()

		err := sup.Run(ctxAmb)
		amb.finalizar(err)

		// A rota sai da tabela quando o ambiente cai SOZINHO, não só no
		// stop explícito. Sem isso, o domínio continuaria anunciado e todo
		// acesso devolveria 502 — com o agravante de o `devm proxy status`
		// listar um domínio que não atende ninguém.
		if amb.dominio != "" {
			s.tabela.Remover(amb.dominio)
		}

		if err != nil {
			registrar("ambiente encerrado: %v", err)
		} else {
			registrar("ambiente encerrado")
		}
	}()

	registrar("ambiente iniciado com PHP %s", rt.Version)
	return amb, nil
}

// garantirServicos sobe os serviços declarados pelo projeto.
//
// Reaproveita o MESMO plano do `devm up`, filtrando os passos de serviço.
// Duplicar a lógica aqui faria o daemon e a CLI divergirem.
func (s *Servidor) garantirServicos(
	ctx context.Context,
	p *project.Project,
	ex prepare.Executor,
	anel *Anel,
	registrar func(string, ...any),
) ([]services.Spec, error) {
	specs, err := prepare.SpecsDoProjeto(p)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, nil
	}

	engine, err := services.DetectarConfigurado(ctx)
	if err != nil {
		// Sem engine, seguimos sem os serviços: o ambiente ainda tem valor
		// para um projeto que use SQLite, e o erro fica registrado no log
		// em vez de impedir o start.
		registrar("aviso: serviços não podem subir — %v", err)
		return nil, nil
	}

	m := &services.Manager{Engine: engine, Saida: anel.Escritor("devm")}

	// Quem já estava de pé antes de nós não é nosso para desligar depois.
	antes := containersRodando(ctx, m)

	pendentes := prepare.Pendentes(prepare.Plano(p, ex, prepare.Opcoes{
		Servicos: m,
		SemNode:  true,
		Saida:    anel.Escritor("devm"),
	}))

	for _, passo := range pendentes {
		if !ehPassoDeServico(passo.Nome) {
			continue
		}
		if passo.Bloqueado != "" {
			registrar("%s: %s", passo.Nome, passo.Bloqueado)
			continue
		}

		registrar("%s...", passo.Nome)
		if err := passo.Executar(ctx); err != nil {
			return nil, fmt.Errorf("%s: %w", passo.Nome, err)
		}
	}

	// Comparar o depois com o antes é mais exato que confiar no plano: um
	// passo pode ter sido pulado, e um contêiner pode ter subido por outro
	// caminho no meio do caminho.
	depois := containersRodando(ctx, m)
	for _, spec := range specs {
		container := spec.Container()
		if depois[container] && !antes[container] {
			s.registrarSubido(spec)
		}

		// Vale também para quem JÁ estava rodando: um contêiner criado por
		// uma versão antiga do devm continuaria voltando sozinho a cada
		// boot se nunca fosse parado, e é justamente esse que escaparia da
		// correção feita na criação.
		m.DesligarReinicioAutomatico(ctx, spec)
	}
	return specs, nil
}

func ehPassoDeServico(nome string) bool {
	const prefixo = "serviço "
	return len(nome) > len(prefixo) && nome[:len(prefixo)] == prefixo
}
