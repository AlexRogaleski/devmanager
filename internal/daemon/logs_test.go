package daemon

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAnelGuardaEDevolveHistorico(t *testing.T) {
	a := NovoAnel(10)
	agora := time.Now()

	a.Escrever("serve", "primeira", agora)
	a.Escrever("vite", "segunda", agora)

	h := a.Historico()
	if len(h) != 2 {
		t.Fatalf("histórico com %d linhas, esperava 2", len(h))
	}
	if h[0].Processo != "serve" || h[0].Texto != "primeira" {
		t.Errorf("linha 0 = %+v", h[0])
	}
}

// Ao encher, o anel descarta as mais ANTIGAS — é o que garante que um
// queue:work falante não consuma memória indefinidamente.
func TestAnelDescartaAsMaisAntigas(t *testing.T) {
	a := NovoAnel(3)
	agora := time.Now()

	for _, texto := range []string{"1", "2", "3", "4", "5"} {
		a.Escrever("p", texto, agora)
	}

	h := a.Historico()
	if len(h) != 3 {
		t.Fatalf("esperava 3 linhas, veio %d", len(h))
	}
	if h[0].Texto != "3" || h[2].Texto != "5" {
		t.Errorf("guardou as linhas erradas: %v", textos(h))
	}
}

// Historico devolve CÓPIA: o slice interno vazando permitiria leitura
// concorrente com nossa escrita, o que é uma corrida de dados.
func TestHistoricoEhCopia(t *testing.T) {
	a := NovoAnel(5)
	a.Escrever("p", "original", time.Now())

	h := a.Historico()
	h[0].Texto = "modificado"

	if a.Historico()[0].Texto != "original" {
		t.Error("alterar o retorno de Historico afetou o anel")
	}
}

func TestAnelNotificaInscritos(t *testing.T) {
	a := NovoAnel(10)
	novas, cancelar := a.Inscrever(4)
	defer cancelar()

	a.Escrever("serve", "olá", time.Now())

	select {
	case linha := <-novas:
		if linha.Texto != "olá" {
			t.Errorf("linha = %+v", linha)
		}
	case <-time.After(time.Second):
		t.Fatal("o inscrito não recebeu a linha")
	}
}

// A garantia mais importante do anel: um cliente lento acompanhando logs
// JAMAIS pode travar o processo que está escrevendo.
func TestInscritoLentoNaoTravaAEscrita(t *testing.T) {
	a := NovoAnel(10)

	// Buffer de 1 e ninguém lendo: enche na primeira linha.
	_, cancelar := a.Inscrever(1)
	defer cancelar()

	pronto := make(chan struct{})
	go func() {
		defer close(pronto)
		for i := 0; i < 1000; i++ {
			a.Escrever("p", "linha", time.Now())
		}
	}()

	select {
	case <-pronto:
	case <-time.After(3 * time.Second):
		t.Fatal("a escrita travou por causa de um inscrito que não lê")
	}

	// E o histórico continua íntegro para quem pedir depois.
	if len(a.Historico()) != 10 {
		t.Errorf("histórico = %d linhas, esperava 10", len(a.Historico()))
	}
}

// Sem cancelar, o anel guardaria o canal para sempre — vazamento a cada
// cliente que desconecta.
func TestCancelarInscricaoRemoveOCanal(t *testing.T) {
	a := NovoAnel(10)
	novas, cancelar := a.Inscrever(4)

	cancelar()

	if _, aberto := <-novas; aberto {
		t.Error("o canal deveria ter sido fechado")
	}

	a.mu.Lock()
	restantes := len(a.inscritos)
	a.mu.Unlock()
	if restantes != 0 {
		t.Errorf("sobraram %d inscritos", restantes)
	}

	// Cancelar duas vezes não pode entrar em pânico fechando canal fechado.
	cancelar()
}

func TestFecharEncerraTodos(t *testing.T) {
	a := NovoAnel(10)
	a1, _ := a.Inscrever(2)
	a2, _ := a.Inscrever(2)

	a.Fechar()

	for i, ch := range []<-chan Linha{a1, a2} {
		if _, aberto := <-ch; aberto {
			t.Errorf("canal %d não foi fechado", i)
		}
	}
}

// O supervisor entrega bytes como o processo os produziu: "Server ru" numa
// chamada, "nning\n" na outra.
func TestEscritorJuntaEscritasParciais(t *testing.T) {
	a := NovoAnel(10)
	w := a.Escritor("serve")

	w.Write([]byte("Server ru"))
	if len(a.Historico()) != 0 {
		t.Error("emitiu linha incompleta")
	}

	w.Write([]byte("nning\n"))

	h := a.Historico()
	if len(h) != 1 || h[0].Texto != "Server running" {
		t.Errorf("histórico = %v", textos(h))
	}
	if h[0].Processo != "serve" {
		t.Errorf("Processo = %q", h[0].Processo)
	}
}

func TestEscritorVariasLinhasEDescartaCR(t *testing.T) {
	a := NovoAnel(10)
	w := a.Escritor("vite")

	w.Write([]byte("um\r\ndois\ntrês\n"))

	esperado := []string{"um", "dois", "três"}
	got := textos(a.Historico())
	if strings.Join(got, ",") != strings.Join(esperado, ",") {
		t.Errorf("= %v, esperava %v", got, esperado)
	}
}

// Devolver menos que len(p) faria o io.Copy do os/exec abortar com
// ErrShortWrite, matando a captura de saída do processo.
func TestEscritorReportaTudoConsumido(t *testing.T) {
	a := NovoAnel(10)
	w := a.Escritor("p")

	entrada := []byte("sem quebra de linha")
	n, err := w.Write(entrada)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(entrada) {
		t.Errorf("Write devolveu %d, esperava %d", n, len(entrada))
	}
}

func TestEscritoresConcorrentes(t *testing.T) {
	a := NovoAnel(1000)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w := a.Escritor("proc")
			for j := 0; j < 50; j++ {
				w.Write([]byte("linha\n"))
			}
		}(i)
	}
	wg.Wait()

	if len(a.Historico()) != 400 {
		t.Errorf("histórico = %d linhas, esperava 400", len(a.Historico()))
	}
}

func textos(linhas []Linha) []string {
	out := make([]string, len(linhas))
	for i, l := range linhas {
		out[i] = l.Texto
	}
	return out
}
