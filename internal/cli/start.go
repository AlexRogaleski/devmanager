package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/config"
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

	apenas := fs.String("only", "", "sobe apenas estes processos (separados por vírgula)")
	porta := fs.Int("port", 0, "porta do servidor (padrão: uma porta livre)")
	listar := fs.Bool("list", false, "mostra os processos configurados e sai")

	if _, err := parseArgs(fs, args); err != nil {
		return err
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
		*porta, err = portaLivre()
		if err != nil {
			return err
		}
	}

	processos, err := montarProcessos(p, *porta)
	if err != nil {
		return err
	}

	if *apenas != "" {
		processos, err = filtrar(processos, *apenas)
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

	if temServidor(processos) {
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

// montarProcessos decide o que subir.
//
// O devmanager.yaml, quando define processes, tem a palavra final: o projeto
// sabe melhor que a ferramenta o que precisa rodar. Sem ele, montamos um
// padrão a partir do que o projeto É — um Laravel com package.json quer
// servidor e bundler.
func montarProcessos(p *project.Project, porta int) ([]supervisor.Processo, error) {
	if p.Config != nil && len(p.Config.Processes) > 0 {
		nomes := make([]string, 0, len(p.Config.Processes))
		for nome := range p.Config.Processes {
			nomes = append(nomes, nome)
		}
		// Ordem de iteração de map em Go é ALEATÓRIA de propósito, para
		// impedir que alguém dependa dela. Ordenamos para que a saída seja
		// a mesma a cada execução.
		slices.Sort(nomes)

		procs := make([]supervisor.Processo, 0, len(nomes))
		for _, nome := range nomes {
			procs = append(procs, supervisor.Processo{Nome: nome, Linha: p.Config.Processes[nome]})
		}
		return procs, nil
	}

	var procs []supervisor.Processo

	if p.IsLaravel() {
		procs = append(procs, supervisor.Processo{
			Nome:  "serve",
			Linha: fmt.Sprintf("php artisan serve --host=127.0.0.1 --port=%d", porta),
		})
	}

	if script, ok := scriptDeFrontend(p.Path); ok {
		procs = append(procs, supervisor.Processo{Nome: "vite", Linha: script})
	}

	if len(procs) == 0 {
		return nil, fmt.Errorf(
			"nenhum processo para rodar neste projeto\n"+
				"  declare processes no %s, por exemplo:\n\n"+
				"  processes:\n    serve: php artisan serve\n    queue: php artisan queue:work\n",
			config.FileName)
	}
	return procs, nil
}

// scriptDeFrontend descobre o comando de desenvolvimento do package.json.
//
// Procuramos o script "dev" e, como reserva, "watch": são as convenções do
// Laravel com Vite e do Laravel Mix antigo. Nada é assumido às cegas — se o
// projeto não declara nenhum dos dois, não inventamos um processo.
func scriptDeFrontend(dirProjeto string) (string, bool) {
	dados, err := os.ReadFile(filepath.Join(dirProjeto, "package.json"))
	if err != nil {
		return "", false
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(dados, &pkg); err != nil {
		return "", false
	}

	gerenciador := gerenciadorDeNode(dirProjeto)
	for _, nome := range []string{"dev", "watch"} {
		if _, ok := pkg.Scripts[nome]; ok {
			return gerenciador + " run " + nome, true
		}
	}
	return "", false
}

func gerenciadorDeNode(dirProjeto string) string {
	locks := map[string]string{
		"bun.lockb":         "bun",
		"bun.lock":          "bun",
		"pnpm-lock.yaml":    "pnpm",
		"yarn.lock":         "yarn",
		"package-lock.json": "npm",
	}
	for lock, gerenciador := range locks {
		if _, err := os.Stat(filepath.Join(dirProjeto, lock)); err == nil {
			return gerenciador
		}
	}
	return "npm"
}

func filtrar(procs []supervisor.Processo, lista string) ([]supervisor.Processo, error) {
	querido := map[string]bool{}
	for _, nome := range strings.Split(lista, ",") {
		querido[strings.TrimSpace(nome)] = true
	}

	var saida []supervisor.Processo
	for _, p := range procs {
		if querido[p.Nome] {
			saida = append(saida, p)
			delete(querido, p.Nome)
		}
	}

	if len(querido) > 0 {
		faltando := make([]string, 0, len(querido))
		for nome := range querido {
			faltando = append(faltando, nome)
		}
		slices.Sort(faltando)
		return nil, fmt.Errorf("processo(s) não configurado(s): %s", strings.Join(faltando, ", "))
	}
	return saida, nil
}

func temServidor(procs []supervisor.Processo) bool {
	for _, p := range procs {
		if strings.Contains(p.Linha, "artisan serve") {
			return true
		}
	}
	return false
}

// portaLivre pede ao sistema uma porta disponível.
//
// Pedir a porta 0 faz o kernel escolher uma livre; lemos qual foi e liberamos.
// Há uma janela de corrida entre fechar e o processo real abrir, mas é a
// técnica padrão e o risco em desenvolvimento é desprezível — bem menor que
// o de fixar 8000 e colidir toda vez que dois projetos subirem juntos.
func portaLivre() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("procurando uma porta livre: %w", err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

// sairComCodigo encerra com um código específico, sem imprimir mensagem.
type sairComCodigo struct{ codigo int }

func (e *sairComCodigo) Error() string { return fmt.Sprintf("saída com código %d", e.codigo) }
func (e *sairComCodigo) Code() int     { return e.codigo }
