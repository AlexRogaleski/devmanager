package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/registry"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// carregarRegistro abre o registro de projetos do usuário.
func carregarRegistro() (*registry.Registro, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return nil, err
	}
	return registry.Carregar(registry.Path(dir))
}

// addCmd registra um projeto no Dev Manager.
//
//	devm add                 registra o projeto da pasta atual
//	devm add ~/Projetos/api  registra outro caminho
//	devm add --name loja     registra com nome diferente do da pasta
func addCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(w)
	nome := fs.String("name", "", "nome do projeto (padrão: nome da pasta)")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	dir := "."
	if len(posicionais) > 0 {
		dir = posicionais[0]
	}

	// Find sobe na árvore: `devm add` de dentro de app/Models registra a raiz,
	// não a subpasta. Registrar uma subpasta criaria um projeto que nunca
	// funcionaria direito.
	p, err := project.Find(dir)
	if err != nil {
		return err
	}

	reg, err := carregarRegistro()
	if err != nil {
		return err
	}

	if err := reg.Adicionar(registry.Projeto{Nome: *nome, Caminho: p.Path}); err != nil {
		return err
	}
	if err := reg.Salvar(); err != nil {
		return err
	}

	registrado, _ := reg.BuscarPorCaminho(p.Path)
	fmt.Fprintf(w, "registrado: %s  (%s)\n", registrado.Nome, p.Path)
	fmt.Fprintf(w, "domínio futuro: http://%s.test\n", registrado.Nome)
	return nil
}

// removeCmd tira um projeto do registro, sem tocar no disco.
func removeCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm remove <nome>")
	}

	reg, err := carregarRegistro()
	if err != nil {
		return err
	}

	if !reg.Remover(posicionais[0]) {
		return fmt.Errorf("projeto %q não está registrado", posicionais[0])
	}
	if err := reg.Salvar(); err != nil {
		return err
	}

	fmt.Fprintf(w, "removido do registro: %s\n", posicionais[0])
	fmt.Fprintln(w, "(os arquivos do projeto não foram tocados)")
	return nil
}

// estadoProjeto é a visão de um projeto registrado, com o estado atual do disco.
//
// Nada disso é persistido: o registro guarda só nome e caminho, e tudo aqui é
// recalculado a cada execução. Estado derivado que é salvo fica velho.
type estadoProjeto struct {
	Nome       string `json:"name"`
	Caminho    string `json:"path"`
	Existe     bool   `json:"exists"`
	Tipo       string `json:"kind,omitempty"`
	PHP        string `json:"php,omitempty"`
	OrigemPHP  string `json:"php_origin,omitempty"`
	Pronto     bool   `json:"ready"`
	Pendencias int    `json:"pending_steps"`
	Dominio    string `json:"domain,omitempty"`
}

// listCmd mostra os projetos registrados e o estado de cada um.
func listCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	reg, err := carregarRegistro()
	if err != nil {
		return err
	}
	if len(reg.Projetos) == 0 {
		fmt.Fprintln(w, "nenhum projeto registrado")
		fmt.Fprintln(w, "\nuse `devm add` na pasta de um projeto, ou")
		fmt.Fprintln(w, "`devm scan ~/Projetos` para registrar vários de uma vez")
		return nil
	}

	// Uma única consulta de runtimes para todos os projetos.
	disponiveis, err := defaultManager().List(context.Background(), "php")
	if err != nil {
		disponiveis = nil // sem PHP algum: ainda dá para listar o resto
	}

	estados := make([]estadoProjeto, 0, len(reg.Projetos))
	for _, entrada := range reg.Projetos {
		estados = append(estados, avaliarProjeto(entrada, disponiveis))
	}

	if *comoJSON {
		saida, err := json.MarshalIndent(estados, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	imprimirTabela(w, estados)
	return nil
}

func avaliarProjeto(entrada registry.Projeto, disponiveis []runtimes.Runtime) estadoProjeto {
	e := estadoProjeto{
		Nome:    entrada.Nome,
		Caminho: entrada.Caminho,
		Dominio: entrada.Nome + ".test",
	}

	p, err := project.Detect(entrada.Caminho)
	if err != nil {
		// Pasta apagada ou movida: reportamos, não falhamos. Um projeto
		// ausente não pode impedir a listagem dos outros.
		return e
	}

	e.Existe = true
	e.Tipo = string(p.Kind)

	pendencias := resumoPendencias(p)
	e.Pendencias = len(pendencias)
	e.Pronto = len(pendencias) == 0

	exigencia, origem := p.PHPRequirement()
	e.OrigemPHP = string(origem)

	// Sem exigência declarada, o projeto usa a versão mais nova — que é o que
	// o `devm run` faria. A coluna precisa mostrar o que VAI acontecer, não
	// ficar vazia: um branco aqui pareceria "sem PHP", e não é o caso.
	c := semver.Any
	if exigencia != "" {
		parsed, err := semver.ParseConstraint(exigencia)
		if err != nil {
			e.PHP = "constraint inválida"
			return e
		}
		c = parsed
	}

	rt, ok := runtimes.Escolher(disponiveis, c)
	if !ok {
		e.PHP = exigencia + " (ausente)"
		return e
	}

	e.PHP = rt.Version.String()
	if origem == project.OrigemConfig {
		// Marca o que foi escolha explícita, para distinguir de resolução
		// automática numa olhada rápida.
		e.PHP += "*"
	}
	return e
}

func imprimirTabela(w io.Writer, estados []estadoProjeto) {
	// Calcula a largura de cada coluna a partir do conteúdo, para a tabela
	// não quebrar com nomes longos nem desperdiçar espaço com nomes curtos.
	larguraNome, larguraPHP := len("NOME"), len("PHP")
	for _, e := range estados {
		larguraNome = max(larguraNome, len(e.Nome))
		larguraPHP = max(larguraPHP, len(e.PHP))
	}

	fmt.Fprintf(w, "%-*s  %-*s  %-15s %s\n", larguraNome, "NOME", larguraPHP, "PHP", "ESTADO", "CAMINHO")
	for _, e := range estados {
		fmt.Fprintf(w, "%-*s  %-*s  %-15s %s\n",
			larguraNome, e.Nome, larguraPHP, e.PHP, descreverEstado(e), encurtarHome(e.Caminho))
	}

	for _, e := range estados {
		if strings.HasSuffix(e.PHP, "*") {
			fmt.Fprintf(w, "\n* versão fixada em %s\n", "devmanager.yaml")
			break
		}
	}
}

func descreverEstado(e estadoProjeto) string {
	switch {
	case !e.Existe:
		return "ausente"
	case e.Pronto:
		return "pronto"
	default:
		return fmt.Sprintf("%d %s", e.Pendencias, plural(e.Pendencias, "pendência", "pendências"))
	}
}

// encurtarHome troca o diretório home por ~, como os shells mostram.
func encurtarHome(caminho string) string {
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(caminho, home) {
		return caminho
	}
	return "~" + caminho[len(home):]
}

// scanCmd varre um diretório e registra todos os projetos encontrados.
//
// É o comando que torna a adoção viável: ninguém vai rodar `devm add` trinta
// vezes. A varredura não desce dentro de um projeto já encontrado — uma vez
// que há composer.json, o que estiver abaixo é conteúdo dele.
func scanCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(w)
	profundidade := fs.Int("depth", 3, "níveis de subdiretórios a percorrer")
	dryRun := fs.Bool("dry-run", false, "mostra o que seria registrado, sem gravar")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	raiz := "."
	if len(posicionais) > 0 {
		raiz = posicionais[0]
	}
	abs, err := filepath.Abs(raiz)
	if err != nil {
		return fmt.Errorf("resolvendo caminho %q: %w", raiz, err)
	}

	encontrados, err := procurarProjetos(abs, *profundidade)
	if err != nil {
		return err
	}
	if len(encontrados) == 0 {
		fmt.Fprintf(w, "nenhum projeto encontrado em %s (profundidade %d)\n", abs, *profundidade)
		return nil
	}

	reg, err := carregarRegistro()
	if err != nil {
		return err
	}

	var novos, jaTinha, conflitos int
	for _, caminho := range encontrados {
		err := reg.Adicionar(registry.Projeto{Caminho: caminho})

		switch e := err.(type) {
		case nil:
			novos++
			fmt.Fprintf(w, "  + %s\n", encurtarHome(caminho))
		case *registry.JaRegistradoError:
			jaTinha++
		case *registry.NomeEmUsoError:
			conflitos++
			fmt.Fprintf(w, "  ! %s — %s\n", encurtarHome(caminho), e.Error())
		default:
			fmt.Fprintf(w, "  ! %s — %v\n", encurtarHome(caminho), err)
		}
	}

	fmt.Fprintf(w, "\n%d %s, %d já %s", novos, plural(novos, "novo", "novos"),
		jaTinha, plural(jaTinha, "registrado", "registrados"))
	if conflitos > 0 {
		fmt.Fprintf(w, ", %d com nome em conflito", conflitos)
	}
	fmt.Fprintln(w)

	if *dryRun {
		fmt.Fprintln(w, "(simulação — rode sem --dry-run para gravar)")
		return nil
	}
	if novos == 0 {
		return nil
	}
	return reg.Salvar()
}

// procurarProjetos percorre a árvore procurando raízes de projeto.
func procurarProjetos(raiz string, profundidadeMax int) ([]string, error) {
	var encontrados []string

	var percorrer func(dir string, nivel int) error
	percorrer = func(dir string, nivel int) error {
		if ehProjeto(dir) {
			encontrados = append(encontrados, dir)
			return nil // não desce: o que está abaixo é conteúdo do projeto
		}
		if nivel >= profundidadeMax {
			return nil
		}

		entradas, err := os.ReadDir(dir)
		if err != nil {
			return nil // pasta sem permissão: pula em silêncio
		}

		for _, e := range entradas {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			// Pastas de dependências nunca contêm projetos nossos e são
			// enormes: descer nelas custaria segundos por nada.
			if e.Name() == "vendor" || e.Name() == "node_modules" {
				continue
			}
			if err := percorrer(filepath.Join(dir, e.Name()), nivel+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := percorrer(raiz, 0); err != nil {
		return nil, err
	}
	return encontrados, nil
}

func ehProjeto(dir string) bool {
	for _, marcador := range []string{"composer.json", "artisan", "devmanager.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, marcador)); err == nil {
			return true
		}
	}
	return false
}

// pruneCmd remove do registro os projetos cuja pasta não existe mais.
//
// É o companheiro do add: sem ele, mover ou apagar um projeto deixa uma
// entrada morta para sempre, e a lista vai acumulando ruído. Continua sem
// tocar em disco — remover do registro nunca apaga arquivos.
func pruneCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(w)
	dryRun := fs.Bool("dry-run", false, "mostra o que seria removido, sem gravar")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	reg, err := carregarRegistro()
	if err != nil {
		return err
	}

	var ausentes []registry.Projeto
	for _, p := range reg.Projetos {
		if _, err := os.Stat(p.Caminho); err != nil {
			ausentes = append(ausentes, p)
		}
	}

	if len(ausentes) == 0 {
		fmt.Fprintln(w, "nenhum projeto ausente no registro")
		return nil
	}

	for _, p := range ausentes {
		fmt.Fprintf(w, "  - %s  (%s)\n", p.Nome, encurtarHome(p.Caminho))
	}

	if *dryRun {
		fmt.Fprintf(w, "\n%d %s removido%s (simulação)\n",
			len(ausentes), plural(len(ausentes), "seria", "seriam"), plural(len(ausentes), "", "s"))
		return nil
	}

	for _, p := range ausentes {
		reg.Remover(p.Nome)
	}
	if err := reg.Salvar(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n%d %s do registro\n", len(ausentes), plural(len(ausentes), "removido", "removidos"))
	return nil
}

// plural escolhe a forma certa. Mensagem com "1 removidos" denuncia
// desatenção e é barata de evitar.
func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
