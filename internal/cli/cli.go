// Package cli implementa a interface de linha de comando do Dev Manager.
package cli

import (
	"fmt"
	"io"
)

// IO agrupa os três fluxos padrão do processo.
//
// Antes bastava um io.Writer para a saída, mas comandos como `devm artisan`
// criam processos filhos que precisam dos TRÊS fluxos: stdin para prompts
// interativos (`artisan migrate` pergunta antes de rodar em produção), stdout
// para o resultado e stderr para os erros.
//
// Agrupar num struct em vez de passar três parâmetros soltos mantém as
// assinaturas legíveis e permite acrescentar campos depois sem quebrar
// nenhuma chamada existente.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run executa um comando a partir dos argumentos recebidos.
//
// args chega SEM o nome do programa, então quem chama passa os.Args[1:].
// Run devolve error em vez de encerrar o processo: encerrar é responsabilidade
// da main, não de uma função de biblioteca — os.Exit não roda os defers.
func Run(args []string, stdio IO) error {
	if len(args) == 0 {
		printUsage(stdio.Out)
		return nil
	}

	comando, resto := args[0], args[1:]

	switch comando {
	case "detect":
		return detectCmd(stdio.Out, resto)
	case "php":
		return phpCmd(stdio.Out, resto)

	// Estes três repassam os argumentos VERBATIM para o processo filho.
	// Nenhuma flag é interpretada pelo devm, senão `devm artisan migrate
	// --force` roubaria o --force do artisan.
	case "add":
		return addCmd(stdio.Out, resto)
	case "list", "ls":
		return listCmd(stdio.Out, resto)
	case "remove", "rm":
		return removeCmd(stdio.Out, resto)
	case "scan":
		return scanCmd(stdio.Out, resto)
	case "prune":
		return pruneCmd(stdio.Out, resto)

	case "up":
		return upCmd(stdio, resto)
	case "start":
		return startCmd(stdio, resto)
	case "run":
		return runCmd(stdio, resto)
	case "artisan":
		return artisanCmd(stdio, resto)
	case "composer":
		return composerCmd(stdio, resto)

	case "ide":
		return ideCmd(stdio.Out, resto)

	case "version":
		return versionCmd(stdio.Out, resto)
	case "help", "-h", "--help":
		printUsage(stdio.Out)
		return nil
	default:
		return fmt.Errorf("comando desconhecido: %q (rode \"devm help\")", comando)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `devm — Dev Manager

Uso:
  devm <comando> [argumentos]

Projetos:
  list       lista os projetos registrados e o estado de cada um
  add        registra um projeto
  scan       registra todos os projetos de um diretório
  remove     tira um projeto do registro
  prune      limpa entradas de projetos que não existem mais
  detect     inspeciona uma pasta e descreve o projeto encontrado
  up         prepara o projeto para rodar (dependências, .env, chave)
  start      sobe os processos do projeto (servidor, filas, frontend)

Execução (usa o PHP exigido pelo projeto da pasta atual):
  artisan    roda php artisan
  composer   roda o composer
  run        roda qualquer comando com o PHP do projeto no PATH

Editor:
  ide        aponta as extensões do editor para o PHP do projeto

Runtimes:
  php list   lista as versões de PHP disponíveis
  php which  mostra qual PHP satisfaz uma constraint
  php use    fixa a versão de PHP do projeto no devmanager.yaml
  php avail  lista versões que podem ser instaladas
  php install  baixa uma versão de PHP isolada do sistema

Outros:
  version    mostra a versão do Dev Manager
  help       mostra esta ajuda
`)
}
