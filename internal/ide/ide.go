// Package ide configura editores para usarem o PHP escolhido pelo Dev Manager.
//
// Sem isso, o ambiente fica pela metade: o terminal roda no PHP 8.3, mas o
// Intelephense analisa o código como 8.5 e reclama de sintaxe válida — ou,
// pior, deixa passar sintaxe que vai quebrar em produção.
package ide

import "github.com/AlexRogaleski/devmanager/internal/semver"

// Config é o que um editor precisa saber para se configurar.
type Config struct {
	ProjectPath string         // raiz do projeto
	PHPBin      string         // caminho do shim: .../shims/<projeto>/php
	PHPVersion  semver.Version // versão concreta resolvida
	Force       bool           // sobrescreve arquivo que não conseguimos ler
}

// Result descreve o que foi (ou seria) alterado.
type Result struct {
	File     string            // arquivo afetado
	Changes  map[string]string // chave de configuração -> novo valor
	Backup   string            // caminho do backup, se houve
	NoChange bool              // já estava tudo certo
}

// Editor é um editor configurável.
//
// Mesma escolha de design do Provider de runtimes: interface pequena, para que
// acrescentar PhpStorm ou Zed depois seja escrever um struct novo e registrá-lo,
// sem tocar em nada existente.
type Editor interface {
	// Name identifica o editor: "vscode", "phpstorm".
	Name() string

	// Detected informa se este editor aparenta ser usado neste projeto.
	Detected(projectPath string) bool

	// Configure aplica a configuração. DryRun mostra sem gravar.
	Configure(cfg Config, dryRun bool) (Result, error)
}

// All devolve os editores suportados.
//
// Acrescentar um editor novo é escrever um struct com estes três métodos e
// somar uma linha aqui. Nenhum outro arquivo do projeto muda.
func All() []Editor {
	return []Editor{&VSCode{}, &PhpStorm{}}
}
