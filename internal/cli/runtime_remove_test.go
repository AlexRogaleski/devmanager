package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// phpFalsoInstalado escreve uma instalação de PHP no lugar onde o
// StaticProvider procura, dentro do HOME isolado do teste.
func phpFalsoInstalado(t *testing.T, versao string) string {
	t.Helper()

	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		t.Fatal("chame ambienteIsolado antes")
	}

	dir := filepath.Join(data, "devmanager", "runtimes", "php", versao, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "php")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '"+versao+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fixarPHP grava a versão no devmanager.yaml do projeto.
//
// Os testes de remoção fixam uma versão que não existe no mundo real
// (8.3.99) de propósito. Esvaziar o PATH não isola nada: o SystemProvider
// também varre /usr/bin e /usr/local/bin, então o PHP da máquina que roda o
// teste apareceria e satisfaria um "^8.3" — e a recusa que o teste quer
// observar nunca aconteceria. Uma versão impossível é o que torna o teste
// independente da máquina.
func fixarPHP(t *testing.T, dir, versao string) {
	t.Helper()

	caminho := filepath.Join(dir, "devmanager.yaml")
	if err := os.WriteFile(caminho, []byte("php: \""+versao+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPhpRemoveRecusaQuandoOProjetoFicaSemVersao(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	raiz := ambienteIsolado(t)

	dir := projetoLaravel(t, raiz, "minha-api")
	fixarPHP(t, dir, "8.3.99")
	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}
	phpFalsoInstalado(t, "8.3.99")

	saida, err := rodar(t, "php", "remove", "8.3.99")
	if err == nil {
		t.Fatalf("remover a única versão que serve deveria falhar:\n%s", saida)
	}
	if !strings.Contains(err.Error(), "minha-api") {
		t.Errorf("o erro não diz qual projeto quebraria: %v", err)
	}

	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"),
		"devmanager", "runtimes", "php", "8.3.99")); err != nil {
		t.Errorf("a versão foi apagada apesar da recusa: %v", err)
	}
}

func TestPhpRemoveComForceApaga(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	raiz := ambienteIsolado(t)

	dir := projetoLaravel(t, raiz, "minha-api")
	fixarPHP(t, dir, "8.3.99")
	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}
	phpFalsoInstalado(t, "8.3.99")

	saida, err := rodar(t, "php", "remove", "8.3.99", "--force")
	if err != nil {
		t.Fatalf("--force deveria apagar assim mesmo: %v\n%s", err, saida)
	}
	if !strings.Contains(saida, "removido") {
		t.Errorf("saída não confirma a remoção:\n%s", saida)
	}
	if !strings.Contains(saida, "minha-api") {
		t.Errorf("--force deveria avisar qual projeto ficou sem versão:\n%s", saida)
	}

	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"),
		"devmanager", "runtimes", "php", "8.3.99")); !os.IsNotExist(err) {
		t.Errorf("a pasta continua lá depois do --force")
	}
}

// TestPhpRemoveNaoEscolheEntreVersoesAmbiguas: com 8.3.98 e 8.3.99 instalados,
// "8.3" casa com as duas. Apagar a errada não tem volta, então o comando para
// e pede a versão exata em vez de decidir sozinho.
func TestPhpRemoveNaoEscolheEntreVersoesAmbiguas(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	ambienteIsolado(t)
	phpFalsoInstalado(t, "8.3.98")
	phpFalsoInstalado(t, "8.3.99")

	saida, err := rodar(t, "php", "remove", "8.3")
	if err == nil {
		t.Fatalf("esperava recusa por ambiguidade:\n%s", saida)
	}
	if !strings.Contains(err.Error(), "mais de uma") {
		t.Errorf("mensagem não explica a ambiguidade: %v", err)
	}
	for _, v := range []string{"8.3.98", "8.3.99"} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("o erro não lista %s: %v", v, err)
		}
	}
}

func TestPhpRemoveSemProjetoAfetadoApaga(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	ambienteIsolado(t)
	phpFalsoInstalado(t, "8.1.99")

	saida, err := rodar(t, "php", "remove", "8.1.99")
	if err != nil {
		t.Fatalf("sem projeto dependendo, deveria apagar: %v\n%s", err, saida)
	}
	if !strings.Contains(saida, "liberados") {
		t.Errorf("saída não informa o espaço liberado:\n%s", saida)
	}
}

// TestPhpRemoveNaoEnxergaOPHPDoSistema: o SystemProvider não implementa
// Removedor, então um PHP da distro nem sequer entra na lista de candidatos.
// O comando não precisa de uma checagem extra para protegê-lo — a interface
// já o deixa de fora.
func TestPhpRemoveNaoEnxergaOPHPDoSistema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	raiz := ambienteIsolado(t)
	phpFalsoInstalado(t, "8.4.99")

	sistema := filepath.Join(raiz, "usr-bin")
	if err := os.MkdirAll(sistema, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(sistema, "php")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '8.2.99'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVMANAGER_PHP_DIRS", sistema)

	// O 8.2.99 aparece no `php list`...
	lista, err := rodar(t, "php", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lista, "8.2.99") {
		t.Fatalf("o PHP do sistema não foi detectado:\n%s", lista)
	}

	// ...mas não é candidato a remoção.
	saida, err := rodar(t, "php", "remove", "8.2.99")
	if err == nil {
		t.Fatalf("o PHP do sistema não é nosso para remover:\n%s", saida)
	}
	if !strings.Contains(err.Error(), "8.4.99") {
		t.Errorf("o erro deveria listar só as versões gerenciadas: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("o binário do sistema foi apagado: %v", err)
	}
}

func TestPhpListMostraTamanho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script de shell")
	}

	ambienteIsolado(t)
	phpFalsoInstalado(t, "8.4.99")

	saida, err := rodar(t, "php", "list")
	if err != nil {
		t.Fatalf("php list falhou: %v\n%s", err, saida)
	}
	if !strings.Contains(saida, "TAMANHO") {
		t.Errorf("a listagem não tem a coluna de tamanho:\n%s", saida)
	}
}
