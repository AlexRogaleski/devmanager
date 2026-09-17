package supervisor

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// Escrita parcial é a regra, não a exceção: um processo escreve quando o
// buffer dele enche, não quando a linha acaba.
func TestPrefixoJuntaEscritasParciais(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	e := novoEscritor(&buf, &mu, "serve", 5, 0, false)

	e.Write([]byte("Server ru"))
	if buf.Len() != 0 {
		t.Errorf("emitiu linha incompleta: %q", buf.String())
	}

	e.Write([]byte("nning\n"))
	if got := buf.String(); got != "serve | Server running\n" {
		t.Errorf("= %q", got)
	}
}

func TestPrefixoVariasLinhasDeUmaVez(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	e := novoEscritor(&buf, &mu, "vite", 5, 0, false)
	e.Write([]byte("um\ndois\ntrês\n"))

	esperado := "vite  | um\nvite  | dois\nvite  | três\n"
	if got := buf.String(); got != esperado {
		t.Errorf("= %q, esperava %q", got, esperado)
	}
}

// Sem "\n" no fim, o resto ficaria preso no buffer para sempre.
func TestPrefixoFlushEmiteRestoSemQuebra(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	e := novoEscritor(&buf, &mu, "q", 1, 0, false)
	e.Write([]byte("sem quebra no fim"))
	e.Flush()

	if got := buf.String(); got != "q | sem quebra no fim\n" {
		t.Errorf("= %q", got)
	}
}

// Write precisa reportar ter consumido TUDO, mesmo guardando parte no buffer:
// devolver menos faz o io.Copy do os/exec abortar com ErrShortWrite.
func TestPrefixoReportaTudoConsumido(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	e := novoEscritor(&buf, &mu, "x", 1, 0, false)

	entrada := []byte("parcial sem quebra")
	n, err := e.Write(entrada)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(entrada) {
		t.Errorf("Write devolveu %d, esperava %d", n, len(entrada))
	}
}

// O mutex é compartilhado justamente para que linhas de processos diferentes
// nunca se misturem no terminal.
func TestPrefixoNaoEntrelacaLinhas(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	a := novoEscritor(&buf, &mu, "aaa", 3, 0, false)
	b := novoEscritor(&buf, &mu, "bbb", 3, 1, false)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); a.Write([]byte("linha-de-a\n")) }()
		go func() { defer wg.Done(); b.Write([]byte("linha-de-b\n")) }()
	}
	wg.Wait()

	for _, linha := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		okA := linha == "aaa | linha-de-a"
		okB := linha == "bbb | linha-de-b"
		if !okA && !okB {
			t.Fatalf("linha corrompida por entrelaçamento: %q", linha)
		}
	}
}

func TestPrefixoComCores(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex

	e := novoEscritor(&buf, &mu, "serve", 5, 0, true)
	e.Write([]byte("oi\n"))

	if !strings.Contains(buf.String(), "\033[") {
		t.Errorf("esperava código ANSI de cor: %q", buf.String())
	}
	if !strings.Contains(buf.String(), resetANSI) {
		t.Errorf("a cor não foi resetada, vazaria para o resto da linha: %q", buf.String())
	}
}
