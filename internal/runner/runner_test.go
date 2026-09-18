package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// phpFalso cria um script que se comporta como um PHP para os nossos fins.
func phpFalso(t *testing.T) runtimes.Runtime {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "php-falso")

	// Imprime a versão e ecoa os argumentos, para podermos conferir o que
	// chegou até ele.
	script := "#!/bin/sh\necho \"php-falso 8.3.15 args:$*\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return runtimes.Runtime{
		Language: "php",
		Version:  semver.MustParse("8.3.15"),
		Bin:      bin,
		Source:   "teste",
	}
}

func TestEnsureShimCriaLink(t *testing.T) {
	rt := phpFalso(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatalf("EnsureShim falhou: %v", err)
	}

	alvo, err := os.Readlink(PHPPath(shimDir))
	if err != nil {
		t.Fatalf("link php não foi criado: %v", err)
	}
	if alvo != rt.Bin {
		t.Errorf("link aponta para %q, esperava %q", alvo, rt.Bin)
	}
}

// Trocar a versão do projeto tem que reapontar o link, não falhar por ele
// já existir — é o caminho de `devm php use 8.4`.
func TestEnsureShimEhIdempotenteEReaponta(t *testing.T) {
	rt := phpFalso(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatalf("segunda chamada falhou: %v", err)
	}

	outro := phpFalso(t)
	if _, err := EnsureShim(shimDir, outro); err != nil {
		t.Fatalf("reapontar falhou: %v", err)
	}

	alvo, _ := os.Readlink(PHPPath(shimDir))
	if alvo != outro.Bin {
		t.Errorf("link não foi reapontado: %q", alvo)
	}
}

// O shim tem que entrar na FRENTE do PATH, senão os subprocessos caem no PHP
// do sistema — o bug mais confuso que essa arquitetura pode produzir.
func TestShimVemNaFrenteDoPath(t *testing.T) {
	rt := phpFalso(t)
	var saida bytes.Buffer

	r := &Runner{
		Runtime: rt,
		Stdout:  &saida,
		Stderr:  &saida,
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}

	// "php" sem caminho: só encontra alguma coisa se o shim estiver no PATH.
	if err := r.Run(context.Background(), "php", "-v"); err != nil {
		t.Fatalf("Run falhou: %v", err)
	}
	if !strings.Contains(saida.String(), "php-falso") {
		t.Errorf("executou outro php:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "args:-v") {
		t.Errorf("argumentos não chegaram ao filho:\n%s", saida.String())
	}
}

func TestRunDefineVariaveisDeAmbiente(t *testing.T) {
	rt := phpFalso(t)
	var saida bytes.Buffer

	r := &Runner{
		Runtime: rt,
		Stdout:  &saida,
		Stderr:  &saida,
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}

	if err := r.Run(context.Background(), "sh", "-c", "echo $DEVMANAGER/$DEVMANAGER_PHP"); err != nil {
		t.Fatalf("Run falhou: %v", err)
	}
	if !strings.Contains(saida.String(), "1/"+rt.Bin) {
		t.Errorf("ambiente do filho = %q", saida.String())
	}
}

// Código de saída tem que ser repassado exatamente: é o que faz
// `devm artisan migrate && deploy` se comportar como o comando original.
func TestRunPropagaCodigoDeSaida(t *testing.T) {
	rt := phpFalso(t)
	var saida bytes.Buffer

	r := &Runner{
		Runtime: rt,
		Stdout:  &saida,
		Stderr:  &saida,
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}

	err := r.Run(context.Background(), "sh", "-c", "exit 42")
	if err == nil {
		t.Fatal("esperava erro para saída != 0")
	}

	var saiu *ExitError
	if !errors.As(err, &saiu) {
		t.Fatalf("erro = %T, esperava *ExitError", err)
	}
	if saiu.Code != 42 {
		t.Errorf("Code = %d, esperava 42", saiu.Code)
	}
}

func TestRunComandoInexistente(t *testing.T) {
	rt := phpFalso(t)
	r := &Runner{
		Runtime: rt,
		Stdout:  io_Discard(),
		Stderr:  io_Discard(),
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}

	err := r.Run(context.Background(), "comando-que-nao-existe-mesmo")
	if err == nil {
		t.Fatal("esperava erro")
	}
	// Um comando que nem existe NÃO é ExitError: nada rodou para sair com código.
	var saiu *ExitError
	if errors.As(err, &saiu) {
		t.Error("comando inexistente não deveria virar ExitError")
	}
}

// Sem ShimDir, o Runner usa um diretório temporário e o remove no fim.
func TestShimTemporarioEhRemovido(t *testing.T) {
	rt := phpFalso(t)
	var saida bytes.Buffer

	r := &Runner{Runtime: rt, Stdout: &saida, Stderr: &saida}

	if err := r.Run(context.Background(), "sh", "-c", "echo $PATH | cut -d: -f1"); err != nil {
		t.Fatal(err)
	}

	usado := strings.TrimSpace(saida.String())
	if usado == "" {
		t.Fatal("não consegui capturar o shim usado")
	}
	if _, err := os.Stat(usado); !os.IsNotExist(err) {
		t.Errorf("o shim temporário %s não foi removido", usado)
	}
}

func io_Discard() *bytes.Buffer { return &bytes.Buffer{} }

// Regressão: um script com shebang de caminho ABSOLUTO (#!/usr/bin/php)
// atravessa o shim, porque o kernel obedece ao shebang e ignora o PATH.
// É o caso do /usr/bin/composer do Ubuntu — e o efeito era o pior possível:
// o composer resolvia dependências numa versão de PHP e o artisan rodava
// em outra.
func TestScriptComShebangAbsolutoUsaOPHPDoProjeto(t *testing.T) {
	rt := phpFalso(t)
	dir := t.TempDir()

	// Simula o composer da distro: shebang com caminho ABSOLUTO para um
	// binário chamado "php" que não existe aqui. Executado direto, falharia
	// com ENOENT; só funciona se o runner desviar para o PHP do projeto.
	script := filepath.Join(dir, "composer")
	conteudo := "#!/caminho/inexistente/php\n<?php echo 'nunca chega aqui';\n"
	if err := os.WriteFile(script, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}

	var saida bytes.Buffer
	r := &Runner{
		Runtime: rt,
		Stdout:  &saida,
		Stderr:  &saida,
		ShimDir: filepath.Join(t.TempDir(), "shim"),
	}

	if err := r.Run(context.Background(), script, "install"); err != nil {
		t.Fatalf("Run falhou: %v", err)
	}

	// O php falso ecoa os argumentos: se ele recebeu o script, o desvio
	// funcionou e o shebang foi ignorado.
	if !strings.Contains(saida.String(), "php-falso") {
		t.Errorf("não passou pelo PHP do projeto:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "composer install") {
		t.Errorf("argumentos errados:\n%s", saida.String())
	}
}

func TestDeteccaoDeScriptPHP(t *testing.T) {
	dir := t.TempDir()

	casos := map[string]struct {
		conteudo string
		querPHP  bool
	}{
		"env php":        {"#!/usr/bin/env php\n<?php\n", true},
		"caminho direto": {"#!/usr/bin/php\n<?php\n", true},
		"versionado":     {"#!/usr/bin/php8.3\n<?php\n", true},
		"shell":          {"#!/bin/sh\necho oi\n", false},
		"python":         {"#!/usr/bin/env python3\nprint()\n", false},
		"sem shebang":    {"binário qualquer", false},
		"vazio":          {"", false},
	}

	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			caminho := filepath.Join(dir, strings.ReplaceAll(nome, " ", "-"))
			if err := os.WriteFile(caminho, []byte(c.conteudo), 0o755); err != nil {
				t.Fatal(err)
			}

			_, ok := ehScriptPHP(caminho)
			if ok != c.querPHP {
				t.Errorf("ehScriptPHP = %v, esperava %v", ok, c.querPHP)
			}
		})
	}
}

func TestEnsureComposerShim(t *testing.T) {
	shimDir := filepath.Join(t.TempDir(), "shim")

	if err := EnsureComposerShim(shimDir, "/x/php", "/y/composer.phar"); err != nil {
		t.Fatalf("EnsureComposerShim falhou: %v", err)
	}

	caminho := filepath.Join(shimDir, "composer")
	dados, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dados), "/x/php") || !strings.Contains(string(dados), "/y/composer.phar") {
		t.Errorf("wrapper não referencia o par correto:\n%s", dados)
	}
	if !strings.HasPrefix(string(dados), "#!/bin/sh") {
		t.Errorf("wrapper sem shebang:\n%s", dados)
	}

	info, _ := os.Stat(caminho)
	if info.Mode()&0o111 == 0 {
		t.Errorf("wrapper sem permissão de execução: %v", info.Mode())
	}

	// Idempotente: chamar de novo não deve falhar nem alterar o conteúdo.
	if err := EnsureComposerShim(shimDir, "/x/php", "/y/composer.phar"); err != nil {
		t.Fatalf("segunda chamada falhou: %v", err)
	}
}

// Regressão: o shim só ACRESCENTAVA links. Desfixar a versão de Node deixava
// o link antigo no lugar, e o projeto continuava usando a versão que a pessoa
// acabou de remover da configuração — sem nenhum sinal do motivo.
func TestShimRemoveLinksObsoletos(t *testing.T) {
	rt := phpFalso(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	node := runtimes.Runtime{
		Language: "node",
		Version:  semver.MustParse("22.11.0"),
		Bin:      rt.Bin, // qualquer executável serve para o teste
		Comandos: map[string]string{"node": rt.Bin, "npm": rt.Bin, "npx": rt.Bin},
	}

	if _, err := EnsureShim(shimDir, rt, node); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"php", "node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(shimDir, n)); err != nil {
			t.Fatalf("link %q não foi criado: %v", n, err)
		}
	}

	// Agora sem o Node: os três links dele têm que sumir.
	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(shimDir, n)); err == nil {
			t.Errorf("link obsoleto %q permaneceu", n)
		}
	}
	if _, err := os.Lstat(filepath.Join(shimDir, "php")); err != nil {
		t.Errorf("o php não deveria ter sido removido: %v", err)
	}
}

// O composer é gerenciado à parte e não pode ser apagado pela limpeza.
func TestShimNaoRemoveOComposer(t *testing.T) {
	rt := phpFalso(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatal(err)
	}
	if err := EnsureComposerShim(shimDir, rt.Bin, "/x/composer.phar"); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureShim(shimDir, rt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shimDir, "composer")); err != nil {
		t.Errorf("o composer foi apagado pela limpeza: %v", err)
	}
}
