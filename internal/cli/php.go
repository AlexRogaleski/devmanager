package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// defaultManager monta a cadeia de Providers na ordem de precedência.
//
// Hoje só há o do sistema. Quando o StaticProvider existir, ele entra ANTES
// desta linha e passa a ganhar nos empates — e nenhum comando precisa mudar.
func defaultManager() *runtimes.Manager {
	return runtimes.NewManager(&runtimes.SystemProvider{})
}

// phpCmd despacha os subcomandos de `devm php`.
func phpCmd(w io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(w, "uso: devm php <list|which> [argumentos]\n")
		return nil
	}

	switch args[0] {
	case "list", "ls":
		return phpListCmd(w, args[1:])
	case "which":
		return phpWhichCmd(w, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: php %q", args[0])
	}
}

// phpListCmd mostra todo PHP que o Dev Manager consegue enxergar.
func phpListCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("php list", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	// context.Background() é a raiz de toda cadeia de contextos: o ponto em
	// que o programa diz "esta operação não tem cancelamento vindo de cima".
	// Numa CLI é aqui; num servidor HTTP viria da requisição.
	ctx := context.Background()

	encontrados, err := defaultManager().List(ctx, "php")
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
		fmt.Fprintln(w, "nenhum PHP encontrado nesta máquina")
		return nil
	}

	fmt.Fprintf(w, "%-10s %-10s %s\n", "VERSÃO", "ORIGEM", "CAMINHO")
	for _, r := range encontrados {
		fmt.Fprintf(w, "%-10s %-10s %s\n", r.Version, r.Source, r.Bin)
	}
	return nil
}

// phpWhichCmd responde "que PHP você usaria para esta exigência?".
//
// É o mesmo caminho de código que o `devm run` vai usar para escolher o
// interpretador — expor isso como comando torna a decisão inspecionável
// em vez de mágica.
func phpWhichCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("php which", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm php which <constraint>   (ex.: devm php which \"^8.3\")")
	}

	c, err := semver.ParseConstraint(posicionais[0])
	if err != nil {
		return err
	}

	r, err := defaultManager().Resolve(context.Background(), "php", c)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s\n", r.Bin)
	return nil
}
