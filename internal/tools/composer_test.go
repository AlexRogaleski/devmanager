package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pharFalso = "#!/usr/bin/env php\n<?php echo 'composer falso';"

// servidorComposer simula o getcomposer.org.
func servidorComposer(t *testing.T, versao string, corromper bool) *httptest.Server {
	t.Helper()

	soma := sha256.Sum256([]byte(pharFalso))
	hash := hex.EncodeToString(soma[:])
	if corromper {
		// Anuncia um checksum que não corresponde ao conteúdo servido.
		hash = strings.Repeat("0", 64)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/versions":
			fmt.Fprintf(w, `{"stable":[{"path":"/download/%s/composer.phar","version":"%s"}]}`, versao, versao)

		case strings.HasSuffix(r.URL.Path, "composer.phar.sha256sum"):
			fmt.Fprintf(w, "%s  composer.phar\n", hash)

		case strings.HasSuffix(r.URL.Path, "composer.phar"):
			fmt.Fprint(w, pharFalso)

		default:
			http.NotFound(w, r)
		}
	}))

	t.Cleanup(srv.Close)
	return srv
}

func composerDeTeste(t *testing.T, versao string, corromper bool) *Composer {
	t.Helper()
	return &Composer{
		Dir:     filepath.Join(t.TempDir(), "composer"),
		BaseURL: servidorComposer(t, versao, corromper).URL,
	}
}

func TestComposerEnsure(t *testing.T) {
	c := composerDeTeste(t, "2.10.3", false)

	phar, err := c.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatalf("Ensure falhou: %v", err)
	}

	dados, err := os.ReadFile(phar)
	if err != nil {
		t.Fatalf("phar não foi gravado: %v", err)
	}
	if string(dados) != pharFalso {
		t.Errorf("conteúdo inesperado: %q", dados)
	}

	info, _ := os.Stat(phar)
	if info.Mode()&0o111 == 0 {
		t.Errorf("phar sem permissão de execução: %v", info.Mode())
	}
	if !strings.Contains(phar, "2.10.3") {
		t.Errorf("caminho não versionado: %q", phar)
	}
}

// Checksum divergente tem que abortar a instalação — e não deixar nada para trás.
func TestComposerRecusaChecksumErrado(t *testing.T) {
	c := composerDeTeste(t, "2.10.3", true)

	_, err := c.Ensure(context.Background(), nil)
	if err == nil {
		t.Fatal("esperava erro de checksum")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("mensagem pouco clara: %v", err)
	}

	entradas, err := os.ReadDir(c.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entradas {
		t.Errorf("sobrou lixo após falha: %s", e.Name())
	}
}

func TestComposerEhIdempotente(t *testing.T) {
	c := composerDeTeste(t, "2.10.3", false)
	ctx := context.Background()

	primeiro, err := c.Ensure(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := c.Ensure(ctx, nil)
	if err != nil {
		t.Fatalf("segunda chamada falhou: %v", err)
	}
	if primeiro != segundo {
		t.Errorf("caminhos diferentes: %q e %q", primeiro, segundo)
	}
}

// Sem rede, um composer já baixado ainda serve: falhar só porque a consulta
// de versões não respondeu seria hostil com quem está offline.
func TestComposerFuncionaOffline(t *testing.T) {
	c := composerDeTeste(t, "2.10.3", false)

	if _, err := c.Ensure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	// Aponta para um servidor que não existe.
	c.BaseURL = "http://127.0.0.1:1"

	phar, err := c.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatalf("deveria reusar o composer já baixado: %v", err)
	}
	if !strings.Contains(phar, "2.10.3") {
		t.Errorf("caminho inesperado: %q", phar)
	}
}

func TestComposerSemNadaInstaladoESemRede(t *testing.T) {
	c := &Composer{Dir: filepath.Join(t.TempDir(), "composer"), BaseURL: "http://127.0.0.1:1"}

	if _, err := c.Ensure(context.Background(), nil); err == nil {
		t.Fatal("esperava erro sem rede e sem instalação prévia")
	}
}

func TestComposerReportaProgresso(t *testing.T) {
	c := composerDeTeste(t, "2.10.3", false)

	var chamadas int
	prog := Progresso(func(baixado, total int64) { chamadas++ })

	if _, err := c.Ensure(context.Background(), prog); err != nil {
		t.Fatal(err)
	}
	if chamadas == 0 {
		t.Error("o callback de progresso nunca foi chamado")
	}
}
