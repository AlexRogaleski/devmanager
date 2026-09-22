package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// As três formas que DB_DATABASE assume num projeto Laravel com SQLite.
func TestCaminhoSQLite(t *testing.T) {
	projeto := "/casa/projetos/app"

	casos := []struct {
		valor    string
		esperado string
		temArq   bool
	}{
		{"", "/casa/projetos/app/database/database.sqlite", true},
		{"database/database.sqlite", "/casa/projetos/app/database/database.sqlite", true},
		{"/dados/app.sqlite", "/dados/app.sqlite", true},
		{"  database/teste.sqlite  ", "/casa/projetos/app/database/teste.sqlite", true},
		{":memory:", "", false},
	}

	for _, c := range casos {
		obtido, ok := caminhoSQLite(projeto, c.valor)
		if ok != c.temArq {
			t.Errorf("%q: temArquivo = %v, esperava %v", c.valor, ok, c.temArq)
			continue
		}
		if obtido != c.esperado {
			t.Errorf("%q → %q, esperava %q", c.valor, obtido, c.esperado)
		}
	}
}

// A cópia é atômica: um banco meio copiado é pior que nenhum.
func TestCopiaAtomicaSubstituiSemDeixarLixo(t *testing.T) {
	dir := t.TempDir()
	origem := filepath.Join(dir, "origem.sqlite")
	destino := filepath.Join(dir, "destino.sqlite")

	if err := os.WriteFile(origem, []byte("banco novo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destino, []byte("banco antigo"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copiarArquivo(origem, destino); err != nil {
		t.Fatal(err)
	}

	conteudo, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	if string(conteudo) != "banco novo" {
		t.Errorf("destino = %q", conteudo)
	}

	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entradas) != 2 {
		nomes := []string{}
		for _, e := range entradas {
			nomes = append(nomes, e.Name())
		}
		t.Errorf("sobrou temporário: %v", nomes)
	}
}

func TestRestoreCriaODiretorioDoBanco(t *testing.T) {
	dir := t.TempDir()
	origem := filepath.Join(dir, "copia.sqlite")
	if err := os.WriteFile(origem, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	destino := filepath.Join(dir, "projeto", "database", "database.sqlite")
	if err := restoreSQLite(IO{}, origem, destino); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destino); err != nil {
		t.Errorf("não criou o banco: %v", err)
	}
}
