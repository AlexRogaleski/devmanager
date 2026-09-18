package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// nodeCmd despacha os subcomandos de `devm node`.
func nodeCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(stdio.Out, "uso: devm node <list|available|install|remove|use|which> [argumentos]\n")
		return nil
	}

	w := stdio.Out
	switch args[0] {
	case "list", "ls":
		return nodeListCmd(w, args[1:])
	case "available", "avail":
		return nodeAvailableCmd(w, args[1:])
	case "install":
		return nodeInstallCmd(w, args[1:])
	case "remove", "rm", "uninstall":
		return nodeRemoveCmd(w, args[1:])
	case "use":
		return nodeUseCmd(w, args[1:])
	case "which":
		return nodeWhichCmd(w, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: node %q", args[0])
	}
}

func nodeOficial() (*runtimes.NodeOficialProvider, error) {
	dir, err := paths.RuntimesDir()
	if err != nil {
		return nil, err
	}
	return &runtimes.NodeOficialProvider{Dir: filepath.Join(dir, "node")}, nil
}

func nodeListCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("node list", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	encontrados, err := environment.NodeRuntimes().List(context.Background(), "node")
	if err != nil {
		return err
	}

	if *comoJSON {
		saida, err := json.MarshalIndent(encontrados, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	if len(encontrados) == 0 {
		fmt.Fprintln(w, "nenhum Node encontrado")
		fmt.Fprintln(w, "\ninstale um com `devm node install 22`, ou use o nvm que você já tem")
		return nil
	}

	fmt.Fprintf(w, "%-12s %-12s %-8s %s\n", "VERSÃO", "ORIGEM", "TAMANHO", "CAMINHO")
	for _, r := range encontrados {
		fmt.Fprintf(w, "%-12s %-12s %-8s %s\n", r.Version, r.Source, tamanhoDoRuntime(r), r.Bin)
	}
	return nil
}

// nodeRemoveCmd apaga um Node baixado pelo Dev Manager.
//
// Só os nossos: um Node do nvm aparece no `devm node list`, mas quem o
// instalou foi o nvm, e é lá que ele deve ser removido. O NodeSystemProvider
// não implementa Removedor justamente para isso não ser possível.
func nodeRemoveCmd(w io.Writer, args []string) error {
	p, err := nodeOficial()
	if err != nil {
		return err
	}
	return removerRuntime(w, remocao{
		Lingua:   "node",
		Comando:  "devm node remove",
		Provider: p,
		Todos: func(ctx context.Context) ([]runtimes.Runtime, error) {
			return environment.NodeRuntimes().List(ctx, "node")
		},
		Exigencia: func(p *project.Project) (string, project.Origem) {
			return p.NodeRequirement()
		},
	}, args)
}

func nodeAvailableCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("node available", flag.ContinueOnError)
	fs.SetOutput(w)
	todas := fs.Bool("all", false, "lista todos os patches, não só o mais recente de cada série")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	p, err := nodeOficial()
	if err != nil {
		return err
	}

	versoes, err := p.Installable(context.Background())
	if err != nil {
		return err
	}
	if len(versoes) == 0 {
		fmt.Fprintln(w, "nenhuma versão disponível para esta plataforma")
		return nil
	}

	if *todas {
		for _, v := range versoes {
			fmt.Fprintln(w, v)
		}
		return nil
	}

	// A lista traz centenas de versões, incluindo as antigas demais para
	// interessar. Mostramos a mais nova de cada série maior, e só das
	// séries ainda em uso.
	vistas := map[int]bool{}
	fmt.Fprintf(w, "%-10s %s\n", "SÉRIE", "MAIS RECENTE")
	for _, v := range versoes {
		if vistas[v.Major] || v.Major < 18 {
			continue
		}
		vistas[v.Major] = true
		fmt.Fprintf(w, "%-10d %s\n", v.Major, v)
	}
	fmt.Fprintln(w, "\nuse --all para ver todas")
	return nil
}

func nodeInstallCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("node install", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm node install <versão>   (ex.: devm node install 22)")
	}

	c, err := semver.ParseConstraint(posicionais[0])
	if err != nil {
		return fmt.Errorf("versão inválida %q: %w", posicionais[0], err)
	}

	p, err := nodeOficial()
	if err != nil {
		return err
	}
	ctx := context.Background()

	fmt.Fprintln(w, "consultando versões disponíveis...")
	disponiveis, err := p.Installable(ctx)
	if err != nil {
		return err
	}

	alvo, ok := c.Best(disponiveis)
	if !ok {
		return fmt.Errorf("nenhuma versão disponível satisfaz %q (rode `devm node available`)", posicionais[0])
	}

	fmt.Fprintf(w, "instalando Node %s\n", alvo)

	rt, err := p.Install(ctx, alvo, progressoNoTerminal(w))
	if err != nil {
		return err
	}

	if ehTerminal(w) {
		fmt.Fprint(w, "\r", "                                                  ", "\r")
	}
	fmt.Fprintf(w, "instalado em %s\n", rt.Bin)

	nomes := make([]string, 0, len(rt.Comandos))
	for nome := range rt.Comandos {
		nomes = append(nomes, nome)
	}
	fmt.Fprintf(w, "comandos disponíveis: %v\n", nomes)
	return nil
}

func nodeUseCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("node use", flag.ContinueOnError)
	fs.SetOutput(w)
	limpar := fs.Bool("clear", false, "remove a fixação e volta ao .nvmrc ou package.json")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	p, err := localizarProjeto()
	if err != nil {
		return err
	}

	if *limpar {
		if err := config.SetChave(p.Path, "node", ""); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: fixação de Node removida\n", config.FileName)
		return nil
	}

	if len(posicionais) == 0 {
		return mostrarNodeAtual(w, p)
	}

	versao := posicionais[0]
	if _, err := semver.ParseConstraint(versao); err != nil {
		return fmt.Errorf("versão inválida %q: %w", versao, err)
	}

	if err := config.SetChave(p.Path, "node", versao); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: node fixado em %q\n", config.FileName, versao)

	atualizado, err := project.Detect(p.Path)
	if err != nil {
		return err
	}
	return mostrarNodeAtual(w, atualizado)
}

func mostrarNodeAtual(w io.Writer, p *project.Project) error {
	exigencia, origem := p.NodeRequirement()
	if exigencia == "" {
		fmt.Fprintln(w, "nenhuma versão de Node exigida ou fixada neste projeto")
		fmt.Fprintln(w, "rode `devm node use <versão>` para fixar uma")
		return nil
	}

	fmt.Fprintf(w, "exigência  %s  (de %s)\n", exigencia, origem)

	rt, ok := environment.ResolverNode(context.Background(), p)
	if !ok {
		fmt.Fprintf(w, "resolvida  nenhum Node instalado satisfaz %s\n", exigencia)
		fmt.Fprintf(w, "\ninstale com `devm node install %s`\n", exigencia)
		return nil
	}

	fmt.Fprintf(w, "resolvida  %s  (%s)\n", rt.Version, rt.Bin)
	return nil
}

func nodeWhichCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("node which", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm node which <constraint>   (ex.: devm node which \"^22\")")
	}

	c, err := semver.ParseConstraint(posicionais[0])
	if err != nil {
		return err
	}

	rt, err := environment.NodeRuntimes().Resolve(context.Background(), "node", c)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s\n", rt.Bin)
	return nil
}
