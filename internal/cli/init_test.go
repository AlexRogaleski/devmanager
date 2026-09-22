package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/services"
)

func projetoEm(t *testing.T, arquivos map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for nome, conteudo := range arquivos {
		if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const composeDoSail = `services:
  laravel.test:
    build:
      context: './vendor/laravel/sail/runtimes/8.4'
      dockerfile: Dockerfile
  pgsql:
    image: 'postgres:17'
`

// O composer.json costuma declarar o PISO ("^8.2") de um projeto que roda em
// 8.4 há anos. Migrar não pode trocar a versão de execução por baixo.
func TestPHPVemDoRuntimeDoSail(t *testing.T) {
	dir := projetoEm(t, map[string]string{
		"compose.yaml":  composeDoSail,
		"composer.json": `{"require":{"php":"^8.2"}}`,
		"artisan":       "",
	})

	c, origem, achados := deduzir(dir)

	if c.PHP != "8.4" {
		t.Errorf("php = %q, esperava 8.4 (o runtime do Sail)", c.PHP)
	}
	if !strings.Contains(origem, "Sail") {
		t.Errorf("origem = %q, devia citar o Sail", origem)
	}
	if len(c.Services) != 1 || c.Services[0] != "postgres:17" {
		t.Errorf("services = %v", c.Services)
	}
	if len(achados) != 1 || achados[0].Origem != "compose.yaml" {
		t.Errorf("achados = %+v", achados)
	}
}

func TestSemComposeNaoInventaVersaoDeSail(t *testing.T) {
	dir := projetoEm(t, map[string]string{"composer.json": `{"require":{"php":"^8.3"}}`})

	if _, _, ok := phpDoSail(dir); ok {
		t.Error("achou runtime do Sail onde não há compose")
	}
}

// Compose sem Sail (um projeto que montava os serviços à mão) não tem
// runtime nenhum a declarar.
func TestComposeSemSailNaoDaVersaoDePHP(t *testing.T) {
	dir := projetoEm(t, map[string]string{
		"compose.yaml": "services:\n  db:\n    image: 'mariadb:11'\n",
	})

	if versao, _, ok := phpDoSail(dir); ok {
		t.Errorf("achou %q onde não há runtime do Sail", versao)
	}
}

func TestNaoDeclaradosSoMostraOQueFalta(t *testing.T) {
	dir := projetoEm(t, map[string]string{
		"devmanager.yaml": "php: \"8.4\"\nservices:\n  - postgres:17\n",
	})

	achados := []services.Achado{
		{Spec: specDoTeste(t, "postgres:17"), Origem: "compose.yaml"},
		{Spec: specDoTeste(t, "redis"), Origem: ".env (QUEUE_CONNECTION=redis)"},
	}

	faltando := naoDeclarados(dir, achados)
	if len(faltando) != 1 || faltando[0].Spec.Nome != "redis" {
		t.Errorf("faltando = %+v", faltando)
	}
}

func specDoTeste(t *testing.T, texto string) services.Spec {
	t.Helper()
	s, err := services.ParseSpec(texto)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Trocar de versão é substituir a linha, não somar um segundo banco.
func TestAddTrocaAVersaoDoMesmoServico(t *testing.T) {
	lista := []string{"postgres:16", "redis:8"}
	lista = substituirOuIncluir(lista, specDoTeste(t, "postgres:17"))

	if strings.Join(lista, " ") != "postgres:17 redis:8" {
		t.Errorf("lista = %v", lista)
	}
}

func TestAddAcrescentaOQueNaoHavia(t *testing.T) {
	lista := substituirOuIncluir([]string{"postgres:17"}, specDoTeste(t, "mailpit"))

	if strings.Join(lista, " ") != "postgres:17 mailpit" {
		t.Errorf("lista = %v", lista)
	}
}

// "latest" não é versão: o arquivo fica mais honesto sem ela.
func TestMailpitVaiSemVersao(t *testing.T) {
	if texto := textoDaSpec(specDoTeste(t, "mailpit")); texto != "mailpit" {
		t.Errorf("texto = %q", texto)
	}
}

func TestDropTiraIndependenteDaVersao(t *testing.T) {
	lista, saiu := semOServico([]string{"postgres:17", "redis:8"}, "redis")
	if !saiu || strings.Join(lista, " ") != "postgres:17" {
		t.Errorf("lista = %v, saiu = %v", lista, saiu)
	}

	if _, saiu := semOServico([]string{"postgres:17"}, "mailpit"); saiu {
		t.Error("disse ter removido o que não estava lá")
	}
}
