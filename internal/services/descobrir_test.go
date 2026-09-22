package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func escreverArquivo(t *testing.T, dir, nome, conteudo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resumo(achados []Achado) []string {
	linhas := make([]string, 0, len(achados))
	for _, a := range achados {
		linhas = append(linhas, a.Spec.Nome+":"+a.Spec.Versao)
	}
	return linhas
}

// O compose do Laravel Sail é o caso central: é dele que vinham os projetos
// traduzidos à mão.
func TestDescobrirLeOComposeDoSail(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, "compose.yaml", `
services:
  laravel.test:
    build:
      context: './vendor/laravel/sail/runtimes/8.4'
    ports:
      - '80:80'
  pgsql:
    image: 'postgres:17'
  redis:
    image: 'redis:alpine'
  mailpit:
    image: 'axllent/mailpit:latest'
`)

	obtido := strings.Join(resumo(Descobrir(dir)), " ")
	esperado := "mailpit:latest postgres:17 redis:8"
	if obtido != esperado {
		t.Errorf("achados = %q, esperava %q", obtido, esperado)
	}
}

// O serviço construído por Dockerfile é a aplicação, não algo que saibamos
// subir: incluí-lo criaria um contêiner sem sentido.
func TestDescobrirIgnoraOServicoDoProprioApp(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, "compose.yaml", `
services:
  app:
    build: .
  worker:
    image: 'nginx:1.27'
`)

	if achados := Descobrir(dir); len(achados) != 0 {
		t.Errorf("achados = %v, esperava nenhum", resumo(achados))
	}
}

// Tags que não são versão ("alpine", "8-bookworm") não dizem nada ao
// catálogo; o padrão dele é melhor palpite.
func TestDescobrirUsaOPadraoQuandoATagNaoEVersao(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, "docker-compose.yml", `
services:
  cache:
    image: redis:alpine
  db:
    image: 'mysql/mysql-server:8.0'
`)

	obtido := strings.Join(resumo(Descobrir(dir)), " ")
	if obtido != "mysql:8.0 redis:8" {
		t.Errorf("achados = %q", obtido)
	}
}

// O MariaDB do catálogo é sondado com mariadb-admin, que não existe no 10.6:
// declarar a versão do compose criaria um serviço que nunca fica pronto.
func TestDescobrirSobeOMariaDBAntigoParaAVersaoSondavel(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, "compose.yaml", "services:\n  db:\n    image: 'mariadb:10.6'\n")

	achados := Descobrir(dir)
	if len(achados) != 1 || achados[0].Spec.Versao != "11" {
		t.Fatalf("achados = %v", resumo(achados))
	}
	if !strings.Contains(achados[0].Origem, "10.6 → 11") {
		t.Errorf("a origem devia mostrar o ajuste: %q", achados[0].Origem)
	}
}

// Projeto que nunca usou contêiner: as pistas estão no .env.
func TestDescobrirLeOEnvQuandoNaoHaCompose(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, ".env", `
APP_NAME=Legado
DB_CONNECTION=mysql
QUEUE_CONNECTION=redis
MAIL_MAILER=smtp
MAIL_HOST=127.0.0.1
MAIL_PORT=1025
`)

	obtido := strings.Join(resumo(Descobrir(dir)), " ")
	if obtido != "mailpit:latest mysql:8.4 redis:8" {
		t.Errorf("achados = %q", obtido)
	}
}

// REDIS_HOST vem preenchido no .env.example de todo Laravel, mesmo em projeto
// que não usa Redis. Declarar por causa dele subiria um contêiner à toa.
func TestDescobrirNaoInventaRedisPorCausaDoRedisHost(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, ".env", "DB_CONNECTION=sqlite\nREDIS_HOST=127.0.0.1\nCACHE_STORE=database\n")

	if achados := Descobrir(dir); len(achados) != 0 {
		t.Errorf("achados = %v, esperava nenhum", resumo(achados))
	}
}

// Sem .env, o .env.example ainda descreve o que o projeto espera.
func TestDescobrirCaiNoEnvExample(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, ".env.example", "DB_CONNECTION=pgsql\n")

	achados := Descobrir(dir)
	if len(achados) != 1 || achados[0].Spec.Nome != "postgres" {
		t.Fatalf("achados = %v", resumo(achados))
	}
	if !strings.Contains(achados[0].Origem, ".env.example") {
		t.Errorf("origem = %q", achados[0].Origem)
	}
}

// O compose diz a versão; o .env só diz o dialeto. Em conflito, vence quem
// sabe mais.
func TestComposeVenceOEnv(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, dir, "compose.yaml", "services:\n  db:\n    image: 'postgres:16'\n")
	escreverArquivo(t, dir, ".env", "DB_CONNECTION=pgsql\n")

	achados := Descobrir(dir)
	if len(achados) != 1 || achados[0].Spec.Versao != "16" {
		t.Fatalf("achados = %v", resumo(achados))
	}
}

func TestDescobrirEmProjetoSemPistaNenhuma(t *testing.T) {
	if achados := Descobrir(t.TempDir()); len(achados) != 0 {
		t.Errorf("achados = %v, esperava nenhum", resumo(achados))
	}
}
