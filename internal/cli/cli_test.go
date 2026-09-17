package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Aqui se paga a decisão de Run receber um io.Writer em vez de escrever
// direto em os.Stdout: um bytes.Buffer é um io.Writer, então o teste captura
// a saída em memória e a inspeciona, sem tocar no terminal nem em arquivos.
func rodar(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	err := Run(args, &buf)
	return buf.String(), err
}

func TestRunSemArgumentosMostraAjuda(t *testing.T) {
	saida, err := rodar(t)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(saida, "Uso:") {
		t.Errorf("saída não contém a ajuda:\n%s", saida)
	}
}

func TestRunVersion(t *testing.T) {
	saida, err := rodar(t, "version")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(saida, "devm") {
		t.Errorf("saída = %q, esperava conter \"devm\"", saida)
	}
}

func TestRunComandoDesconhecido(t *testing.T) {
	_, err := rodar(t, "banana")
	if err == nil {
		t.Fatal("esperava erro para comando desconhecido")
	}
	if !strings.Contains(err.Error(), "banana") {
		t.Errorf("erro = %v, esperava citar o comando inválido", err)
	}
}

func TestDetectSaidaLegivel(t *testing.T) {
	dir := laravelFalso(t)

	saida, err := rodar(t, "detect", dir)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	for _, esperado := range []string{"laravel", "^8.3", "^13.0", ".test"} {
		if !strings.Contains(saida, esperado) {
			t.Errorf("saída não contém %q:\n%s", esperado, saida)
		}
	}
}

// A flag precisa funcionar antes E depois do posicional — é o bug que o
// parseArgs corrige, e este teste impede que ele volte.
func TestDetectJSONEmQualquerOrdem(t *testing.T) {
	dir := laravelFalso(t)

	ordens := map[string][]string{
		"flag antes":  {"detect", "--json", dir},
		"flag depois": {"detect", dir, "--json"},
	}

	for nome, args := range ordens {
		t.Run(nome, func(t *testing.T) {
			saida, err := rodar(t, args...)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}

			// Validar que é JSON de verdade, e não só procurar por "{".
			var resultado map[string]any
			if err := json.Unmarshal([]byte(saida), &resultado); err != nil {
				t.Fatalf("saída não é JSON válido (%v):\n%s", err, saida)
			}
			if resultado["kind"] != "laravel" {
				t.Errorf("kind = %v, esperava \"laravel\"", resultado["kind"])
			}
		})
	}
}

func laravelFalso(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	arquivos := map[string]string{
		"composer.json": `{"require":{"php":"^8.3","laravel/framework":"^13.0"}}`,
		"artisan":       "#!/usr/bin/env php",
	}
	for nome, conteudo := range arquivos {
		if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
