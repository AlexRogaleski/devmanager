package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contextoDeTeste() Contexto {
	return Contexto{
		Projeto:    "meu-app",
		PHP:        "8.4.23",
		PHPOrigem:  "devmanager.yaml",
		Node:       "22.11.0",
		TemNode:    true,
		TemArtisan: true,
		Dominio:    "meu-app.test",
		Servicos: []Servico{
			{Nome: "mysql", Versao: "8.4", Portas: []int{3306}},
			{Nome: "mailpit", Versao: "latest", Portas: []int{35097, 36527}},
		},
	}
}

// O documento vale pelo que é ESPECÍFICO do projeto. Dizer "use o devm" sem
// os detalhes só transfere a dúvida para o assistente.
func TestDocumentoUsaOEstadoReal(t *testing.T) {
	doc := Documento(contextoDeTeste())

	for _, esperado := range []string{
		"8.4.23",          // a versão resolvida
		"devmanager.yaml", // de onde ela veio
		"22.11.0",         // o Node
		"meu-app.test",    // o domínio
		"mysql 8.4",       // os serviços
		"127.0.0.1:3306",  // com as portas reais
		"127.0.0.1:35097, 36527",
		"devm logs meu-app", // comandos com o nome do projeto
	} {
		if !strings.Contains(doc, esperado) {
			t.Errorf("o documento não menciona %q", esperado)
		}
	}
}

// A instrução mais importante: um projeto migrado tinha, no CLAUDE.md, uma
// linha mandando usar vendor/bin/sail. O documento precisa contradizê-la.
func TestDocumentoDesaconselhaOSail(t *testing.T) {
	doc := Documento(contextoDeTeste())

	if !strings.Contains(doc, "sail") {
		t.Error("o documento deveria mencionar o Sail para desaconselhá-lo")
	}
	if !strings.Contains(doc, "NÃO fazer") {
		t.Error("faltou a seção do que não fazer")
	}
}

func TestDocumentoSemNodeNemServicos(t *testing.T) {
	doc := Documento(Contexto{Projeto: "simples", PHP: "8.3.0", TemArtisan: true})

	if strings.Contains(doc, "Node") {
		t.Error("mencionou Node num projeto que não usa")
	}
	if strings.Contains(doc, "Serviços") {
		t.Error("mencionou serviços num projeto que não declara nenhum")
	}
	if !strings.Contains(doc, "devm artisan") {
		t.Error("deveria explicar o devm artisan mesmo sem Node nem serviços")
	}
}

// Projeto sem artisan não é Laravel: mandar rodar `devm artisan` confundiria.
func TestDocumentoSemArtisan(t *testing.T) {
	doc := Documento(Contexto{Projeto: "lib", PHP: "8.3.0"})

	if strings.Contains(doc, "devm artisan <comando>") {
		t.Error("ofereceu artisan num projeto que não tem")
	}
	if !strings.Contains(doc, "devm composer") {
		t.Error("o composer vale para qualquer projeto PHP")
	}
}

func TestEscreverCriaEEhIdempotente(t *testing.T) {
	dir := t.TempDir()
	c := contextoDeTeste()

	r, err := Escrever(dir, c, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Criado {
		t.Error("deveria reportar criação")
	}

	if _, err := os.Stat(filepath.Join(dir, Arquivo)); err != nil {
		t.Fatalf("arquivo não foi gravado: %v", err)
	}

	r, err = Escrever(dir, c, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.NoChange {
		t.Error("segunda execução deveria ser no-op")
	}
}

func TestEscreverDryRunNaoGrava(t *testing.T) {
	dir := t.TempDir()

	if _, err := Escrever(dir, contextoDeTeste(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, Arquivo)); !os.IsNotExist(err) {
		t.Error("dry run gravou o arquivo")
	}
}

// A garantia central desta abordagem: nada escrito à mão corre risco.
func TestEscreverNaoTocaEmOutrosArquivos(t *testing.T) {
	dir := t.TempDir()

	original := "# CLAUDE.md\n\nInstruções escritas à mão.\n"
	claude := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Escrever(dir, contextoDeTeste(), false); err != nil {
		t.Fatal(err)
	}

	depois, _ := os.ReadFile(claude)
	if string(depois) != original {
		t.Errorf("o CLAUDE.md foi alterado:\n%s", depois)
	}
}

func TestAlvosDeLigacao(t *testing.T) {
	dir := t.TempDir()

	// Sem arquivos de instrução, não há a quem ligar.
	if alvos := AlvosDeLigacao(dir); len(alvos) != 0 {
		t.Errorf("alvos = %v, esperava nenhum", alvos)
	}

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# oi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("já cita "+Arquivo+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	alvos := AlvosDeLigacao(dir)
	if len(alvos) != 1 || alvos[0] != "CLAUDE.md" {
		t.Errorf("alvos = %v, esperava só o CLAUDE.md (o AGENTS.md já aponta)", alvos)
	}
}

// A ligação é UMA linha no fim, sem tocar no resto.
func TestLigarAcrescentaUmaLinha(t *testing.T) {
	dir := t.TempDir()

	original := "# CLAUDE.md\n\nMinhas instruções.\n"
	claude := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Ligar(dir, false); err != nil {
		t.Fatal(err)
	}

	depois, _ := os.ReadFile(claude)
	if !strings.HasPrefix(string(depois), original) {
		t.Errorf("o conteúdo original não foi preservado no início:\n%s", depois)
	}
	if !strings.Contains(string(depois), Arquivo) {
		t.Errorf("a referência não foi acrescentada:\n%s", depois)
	}

	// Rodar de novo não pode duplicar a linha.
	if _, err := Ligar(dir, false); err != nil {
		t.Fatal(err)
	}
	depois2, _ := os.ReadFile(claude)
	if strings.Count(string(depois2), Arquivo) != 1 {
		t.Errorf("a linha foi duplicada:\n%s", depois2)
	}
}

func TestLigarDryRunNaoGrava(t *testing.T) {
	dir := t.TempDir()

	original := "# CLAUDE.md\n"
	claude := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	resultados, err := Ligar(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultados) != 1 {
		t.Errorf("deveria reportar o alvo mesmo em simulação: %v", resultados)
	}

	depois, _ := os.ReadFile(claude)
	if string(depois) != original {
		t.Error("dry run alterou o arquivo")
	}
}
