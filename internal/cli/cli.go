// Package cli implementa a interface de linha de comando do Dev Manager.
package cli

import (
	"fmt"
	"io"
)

// Run executa um comando a partir dos argumentos recebidos.
//
// Repare em duas escolhas de assinatura:
//
//   - args chega SEM o nome do programa, então quem chama passa os.Args[1:].
//     Assim Run não precisa saber como foi invocada e fica testável.
//   - stdout é um io.Writer, não os.Stdout direto. io.Writer é uma interface
//     com um único método, Write. Em produção passamos os.Stdout; num teste
//     passamos um buffer em memória e conferimos o que foi escrito.
//     Essa é a forma idiomática de tornar saída testável em Go.
//
// E Run devolve error em vez de chamar os.Exit: encerrar o processo é
// responsabilidade da main, não de uma função de biblioteca.
func Run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	// Atribuição múltipla: separa o comando do resto dos argumentos.
	comando, resto := args[0], args[1:]

	switch comando {
	case "detect":
		return detectCmd(stdout, resto)
	case "version":
		return versionCmd(stdout, resto)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("comando desconhecido: %q (rode \"devm help\")", comando)
	}
}

func printUsage(w io.Writer) {
	// Crase abre uma raw string: nada é interpretado dentro dela,
	// nem \n nem aspas. Ideal para blocos de texto como este.
	fmt.Fprint(w, `devm — Dev Manager

Uso:
  devm <comando> [argumentos]

Comandos:
  detect     inspeciona uma pasta e descreve o projeto encontrado
  version    mostra a versão do Dev Manager
  help       mostra esta ajuda
`)
}
