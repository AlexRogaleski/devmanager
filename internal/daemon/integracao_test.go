package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/daemon"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/registry"
)

// cenario monta um Dev Manager completo e isolado: HOME próprio, PHP falso,
// projeto registrado e daemon rodando num socket temporário.
//
// O teste está no pacote daemon_test (externo) de propósito: ele exercita o
// daemon pela API pública, do mesmo jeito que a CLI e uma futura GUI fariam.
// Se algo aqui precisasse de acesso interno, seria sinal de que a API está
// incompleta.
type cenario struct {
	cliente *client.Cliente
	projeto string
	dir     string
}

func montarCenario(t *testing.T, devmanagerYAML string) *cenario {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("o processo de teste é um script de shell")
	}

	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))

	// PHP falso: o daemon resolve um runtime antes de subir qualquer coisa,
	// e não queremos depender do PHP da máquina que roda os testes.
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "php"),
		[]byte("#!/bin/sh\nprintf '8.4.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVMANAGER_PHP_DIRS", binDir)

	// Projeto mínimo, com processo próprio declarado para não precisar de
	// artisan nem de composer.
	projDir := filepath.Join(base, "projeto")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A versão é fixada no cenário para que o PHP falso seja escolhido de
	// forma determinística. Sem constraint, o Best() pega a MAIOR versão
	// disponível — e o PHP do sistema da máquina de teste venceria,
	// tornando o resultado dependente de onde a suíte roda.
	arquivos := map[string]string{
		"composer.json":   `{"require":{}}`,
		"devmanager.yaml": "php: \"8.4\"\n\n" + devmanagerYAML,
	}
	for nome, conteudo := range arquivos {
		if err := os.WriteFile(filepath.Join(projDir, nome), []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dataDir, err := paths.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Carregar(registry.Path(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Adicionar(registry.Projeto{Nome: "teste", Caminho: projDir}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Salvar(); err != nil {
		t.Fatal(err)
	}

	// Socket em /tmp, não em t.TempDir(): caminho de socket Unix tem limite
	// de ~108 bytes, e o diretório de teste do Go já é longo o bastante para
	// estourar isso quando o nome do teste é grande.
	sockDir, err := os.MkdirTemp("", "dvm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "d.sock")

	s := daemon.NovoServidor(socket, "teste", nil)
	if err := s.Escutar(); err != nil {
		t.Fatal(err)
	}

	ctx, parar := context.WithCancel(context.Background())
	encerrado := make(chan struct{})
	go func() {
		defer close(encerrado)
		_ = s.Servir(ctx)
	}()

	t.Cleanup(func() {
		parar()
		select {
		case <-encerrado:
		case <-time.After(30 * time.Second):
			t.Error("o daemon não encerrou a tempo")
		}
	})

	c := client.Novo(socket)
	esperarDaemon(t, c)

	return &cenario{cliente: c, projeto: "teste", dir: projDir}
}

func esperarDaemon(t *testing.T, c *client.Cliente) {
	t.Helper()

	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if c.Rodando(context.Background()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("o daemon não respondeu")
}

const processoLongo = "processes:\n  trabalho: sh -c \"while true; do echo trabalhando; sleep 0.2; done\"\n"

func TestSaude(t *testing.T) {
	c := montarCenario(t, processoLongo).cliente

	saude, err := c.Saude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !saude.OK {
		t.Error("OK = false")
	}
	if saude.APIVersao != daemon.Versao {
		t.Errorf("APIVersao = %q, esperava %q", saude.APIVersao, daemon.Versao)
	}
	if saude.PID != os.Getpid() {
		t.Errorf("PID = %d, esperava %d", saude.PID, os.Getpid())
	}
}

func TestCicloCompleto(t *testing.T) {
	cen := montarCenario(t, processoLongo)
	ctx := context.Background()

	if lista, err := cen.cliente.Listar(ctx); err != nil {
		t.Fatal(err)
	} else if len(lista) != 0 {
		t.Fatalf("esperava nenhum ambiente, veio %d", len(lista))
	}

	amb, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{})
	if err != nil {
		t.Fatalf("Start falhou: %v", err)
	}
	if amb.Projeto != "teste" {
		t.Errorf("Projeto = %q", amb.Projeto)
	}
	if amb.PHP != "8.4.0" {
		t.Errorf("PHP = %q, esperava 8.4.0 (do binário falso)", amb.PHP)
	}
	if !amb.Rodando() {
		t.Errorf("ambiente deveria estar rodando: %+v", amb.Processos)
	}

	lista, err := cen.cliente.Listar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 {
		t.Fatalf("esperava 1 ambiente, veio %d", len(lista))
	}

	// Os logs precisam conter a saída do processo, prova de que o SaidaDe do
	// supervisor está ligado ao anel.
	esperarLog(t, cen, "trabalhando")

	if _, err := cen.cliente.Stop(ctx, cen.projeto); err != nil {
		t.Fatalf("Stop falhou: %v", err)
	}

	if lista, err := cen.cliente.Listar(ctx); err != nil {
		t.Fatal(err)
	} else if len(lista) != 0 {
		t.Errorf("o ambiente continua listado depois do stop: %+v", lista)
	}
}

// Subir algo que já está de pé é a intenção satisfeita, não uma falha.
func TestStartDuasVezes(t *testing.T) {
	cen := montarCenario(t, processoLongo)
	ctx := context.Background()

	primeiro, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{})
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{})
	if err != nil {
		t.Fatalf("segundo Start falhou: %v", err)
	}
	if !primeiro.DesdeQue.Equal(segundo.DesdeQue) {
		t.Error("o ambiente foi recriado em vez de reaproveitado")
	}

	t.Cleanup(func() { _, _ = cen.cliente.Stop(ctx, cen.projeto) })
}

func TestStartDeProjetoNaoRegistrado(t *testing.T) {
	cen := montarCenario(t, processoLongo)

	_, err := cen.cliente.Start(context.Background(), "inexistente", daemon.PedidoStart{})
	if err == nil {
		t.Fatal("esperava erro")
	}
	if !strings.Contains(err.Error(), "devm add") {
		t.Errorf("a mensagem deveria orientar: %v", err)
	}
}

func TestStopDeAmbienteParado(t *testing.T) {
	cen := montarCenario(t, processoLongo)

	_, err := cen.cliente.Stop(context.Background(), cen.projeto)
	if err == nil {
		t.Fatal("esperava erro ao parar ambiente que não está rodando")
	}
}

func TestPortaFixada(t *testing.T) {
	cen := montarCenario(t, "processes:\n  eco: sh -c \"sleep 5\"\n")
	ctx := context.Background()

	amb, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{Porta: 45678})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cen.cliente.Stop(ctx, cen.projeto) })

	// Sem processo de servidor, a porta não é reportada — reportá-la
	// sugeriria um endereço que ninguém está atendendo.
	if amb.Porta != 0 {
		t.Errorf("Porta = %d, esperava 0 para projeto sem servidor", amb.Porta)
	}
}

func TestApenasFiltraProcessos(t *testing.T) {
	cen := montarCenario(t,
		"processes:\n  um: sh -c \"sleep 5\"\n  dois: sh -c \"sleep 5\"\n")
	ctx := context.Background()

	amb, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{Apenas: []string{"um"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cen.cliente.Stop(ctx, cen.projeto) })

	if len(amb.Processos) != 1 || amb.Processos[0].Nome != "um" {
		t.Errorf("processos = %+v, esperava só \"um\"", amb.Processos)
	}
}

func TestApenasComProcessoInexistente(t *testing.T) {
	cen := montarCenario(t, processoLongo)

	_, err := cen.cliente.Start(context.Background(), cen.projeto,
		daemon.PedidoStart{Apenas: []string{"nao-existe"}})
	if err == nil {
		t.Fatal("esperava erro")
	}
}

// O ambiente marca o erro quando um processo morre sozinho — é o que o
// `devm ps` mostra como "parado" em vez de fingir que está de pé.
func TestAmbienteRegistraMorteDeProcesso(t *testing.T) {
	cen := montarCenario(t, "processes:\n  curto: sh -c \"exit 3\"\n")
	ctx := context.Background()

	if _, err := cen.cliente.Start(ctx, cen.projeto, daemon.PedidoStart{}); err != nil {
		t.Fatal(err)
	}

	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		lista, err := cen.cliente.Listar(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(lista) == 1 && !lista[0].Rodando() {
			if lista[0].Erro == "" {
				t.Error("o ambiente parou sem registrar o motivo")
			}
			if !strings.Contains(lista[0].Erro, "curto") {
				t.Errorf("Erro = %q, deveria citar o processo", lista[0].Erro)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("o ambiente não foi marcado como parado")
}

// Segundo daemon no mesmo socket precisa recusar, não sobrescrever.
func TestSegundoDaemonRecusa(t *testing.T) {
	cen := montarCenario(t, processoLongo)

	outro := daemon.NovoServidor(cen.cliente.Socket, "teste", nil)
	err := outro.Escutar()
	if err == nil {
		t.Fatal("o segundo daemon deveria ter recusado")
	}
	if !strings.Contains(err.Error(), "já existe um daemon") {
		t.Errorf("erro = %v", err)
	}
}

// Socket órfão de um daemon morto tem que ser removido, não bloquear.
func TestSocketOrfaoEhRemovido(t *testing.T) {
	dir, err := os.MkdirTemp("", "dvm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	socket := filepath.Join(dir, "d.sock")
	// Arquivo comum no lugar do socket: ninguém atende, é lixo.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	s := daemon.NovoServidor(socket, "teste", nil)
	if err := s.Escutar(); err != nil {
		t.Fatalf("deveria remover o órfão e subir: %v", err)
	}
	t.Cleanup(func() { _ = s.Encerrar() })
}

// O socket carrega uma API que executa comandos arbitrários: permissão
// frouxa seria execução remota de código para outros usuários da máquina.
func TestSocketTemPermissaoRestrita(t *testing.T) {
	cen := montarCenario(t, processoLongo)

	info, err := os.Stat(cen.cliente.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if modo := info.Mode().Perm(); modo != 0o600 {
		t.Errorf("permissão = %v, esperava 0600", modo)
	}
}

func TestClienteSemDaemon(t *testing.T) {
	c := client.Novo(filepath.Join(t.TempDir(), "nao-existe.sock"))

	if c.Rodando(context.Background()) {
		t.Error("Rodando devolveu true sem daemon")
	}

	_, err := c.Listar(context.Background())
	if err == nil {
		t.Fatal("esperava erro")
	}

	var semDaemon *client.SemDaemonError
	if !errors.As(err, &semDaemon) {
		t.Fatalf("erro = %T, esperava *client.SemDaemonError", err)
	}
	if !strings.Contains(err.Error(), "devm daemon start") {
		t.Errorf("a mensagem deveria orientar: %v", err)
	}
}

func esperarLog(t *testing.T, cen *cenario, trecho string) {
	t.Helper()

	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		var encontrou bool
		err := cen.cliente.Logs(context.Background(), cen.projeto, false, func(l daemon.Linha) error {
			if strings.Contains(l.Texto, trecho) {
				encontrou = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if encontrou {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("o log não conteve %q", trecho)
}
