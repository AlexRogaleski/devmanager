package shell

import (
	"os/exec"
	"runtime"
	"testing"
)

// O teste não confere o texto gerado: ele o entrega ao sh e confere o que o
// sh devolve. Um texto "parecido com o certo" que o shell lê de outro jeito é
// exatamente o defeito que esta função existe para evitar.
func TestAspasSobreviveAoShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("precisa de sh")
	}

	for _, valor := range []string{
		"simples",
		"com espaço",
		"it's",
		"''",
		`$HOME`,
		"`whoami`",
		`barra \ invertida`,
		"quebra\nde linha",
		"",
	} {
		saida, err := exec.Command("sh", "-c", "printf '%s' "+Aspas(valor)).Output()
		if err != nil {
			t.Fatalf("sh falhou para %q: %v", valor, err)
		}
		if string(saida) != valor {
			t.Errorf("Aspas(%q): o shell leu %q", valor, saida)
		}
	}
}
