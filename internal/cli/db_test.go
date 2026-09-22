package cli

import (
	"bytes"
	"strings"
	"testing"
)

// Apagar o banco é irreversível: só um "sim" explícito passa.
func TestConfirmacaoExigeSimExplicito(t *testing.T) {
	casos := map[string]bool{
		"s\n":   true,
		"sim\n": true,
		"S\n":   true,
		"n\n":   false,
		"\n":    false,
		"y\n":   false, // o prompt é em português; "y" não é resposta daqui
		"":      false, // stdin fechado
	}

	for entrada, esperado := range casos {
		var saida bytes.Buffer
		stdio := IO{In: strings.NewReader(entrada), Out: &saida, Err: &saida}

		if obtido := confirmado(stdio); obtido != esperado {
			t.Errorf("resposta %q → %v, esperava %v", entrada, obtido, esperado)
		}
		if !strings.Contains(saida.String(), "continuar?") {
			t.Errorf("resposta %q: a pergunta não apareceu", entrada)
		}
	}
}

// Sem terminal não há como confirmar, e o padrão tem de ser NÃO apagar.
func TestSemEntradaNaoConfirma(t *testing.T) {
	var saida bytes.Buffer
	if confirmado(IO{Out: &saida}) {
		t.Error("confirmou sem ninguém ter respondido")
	}
}

func TestTamanhoLegivel(t *testing.T) {
	casos := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KB",
		493831:     "482.3 KB",
		5368709120: "5.0 GB",
	}

	for bytes, esperado := range casos {
		if obtido := tamanhoLegivel(bytes); obtido != esperado {
			t.Errorf("%d → %q, esperava %q", bytes, obtido, esperado)
		}
	}
}
