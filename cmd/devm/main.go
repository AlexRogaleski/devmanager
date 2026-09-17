// Comando devm é o cliente de linha de comando do Dev Manager.
package main

import (
	"fmt"
	"os"

	"github.com/AlexRogaleski/devmanager/internal/cli"
)

// main é deliberadamente minúscula. Toda a lógica está em cli.Run;
// aqui só decidimos para onde vai o erro e com que código saímos.
//
// Por que não chamar os.Exit lá dentro? Porque os.Exit NÃO executa os
// defers pendentes. Concentrando a saída num único ponto, o resto do
// programa pode confiar que seus defers sempre rodam.
func main() {
	if err := cli.Run(os.Args[1:], os.Stdout); err != nil {
		// Erro vai para stderr, não stdout: assim `devm version > arquivo`
		// grava só a versão, e a mensagem de erro continua aparecendo no terminal.
		fmt.Fprintln(os.Stderr, "devm:", err)
		os.Exit(1)
	}
}
