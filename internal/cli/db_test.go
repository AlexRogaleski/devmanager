package cli

import (
	"bytes"
	"flag"
	"io"
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

// O `devm upgrade` não pode trocar um binário recém-compilado pela release:
// o carimbo do git describe é justamente o sinal de que ele é mais novo.
func TestUpgradeReconheceBuildLocal(t *testing.T) {
	casos := map[string]situacaoDaVersao{
		"v0.5.0":              mesmaVersao,
		"v0.4.0":              temVersaoNova,
		"v0.5.0-2-gfa5f797":   versaoLocal,
		"v0.5.0-2-g123-dirty": versaoLocal,
		"dev":                 versaoLocal,
		" v0.5.0 ":            mesmaVersao,
	}

	for instalada, esperado := range casos {
		if obtido := compararVersoes(instalada, "v0.5.0"); obtido != esperado {
			t.Errorf("%q → %v, esperava %v", instalada, obtido, esperado)
		}
	}
}

// O segundo plano virou o padrão; --attach traz de volta o terminal.
func TestModoDoStart(t *testing.T) {
	casos := []struct {
		args       []string
		noTerminal bool
	}{
		{nil, false}, // padrão: segundo plano
		{[]string{"--attach"}, true},
		{[]string{"-a"}, true},
		{[]string{"--list"}, true}, // listar não sobe nada
		{[]string{"-d"}, false},    // hábito antigo: segue valendo
		{[]string{"--detach"}, false},
		{[]string{"-d", "--attach"}, true}, // o pedido explícito vence
	}

	for _, c := range casos {
		fs := flag.NewFlagSet("start", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		o := registrarFlagsDoStart(fs)

		if err := fs.Parse(c.args); err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if obtido := o.noTerminal(); obtido != c.noTerminal {
			t.Errorf("%v: noTerminal = %v, esperava %v", c.args, obtido, c.noTerminal)
		}
	}
}

// As opções de sempre continuam chegando onde devem.
func TestFlagsDoStartPreservadas(t *testing.T) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := registrarFlagsDoStart(fs)

	if err := fs.Parse([]string{"--only", "serve,queue", "--port", "8080", "--no-node"}); err != nil {
		t.Fatal(err)
	}
	if o.apenas != "serve,queue" || o.porta != 8080 || !o.semNode {
		t.Errorf("opções lidas: %+v", o)
	}
}
