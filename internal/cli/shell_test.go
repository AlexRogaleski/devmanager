package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/paths"
)

// historicoDeTeste é o mesmo caminho que o `devm shell` usaria.
func historicoDeTeste(projeto string) (string, error) {
	return paths.HistoricoDir(projeto)
}

// Cada shell guarda o histórico de um jeito, e os formatos não são
// compatíveis: o arquivo do bash e o do zsh não podem ser o mesmo.
func TestHistoricoDoShell(t *testing.T) {
	dir := "/data/historicos/api-1234abcd"

	casos := []struct {
		shell string
		quer  string
	}{
		{"/bin/bash", "HISTFILE=" + filepath.Join(dir, "bash_history")},
		{"/usr/bin/zsh", "HISTFILE=" + filepath.Join(dir, "zsh_history")},
		{"-zsh", "HISTFILE=" + filepath.Join(dir, "zsh_history")},
		// O fish não aceita caminho: ele quer o NOME de uma sessão, e
		// resolve o arquivo dentro do diretório de dados dele.
		{"/opt/homebrew/bin/fish", "fish_history=api-1234abcd"},
		// Um shell desconhecido recebe HISTFILE: quem não usa a variável
		// simplesmente a ignora.
		{"/bin/ksh", "HISTFILE=" + filepath.Join(dir, "bash_history")},
	}

	for _, c := range casos {
		vars := historicoDoShell(c.shell, dir)
		if len(vars) == 0 || vars[0] != c.quer {
			t.Errorf("%s: %q, esperava começar com %q", c.shell, vars, c.quer)
		}
	}
}

// TestZshPrecisaDeSavehist guarda um detalhe que só aparece num zsh de
// verdade: o padrão de SAVEHIST é ZERO, e com ele o zsh não grava nada.
// Apontar só o HISTFILE entregaria a funcionalidade inteira sem gravar uma
// linha — e sem erro nenhum para denunciar.
func TestZshPrecisaDeSavehist(t *testing.T) {
	vars := strings.Join(historicoDoShell("/usr/bin/zsh", "/data/hist"), " ")

	for _, esperado := range []string{"SAVEHIST=", "HISTSIZE="} {
		if !strings.Contains(vars, esperado) {
			t.Errorf("faltou %s: %s", esperado, vars)
		}
	}
	if strings.Contains(vars, "SAVEHIST=0") {
		t.Errorf("SAVEHIST=0 não grava nada: %s", vars)
	}

	// O bash grava por padrão e não precisa dos dois.
	if bash := strings.Join(historicoDoShell("/bin/bash", "/data/hist"), " "); strings.Contains(bash, "SAVEHIST") {
		t.Errorf("SAVEHIST é coisa do zsh: %s", bash)
	}
}

// O $SHELL é o shell de LOGIN e não muda quando a pessoa roda outro por cima.
// Numa máquina cujo login é bash mas cujo terminal abre zsh, usar $SHELL
// abriria bash e jogaria o histórico num arquivo que ninguém lê.
func TestEscolhaDoShellPreferioQueEstaEmUso(t *testing.T) {
	procurar := func(nome string) (string, error) { return "/usr/bin/" + nome, nil }

	casos := []struct {
		nome      string
		reportado string
		quer      string
	}{
		{"caminho absoluto do pai", "/usr/bin/zsh", "/usr/bin/zsh"},
		{"shell de login vem com traço", "-zsh", "/usr/bin/zsh"},
		{"só o nome", "fish", "/usr/bin/fish"},
		// Sem esta recusa, um `devm shell` disparado por um agente abriria
		// o próprio agente como se fosse um shell.
		{"pai que não é shell", "/usr/bin/node", ""},
		{"pai desconhecido", "", ""},
	}

	for _, c := range casos {
		if got := normalizarShell(c.reportado, procurar); got != c.quer {
			t.Errorf("%s: normalizarShell(%q) = %q, queria %q", c.nome, c.reportado, got, c.quer)
		}
	}
}

// O histórico não pode apontar para dentro do projeto: é do desenvolvedor,
// não do código, e ali entraria no caminho do git.
func TestHistoricoFicaForaDoProjeto(t *testing.T) {
	raiz := ambienteIsolado(t)
	projeto := projetoLaravel(t, raiz, "minha-api")

	dir, err := historicoDeTeste(projeto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(dir, projeto) {
		t.Errorf("o histórico caiu dentro do projeto: %s", dir)
	}
	if !strings.Contains(dir, "minha-api") {
		t.Errorf("o diretório deveria identificar o projeto: %s", dir)
	}
}

// Dois projetos de mesmo nome em lugares diferentes não podem dividir o
// histórico — a chave vem do caminho absoluto.
func TestHistoricoSeparaProjetosDeMesmoNome(t *testing.T) {
	raiz := ambienteIsolado(t)
	um := projetoLaravel(t, filepath.Join(raiz, "cliente-a"), "api")
	outro := projetoLaravel(t, filepath.Join(raiz, "cliente-b"), "api")

	a, err := historicoDeTeste(um)
	if err != nil {
		t.Fatal(err)
	}
	b, err := historicoDeTeste(outro)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("os dois projetos compartilhariam o histórico: %s", a)
	}
}
