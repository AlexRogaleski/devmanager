package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// staticProvider monta o Provider de binários estáticos.
func staticProvider() (*runtimes.StaticProvider, error) {
	dir, err := paths.RuntimesDir()
	if err != nil {
		return nil, err
	}
	return &runtimes.StaticProvider{
		Dir:     filepath.Join(dir, "php"),
		Variant: os.Getenv("DEVMANAGER_PHP_VARIANT"), // vazio usa o padrão
	}, nil
}

// phpAvailableCmd lista o que dá para instalar.
func phpAvailableCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("php available", flag.ContinueOnError)
	fs.SetOutput(w)
	todas := fs.Bool("all", false, "lista todos os patches, não só o mais recente de cada série")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	sp, err := staticProvider()
	if err != nil {
		return err
	}

	versoes, err := sp.Installable(context.Background())
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

	// Sem --all, mostramos só a mais nova de cada série 8.x. A lista vem
	// ordenada do maior para o menor, então a primeira de cada série é a
	// que queremos — um mapa de "já vi" basta.
	vistas := map[string]bool{}
	fmt.Fprintf(w, "%-10s %s\n", "SÉRIE", "MAIS RECENTE")
	for _, v := range versoes {
		serie := v.MajorMinor()
		if vistas[serie] {
			continue
		}
		vistas[serie] = true
		fmt.Fprintf(w, "%-10s %s\n", serie, v)
	}
	fmt.Fprintln(w, "\nuse --all para ver todos os patches")
	return nil
}

// phpInstallCmd baixa uma versão de PHP isolada do sistema.
//
//	devm php install 8.3       instala o 8.3 mais recente disponível
//	devm php install 8.3.32    instala exatamente essa
//	devm php install ^8.2      instala a maior que satisfaça a faixa
func phpInstallCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("php install", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm php install <versão>   (ex.: devm php install 8.3)")
	}

	c, err := semver.ParseConstraint(posicionais[0])
	if err != nil {
		return fmt.Errorf("versão inválida %q: %w", posicionais[0], err)
	}

	sp, err := staticProvider()
	if err != nil {
		return err
	}
	ctx := context.Background()

	fmt.Fprintln(w, "consultando versões disponíveis...")
	disponiveis, err := sp.Installable(ctx)
	if err != nil {
		return err
	}

	alvo, ok := c.Best(disponiveis)
	if !ok {
		return fmt.Errorf("nenhuma versão disponível satisfaz %q (rode `devm php available`)", posicionais[0])
	}

	fmt.Fprintf(w, "instalando PHP %s\n", alvo)

	rt, err := sp.Install(ctx, alvo, progressoNoTerminal(w))
	if err != nil {
		return err
	}

	if ehTerminal(w) {
		fmt.Fprint(w, "\r", strings.Repeat(" ", 50), "\r")
	}
	fmt.Fprintf(w, "instalado em %s\n", rt.Bin)
	return mostrarExtensoes(ctx, w, rt)
}

// progressoNoTerminal devolve um callback que desenha a barra de download.
//
// O \r volta o cursor ao início da linha sem pular linha, sobrescrevendo a
// anterior — é assim que se faz uma barra de progresso num terminal.
//
// Mas só num terminal: redirecionado para arquivo ou pipe, cada \r vira lixo
// no meio do texto, porque nada reposiciona o cursor. Ferramenta de linha de
// comando que ignora isso produz log ilegível em CI.
func progressoNoTerminal(w io.Writer) runtimes.Progresso {
	if !ehTerminal(w) {
		return nil // sem callback: o download roda calado
	}

	return func(baixado, total int64) {
		if total <= 0 {
			fmt.Fprintf(w, "\rbaixando... %.1f MB", float64(baixado)/(1<<20))
			return
		}
		pct := float64(baixado) / float64(total) * 100
		fmt.Fprintf(w, "\rbaixando... %5.1f%%  (%.1f de %.1f MB)",
			pct, float64(baixado)/(1<<20), float64(total)/(1<<20))
	}
}

// ehTerminal informa se a saída é um terminal interativo.
//
// A checagem é feita por type assertion: perguntamos se o io.Writer que
// recebemos é, por baixo, um *os.File. Num teste ele é um bytes.Buffer, a
// asserção falha e a resposta é "não é terminal" — sem precisar de build tags
// nem de dependência externa.
func ehTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}
	// ModeCharDevice distingue um terminal de um arquivo ou pipe comum.
	return info.Mode()&os.ModeCharDevice != 0
}
