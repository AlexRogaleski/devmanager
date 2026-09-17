package cli

import (
	"fmt"
	"io"
	"runtime"
)

// Version é o número de versão do binário.
//
// O valor "dev" é só o padrão de quem compila localmente: na hora do build
// a gente sobrescreve isso sem tocar no código, com
//
//	go build -ldflags "-X .../internal/cli.Version=0.1.0"
//
// É assim que binários Go carimbam versão e commit sem gerar arquivo.
var Version = "dev"

// O _ no segundo parâmetro descarta o nome: o compilador exige que toda
// variável declarada seja usada, então quando um parâmetro ainda não serve
// para nada, usamos _ para deixar isso explícito.
func versionCmd(w io.Writer, _ []string) error {
	fmt.Fprintf(w, "devm %s (%s %s/%s)\n",
		Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}
