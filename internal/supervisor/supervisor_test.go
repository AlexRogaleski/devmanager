package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// saidaSegura é um bytes.Buffer que pode ser lido enquanto as goroutines dos
// processos ainda escrevem nele. Sem o mutex, o teste dispararia o detector
// de corrida — o mesmo problema real que o escritorComPrefixo resolve.
type saidaSegura struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *saidaSegura) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *saidaSegura) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func runnerDeTeste(t *testing.T) *runner.Runner {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "php-falso")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho php-falso\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return &runner.Runner{
		Runtime: runtimes.Runtime{
			Language: "php",
			Version:  semver.MustParse("8.3.15"),
			Bin:      bin,
			Source:   "teste",
		},
		Dir:     dir,
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}
}

func supervisorDeTeste(t *testing.T, saida *saidaSegura, procs ...Processo) *Supervisor {
	t.Helper()
	return &Supervisor{
		Processos:    procs,
		Runner:       runnerDeTeste(t),
		Saida:        saida,
		Encerramento: 2 * time.Second,
	}
}

func TestSupervisorSemProcessos(t *testing.T) {
	s := supervisorDeTeste(t, &saidaSegura{})
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("esperava erro sem processos configurados")
	}
}

// Quando um processo termina, todos os outros são encerrados. É o
// comportamento de qualquer executor de Procfile: se o servidor caiu, deixar
// o bundler rodando só finge que o ambiente está de pé.
func TestSupervisorEncerraTudoQuandoUmTermina(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os comandos do teste são scripts de shell")
	}

	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida,
		Processo{Nome: "curto", Linha: `sh -c "sleep 0.2; exit 7"`},
		Processo{Nome: "longo", Linha: `sh -c "while true; do sleep 0.1; done"`},
	)

	inicio := time.Now()
	err := s.Run(context.Background())
	decorrido := time.Since(inicio)

	var terminou *ProcessoTerminouError
	if !errors.As(err, &terminou) {
		t.Fatalf("erro = %T (%v), esperava *ProcessoTerminouError", err, err)
	}
	if !strings.Contains(terminou.Descricao, "curto") || !strings.Contains(terminou.Descricao, "7") {
		t.Errorf("descrição não identifica quem saiu nem o código: %q", terminou.Descricao)
	}

	// O "longo" rodaria para sempre; se voltamos rápido, ele foi encerrado.
	if decorrido > 4*time.Second {
		t.Errorf("demorou %v — o processo longo não foi encerrado a tempo", decorrido)
	}
	if !strings.Contains(saida.String(), "todos os processos encerrados") {
		t.Errorf("não reportou o encerramento:\n%s", saida.String())
	}
}

// Cancelar o contexto é o que o daemon vai usar para derrubar um ambiente.
func TestSupervisorRespeitaCancelamento(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os comandos do teste são scripts de shell")
	}

	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida,
		Processo{Nome: "a", Linha: `sh -c "while true; do sleep 0.1; done"`},
		Processo{Nome: "b", Linha: `sh -c "while true; do sleep 0.1; done"`},
	)

	ctx, cancelar := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancelar()
	}()

	inicio := time.Now()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("cancelamento não deveria virar erro: %v", err)
	}
	if decorrido := time.Since(inicio); decorrido > 4*time.Second {
		t.Errorf("demorou %v para encerrar após o cancelamento", decorrido)
	}
}

// A saída de cada processo precisa chegar prefixada e identificável.
func TestSupervisorPrefixaSaidaDeCadaProcesso(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os comandos do teste são scripts de shell")
	}

	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida,
		Processo{Nome: "alfa", Linha: `sh -c "echo mensagem-de-alfa"`},
		Processo{Nome: "beta", Linha: `sh -c "sleep 0.5; echo mensagem-de-beta"`},
	)

	_ = s.Run(context.Background())

	texto := saida.String()
	if !strings.Contains(texto, "alfa | mensagem-de-alfa") {
		t.Errorf("saída de alfa sem prefixo:\n%s", texto)
	}
}

func TestSupervisorComandoInvalido(t *testing.T) {
	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida, Processo{Nome: "ruim", Linha: `echo "aspas abertas`})

	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("esperava erro para linha de comando inválida")
	}
	if !strings.Contains(err.Error(), "ruim") {
		t.Errorf("o erro deveria identificar o processo: %v", err)
	}
}

func TestSupervisorComandoInexistente(t *testing.T) {
	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida,
		Processo{Nome: "fantasma", Linha: `comando-que-nao-existe-mesmo`},
	)

	if err := s.Run(context.Background()); err == nil {
		t.Fatal("esperava erro para comando inexistente")
	}
}

// Descendentes têm que morrer junto: matar só o processo direto deixaria o
// vite segurando a porta quando o npm é encerrado.
func TestSupervisorEncerraDescendentes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grupos de processo não se aplicam")
	}

	marcador := filepath.Join(t.TempDir(), "neto-vivo")

	saida := &saidaSegura{}
	s := supervisorDeTeste(t, saida,
		// O sh inicia um filho em background e espera: é a mesma forma do
		// "npm run dev" que dispara o vite.
		Processo{Nome: "pai", Linha: `sh -c "sh -c 'while true; do touch ` + marcador + `; sleep 0.1; done' & wait"`},
		Processo{Nome: "gatilho", Linha: `sh -c "sleep 1"`},
	)

	_ = s.Run(context.Background())

	// Após o encerramento, o neto não pode mais estar tocando o arquivo.
	if err := os.Remove(marcador); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	if _, err := os.Stat(marcador); err == nil {
		t.Error("o neto continuou vivo depois do encerramento")
	}
}

// Um worker de fila sai de propósito — `queue:work --max-time` recicla a
// memória, `queue:restart` reage a um deploy. Derrubar o servidor junto, que
// era o comportamento antigo, é desproporcional.
func TestProcessoQueSaiLimpoEReiniciado(t *testing.T) {
	dir := t.TempDir()
	marcas := filepath.Join(dir, "voltas.txt")

	var saida saidaSegura
	s := supervisorDeTeste(t, &saida,
		// Cada encarnação anota uma linha e sai com 0, como um worker que
		// cumpriu o ciclo.
		Processo{Nome: "queue", Linha: "sh -c 'echo volta >> " + marcas + "; sleep 0.2'"},
		Processo{Nome: "serve", Linha: "sleep 30"},
	)

	ctx, cancelar := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelar()

	err := s.Run(ctx)

	dados, errLeitura := os.ReadFile(marcas)
	if errLeitura != nil {
		t.Fatal(errLeitura)
	}
	voltas := strings.Count(string(dados), "volta")

	// A primeira execução mais os reinícios permitidos pelo freio.
	if voltas < 2 {
		t.Errorf("o processo rodou %d vez(es): não foi reiniciado", voltas)
	}
	if voltas > reiniciosSeguidos+1 {
		t.Errorf("o processo rodou %d vezes: o freio não segurou", voltas)
	}

	if !strings.Contains(saida.String(), "queue terminou limpo — reiniciando") {
		t.Errorf("a saída não conta o reinício:\n%s", saida.String())
	}

	// O freio parou o ciclo, e a mensagem final diz por quê.
	var terminou *ProcessoTerminouError
	if errors.As(err, &terminou) && !strings.Contains(terminou.Descricao, "já havia reiniciado") {
		t.Errorf("motivo pouco explicativo: %q", terminou.Descricao)
	}
}

// Já um processo que sai com ERRO continua derrubando o ambiente: é falha de
// verdade, e um bundler sozinho produz a ilusão de ambiente de pé.
func TestProcessoQueFalhaDerrubaOAmbiente(t *testing.T) {
	var saida saidaSegura
	s := supervisorDeTeste(t, &saida,
		Processo{Nome: "serve", Linha: "sh -c 'exit 3'"},
		Processo{Nome: "vite", Linha: "sleep 30"},
	)

	inicio := time.Now()
	err := s.Run(context.Background())

	if time.Since(inicio) > 5*time.Second {
		t.Error("o ambiente não caiu junto com o processo que falhou")
	}

	var terminou *ProcessoTerminouError
	if !errors.As(err, &terminou) {
		t.Fatalf("erro = %v, esperava ProcessoTerminouError", err)
	}
	if !strings.Contains(terminou.Descricao, "código 3") {
		t.Errorf("motivo = %q, esperava o código de saída", terminou.Descricao)
	}
	if strings.Contains(saida.String(), "reiniciando") {
		t.Error("processo que falhou não pode ser reiniciado")
	}
}

// O freio é por janela deslizante: reinícios espaçados não se acumulam.
func TestFreioContaSoOQueEstaNaJanela(t *testing.T) {
	historico := map[string][]time.Time{}

	for i := range reiniciosSeguidos {
		if !podeReiniciar(historico, "queue") {
			t.Fatalf("negou o reinício %d, dentro da cota", i+1)
		}
	}
	if podeReiniciar(historico, "queue") {
		t.Error("permitiu além da cota dentro da janela")
	}

	// Marcas antigas saem da conta.
	historico["queue"] = []time.Time{time.Now().Add(-2 * janelaDeReinicio)}
	if !podeReiniciar(historico, "queue") {
		t.Error("negou o reinício por causa de marcas fora da janela")
	}
}
