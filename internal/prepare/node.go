package prepare

import (
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/project"
)

// gerenciadoresNode mapeia o arquivo de lock ao gerenciador correspondente.
//
// A ordem importa: um projeto migrado de npm para pnpm pode ter os dois locks
// por um tempo, e o primeiro da lista vence. Bun e pnpm vêm antes porque, se
// estão presentes, foram escolha deliberada.
var gerenciadoresNode = []struct {
	lock    string
	comando string
	subcmd  string
}{
	{"bun.lockb", "bun", "install"},
	{"bun.lock", "bun", "install"},
	{"pnpm-lock.yaml", "pnpm", "install"},
	{"yarn.lock", "yarn", "install"},
	{"package-lock.json", "npm", "install"},
}

// passoNode instala as dependências de frontend, se houver package.json.
//
// Detectar o gerenciador pelo lockfile — em vez de assumir npm — evita o
// clássico de gerar um package-lock.json num projeto que usa pnpm, o que
// suja o diff e confunde o resto do time.
func passoNode(p *project.Project, ex Executor) (Passo, bool) {
	if !existe(filepath.Join(p.Path, "package.json")) {
		return Passo{}, false
	}

	gerenciador, subcmd := "npm", "install"
	for _, g := range gerenciadoresNode {
		if existe(filepath.Join(p.Path, g.lock)) {
			gerenciador, subcmd = g.comando, g.subcmd
			break
		}
	}

	return Passo{
		Nome:     gerenciador + " " + subcmd,
		Porque:   "instala as dependências de frontend",
		Pendente: !existe(filepath.Join(p.Path, "node_modules")),
		executar: comando(ex, gerenciador, subcmd),
	}, true
}
