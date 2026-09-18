package runtimes

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// instalarPHPFalso escreve uma instalação de mentira no layout real:
// <raiz>/<versão>/bin/php, com um script que responde a versão pedida.
func instalarPHPFalso(t *testing.T, raiz, versao string) string {
	t.Helper()

	dir := filepath.Join(raiz, versao, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "php")
	script := "#!/bin/sh\nprintf '" + versao + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestRemoveApagaSoAVersaoPedida(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	raiz := t.TempDir()
	instalarPHPFalso(t, raiz, "8.3.32")
	instalarPHPFalso(t, raiz, "8.4.23")

	p := &StaticProvider{Dir: raiz}

	if err := p.Remove(semver.Version{Major: 8, Minor: 3, Patch: 32}); err != nil {
		t.Fatalf("Remove falhou: %v", err)
	}

	// A prova que importa é a do disco: List varre o diretório de verdade,
	// então se a pasta ainda estivesse lá ela apareceria aqui.
	sobraram, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sobraram) != 1 {
		t.Fatalf("esperava 1 versão restante, veio %d: %v", len(sobraram), sobraram)
	}
	if sobraram[0].Version.String() != "8.4.23" {
		t.Errorf("sobrou a versão errada: %s", sobraram[0].Version)
	}
}

func TestRemoveVersaoInexistenteEhErro(t *testing.T) {
	p := &StaticProvider{Dir: t.TempDir()}

	err := p.Remove(semver.Version{Major: 9, Minor: 9, Patch: 9})
	if err == nil {
		t.Fatal("remover o que não existe deveria falhar")
	}
	if !strings.Contains(err.Error(), "não está instalado") {
		t.Errorf("mensagem pouco clara: %v", err)
	}
}

// TestRemoverDirRecusaForaDaRaiz protege contra o pior erro possível deste
// código: um RemoveAll apontado para o lugar errado.
//
// Hoje DirDaVersao não consegue escapar da raiz, mas o teste não é sobre
// hoje — é sobre a próxima pessoa que mudar a montagem do caminho.
func TestRemoverDirRecusaForaDaRaiz(t *testing.T) {
	base := t.TempDir()
	raiz := filepath.Join(base, "runtimes")
	fora := filepath.Join(base, "importante")

	for _, d := range []string{raiz, fora} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	casos := []string{
		fora,                                    // irmão da raiz
		filepath.Join(raiz, "..", "importante"), // escapando com ..
		raiz,                                    // a própria raiz
	}

	for _, alvo := range casos {
		if err := removerDir(raiz, alvo); err == nil {
			t.Errorf("removerDir aceitou apagar %q", alvo)
		}
		if _, err := os.Stat(fora); err != nil {
			t.Fatalf("a pasta de fora foi apagada ao tentar %q", alvo)
		}
	}
}

func TestTamanhoEmDisco(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "bin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "php"), make([]byte, 1000), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LEIAME"), make([]byte, 24), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := TamanhoEmDisco(dir); got != 1024 {
		t.Errorf("TamanhoEmDisco = %d, queria 1024", got)
	}
}

func TestTamanhoEmDiscoDePastaInexistenteEhZero(t *testing.T) {
	if got := TamanhoEmDisco(filepath.Join(t.TempDir(), "nao-existe")); got != 0 {
		t.Errorf("TamanhoEmDisco = %d, queria 0", got)
	}
}

func TestFormatarTamanho(t *testing.T) {
	casos := []struct {
		bytes int64
		quer  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2 KB"},
		{31 << 20, "31 MB"},
		{3 << 30, "3.0 GB"},
	}

	for _, c := range casos {
		if got := FormatarTamanho(c.bytes); got != c.quer {
			t.Errorf("FormatarTamanho(%d) = %q, queria %q", c.bytes, got, c.quer)
		}
	}
}
