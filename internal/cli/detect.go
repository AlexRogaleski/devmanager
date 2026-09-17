package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// detectCmd implementa `devm detect [--json] [caminho]`.
//
// Cada subcomando ganha seu próprio FlagSet, em vez de usar o flag global do
// pacote. Assim `devm detect --json` e um futuro `devm up --json` podem ter
// flags com o mesmo nome e significados diferentes, sem colisão.
func detectCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	fs.SetOutput(w)

	// fs.Bool devolve um *bool: a flag só é preenchida quando fs.Parse roda.
	// Por isso lemos o valor com *comoJSON, e não comoJSON.
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	fs.Usage = func() {
		fmt.Fprint(w, "uso: devm detect [--json] [caminho]\n\n")
		fs.PrintDefaults()
	}

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	dir := "."
	if len(posicionais) > 0 {
		dir = posicionais[0]
	}

	p, err := project.Detect(dir)
	if err != nil {
		return err
	}

	if *comoJSON {
		// MarshalIndent produz JSON legível. O encoder respeita as tags
		// `json:"..."` declaradas no struct Project.
		saida, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	printProject(w, p, resolverPHP(p))
	return nil
}

// resolverPHP responde "qual PHP roda este projeto?".
//
// Repare que quem faz isso é a CAMADA DE CLI, não o pacote project. O detector
// continua sabendo só ler arquivos; casar a exigência com o que está instalado
// é outra responsabilidade. Manter essa fronteira é o que permite testar o
// detector sem PHP na máquina, e trocar a estratégia de runtime sem tocar nele.
//
// O retorno é (runtime, erro) empacotado num struct porque a CLI quer mostrar
// as duas coisas: ou o PHP escolhido, ou o motivo de não haver um.
func resolverPHP(p *project.Project) resultadoPHP {
	if p.PHPConstraint == "" {
		return resultadoPHP{}
	}

	c, err := semver.ParseConstraint(p.PHPConstraint)
	if err != nil {
		return resultadoPHP{Erro: err}
	}

	r, err := defaultManager().Resolve(context.Background(), "php", c)
	if err != nil {
		return resultadoPHP{Erro: err}
	}
	return resultadoPHP{Runtime: r, Achou: true}
}

type resultadoPHP struct {
	Runtime runtimes.Runtime
	Achou   bool
	Erro    error
}

// printProject escreve o relatório legível por humanos.
func printProject(w io.Writer, p *project.Project, php resultadoPHP) {
	fmt.Fprintf(w, "%s  (%s)\n", p.Name, p.Kind)
	fmt.Fprintf(w, "  %-10s %s\n", "Caminho", p.Path)

	if p.PHPConstraint != "" {
		linha := p.PHPConstraint
		switch {
		case php.Achou:
			linha = fmt.Sprintf("%s  →  %s  (%s)", p.PHPConstraint, php.Runtime.Version, php.Runtime.Bin)
		case php.Erro != nil:
			linha = fmt.Sprintf("%s  →  %s", p.PHPConstraint, php.Erro)
		}
		fmt.Fprintf(w, "  %-10s %s\n", "PHP", linha)
	}

	if p.LaravelRequire != "" || p.LaravelLocked != "" {
		versao := p.LaravelRequire
		if p.LaravelLocked != "" {
			versao = fmt.Sprintf("%s  (instalado: %s)", p.LaravelRequire, p.LaravelLocked)
		}
		fmt.Fprintf(w, "  %-10s %s\n", "Laravel", versao)
	}

	if p.IsLaravel() {
		fmt.Fprintf(w, "  %-10s http://%s\n", "Domínio", p.Domain())
	}

	if p.Kind == project.KindUnknown {
		fmt.Fprintf(w, "  %-10s nenhum projeto PHP encontrado nesta pasta\n", "Estado")
		return
	}

	if p.Ready() {
		fmt.Fprintf(w, "  %-10s pronto para rodar\n", "Estado")
		return
	}

	fmt.Fprintf(w, "  %-10s falta preparar\n", "Estado")
	for _, passo := range p.Missing() {
		fmt.Fprintf(w, "  %-10s → %s\n", "", passo)
	}
}
