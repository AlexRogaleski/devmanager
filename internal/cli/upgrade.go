package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/upgrade"
)

// upgradeCmd troca este binário pela última versão publicada.
//
//	devm upgrade          baixa e instala, se houver versão nova
//	devm upgrade --check   só informa, sem instalar
//
// O caminho de antes era clonar o repositório, compilar e instalar — o que
// serve a quem desenvolve a ferramenta, não a quem só a usa.
func upgradeCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	apenasVerificar := fs.Bool("check", false, "só informa se há versão nova")
	forcar := fs.Bool("force", false, "instala mesmo que o binário seja local ou já atual")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	w := stdio.Out
	ctx := context.Background()
	a := &upgrade.Atualizador{}

	ultima, err := a.Ultima(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "instalada  %s\n", Version)
	fmt.Fprintf(w, "publicada  %s\n\n", ultima.Tag)

	situacao := compararVersoes(Version, ultima.Tag)
	if *forcar {
		situacao = temVersaoNova
	}

	switch situacao {
	case mesmaVersao:
		fmt.Fprintln(w, "já está na última versão")
		return nil
	case versaoLocal:
		// Um binário compilado do código local é quase sempre mais novo que
		// a release; trocá-lo por engano descartaria o que a pessoa acabou
		// de compilar.
		fmt.Fprintln(w, "este binário foi compilado do código local, não de uma versão publicada")
		fmt.Fprintf(w, "para trocá-lo pela %s:  devm upgrade --force\n", ultima.Tag)
		return nil
	}

	if *apenasVerificar {
		fmt.Fprintf(w, "há uma versão nova: %s\n", ultima.Tag)
		fmt.Fprintln(w, "instale com:  devm upgrade")
		return nil
	}

	fmt.Fprintf(w, "baixando %s...\n", a.NomeDoArquivo())

	destino, err := a.Instalar(ctx, ultima, upgrade.Progresso(progressoNoTerminal(w)))
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\r%s atualizado para %s\n", destino, ultima.Tag)
	return nil
}

type situacaoDaVersao int

const (
	temVersaoNova situacaoDaVersao = iota
	mesmaVersao
	versaoLocal
)

// compararVersoes decide o que fazer com o que está instalado.
//
// A comparação é por igualdade de texto, não por ordem: o carimbo vem do
// `git describe`, e o que interessa é distinguir três casos — é exatamente a
// versão publicada, é um build local (tem sufixo de commit), ou é outra
// versão qualquer.
func compararVersoes(instalada, publicada string) situacaoDaVersao {
	instalada = strings.TrimSpace(instalada)

	switch {
	case instalada == publicada:
		return mesmaVersao
	case instalada == "dev", strings.Contains(instalada, "-g"), strings.HasSuffix(instalada, "-dirty"):
		return versaoLocal
	}
	return temVersaoNova
}
