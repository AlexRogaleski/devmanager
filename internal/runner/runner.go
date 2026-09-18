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

	// Extras são outros runtimes que o projeto usa, como o Node. Eles entram
	// no mesmo shim, então um `npm run dev` disparado por qualquer processo
	// encontra a versão certa.
	Extras []runtimes.Runtime
	Dir    string // diretório de trabalho do comando
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

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
	cmd, limpar, err := r.Comando(ctx, nome, args...)
	if err != nil {
		return err
	}
	defer limpar()

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

// Comando monta um *exec.Cmd já configurado com o ambiente do projeto, sem
// executá-lo. Devolve também a função de limpeza do shim temporário.
//
// Separar a MONTAGEM da EXECUÇÃO é o que permite o supervisor existir: ele
// precisa iniciar vários processos, conectar a saída de cada um a um writer
// com prefixo e esperar por todos ao mesmo tempo — coisas impossíveis com uma
// função que bloqueia até o processo terminar.
func (r *Runner) Comando(ctx context.Context, nome string, args ...string) (*exec.Cmd, func(), error) {
	// O shim coloca o PHP escolhido na frente do PATH, para toda a árvore de
	// processos. Ver EnsureShim em shim.go para o porquê.
	shim, limpar, err := r.prepararShim()
	if err != nil {
		return nil, nil, err
	}

	caminho, err := resolverExecutavel(nome, shim)
	if err != nil {
		limpar()
		return nil, nil, err
	}

	// Se o alvo é um script PHP, invocamos o NOSSO interpretador passando o
	// script como argumento, em vez de executá-lo direto.
	//
	// Isso corrige um furo real do shim: o /usr/bin/composer do Ubuntu tem
	// shebang "#!/usr/bin/php" — caminho ABSOLUTO. O kernel obedece ao
	// shebang e ignora o PATH, então o composer rodava no PHP do sistema
	// mesmo com o shim na frente. O resultado era pior que não ter shim:
	// o composer resolvia dependências para uma versão de PHP e o artisan
	// rodava em outra.
	//
	// Só o "#!/usr/bin/env php" respeitaria o PATH — e não é o que a maioria
	// dos pacotes de distro usa.
	if scriptPHP, ok := ehScriptPHP(caminho); ok {
		args = append([]string{scriptPHP}, args...)
		caminho = r.Runtime.Bin
	}

	cmd := exec.CommandContext(ctx, caminho, args...)
	cmd.Dir = r.Dir
	cmd.Env = r.ambiente(shim)
	cmd.Stdin = r.Stdin
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	return cmd, limpar, nil
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
	todos := append([]runtimes.Runtime{r.Runtime}, r.Extras...)

	if r.ShimDir != "" {
		dir, err := EnsureShim(r.ShimDir, todos...)
		return dir, func() {}, err
	}

	dir, err = os.MkdirTemp("", "devmanager-shim-")
	if err != nil {
		return "", nil, fmt.Errorf("criando shim temporário: %w", err)
	}
	limpar = func() { os.RemoveAll(dir) }

	if _, err := EnsureShim(dir, todos...); err != nil {
		limpar()
		return "", nil, err
	}
	return dir, limpar, nil
}

// resolverExecutavel decide o que será executado de fato.
func resolverExecutavel(nome, shim string) (string, error) {
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

// ehScriptPHP informa se um arquivo é um script executado por um interpretador
// PHP, devolvendo o caminho do script.
//
// A detecção é pelo shebang, não pela extensão: o composer não tem extensão
// nenhuma, e ./vendor/bin/pest também não.
func ehScriptPHP(caminho string) (string, bool) {
	f, err := os.Open(caminho)
	if err != nil {
		return "", false
	}
	defer f.Close()

	// 128 bytes cobrem qualquer shebang real com folga.
	buf := make([]byte, 128)
	n, _ := f.Read(buf)
	if n < 2 || buf[0] != '#' || buf[1] != '!' {
		return "", false
	}

	linha := string(buf[2:n])
	if i := strings.IndexAny(linha, "\r\n"); i >= 0 {
		linha = linha[:i]
	}

	// Cobre "#!/usr/bin/php", "#!/usr/bin/php8.3" e "#!/usr/bin/env php".
	for _, campo := range strings.Fields(linha) {
		base := filepath.Base(campo)
		if base == "php" || strings.HasPrefix(base, "php8") || strings.HasPrefix(base, "php7") {
			return caminho, true
		}
	}
	return "", false
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
