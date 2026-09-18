package runtimes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// servidorFalso simula o dl.static-php.dev: o índice em JSON e os tarballs.
//
// httptest.Server sobe um HTTP real num porta local. É melhor que um mock do
// http.Client porque exercita o caminho inteiro — status, corpo, gzip, tar —
// sem depender da internet nem da disponibilidade de um serviço de terceiros.
func servidorFalso(t *testing.T, versoes []string) *httptest.Server {
	t.Helper()

	so, arch := plataforma()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "json" {
			var entradas []entradaIndice
			for _, v := range versoes {
				// Inclui também builds de outra plataforma, para provar que
				// o filtro funciona e não devolvemos um binário incompatível.
				entradas = append(entradas,
					entradaIndice{Name: fmt.Sprintf("php-%s-cli-%s-%s.tar.gz", v, so, arch)},
					entradaIndice{Name: fmt.Sprintf("php-%s-cli-outroso-outraarch.tar.gz", v)},
					entradaIndice{Name: fmt.Sprintf("php-%s-fpm-%s-%s.tar.gz", v, so, arch)},
				)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entradas)
			return
		}

		// Extrai a versão do nome do arquivo pedido.
		nome := filepath.Base(r.URL.Path)
		m := nomeDoBuild.FindStringSubmatch(nome)
		if m == nil {
			http.NotFound(w, r)
			return
		}

		encontrou := false
		for _, v := range versoes {
			if v == m[1] {
				encontrou = true
				break
			}
		}
		if !encontrou {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tarballFalso(t, m[1]))
	}))

	t.Cleanup(srv.Close)
	return srv
}

// tarballFalso monta um .tar.gz contendo um "php" que responde a
// `php -n -r 'echo PHP_VERSION;'` com a versão pedida.
func tarballFalso(t *testing.T, versao string) []byte {
	t.Helper()

	script := "#!/bin/sh\nprintf '" + versao + "'\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{
		Name:     "php",
		Mode:     0o755,
		Size:     int64(len(script)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func providerDeTeste(t *testing.T, versoes ...string) *StaticProvider {
	t.Helper()
	srv := servidorFalso(t, versoes)
	return &StaticProvider{
		Dir:     filepath.Join(t.TempDir(), "php"),
		BaseURL: srv.URL,
	}
}

func TestStaticInstallable(t *testing.T) {
	p := providerDeTeste(t, "8.2.32", "8.3.32", "8.4.23")

	versoes, err := p.Installable(context.Background())
	if err != nil {
		t.Fatalf("Installable falhou: %v", err)
	}

	// Vem ordenado do maior para o menor, e só da nossa plataforma:
	// as 3 versões, não as 9 entradas que o servidor devolveu.
	esperado := []string{"8.4.23", "8.3.32", "8.2.32"}
	if len(versoes) != len(esperado) {
		t.Fatalf("vieram %d versões (%v), esperava %d", len(versoes), versoes, len(esperado))
	}
	for i, e := range esperado {
		if versoes[i].String() != e {
			t.Errorf("posição %d = %s, esperava %s", i, versoes[i], e)
		}
	}
}

func TestStaticInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	p := providerDeTeste(t, "8.3.32")
	v := semver.MustParse("8.3.32")

	rt, err := p.Install(context.Background(), v, nil)
	if err != nil {
		t.Fatalf("Install falhou: %v", err)
	}

	if rt.Version != v {
		t.Errorf("Version = %s, esperava %s", rt.Version, v)
	}
	if rt.Source != SourceStatic {
		t.Errorf("Source = %q, esperava %q", rt.Source, SourceStatic)
	}

	info, err := os.Stat(rt.Bin)
	if err != nil {
		t.Fatalf("binário não foi instalado: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("binário sem permissão de execução: %v", info.Mode())
	}

	// E aparece no List logo em seguida, sem nenhum índice intermediário.
	lista, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].Version != v {
		t.Errorf("List = %v, esperava conter %s", lista, v)
	}
}

func TestStaticInstallEhIdempotente(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	p := providerDeTeste(t, "8.3.32")
	v := semver.MustParse("8.3.32")
	ctx := context.Background()

	primeira, err := p.Install(ctx, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	segunda, err := p.Install(ctx, v, nil)
	if err != nil {
		t.Fatalf("segunda instalação falhou: %v", err)
	}
	if primeira.Bin != segunda.Bin {
		t.Errorf("caminhos diferentes: %q e %q", primeira.Bin, segunda.Bin)
	}
}

// Falha de instalação não pode deixar meia-instalação para trás: o List
// seguinte listaria um PHP que não funciona.
func TestStaticInstallNaoDeixaLixo(t *testing.T) {
	p := providerDeTeste(t, "8.3.32")

	_, err := p.Install(context.Background(), semver.MustParse("9.9.9"), nil)
	if err == nil {
		t.Fatal("esperava erro para versão inexistente")
	}
	if !strings.Contains(err.Error(), "não existe") {
		t.Errorf("mensagem pouco clara: %v", err)
	}

	entradas, err := os.ReadDir(p.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entradas {
		t.Errorf("sobrou lixo no diretório de instalação: %s", e.Name())
	}
}

func TestStaticListSemNada(t *testing.T) {
	p := &StaticProvider{Dir: filepath.Join(t.TempDir(), "nunca-criado")}

	lista, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("diretório ausente não deveria ser erro: %v", err)
	}
	if len(lista) != 0 {
		t.Errorf("esperava lista vazia, veio %v", lista)
	}
}

// O context tem que cortar o download: sem isso, uma rede travada penduraria
// a CLI para sempre.
func TestStaticInstallRespeitaContext(t *testing.T) {
	p := providerDeTeste(t, "8.3.32")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // já cancelado antes de começar

	if _, err := p.Install(ctx, semver.MustParse("8.3.32"), nil); err == nil {
		t.Fatal("esperava erro com context cancelado")
	}
}

func TestURLDoBuild(t *testing.T) {
	p := &StaticProvider{BaseURL: "https://exemplo.test/spc"}
	so, arch := plataforma()

	esperado := fmt.Sprintf("https://exemplo.test/spc/%s/php-8.3.32-cli-%s-%s.tar.gz", VariantePadrao, so, arch)
	if got := p.URLDoBuild(semver.MustParse("8.3.32")); got != esperado {
		t.Errorf("URL = %q, esperava %q", got, esperado)
	}

	p.Variant = "common"
	if got := p.URLDoBuild(semver.MustParse("8.3.32")); !strings.Contains(got, "/common/") {
		t.Errorf("a variante não foi aplicada: %q", got)
	}
}
