// Package supervisor roda os processos de desenvolvimento de um projeto.
//
// Um projeto Laravel típico precisa de vários processos ao mesmo tempo: o
// servidor, o worker de filas, o bundler do frontend. Rodar cada um num
// terminal separado funciona, mas é exatamente o atrito que a ferramenta
// existe para eliminar — e ninguém lembra de matar todos quando termina.
//
// Este pacote é o ensaio do daemon: a mesma lógica de supervisão, rodando em
// primeiro plano. Quando o devmanagerd existir, ele reaproveita isto quase
// inteiro; o que muda é quem chama e para onde vão os logs.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/runner"
)

// Processo é um comando a manter rodando.
type Processo struct {
	Nome  string // rótulo curto que prefixa a saída: "serve", "vite"
	Linha string // linha de comando, como se escreveria no terminal
}

// EncerramentoPadrao é quanto esperamos entre o SIGTERM e o SIGKILL.
//
// Cinco segundos dão folga para um servidor fechar conexões e um bundler
// gravar o cache, sem deixar quem digitou Ctrl+C esperando a ferramenta.
const EncerramentoPadrao = 5 * time.Second

// Supervisor roda um conjunto de processos até que um deles termine ou até
// receber um sinal de encerramento.
type Supervisor struct {
	Processos []Processo

	// Ambiente são variáveis extras para todos os processos, no formato
	// "CHAVE=valor". É por aqui que as portas do ambiente chegam a eles.
	Ambiente []string

	// Runner fornece o ambiente do projeto: PHP correto, shim no PATH,
	// diretório de trabalho.
	Runner *runner.Runner

	Saida io.Writer
	Cores bool

	// SaidaDe, quando definido, dá a cada processo seu próprio destino em vez
	// do escritor com prefixo compartilhado.
	//
	// É o que o daemon precisa: ele não escreve num terminal, e quer as
	// linhas separadas por processo para guardar no anel de logs e servir
	// pela API. Num terminal, prefixar é a apresentação certa; numa API,
	// prefixar seria jogar fora a estrutura para o cliente ter que parseá-la
	// de volta.
	SaidaDe func(processo string) io.Writer

	// Encerramento é o prazo entre o pedido de parada e a força bruta.
	Encerramento time.Duration
}

// saida resolve o destino das mensagens do supervisor.
//
// O daemon não tem terminal: ele deixa Saida em nil e recebe as linhas dos
// processos por SaidaDe. Devolver io.Discard aqui evita ter que guardar cada
// Fprintf com um if — e escrever num io.Writer nil entraria em pânico.
func (s *Supervisor) saida() io.Writer {
	if s.Saida == nil {
		return io.Discard
	}
	return s.Saida
}

// resultado é o que uma goroutine de espera reporta quando um processo termina.
type resultado struct {
	nome string
	err  error
}

// Run inicia todos os processos e bloqueia até que a execução termine.
//
// O ciclo de vida é: inicia todos, espera o PRIMEIRO evento relevante
// (processo terminou, sinal recebido, contexto cancelado) e a partir dele
// encerra o restante ordenadamente.
//
// Encerrar tudo quando um processo cai é deliberado, e é o comportamento de
// qualquer executor de Procfile: se o servidor morreu, deixar o bundler
// rodando só produz a ilusão de que o ambiente está de pé.
func (s *Supervisor) Run(ctx context.Context) error {
	if len(s.Processos) == 0 {
		return errors.New("nenhum processo configurado")
	}

	prazo := s.Encerramento
	if prazo == 0 {
		prazo = EncerramentoPadrao
	}

	// O contexto derivado é o botão de desligar: cancelá-lo dispara o
	// encerramento de todos os processos de uma vez, porque cada exec.Cmd
	// foi montado com ele.
	ctx, desligar := context.WithCancel(ctx)
	defer desligar()

	largura := larguraDosNomes(s.Processos)

	// Um único mutex compartilhado por TODOS os escritores: é ele que impede
	// duas linhas de processos diferentes de se misturarem no terminal.
	var mu sync.Mutex

	var (
		iniciados  []*exec.Cmd
		limpezas   []func()
		escritores []*escritorComPrefixo
	)
	defer func() {
		for _, limpar := range limpezas {
			limpar()
		}
		for _, e := range escritores {
			e.Flush() // emite a última linha sem "\n", se houver
		}
	}()

	// Canal com buffer do tamanho do time: cada goroutine de espera escreve
	// uma vez e termina. Sem o buffer, uma goroutine ficaria bloqueada se
	// ninguém estivesse lendo — e vazaria.
	resultados := make(chan resultado, len(s.Processos))

	for i, p := range s.Processos {
		args, err := DividirComando(p.Linha)
		if err != nil {
			return fmt.Errorf("processo %q: %w", p.Nome, err)
		}

		var destino io.Writer
		if s.SaidaDe != nil {
			destino = s.SaidaDe(p.Nome)
		} else {
			esc := novoEscritor(s.saida(), &mu, p.Nome, largura, i, s.Cores)
			escritores = append(escritores, esc)
			destino = esc
		}

		cmd, limpar, err := s.montar(ctx, args, destino, prazo)
		if err != nil {
			desligar()
			esperarTodos(resultados, len(iniciados), prazo)
			return fmt.Errorf("processo %q: %w", p.Nome, err)
		}
		limpezas = append(limpezas, limpar)

		if err := cmd.Start(); err != nil {
			desligar()
			esperarTodos(resultados, len(iniciados), prazo)
			return fmt.Errorf("iniciando %q: %w", p.Nome, err)
		}
		iniciados = append(iniciados, cmd)

		fmt.Fprintf(s.saida(), "iniciado  %-*s  %s\n", largura, p.Nome, p.Linha)

		// Uma goroutine por processo, cuja única tarefa é esperar e reportar.
		// Wait() bloqueia, então ele precisa de uma linha de execução própria;
		// o canal traz o resultado de volta para o laço principal.
		nome := p.Nome
		go func(c *exec.Cmd) {
			resultados <- resultado{nome: nome, err: c.Wait()}
		}(cmd)
	}

	fmt.Fprintf(s.saida(), "\n%d processos rodando — Ctrl+C para encerrar\n\n", len(iniciados))

	sinais := make(chan os.Signal, 1)
	signal.Notify(sinais, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sinais)

	// select espera em vários canais ao mesmo tempo e segue pelo primeiro que
	// ficar pronto. É a construção que torna "espere por qualquer um destes
	// eventos" uma linha de código em vez de uma máquina de estados.
	var motivo string
	select {
	case r := <-resultados:
		motivo = descreverSaida(r)
		fmt.Fprintf(s.saida(), "\n%s\n", motivo)

	case sig := <-sinais:
		fmt.Fprintf(s.saida(), "\nrecebido %s, encerrando...\n", sig)

	case <-ctx.Done():
		fmt.Fprintln(s.saida(), "\nencerrando...")
	}

	desligar()
	restantes := len(iniciados) - 1
	if restantes < 0 {
		restantes = 0
	}
	esperarTodos(resultados, restantes, prazo+time.Second)

	fmt.Fprintln(s.saida(), "todos os processos encerrados")

	if motivo != "" {
		return &ProcessoTerminouError{Descricao: motivo}
	}
	return nil
}

// montar prepara um exec.Cmd com encerramento gracioso.
func (s *Supervisor) montar(ctx context.Context, args []string, saida io.Writer, prazo time.Duration) (*exec.Cmd, func(), error) {
	// O Runner empresta o ambiente do projeto, mas a saída é nossa: cada
	// processo escreve no seu escritor com prefixo, não no terminal direto.
	r := *s.Runner
	r.Stdin = nil // processos supervisionados não leem do terminal
	r.Stdout = saida
	r.Stderr = saida

	cmd, limpar, err := r.Comando(ctx, args[0], args[1:]...)
	if err != nil {
		return nil, nil, err
	}

	// As variáveis do ambiente entram DEPOIS das do Runner: o cmd.Env já
	// veio montado, e só acrescentamos o que é nosso.
	cmd.Env = append(cmd.Env, s.Ambiente...)

	isolarGrupo(cmd)

	// Cancel e WaitDelay (Go 1.20+) definem o que o cancelamento do contexto
	// faz com o processo. Por padrão seria um SIGKILL imediato — brutal
	// demais para um servidor: conexões abertas morreriam sem aviso.
	//
	// Aqui, cancelar manda SIGTERM para o GRUPO inteiro, e o WaitDelay dá o
	// prazo para saírem sozinhos. Só depois disso o Go aplica o SIGKILL.
	cmd.Cancel = func() error { return sinalizarGrupo(cmd, syscall.SIGTERM) }
	cmd.WaitDelay = prazo

	return cmd, limpar, nil
}

// esperarTodos drena os resultados restantes, com prazo máximo.
func esperarTodos(resultados <-chan resultado, quantos int, prazo time.Duration) {
	// time.After devolve um canal que recebe um valor depois do prazo. Usado
	// dentro de um select, é o jeito idiomático de pôr limite numa espera.
	limite := time.After(prazo)

	for i := 0; i < quantos; i++ {
		select {
		case <-resultados:
		case <-limite:
			return // algo não respondeu a tempo; não vamos travar por isso
		}
	}
}

func descreverSaida(r resultado) string {
	if r.err == nil {
		return fmt.Sprintf("processo %q terminou normalmente", r.nome)
	}

	var saida *exec.ExitError
	if errors.As(r.err, &saida) {
		return fmt.Sprintf("processo %q saiu com código %d", r.nome, saida.ExitCode())
	}
	return fmt.Sprintf("processo %q falhou: %v", r.nome, r.err)
}

func larguraDosNomes(ps []Processo) int {
	largura := 0
	for _, p := range ps {
		if len(p.Nome) > largura {
			largura = len(p.Nome)
		}
	}
	return largura
}

// ProcessoTerminouError indica que a execução parou porque um processo saiu.
//
// Tipo próprio, e não erro genérico, porque a CLI precisa distinguir isto de
// uma falha de configuração: sair porque o servidor caiu merece código de
// saída diferente de sair porque o Ctrl+C foi apertado.
type ProcessoTerminouError struct {
	Descricao string
}

func (e *ProcessoTerminouError) Error() string { return e.Descricao }
