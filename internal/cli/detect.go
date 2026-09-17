package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/project"
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

	printProject(w, p)
	return nil
}

// printProject escreve o relatório legível por humanos.
func printProject(w io.Writer, p *project.Project) {
	fmt.Fprintf(w, "%s  (%s)\n", p.Name, p.Kind)
	fmt.Fprintf(w, "  %-10s %s\n", "Caminho", p.Path)

	if p.PHPConstraint != "" {
		fmt.Fprintf(w, "  %-10s %s\n", "PHP", p.PHPConstraint)
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
