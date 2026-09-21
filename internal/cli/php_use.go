package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/ide"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// phpUseCmd fixa a versão de PHP do projeto no devmanager.yaml.
//
//	devm php use            mostra a versão atual e de onde ela vem
//	devm php use 8.3        fixa 8.3 (qualquer 8.3.x)
//	devm php use 8.3.15     fixa exatamente 8.3.15
//	devm php use --clear    remove a fixação, volta ao composer.json
//
// Existe porque detectar não basta. O fapcen declara "^8.2" no composer, mas
// se a produção roda 8.3, é no 8.3 que o desenvolvimento precisa rodar —
// e só o desenvolvedor sabe disso.
func phpUseCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("php use", flag.ContinueOnError)
	fs.SetOutput(w)
	limpar := fs.Bool("clear", false, "remove a fixação e volta a usar o composer.json")
	force := fs.Bool("force", false, "fixa mesmo sem a versão estar instalada")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	// Note que usamos localizarProjeto, e não ambienteDoProjeto: este comando
	// precisa funcionar mesmo quando a versão fixada não existe na máquina.
	p, err := localizarProjeto()
	if err != nil {
		return err
	}

	if *limpar {
		return limparFixacao(w, p)
	}
	if len(posicionais) == 0 {
		return mostrarVersaoAtual(w, p)
	}

	versao := posicionais[0]

	// Validar ANTES de gravar: um devmanager.yaml com constraint inválida
	// quebraria todo comando seguinte, e o erro apareceria longe da causa.
	c, err := semver.ParseConstraint(versao)
	if err != nil {
		return fmt.Errorf("versão inválida %q: %w", versao, err)
	}

	rt, errResolve := defaultManager().Resolve(context.Background(), "php", c)
	if errResolve != nil && !*force {
		return fmt.Errorf("%w\n  Use --force para fixar assim mesmo, ou instale a versão primeiro", errResolve)
	}

	if err := config.SetPHP(p.Path, versao); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: php fixado em %q\n", config.FileName, versao)

	if errResolve != nil {
		fmt.Fprintf(w, "\naviso: %v\n", errResolve)
		fmt.Fprintln(w, "os comandos do projeto vão falhar até essa versão existir")
		return nil
	}

	fmt.Fprintf(w, "usando  %s  (%s)\n", rt.Version, rt.Bin)

	// Reapontar o shim aqui é obrigatório, não conveniência: o editor e todo
	// processo filho passam por ele. Deixar desatualizado faria o terminal e
	// o editor discordarem sobre qual PHP está em uso.
	return sincronizarAmbiente(w, p, rt)
}

func mostrarVersaoAtual(w io.Writer, p *project.Project) error {
	exigencia, origem := p.PHPRequirement()

	if origem == project.OrigemNenhuma {
		fmt.Fprintln(w, "nenhuma versão de PHP exigida ou fixada neste projeto")
		fmt.Fprintln(w, "rode `devm php use <versão>` para fixar uma")
		return nil
	}

	fmt.Fprintf(w, "exigência  %s  (de %s)\n", exigencia, origem)

	c, err := semver.ParseConstraint(exigencia)
	if err != nil {
		return err
	}

	rt, err := defaultManager().Resolve(context.Background(), "php", c)
	if err != nil {
		fmt.Fprintf(w, "resolvida  %v\n", err)
		return nil
	}

	fmt.Fprintf(w, "resolvida  %s  (%s)\n", rt.Version, rt.Bin)
	if origem == project.OrigemComposer {
		fmt.Fprintf(w, "\nrode `devm php use <versão>` para fixar uma versão específica\n")
	}
	return nil
}

func limparFixacao(w io.Writer, p *project.Project) error {
	if !p.PHPPinned() {
		fmt.Fprintln(w, "não havia versão fixada neste projeto")
		return nil
	}

	if err := config.ClearPHP(p.Path); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: fixação removida\n", config.FileName)

	// Recarrega para ver a que exigência o projeto voltou.
	atualizado, err := project.Detect(p.Path)
	if err != nil {
		return err
	}
	return mostrarVersaoAtual(w, atualizado)
}

// sincronizarAmbiente reaponta o shim e reconfigura os editores JÁ configurados.
//
// Só mexemos em editor que já tinha configuração nossa: trocar a versão não é
// motivo para criar arquivos que o desenvolvedor nunca pediu.
func sincronizarAmbiente(w io.Writer, p *project.Project, rt runtimeInfo) error {
	shim, err := paths.ShimDir(p.Path)
	if err != nil {
		return err
	}
	if _, err := runner.EnsureShim(shim, p.PHPIni(), rt); err != nil {
		return err
	}

	cfg := ide.Config{
		ProjectPath: p.Path,
		PHPBin:      runner.PHPPath(shim),
		PHPVersion:  rt.Version,
	}

	for _, editor := range ide.All() {
		if !editor.Detected(p.Path) {
			continue
		}

		// Simula primeiro: se nada mudaria, o editor não estava configurado
		// com valores nossos e não é hora de criar nada.
		previa, err := editor.Configure(cfg, true)
		if err != nil || previa.NoChange {
			continue
		}
		if !jaConfigurado(previa.File) {
			continue
		}

		res, err := editor.Configure(cfg, false)
		if err != nil {
			fmt.Fprintf(w, "aviso: não consegui atualizar %s: %v\n", editor.Name(), err)
			continue
		}
		fmt.Fprintf(w, "%s atualizado (%s)\n", editor.Name(), res.File)
	}
	return nil
}
