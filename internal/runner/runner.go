// Package runner executa comandos dentro do ambiente de um projeto.
//
// "Dentro do ambiente" significa que o comando enxerga o PHP escolhido pelo
// Dev Manager como se fosse o PHP do sistema — inclusive os subprocessos que
// ele criar. É essa transparência que faz `devm artisan` valer a pena em vez
// de simplesmente chamar o binário com o caminho completo.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
)

// Runner executa comandos com o runtime de um projeto.
//
// Os campos de I/O são interfaces, não os.Stdin/os.Stdout diretos: em produção
// recebem os arquivos do terminal; nos testes, buffers em memória.
type Runner struct {
	Runtime runtimes.Runtime // o PHP escolhido para este projeto
	Dir     string           // diretório de trabalho do comando
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer

	// Env sobrescreve o ambiente. Vazio usa o do processo atual.
	Env []string

	// ShimDir é o diretório de shim persistente do projeto. Vazio faz o
	// Runner criar um temporário e removê-lo no fim — útil em testes e em
	// execuções avulsas que não pertencem a nenhum projeto.
	ShimDir string
}

// Run executa um comando e espera ele terminar.
//
// O código de saída do filho é propagado num ExitError, para que
// `devm artisan migrate && deploy` se comporte como `php artisan migrate && deploy`.
func (r *Runner) Run(ctx context.Context, nome string, args ...string) error {
	// O shim coloca o PHP escolhido na frente do PATH, para toda a árvore de
	// processos. Ver EnsureShim em shim.go para o porquê.
	shim, limpar, err := r.prepararShim()
	if err != nil {
		return err
	}
	// defer roda quando a função retorna, por qualquer caminho: retorno normal,
	// erro no meio, ou panic. Aqui ele só tem efeito no shim temporário.
	defer limpar()

	caminho, err := resolverExecutavel(nome, shim, r.ambiente(shim))
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, caminho, args...)
	cmd.Dir = r.Dir
	cmd.Env = r.ambiente(shim)
	cmd.Stdin = r.Stdin
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("iniciando %s: %w", nome, err)
	}

	pararSinais := encaminharSinais(cmd)
	defer pararSinais()

	if err := cmd.Wait(); err != nil {
		// exec.ExitError significa "o processo rodou e saiu com código != 0".
		// Isso NÃO é falha nossa: `artisan migrate` pode legitimamente sair
		// com 1. Convertemos para ExitError nosso, sem mensagem de erro extra,
		// e a CLI só repassa o código.
		var saida *exec.ExitError
		if errors.As(err, &saida) {
			return &ExitError{Code: saida.ExitCode()}
		}
		return fmt.Errorf("executando %s: %w", nome, err)
	}
	return nil
}

// RunPHP executa o próprio interpretador, sem procurar nada no PATH.
func (r *Runner) RunPHP(ctx context.Context, args ...string) error {
	return r.Run(ctx, r.Runtime.Bin, args...)
}

// ambiente monta o ambiente do processo filho.
func (r *Runner) ambiente(shim string) []string {
	base := r.Env
	if base == nil {
		base = os.Environ()
	}

	saida := make([]string, 0, len(base)+1)
	prefixado := false

	for _, kv := range base {
		// Corta no primeiro "=" apenas: valores de variáveis podem conter "=".
		chave, valor, _ := strings.Cut(kv, "=")
		if chave == "PATH" {
			kv = "PATH=" + shim + string(os.PathListSeparator) + valor
			prefixado = true
		}
		saida = append(saida, kv)
	}

	if !prefixado {
		saida = append(saida, "PATH="+shim)
	}

	// Marca o ambiente para que scripts consigam detectar que estão rodando
	// sob o Dev Manager, e para depuração.
	saida = append(saida, "DEVMANAGER=1", "DEVMANAGER_PHP="+r.Runtime.Bin)
	return saida
}

// prepararShim devolve o diretório de shim a usar e uma função de limpeza.
//
// Devolver a limpeza como função (em vez de um método Close) deixa o uso
// óbvio no ponto da chamada: no defer já se vê o que será desfeito. Para o
// shim persistente do projeto, a limpeza é um no-op — ele deve sobreviver.
func (r *Runner) prepararShim() (dir string, limpar func(), err error) {
	if r.ShimDir != "" {
		dir, err := EnsureShim(r.ShimDir, r.Runtime)
		return dir, func() {}, err
	}

	dir, err = os.MkdirTemp("", "devmanager-shim-")
	if err != nil {
		return "", nil, fmt.Errorf("criando shim temporário: %w", err)
	}
	limpar = func() { os.RemoveAll(dir) }

	if _, err := EnsureShim(dir, r.Runtime); err != nil {
		limpar()
		return "", nil, err
	}
	return dir, limpar, nil
}

// resolverExecutavel decide o que será executado de fato.
func resolverExecutavel(nome, shim string, env []string) (string, error) {
	// Um nome com barra é um caminho, não algo a procurar no PATH.
	if strings.ContainsRune(nome, os.PathSeparator) {
		return nome, nil
	}

	// exec.LookPath consulta o PATH do processo ATUAL, não o que montamos.
	// Como o shim precisa ter precedência, procuramos nele primeiro à mão.
	candidato := filepath.Join(shim, nome)
	if info, err := os.Stat(candidato); err == nil && !info.IsDir() {
		return candidato, nil
	}

	caminho, err := exec.LookPath(nome)
	if err != nil {
		return "", fmt.Errorf("comando não encontrado: %s", nome)
	}
	return caminho, nil
}

// encaminharSinais repassa Ctrl+C e SIGTERM ao processo filho.
//
// Sem isso, o Ctrl+C mataria o devm e deixaria o `artisan serve` órfão
// segurando a porta. Quem decide o que fazer com o sinal é o filho; o devm
// apenas sobrevive até ele terminar e então reporta o código de saída.
func encaminharSinais(cmd *exec.Cmd) func() {
	// Canal com buffer 1: o runtime de sinais do Go nunca bloqueia ao
	// entregar, então um canal sem buffer poderia perder o sinal se a
	// goroutine ainda não estivesse pronta para receber.
	sinais := make(chan os.Signal, 1)
	signal.Notify(sinais, os.Interrupt, syscall.SIGTERM)

	// go inicia uma goroutine: esta função passa a rodar concorrentemente
	// enquanto o cmd.Wait() segura a função principal.
	go func() {
		// range sobre canal recebe até o canal ser fechado.
		for s := range sinais {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	return func() {
		signal.Stop(sinais) // para de receber sinais do runtime
		close(sinais)       // encerra o range e a goroutine termina
	}
}

// ExitError carrega o código de saída de um processo que rodou e falhou.
//
// Não tem mensagem de contexto de propósito: o comando já escreveu seu próprio
// erro no stderr, que foi direto para o terminal. Uma mensagem do devm por
// cima seria ruído duplicado.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("comando saiu com código %d", e.Code)
}
