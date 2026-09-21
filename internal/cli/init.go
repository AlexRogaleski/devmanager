package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/project"
)

// initCmd gera o devmanager.yaml do projeto.
//
//	devm init          cria o arquivo com todas as opções documentadas
//	devm init --print  só mostra o modelo, sem gravar nada
//
// O arquivo gerado traz cada opção explicada e exemplificada em comentário.
// Para quem JÁ tem um devmanager.yaml, o --print é o caminho: mostra o
// modelo atual na tela para copiar o pedaço que interessa, sem tocar num
// arquivo que é do projeto, não nosso.
func initCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	imprimir := fs.Bool("print", false, "mostra o modelo sem gravar nada")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	dir := "."
	if len(posicionais) > 0 {
		dir = posicionais[0]
	}

	w := stdio.Out
	novo := config.Config{PHP: phpDetectado(dir)}

	if *imprimir {
		fmt.Fprint(w, config.Modelo(novo))
		return nil
	}

	caminho := config.Path(dir)
	if _, err := os.Stat(caminho); err == nil {
		return fmt.Errorf("%s já existe\n"+
			"  para ver todas as opções disponíveis:  devm init --print", caminho)
	}

	if err := config.Criar(dir, novo); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s criado\n", caminho)
	if novo.PHP != "" {
		fmt.Fprintf(w, "  php %s (a maior instalada que atende o composer.json)\n", novo.PHP)
	}
	fmt.Fprintln(w, "\nas demais opções estão no arquivo, comentadas")
	return nil
}

// phpDetectado devolve a versão a fixar, ou "" se não der para saber.
//
// Fixar a versão RESOLVIDA, e não a exigência do composer.json, é de
// propósito: "^8.2" num projeto que roda em 8.4 fixaria o piso, e o projeto
// passaria a rodar numa versão diferente da que estava em uso.
func phpDetectado(dir string) string {
	p, err := project.Detect(dir)
	if err != nil {
		return ""
	}

	res := resolverPHP(p)
	if !res.Achou {
		return ""
	}
	return res.Runtime.Version.MajorMinor()
}
