// Package paths centraliza onde o Dev Manager guarda suas coisas no disco.
//
// Ter um único lugar que decide caminhos evita que cada pacote invente o seu
// e facilita respeitar as convenções de cada sistema — XDG no Linux hoje,
// ~/Library/Application Support no macOS quando chegar a hora.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const appName = "devmanager"

// DataDir é onde ficam runtimes baixados e shims: dados que o Dev Manager
// gerou e consegue recriar, mas que não são cache descartável.
//
// Segue a XDG Base Directory Specification: $XDG_DATA_HOME quando definido,
// senão ~/.local/share. Respeitar isso é o que faz a ferramenta se comportar
// bem em sistemas imutáveis, onde escrever fora do home simplesmente falha.
func DataDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, appName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("localizando o diretório home: %w", err)
	}
	return filepath.Join(home, ".local", "share", appName), nil
}

// ConfigDir é onde fica a configuração editável pelo usuário.
func ConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, appName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("localizando o diretório home: %w", err)
	}
	return filepath.Join(home, ".config", appName), nil
}

// RuntimesDir é onde os PHPs estáticos serão instalados.
func RuntimesDir() (string, error) {
	data, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "runtimes"), nil
}

// ShimDir é o diretório de shims de um projeto.
//
// O caminho precisa ser ESTÁVEL e PREVISÍVEL, porque ele vai parar dentro do
// .vscode/settings.json do projeto: o Intelephense e o PHP Debug apontam para
// <ShimDir>/php e esperam que ele exista mesmo com o devm parado.
//
// Usamos uma chave derivada do caminho absoluto do projeto, não só do nome da
// pasta: dois projetos chamados "api" em lugares diferentes não podem colidir.
func ShimDir(projectPath string) (string, error) {
	data, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "shims", chaveDoProjeto(projectPath)), nil
}
