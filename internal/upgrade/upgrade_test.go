package upgrade

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

// servidorFalso publica uma "release" com o binário e o SHA256SUMS.
func servidorFalso(t *testing.T, conteudo []byte, somaErrada bool) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	soma := sha256.Sum256(conteudo)
	texto := hex.EncodeToString(soma[:])
	if somaErrada {
		texto = strings.Repeat("0", 64)
	}

	mux.HandleFunc("/repos/"+Repositorio+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
		  "tag_name": "v9.9.9",
		  "assets": [
		    {"name": "devm-linux-amd64", "browser_download_url": "%s/bin"},
		    {"name": "SHA256SUMS", "browser_download_url": "%s/somas"}
		  ]
		}`, srv.URL, srv.URL)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(conteudo) })
	mux.HandleFunc("/somas", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  devm-linux-amd64\n%s  devm-darwin-arm64\n", texto, strings.Repeat("a", 64))
	})

	return srv
}

func atualizadorDeTeste(t *testing.T, srv *httptest.Server) (*Atualizador, string) {
	t.Helper()

	destino := filepath.Join(t.TempDir(), "devm")
	if err := os.WriteFile(destino, []byte("binário antigo"), 0o755); err != nil {
		t.Fatal(err)
	}

	return &Atualizador{
		URLBase: srv.URL,
		Cliente: srv.Client(),
		SO:      "linux",
		Arch:    "amd64",
		Destino: destino,
	}, destino
}

func TestInstalaAVersaoPublicada(t *testing.T) {
	conteudo := []byte("binário novo, de mentira")
	a, destino := atualizadorDeTeste(t, servidorFalso(t, conteudo, false))

	v, err := a.Ultima(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Tag != "v9.9.9" {
		t.Fatalf("tag = %q", v.Tag)
	}

	var ultimoBaixado int64
	caminho, err := a.Instalar(context.Background(), v, func(baixado, total int64) { ultimoBaixado = baixado })
	if err != nil {
		t.Fatal(err)
	}
	if caminho != destino {
		t.Errorf("gravou em %q, esperava %q", caminho, destino)
	}

	gravado, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	if string(gravado) != string(conteudo) {
		t.Errorf("conteúdo gravado = %q", gravado)
	}
	if ultimoBaixado != int64(len(conteudo)) {
		t.Errorf("progresso relatou %d bytes, esperava %d", ultimoBaixado, len(conteudo))
	}

	info, err := os.Stat(destino)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("o binário instalado não é executável: %v", info.Mode())
	}
}

// Um binário é executado com as permissões de quem o roda: checksum que não
// bate significa não instalar, e não instalar significa manter o antigo.
func TestChecksumQueNaoBateNaoInstala(t *testing.T) {
	a, destino := atualizadorDeTeste(t, servidorFalso(t, []byte("conteúdo"), true))

	v, err := a.Ultima(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Instalar(context.Background(), v, nil); err == nil {
		t.Fatal("instalou um binário com checksum errado")
	}

	antigo, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	if string(antigo) != "binário antigo" {
		t.Errorf("o binário antigo foi tocado: %q", antigo)
	}

	// E não pode sobrar lixo no diretório.
	entradas, err := os.ReadDir(filepath.Dir(destino))
	if err != nil {
		t.Fatal(err)
	}
	if len(entradas) != 1 {
		nomes := make([]string, 0, len(entradas))
		for _, e := range entradas {
			nomes = append(nomes, e.Name())
		}
		t.Errorf("sobraram arquivos: %v", nomes)
	}
}

func TestPlataformaSemBinarioPublicado(t *testing.T) {
	a, _ := atualizadorDeTeste(t, servidorFalso(t, []byte("x"), false))
	a.Arch = "riscv64"

	v, _ := a.Ultima(context.Background())
	_, err := a.Instalar(context.Background(), v, nil)
	if err == nil || !strings.Contains(err.Error(), "devm-linux-riscv64") {
		t.Errorf("erro = %v, esperava menção à plataforma", err)
	}
}

func TestNomeDoArquivoPorPlataforma(t *testing.T) {
	a := &Atualizador{SO: "darwin", Arch: "arm64"}
	if nome := a.NomeDoArquivo(); nome != "devm-darwin-arm64" {
		t.Errorf("nome = %q", nome)
	}
}

func TestSomaDe(t *testing.T) {
	conteudo := "abc123  devm-linux-amd64\ndef456 *devm-darwin-arm64\n"

	if soma, err := SomaDe(conteudo, "devm-linux-amd64"); err != nil || soma != "abc123" {
		t.Errorf("soma = %q, err = %v", soma, err)
	}
	// O sha256sum em modo binário prefixa o nome com "*".
	if soma, err := SomaDe(conteudo, "devm-darwin-arm64"); err != nil || soma != "def456" {
		t.Errorf("soma = %q, err = %v", soma, err)
	}
	if _, err := SomaDe(conteudo, "devm-windows-amd64"); err == nil {
		t.Error("achou soma de arquivo que não está na lista")
	}
}

// O binário costuma estar atrás de um link (~/.local/bin/devm → …). Gravar
// por cima do link o transformaria num arquivo comum, e a próxima
// compilação não apareceria mais para quem usa o comando.
func TestLinkSimbolicoEResolvido(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "devm-real")
	if err := os.WriteFile(real, []byte("antigo"), 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "devm")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("sem suporte a symlink: %v", err)
	}

	// O esperado também passa pelo EvalSymlinks: no macOS o próprio /var é
	// um link para /private/var, e comparar com o caminho cru faria o teste
	// falhar lá por um motivo que não é o do teste.
	esperado, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	if obtido := resolverLink(link); obtido != esperado {
		t.Errorf("resolveu para %q, esperava %q", obtido, esperado)
	}

	// O arquivo de verdade resolve para ele mesmo.
	if obtido := resolverLink(real); obtido != esperado {
		t.Errorf("mexeu num caminho que já era o final: %q", obtido)
	}
}
