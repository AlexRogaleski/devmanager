package services

import (
	"context"
	"strings"
	"testing"
)

func TestComandoDeDumpPorDialeto(t *testing.T) {
	casos := map[string]string{
		"postgres:18": "pg_dump -U laravel meu_app",
		"mysql:8.4":   "mysqldump -uroot -psecret --single-transaction meu_app",
		"mariadb:11":  "mariadb-dump -uroot -psecret --single-transaction meu_app",
	}

	for texto, esperado := range casos {
		spec, err := ParseSpec(texto)
		if err != nil {
			t.Fatal(err)
		}
		args, err := comandoDeDump(spec, "meu_app")
		if err != nil {
			t.Fatalf("%s: %v", texto, err)
		}
		if obtido := strings.Join(args, " "); obtido != esperado {
			t.Errorf("%s → %q, esperava %q", texto, obtido, esperado)
		}
	}
}

// Sem ON_ERROR_STOP o psql atravessa o arquivo inteiro reclamando e termina
// com código 0: um restore pela metade passaria por bem-sucedido.
func TestRestoreDoPostgresParaNoPrimeiroErro(t *testing.T) {
	spec, _ := ParseSpec("postgres:18")

	args, err := comandoDeRestore(spec, "meu_app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "ON_ERROR_STOP=1") {
		t.Errorf("comando sem ON_ERROR_STOP: %v", args)
	}
}

func TestServicoSemBancoNaoAceitaDumpNemRestore(t *testing.T) {
	spec, _ := ParseSpec("redis")

	if _, err := comandoDeDump(spec, "x"); err == nil {
		t.Error("aceitou dump de um serviço que não é banco")
	}
	if _, err := comandoDeRestore(spec, "x"); err == nil {
		t.Error("aceitou restore de um serviço que não é banco")
	}
}

// O nome do banco vai para dentro de uma linha de comando e, no MySQL, para
// dentro de uma instrução SQL: aceitar qualquer texto seria injeção.
func TestNomeDeBancoPerigosoERecusado(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:18")
	ctx := context.Background()

	perigosos := []string{"meu_app; DROP DATABASE outro", "meu app", "`crase`", "'aspas'", ""}
	for _, nome := range perigosos {
		if err := m.Dump(ctx, spec, nome, nil); err == nil {
			t.Errorf("Dump aceitou %q", nome)
		}
		if err := m.Restore(ctx, spec, nome, nil); err == nil {
			t.Errorf("Restore aceitou %q", nome)
		}
		if err := m.RecriarBanco(ctx, spec, nome); err == nil {
			t.Errorf("RecriarBanco aceitou %q", nome)
		}
	}
}

func TestComandoDeShellPorDialeto(t *testing.T) {
	casos := map[string]string{
		"postgres:18": "psql -U laravel -d meu_app",
		"mysql:8.4":   "mysql -uroot -psecret meu_app",
		"mariadb:11":  "mariadb -uroot -psecret meu_app",
	}

	for texto, esperado := range casos {
		spec, _ := ParseSpec(texto)
		args, err := comandoDeShell(spec, "meu_app")
		if err != nil {
			t.Fatalf("%s: %v", texto, err)
		}
		if obtido := strings.Join(args, " "); obtido != esperado {
			t.Errorf("%s → %q, esperava %q", texto, obtido, esperado)
		}
	}

	// Serviço sem dialeto não tem cliente a abrir.
	redis, _ := ParseSpec("redis")
	if _, err := comandoDeShell(redis, "x"); err == nil {
		t.Error("aceitou shell de serviço que não é banco")
	}
}
