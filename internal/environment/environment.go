// Package environment resolve o ambiente de execução de um projeto.
//
// Ele responde as perguntas que tanto a CLI quanto o daemon precisam fazer:
// qual PHP este projeto usa, quais processos ele roda, em que porta, e com
// que Runner. Antes isso morava em internal/cli — o que funcionou enquanto a
// CLI era o único cliente, e deixou de funcionar quando o daemon apareceu:
// um servidor não deveria importar o pacote da linha de comando.
package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
	"github.com/AlexRogaleski/devmanager/internal/supervisor"
)

// Runtimes monta a cadeia de Providers na ordem de precedência.
//
// O Provider estático vem primeiro: em empate de versão, o PHP isolado ganha
// do PHP do sistema. Isolamento é o objetivo do projeto; o do sistema é a
// rede de segurança.
func Runtimes() *runtimes.Manager {
	var providers []runtimes.Provider

	if dir, err := paths.RuntimesDir(); err == nil {
		providers = append(providers, &runtimes.StaticProvider{
			Dir:     filepath.Join(dir, "php"),
			Variant: os.Getenv("DEVMANAGER_PHP_VARIANT"),
		})
	}
	// DEVMANAGER_PHP_DIRS aponta diretórios extras onde procurar PHP.
	//
	// Serve a dois propósitos: quem tem PHP num lugar fora do PATH (um
	// Homebrew desalinhado, um build próprio) consegue usá-lo sem gambiarra;
	// e os testes ficam herméticos, apontando para um binário falso em vez
	// de depender do que a máquina do CI tem instalado.
	providers = append(providers, &runtimes.SystemProvider{
		ExtraDirs: filepath.SplitList(os.Getenv("DEVMANAGER_PHP_DIRS")),
	})

	return runtimes.NewManager(providers...)
}

// NodeRuntimes monta a cadeia de Providers de Node.
//
// A ordem espelha a do PHP: o que o Dev Manager baixou vem primeiro, e o que
// já existe na máquina — nvm, fnm, volta, PATH — serve de rede de segurança.
// Diferente do PHP, porém, a rede de segurança aqui costuma bastar: quase
// todo desenvolvedor já tem um nvm com as versões que usa.
func NodeRuntimes() *runtimes.Manager {
	var providers []runtimes.Provider

	if dir, err := paths.RuntimesDir(); err == nil {
		providers = append(providers, &runtimes.NodeOficialProvider{
			Dir: filepath.Join(dir, "node"),
		})
	}
	providers = append(providers, &runtimes.NodeSystemProvider{
		ExtraDirs: filepath.SplitList(os.Getenv("DEVMANAGER_NODE_DIRS")),
	})

	return runtimes.NewManager(providers...)
}

// ResolverNode escolhe o Node do projeto.
//
// Sem exigência declarada, devolve ok=false em vez de escolher a mais nova.
// A diferença de postura em relação ao PHP é deliberada: o PHP é obrigatório
// para rodar um projeto Laravel, então um palpite razoável é melhor que
// falhar. O Node é opcional — muitos projetos não têm frontend — e impor uma
// versão a quem não pediu criaria um shim que sequestra o `npm` do sistema
// sem motivo.
func ResolverNode(ctx context.Context, p *project.Project) (runtimes.Runtime, bool) {
	exigencia, _ := p.NodeRequirement()
	if exigencia == "" {
		return runtimes.Runtime{}, false
	}

	c, err := semver.ParseConstraint(exigencia)
	if err != nil {
		return runtimes.Runtime{}, false
	}

	rt, err := NodeRuntimes().Resolve(ctx, "node", c)
	if err != nil {
		return runtimes.Runtime{}, false
	}
	return rt, true
}

// ResolverPHP escolhe o interpretador do projeto.
//
// Sem exigência declarada, cai para a versão mais nova disponível: é a
// escolha menos surpreendente, e o projeto pode fixar depois com
// `devm php use`.
func ResolverPHP(ctx context.Context, p *project.Project) (runtimes.Runtime, error) {
	exigencia, origem := p.PHPRequirement()

	c := semver.Any
	if exigencia != "" {
		parsed, err := semver.ParseConstraint(exigencia)
		if err != nil {
			return runtimes.Runtime{}, fmt.Errorf("versão de PHP inválida em %s: %w", origem, err)
		}
		c = parsed
	}
	return Runtimes().Resolve(ctx, "php", c)
}

// NovoRunner monta o executor do projeto, apontando para o shim persistente.
//
// O Node entra no mesmo shim quando o projeto declara uma versão. Sem
// declaração, não entra — e o `npm` do sistema continua valendo, que é o
// comportamento esperado por quem nunca pediu gerenciamento de Node.
func NovoRunner(p *project.Project, rt runtimes.Runtime) (*runner.Runner, error) {
	shim, err := paths.ShimDir(p.Path)
	if err != nil {
		return nil, err
	}

	r := &runner.Runner{Runtime: rt, Dir: p.Path, ShimDir: shim}
	if node, ok := ResolverNode(context.Background(), p); ok {
		r.Extras = append(r.Extras, node)
	}
	return r, nil
}

// Processos decide o que subir para este projeto.
//
// O devmanager.yaml, quando define processes, tem a palavra final: o projeto
// sabe melhor que a ferramenta o que precisa rodar. Sem ele, montamos um
// padrão a partir do que o projeto É — um Laravel com package.json quer
// servidor e bundler.
func Processos(p *project.Project, porta int) ([]supervisor.Processo, error) {
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

	if script, ok := ScriptDeFrontend(p.Path); ok {
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

// Filtrar restringe a lista aos processos pedidos.
func Filtrar(procs []supervisor.Processo, querido []string) ([]supervisor.Processo, error) {
	if len(querido) == 0 {
		return procs, nil
	}

	pedidos := make(map[string]bool, len(querido))
	for _, nome := range querido {
		pedidos[nome] = true
	}

	var saida []supervisor.Processo
	for _, p := range procs {
		if pedidos[p.Nome] {
			saida = append(saida, p)
			delete(pedidos, p.Nome)
		}
	}

	if len(pedidos) > 0 {
		faltando := make([]string, 0, len(pedidos))
		for nome := range pedidos {
			faltando = append(faltando, nome)
		}
		slices.Sort(faltando)
		return nil, fmt.Errorf("processo(s) não configurado(s): %v", faltando)
	}
	return saida, nil
}

// ScriptDeFrontend descobre o comando de desenvolvimento do package.json.
//
// Procuramos "dev" e, como reserva, "watch": as convenções do Laravel com
// Vite e do Laravel Mix antigo. Nada é assumido às cegas — se o projeto não
// declara nenhum dos dois, não inventamos um processo.
func ScriptDeFrontend(dirProjeto string) (string, bool) {
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

	gerenciador := GerenciadorDeNode(dirProjeto)
	for _, nome := range []string{"dev", "watch"} {
		if _, ok := pkg.Scripts[nome]; ok {
			return gerenciador + " run " + nome, true
		}
	}
	return "", false
}

// GerenciadorDeNode detecta o gerenciador de pacotes pelo arquivo de lock.
func GerenciadorDeNode(dirProjeto string) string {
	locks := []struct{ arquivo, comando string }{
		{"bun.lockb", "bun"},
		{"bun.lock", "bun"},
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"package-lock.json", "npm"},
	}
	for _, l := range locks {
		if _, err := os.Stat(filepath.Join(dirProjeto, l.arquivo)); err == nil {
			return l.comando
		}
	}
	return "npm"
}

// PortaLivre pede ao sistema uma porta disponível.
//
// Pedir a porta 0 faz o kernel escolher uma livre; lemos qual foi e liberamos.
// Há uma janela de corrida entre fechar e o processo real abrir, mas é a
// técnica padrão e o risco em desenvolvimento é desprezível — bem menor que
// o de fixar 8000 e colidir toda vez que dois projetos subirem juntos.
func PortaLivre() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("procurando uma porta livre: %w", err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

// TemServidor informa se algum processo é o servidor da aplicação.
func TemServidor(procs []supervisor.Processo) bool {
	for _, p := range procs {
		if strings.Contains(p.Linha, "artisan serve") {
			return true
		}
	}
	return false
}
