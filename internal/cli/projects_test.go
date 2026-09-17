package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ambienteIsolado aponta o Dev Manager para um HOME temporário.
//
// paths.DataDir respeita XDG_DATA_HOME, então basta essa variável para que o
// registro, os runtimes e os shims fiquem todos num diretório descartável.
// Foi por isso que os caminhos foram centralizados num pacote só: torna o
// isolamento em teste uma linha.
func ambienteIsolado(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	return dir
}

// projetoLaravel cria um projeto mínimo no disco.
func projetoLaravel(t *testing.T, raiz, nome string) string {
	t.Helper()

	dir := filepath.Join(raiz, nome)
	arquivos := map[string]string{
		"composer.json":       `{"require":{"php":"^8.3","laravel/framework":"^12.0"}}`,
		"artisan":             "#!/usr/bin/env php",
		".env":                "APP_KEY=base64:x\n",
		"vendor/autoload.php": "<?php",
	}
	for caminho, conteudo := range arquivos {
		completo := filepath.Join(dir, caminho)
		if err := os.MkdirAll(filepath.Dir(completo), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(completo, []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAddEList(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "minha-api")

	saida, err := rodar(t, "add", dir)
	if err != nil {
		t.Fatalf("add falhou: %v\n%s", err, saida)
	}
	if !strings.Contains(saida, "minha-api") {
		t.Errorf("add não confirmou o nome:\n%s", saida)
	}

	saida, err = rodar(t, "list")
	if err != nil {
		t.Fatalf("list falhou: %v", err)
	}
	if !strings.Contains(saida, "minha-api") {
		t.Errorf("list não mostrou o projeto:\n%s", saida)
	}
}

func TestListVazio(t *testing.T) {
	ambienteIsolado(t)

	saida, err := rodar(t, "list")
	if err != nil {
		t.Fatalf("list falhou: %v", err)
	}
	if !strings.Contains(saida, "nenhum projeto") {
		t.Errorf("esperava mensagem de registro vazio:\n%s", saida)
	}
	// Registro vazio precisa ensinar o próximo passo, não só informar o vazio.
	if !strings.Contains(saida, "devm add") {
		t.Errorf("a mensagem deveria sugerir o próximo comando:\n%s", saida)
	}
}

func TestListJSON(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "api")

	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}

	saida, err := rodar(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json falhou: %v", err)
	}

	var estados []map[string]any
	if err := json.Unmarshal([]byte(saida), &estados); err != nil {
		t.Fatalf("saída não é JSON válido (%v):\n%s", err, saida)
	}
	if len(estados) != 1 {
		t.Fatalf("esperava 1 projeto, veio %d", len(estados))
	}
	if estados[0]["name"] != "api" {
		t.Errorf("name = %v", estados[0]["name"])
	}
	if estados[0]["exists"] != true {
		t.Errorf("exists = %v, esperava true", estados[0]["exists"])
	}
}

func TestAddDuplicadoFalha(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "api")

	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := rodar(t, "add", dir); err == nil {
		t.Fatal("esperava erro ao registrar o mesmo caminho duas vezes")
	}
}

func TestAddComNomeExplicito(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "api")

	saida, err := rodar(t, "add", "--name", "loja", dir)
	if err != nil {
		t.Fatalf("add falhou: %v", err)
	}
	if !strings.Contains(saida, "loja") {
		t.Errorf("nome explícito não foi usado:\n%s", saida)
	}
}

func TestRemove(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "api")

	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}

	saida, err := rodar(t, "remove", "api")
	if err != nil {
		t.Fatalf("remove falhou: %v", err)
	}
	// Deixar explícito que nada foi apagado do disco evita um susto real.
	if !strings.Contains(saida, "não foram tocados") {
		t.Errorf("remove deveria deixar claro que não apaga arquivos:\n%s", saida)
	}

	saida, _ = rodar(t, "list")
	if strings.Contains(saida, "api") {
		t.Errorf("projeto continua listado:\n%s", saida)
	}

	if _, err := rodar(t, "remove", "nao-existe"); err == nil {
		t.Error("remover projeto inexistente deveria falhar")
	}
}

func TestScan(t *testing.T) {
	raiz := ambienteIsolado(t)
	projetos := filepath.Join(raiz, "Projetos")

	projetoLaravel(t, filepath.Join(projetos, "Laravel"), "alfa")
	projetoLaravel(t, filepath.Join(projetos, "Laravel"), "beta")
	projetoLaravel(t, filepath.Join(projetos, "Outros"), "gama")

	// Uma pasta sem projeto nenhum não pode aparecer.
	if err := os.MkdirAll(filepath.Join(projetos, "Vazia", "nada"), 0o755); err != nil {
		t.Fatal(err)
	}

	saida, err := rodar(t, "scan", projetos)
	if err != nil {
		t.Fatalf("scan falhou: %v\n%s", err, saida)
	}
	for _, nome := range []string{"alfa", "beta", "gama"} {
		if !strings.Contains(saida, nome) {
			t.Errorf("scan não encontrou %q:\n%s", nome, saida)
		}
	}
	if !strings.Contains(saida, "3 novos") {
		t.Errorf("contagem errada:\n%s", saida)
	}

	// Rodar de novo não pode duplicar nada.
	saida, err = rodar(t, "scan", projetos)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida, "0 novos") {
		t.Errorf("scan deveria ser idempotente:\n%s", saida)
	}
}

// O scan não pode descer dentro de um projeto: vendor e node_modules têm
// composer.json aos milhares, e cada um viraria um "projeto".
func TestScanNaoDesceDentroDeProjeto(t *testing.T) {
	raiz := ambienteIsolado(t)
	projetos := filepath.Join(raiz, "Projetos")
	app := projetoLaravel(t, projetos, "app")

	dep := filepath.Join(app, "vendor", "acme", "lib")
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "composer.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	saida, err := rodar(t, "scan", projetos)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida, "1 novo") {
		t.Errorf("esperava só o projeto raiz:\n%s", saida)
	}
}

func TestScanDryRunNaoGrava(t *testing.T) {
	raiz := ambienteIsolado(t)
	projetos := filepath.Join(raiz, "Projetos")
	projetoLaravel(t, projetos, "app")

	if _, err := rodar(t, "scan", "--dry-run", projetos); err != nil {
		t.Fatal(err)
	}

	saida, _ := rodar(t, "list")
	if !strings.Contains(saida, "nenhum projeto") {
		t.Errorf("dry-run gravou o registro:\n%s", saida)
	}
}

func TestPrune(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "some-some")

	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	// Antes do prune, o projeto aparece como ausente — e não quebra a lista.
	saida, err := rodar(t, "list")
	if err != nil {
		t.Fatalf("list com projeto ausente falhou: %v", err)
	}
	if !strings.Contains(saida, "ausente") {
		t.Errorf("esperava estado 'ausente':\n%s", saida)
	}

	saida, err = rodar(t, "prune")
	if err != nil {
		t.Fatalf("prune falhou: %v", err)
	}
	if !strings.Contains(saida, "1 removido do registro") {
		t.Errorf("concordância ou contagem errada:\n%s", saida)
	}

	saida, _ = rodar(t, "list")
	if strings.Contains(saida, "some-some") {
		t.Errorf("prune não removeu:\n%s", saida)
	}
}

func TestPruneSemAusentes(t *testing.T) {
	raiz := ambienteIsolado(t)
	dir := projetoLaravel(t, raiz, "api")

	if _, err := rodar(t, "add", dir); err != nil {
		t.Fatal(err)
	}

	saida, err := rodar(t, "prune")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida, "nenhum projeto ausente") {
		t.Errorf("saída inesperada:\n%s", saida)
	}
}

func TestAddForaDeProjeto(t *testing.T) {
	raiz := ambienteIsolado(t)
	vazio := filepath.Join(raiz, "vazio")
	if err := os.MkdirAll(vazio, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := rodar(t, "add", vazio); err == nil {
		t.Fatal("esperava erro ao registrar uma pasta que não é projeto")
	}
}
