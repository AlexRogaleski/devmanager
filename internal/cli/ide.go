package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/ide"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/runner"
)

// ideCmd configura os editores do projeto para usarem o PHP correto.
//
//	devm ide            mostra o que seria alterado, sem gravar
//	devm ide apply      grava
func ideCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("ide", flag.ContinueOnError)
	fs.SetOutput(w)
	force := fs.Bool("force", false, "regrava settings.json ilegível, após backup")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	aplicar := len(posicionais) > 0 && (posicionais[0] == "apply" || posicionais[0] == "write")

	p, _, err := ambienteDoProjeto()
	if err != nil {
		return err
	}

	rt, err := resolverRuntime(p)
	if err != nil {
		return err
	}

	shim, err := paths.ShimDir(p.Path)
	if err != nil {
		return err
	}

	// Garante que o shim existe ANTES de gravar o caminho no settings.json:
	// apontar o editor para um link inexistente seria pior que não configurar.
	if _, err := runner.EnsureShim(shim, p.PHPIni(), rt); err != nil {
		return err
	}

	cfg := ide.Config{
		ProjectPath: p.Path,
		PHPBin:      runner.PHPPath(shim),
		PHPVersion:  rt.Version,
		Force:       *force,
	}

	fmt.Fprintf(w, "Projeto  %s\n", p.Name)
	fmt.Fprintf(w, "PHP      %s via %s\n\n", rt.Version, cfg.PHPBin)

	algumDetectado := false
	for _, editor := range ide.All() {
		if !editor.Detected(p.Path) {
			continue
		}
		algumDetectado = true

		res, err := editor.Configure(cfg, !aplicar)
		if err != nil {
			return err
		}

		fmt.Fprintf(w, "%s → %s\n", editor.Name(), res.File)
		switch {
		case res.NoChange:
			fmt.Fprintln(w, "  já está configurado")
		default:
			for chave, valor := range res.Changes {
				fmt.Fprintf(w, "  %s = %s\n", chave, valor)
			}
			if res.Backup != "" {
				fmt.Fprintf(w, "  backup do arquivo anterior em %s\n", res.Backup)
			}
		}
	}

	if !algumDetectado {
		fmt.Fprintln(w, "nenhum editor suportado detectado neste projeto")
		return nil
	}
	if !aplicar {
		fmt.Fprintln(w, "\n(simulação — rode `devm ide apply` para gravar)")
	}
	return nil
}
