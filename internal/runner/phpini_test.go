package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// phpQueImprimeOAmbiente é um "php" falso que conta o que recebeu: o PHPRC do
// ambiente e os argumentos. É o que permite testar o shim sem um PHP real.
func phpQueImprimeOAmbiente(t *testing.T) runtimes.Runtime {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "php")
	script := "#!/bin/sh\nprintf 'PHPRC=%s TERMINFO=%s ARGS=%s\\n' \"${PHPRC:-vazio}\" \"${TERMINFO:-vazio}\" \"$*\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return runtimes.Runtime{
		Language: "php",
		Version:  semver.MustParse("8.4.23"),
		Bin:      bin,
		Source:   "teste",
	}
}

func TestPHPIniTemOsPadroesQueFaltavam(t *testing.T) {
	conteudo := ConteudoPHPIni(nil)

	// Os três que o PHP estático, sem ini, deixa em 128M, 2M e 8M — e que a
	// imagem do Sail entregava em -1, 100M e 100M.
	for _, esperado := range []string{
		"memory_limit = -1",
		"upload_max_filesize = 100M",
		"post_max_size = 100M",
	} {
		if !strings.Contains(conteudo, esperado) {
			t.Errorf("faltou %q:\n%s", esperado, conteudo)
		}
	}
}

func TestPHPIniDoProjetoSobrepoeOPadrao(t *testing.T) {
	conteudo := ConteudoPHPIni(map[string]string{
		"memory_limit":       "512M",
		"max_execution_time": "120",
	})

	if !strings.Contains(conteudo, "memory_limit = 512M") {
		t.Errorf("o projeto não sobrepôs o padrão:\n%s", conteudo)
	}
	if strings.Contains(conteudo, "memory_limit = -1") {
		t.Errorf("o padrão sobreviveu à sobreposição:\n%s", conteudo)
	}
	if !strings.Contains(conteudo, "max_execution_time = 120") {
		t.Errorf("chave nova do projeto não entrou:\n%s", conteudo)
	}
	// O que o projeto não mencionou continua valendo.
	if !strings.Contains(conteudo, "upload_max_filesize = 100M") {
		t.Errorf("sobrepor uma chave não pode apagar as outras:\n%s", conteudo)
	}
}

// Conteúdo estável é o que permite não reescrever o arquivo quando nada
// mudou — a ordem de iteração de um map em Go é aleatória de propósito.
func TestPHPIniEhEstavel(t *testing.T) {
	extras := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}
	primeiro := ConteudoPHPIni(extras)
	for i := 0; i < 20; i++ {
		if ConteudoPHPIni(extras) != primeiro {
			t.Fatal("o conteúdo mudou entre chamadas")
		}
	}
}

// TestShimApontaOPHPRC é o teste que importa: executa o shim e vê o que o PHP
// recebeu. Verificar só o texto do script provaria que ele está escrito, não
// que funciona.
func TestShimApontaOPHPRC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}

	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	if _, err := EnsureShim(shimDir, map[string]string{"memory_limit": "512M"}, rt); err != nil {
		t.Fatal(err)
	}

	saida, err := exec.Command(PHPPath(shimDir), "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("executando o shim: %v\n%s", err, saida)
	}

	ini := filepath.Join(shimDir, NomeDoPHPIni)
	if !strings.Contains(string(saida), "PHPRC="+ini) {
		t.Errorf("o shim não apontou o PHPRC: %s", saida)
	}
	// Os argumentos precisam chegar intactos ao PHP.
	if !strings.Contains(string(saida), "ARGS=-v") {
		t.Errorf("os argumentos não chegaram: %s", saida)
	}

	// E o arquivo apontado é o que o projeto pediu.
	conteudo, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conteudo), "memory_limit = 512M") {
		t.Errorf("o php.ini do shim não tem o valor do projeto:\n%s", conteudo)
	}
}

// Quem já definiu um PHPRC manda: dá para rodar um comando com outro ini sem
// mexer no shim.
func TestShimRespeitaPHPRCExistente(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}

	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")
	if _, err := EnsureShim(shimDir, nil, rt); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(PHPPath(shimDir))
	cmd.Env = append(os.Environ(), "PHPRC=/meu/php.ini")
	saida, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executando o shim: %v\n%s", err, saida)
	}
	if !strings.Contains(string(saida), "PHPRC=/meu/php.ini") {
		t.Errorf("o shim sobrescreveu o PHPRC de quem chamou: %s", saida)
	}
}

// TestShimSubstituiOLinkAntigoSemEscreverNoBinario cobre a atualização de uma
// versão anterior, em que o "php" do shim era um symlink.
//
// Escrever num symlink escreve no ALVO. Sem remover o link antes, o script do
// shim seria gravado POR CIMA do binário do PHP — destruindo o runtime
// instalado, e de um jeito que só apareceria na próxima execução.
func TestShimSubstituiOLinkAntigoSemEscreverNoBinario(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}

	rt := phpQueImprimeOAmbiente(t)
	original, err := os.ReadFile(rt.Bin)
	if err != nil {
		t.Fatal(err)
	}

	shimDir := filepath.Join(t.TempDir(), "shim")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// O estado deixado pela versão anterior.
	if err := os.Symlink(rt.Bin, PHPPath(shimDir)); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureShim(shimDir, nil, rt); err != nil {
		t.Fatal(err)
	}

	depois, err := os.ReadFile(rt.Bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(depois) != string(original) {
		t.Fatal("o binário do PHP foi sobrescrito pelo script do shim")
	}

	info, err := os.Lstat(PHPPath(shimDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("o php do shim continua sendo um link")
	}
}

// Sem PHP no shim não há php.ini a manter: ele sai junto, como os links dos
// comandos que deixaram de existir.
func TestShimRemoveOIniQuandoNaoHaPHP(t *testing.T) {
	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	if _, err := EnsureShim(shimDir, nil, rt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shimDir, NomeDoPHPIni)); err != nil {
		t.Fatalf("o php.ini deveria existir: %v", err)
	}

	if _, err := EnsureShim(shimDir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shimDir, NomeDoPHPIni)); !os.IsNotExist(err) {
		t.Error("o php.ini sobreviveu à remoção do PHP do shim")
	}
}

func TestBaseDeTerminfo(t *testing.T) {
	raiz := t.TempDir()
	segunda := filepath.Join(raiz, "lib-terminfo")
	if err := os.MkdirAll(segunda, 0o755); err != nil {
		t.Fatal(err)
	}
	inexistente := filepath.Join(raiz, "nao-existe")

	if got := baseDeTerminfo([]string{inexistente, segunda}); got != segunda {
		t.Errorf("baseDeTerminfo = %q, queria %q", got, segunda)
	}

	// Sem base nenhuma, o shim não deve inventar um caminho: vazio é um
	// resultado legítimo.
	if got := baseDeTerminfo([]string{inexistente}); got != "" {
		t.Errorf("baseDeTerminfo = %q, queria vazio", got)
	}

	// Um arquivo não é uma base.
	arquivo := filepath.Join(raiz, "arquivo")
	if err := os.WriteFile(arquivo, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := baseDeTerminfo([]string{arquivo}); got != "" {
		t.Errorf("baseDeTerminfo aceitou um arquivo: %q", got)
	}
}

// O readline do PHP estático não acha a base de terminfo sozinho, e o tinker
// abre com duas linhas de reclamação. O shim aponta a base quando ela existe.
func TestShimApontaOTerminfo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}
	base := baseDeTerminfo(candidatosDeTerminfo)
	if base == "" {
		t.Skip("esta máquina não tem base de terminfo")
	}

	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")
	if _, err := EnsureShim(shimDir, nil, rt); err != nil {
		t.Fatal(err)
	}

	saida, err := exec.Command(PHPPath(shimDir)).CombinedOutput()
	if err != nil {
		t.Fatalf("executando o shim: %v\n%s", err, saida)
	}
	if !strings.Contains(string(saida), "TERMINFO="+base) {
		t.Errorf("o shim não apontou a base de terminfo: %s", saida)
	}
}

// Quem já definiu manda: um TERMINFO próprio não é sobrescrito.
func TestShimRespeitaTerminfoExistente(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}
	if baseDeTerminfo(candidatosDeTerminfo) == "" {
		t.Skip("esta máquina não tem base de terminfo")
	}

	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")
	if _, err := EnsureShim(shimDir, nil, rt); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(PHPPath(shimDir))
	cmd.Env = append(os.Environ(), "TERMINFO=/meu/terminfo")
	saida, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executando o shim: %v\n%s", err, saida)
	}
	if !strings.Contains(string(saida), "TERMINFO=/meu/terminfo") {
		t.Errorf("o shim sobrescreveu o TERMINFO de quem chamou: %s", saida)
	}
}

// TestRunPHPPassaPeloShim guarda o buraco que a primeira versão do php.ini
// deixou: o `devm artisan` chamava o binário DIRETO, sem o shim, e rodava
// com os 128M do PHP estático sem ini — enquanto o `devm composer`, que
// passa pelo wrapper, rodava com o ini certo. Meia funcionalidade, e a
// metade que faltava era a mais usada.
func TestRunPHPPassaPeloShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}

	rt := phpQueImprimeOAmbiente(t)
	shimDir := filepath.Join(t.TempDir(), "shim")

	var saida strings.Builder
	r := &Runner{Runtime: rt, ShimDir: shimDir, Stdout: &saida, Stderr: &saida}

	if err := r.RunPHP(context.Background(), "-v"); err != nil {
		t.Fatalf("RunPHP falhou: %v\n%s", err, saida.String())
	}

	ini := filepath.Join(shimDir, NomeDoPHPIni)
	if !strings.Contains(saida.String(), "PHPRC="+ini) {
		t.Errorf("o artisan rodaria sem o php.ini do projeto: %s", saida.String())
	}
	if !strings.Contains(saida.String(), "ARGS=-v") {
		t.Errorf("os argumentos não chegaram: %s", saida.String())
	}
}

// Um script com shebang de caminho absoluto — "#!/usr/bin/php" — é
// redirecionado para o nosso PHP. Esse desvio também precisa passar pelo
// shim, senão ele reintroduz o mesmo buraco por outra porta.
func TestShebangRedirecionadoPassaPeloShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o shim é um script de shell")
	}

	rt := phpQueImprimeOAmbiente(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "ferramenta")
	if err := os.WriteFile(script, []byte("#!/usr/bin/php\n<?php\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	shimDir := filepath.Join(dir, "shim")
	var saida strings.Builder
	r := &Runner{Runtime: rt, ShimDir: shimDir, Stdout: &saida, Stderr: &saida}

	if err := r.Run(context.Background(), script); err != nil {
		t.Fatalf("Run falhou: %v\n%s", err, saida.String())
	}
	if !strings.Contains(saida.String(), "PHPRC="+filepath.Join(shimDir, NomeDoPHPIni)) {
		t.Errorf("o script rodaria sem o php.ini do projeto: %s", saida.String())
	}
}
