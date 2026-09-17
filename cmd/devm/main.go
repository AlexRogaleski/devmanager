// Comando devm é o cliente de linha de comando do Dev Manager.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/AlexRogaleski/devmanager/internal/cli"
	"github.com/AlexRogaleski/devmanager/internal/runner"
)

func main() {
	stdio := cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	err := cli.Run(os.Args[1:], stdio)
	if err == nil {
		return
	}

	// Se um processo filho rodou e saiu com código != 0, o devm sai com o
	// MESMO código e sem mensagem própria — o comando já escreveu o erro dele
	// no stderr. É isso que faz `devm artisan migrate && npm run build`
	// se comportar igual a `php artisan migrate && npm run build`.
	var saida *runner.ExitError
	if errors.As(err, &saida) {
		os.Exit(saida.Code)
	}

	// Alguns comandos já explicaram o que houve na própria saída; só precisam
	// do código de retorno. Uma mensagem "devm: ..." aqui seria ruído.
	var comCodigo interface{ Code() int }
	if errors.As(err, &comCodigo) {
		os.Exit(comCodigo.Code())
	}

	fmt.Fprintln(os.Stderr, "devm:", err)
	os.Exit(1)
}
