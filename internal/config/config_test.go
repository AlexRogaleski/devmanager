package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func escrever(t *testing.T, dir, conteudo string) {
	t.Helper()
	if err := os.WriteFile(Path(dir), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadArquivoAusente(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("ausência de arquivo não deveria ser erro: %v", err)
	}
	if c != nil {
		t.Errorf("esperava nil, veio %+v", c)
	}
}

func TestLoadLeCampos(t *testing.T) {
	dir := t.TempDir()
	escrever(t, dir, `php: "8.3"
services:
  - postgres:17
  - redis
processes:
  queue: php artisan queue:work
`)

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load falhou: %v", err)
	}
	if c.PHP != "8.3" {
		t.Errorf("PHP = %q, esperava 8.3", c.PHP)
	}
	if len(c.Services) != 2 || c.Services[0] != "postgres:17" {
		t.Errorf("Services = %v", c.Services)
	}
	if c.Processes["queue"] != "php artisan queue:work" {
		t.Errorf("Processes = %v", c.Processes)
	}
}

// YAML inválido TEM que falhar: silenciar faria o Dev Manager ignorar a
// escolha explícita do desenvolvedor, que é o oposto do esperado.
func TestLoadYAMLInvalido(t *testing.T) {
	dir := t.TempDir()
	escrever(t, dir, "php: \"8.3\"\n  indentação: errada\n")

	if _, err := Load(dir); err == nil {
		t.Fatal("esperava erro para YAML inválido")
	}
}

func TestSetPHPCriaArquivoComModelo(t *testing.T) {
	dir := t.TempDir()

	if err := SetPHP(dir, "8.3"); err != nil {
		t.Fatalf("SetPHP falhou: %v", err)
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.PHP != "8.3" {
		t.Errorf("PHP = %q, esperava 8.3", c.PHP)
	}

	dados, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(dados), "# Dev Manager") {
		t.Errorf("arquivo novo deveria vir comentado:\n%s", dados)
	}
}

// A versão precisa ser gravada com aspas: sem elas, "8.3" volta como número
// na próxima leitura e "8.30" viraria "8.3". É um clássico do YAML.
func TestSetPHPGravaComoString(t *testing.T) {
	dir := t.TempDir()
	if err := SetPHP(dir, "8.30"); err != nil {
		t.Fatal(err)
	}

	dados, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(dados), `"8.30"`) {
		t.Errorf("versão não foi citada:\n%s", dados)
	}

	c, _ := Load(dir)
	if c.PHP != "8.30" {
		t.Errorf("PHP = %q, esperava \"8.30\" — o YAML comeu o zero", c.PHP)
	}
}

func TestSetPHPPreservaComentariosEOutrasChaves(t *testing.T) {
	dir := t.TempDir()
	escrever(t, dir, `# Cabeçalho do projeto
# segunda linha

# Versão do PHP
php: "8.1"

# Infra necessária
services:
  - postgres:17
`)

	if err := SetPHP(dir, "8.4"); err != nil {
		t.Fatalf("SetPHP falhou: %v", err)
	}

	texto, _ := os.ReadFile(Path(dir))
	for _, esperado := range []string{
		"# Cabeçalho do projeto",
		"# Versão do PHP",
		"# Infra necessária",
		"postgres:17",
		`php: "8.4"`,
	} {
		if !strings.Contains(string(texto), esperado) {
			t.Errorf("perdeu %q:\n%s", esperado, texto)
		}
	}
	if strings.Contains(string(texto), "8.1") {
		t.Errorf("versão antiga permaneceu:\n%s", texto)
	}
}

func TestClearPHPRemoveArquivoQuandoFicaVazio(t *testing.T) {
	dir := t.TempDir()
	if err := SetPHP(dir, "8.3"); err != nil {
		t.Fatal(err)
	}
	if err := ClearPHP(dir); err != nil {
		t.Fatalf("ClearPHP falhou: %v", err)
	}

	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		dados, _ := os.ReadFile(Path(dir))
		t.Errorf("arquivo sem conteúdo útil deveria ter sido removido:\n%s", dados)
	}
}

func TestClearPHPMantemArquivoComOutrasChaves(t *testing.T) {
	dir := t.TempDir()
	escrever(t, dir, `# Cabeçalho

php: "8.3"

# Infra
services:
  - redis
`)

	if err := ClearPHP(dir); err != nil {
		t.Fatal(err)
	}

	texto, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("arquivo não deveria ter sido removido: %v", err)
	}
	if strings.Contains(string(texto), "php:") {
		t.Errorf("a chave php permaneceu:\n%s", texto)
	}
	for _, esperado := range []string{"# Cabeçalho", "# Infra", "redis"} {
		if !strings.Contains(string(texto), esperado) {
			t.Errorf("perdeu %q:\n%s", esperado, texto)
		}
	}
}

func TestClearPHPSemArquivo(t *testing.T) {
	if err := ClearPHP(t.TempDir()); err != nil {
		t.Errorf("limpar projeto sem config não deveria falhar: %v", err)
	}
}

func TestPath(t *testing.T) {
	if got := Path("/x/y"); got != filepath.Join("/x/y", FileName) {
		t.Errorf("Path = %q", got)
	}
}
