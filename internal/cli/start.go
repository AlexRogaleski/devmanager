package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/daemon"
	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/supervisor"
)

// startCmd sobe os processos de desenvolvimento do projeto.
//
//	devm start                   sobe tudo
//	devm start --only serve      sobe só um processo
//	devm start --port 8080       força a porta do servidor
//	devm start --list            mostra o que subiria, sem subir
func startCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)

	semNode := fs.Bool("no-node", false, "não sobe os processos de frontend")
	apenas := fs.String("only", "", "sobe apenas estes processos (separados por vírgula)")
	porta := fs.Int("port", 0, "porta do servidor (padrão: uma porta livre)")
	listar := fs.Bool("list", false, "mostra os processos configurados e sai")
	destacar := fs.Bool("d", false, "sobe em segundo plano, via daemon")
	fs.BoolVar(destacar, "detach", false, "o mesmo que -d")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	if *destacar {
		return startDestacadoCmd(stdio, *porta, *apenas, *semNode)
	}

	p, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}
	r.Stdout, r.Stderr = stdio.Out, stdio.Err

	rt, err := resolverRuntime(p)
	if err != nil {
		return err
	}

	if *porta == 0 {
		*porta, err = environment.PortaLivre()
		if err != nil {
			return err
		}
	}

	processos, err := environment.Processos(p, *porta)
	if err != nil {
		return err
	}

	if *apenas != "" {
		processos, err = environment.Filtrar(processos, strings.Split(*apenas, ","))
		if err != nil {
			return err
		}
	}

	ctx := context.Background()
	w := stdio.Out

	fmt.Fprintf(w, "%s  (%s)\n", p.Name, p.Kind)
	fmt.Fprintf(w, "PHP     %s\n\n", rt.Version)

	if *listar {
		for _, proc := range processos {
			fmt.Fprintf(w, "  %-8s %s\n", proc.Nome, proc.Linha)
		}
		return nil
	}

	// Os serviços sobem ANTES dos processos: um artisan serve que inicia sem
	// o banco de pé falha na primeira requisição, e o erro apareceria como
	// problema da aplicação.
	if err := garantirServicos(ctx, w, p, r); err != nil {
		return err
	}

	if environment.TemServidor(processos) {
		fmt.Fprintf(w, "servidor em http://127.0.0.1:%d\n\n", *porta)
	}

	s := &supervisor.Supervisor{
		Processos: processos,
		Runner:    r,
		Saida:     w,
		Cores:     ehTerminal(w),
	}

	err = s.Run(ctx)

	// Um processo ter terminado não é falha da ferramenta: o servidor pode
	// ter caído por um erro no código do usuário. Reportamos e saímos com 1,
	// sem o prefixo "devm:" que sugeriria problema no Dev Manager.
	var terminou *supervisor.ProcessoTerminouError
	if errors.As(err, &terminou) {
		return &sairComCodigo{codigo: 1}
	}
	return err
}

// garantirServicos sobe os serviços declarados pelo projeto.
//
// Reaproveita o MESMO plano do `devm up`, filtrando só os passos de serviço.
// Duplicar a lógica aqui faria o start e o up divergirem com o tempo — foi
// para evitar exatamente isso que o pacote prepare existe.
func garantirServicos(ctx context.Context, w io.Writer, p *project.Project, ex prepare.Executor) error {
	m := servicosDoProjeto(ctx, w, p)
	if m == nil {
		return nil
	}

	pendentes := prepare.Pendentes(prepare.Plano(p, ex, prepare.Opcoes{
		Servicos: m,
		SemNode:  true,
		Saida:    w,
	}))

	var deServico []prepare.Passo
	for _, passo := range pendentes {
		if strings.HasPrefix(passo.Nome, "serviço ") {
			deServico = append(deServico, passo)
		}
	}
	if len(deServico) == 0 {
		return nil
	}

	for _, passo := range deServico {
		fmt.Fprintf(w, "%s...\n", passo.Nome)
		if err := passo.Executar(ctx); err != nil {
			return fmt.Errorf("%s: %w", passo.Nome, err)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// sairComCodigo encerra com um código específico, sem imprimir mensagem.
//
// Usado quando o comando já explicou o que houve na própria saída — o caso do
// start, em que os logs do processo que caiu já foram para o terminal. Um
// "devm: ..." por cima seria ruído duplicado.
type sairComCodigo struct{ codigo int }

func (e *sairComCodigo) Error() string { return fmt.Sprintf("saída com código %d", e.codigo) }
func (e *sairComCodigo) Code() int     { return e.codigo }

// startDestacadoCmd sobe o ambiente no daemon e devolve o terminal.
//
// A diferença entre isto e o start em primeiro plano é só ONDE os processos
// vivem: aqui eles pertencem ao daemon, que sobrevive ao fechamento do
// terminal. O preparo — PHP, serviços, escolha de processos — é idêntico,
// porque os dois caminhos usam o mesmo pacote environment.
func startDestacadoCmd(stdio IO, porta int, apenas string, semNode bool) error {
	w := stdio.Out

	p, err := localizarProjeto()
	if err != nil {
		return err
	}

	nome, err := registrarSeNecessario(w, p)
	if err != nil {
		return err
	}

	c, err := garantirDaemon(w)
	if err != nil {
		return err
	}

	pedido := daemon.PedidoStart{Porta: porta, SemNode: semNode}
	if apenas != "" {
		pedido.Apenas = strings.Split(apenas, ",")
	}

	amb, err := c.Start(context.Background(), nome, pedido)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n%s rodando em segundo plano\n", amb.Projeto)
	fmt.Fprintf(w, "  PHP        %s\n", amb.PHP)
	for _, proc := range amb.Processos {
		fmt.Fprintf(w, "  %-10s %s\n", proc.Nome, proc.Linha)
	}
	if amb.Porta != 0 {
		fmt.Fprintf(w, "\n  http://127.0.0.1:%d\n", amb.Porta)
	}
	fmt.Fprintf(w, "\n  devm logs %s -f   acompanha os logs\n", amb.Projeto)
	fmt.Fprintf(w, "  devm stop %s      derruba\n", amb.Projeto)
	return nil
}
