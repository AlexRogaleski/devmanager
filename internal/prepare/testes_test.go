package prepare

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func comArquivos(t *testing.T, arquivos map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for nome, conteudo := range arquivos {
		caminho := filepath.Join(dir, nome)
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const phpunitCom = `<?xml version="1.0"?>
<phpunit>
  <php>
    <env name="APP_ENV" value="testing"/>
    <env name="DB_DATABASE" value="%s"/>
  </php>
</phpunit>`

// O caso que motivou o passo: a suíte aponta para outro banco, e `artisan
// test` falha na conexão enquanto ele não existe.
func TestBancoDeTestesVemDoPHPUnit(t *testing.T) {
	dir := comArquivos(t, map[string]string{
		"phpunit.xml": fmt.Sprintf(phpunitCom, "testing"),
	})

	if nome := BancoDeTestes(dir, "meu_app"); nome != "testing" {
		t.Errorf("banco = %q, esperava testing", nome)
	}
}

// O phpunit.xml que o Laravel gera traz essas linhas COMENTADAS. Lê-las faria
// o devm criar um banco que o projeto não usa.
func TestLinhasComentadasNaoContam(t *testing.T) {
	dir := comArquivos(t, map[string]string{
		"phpunit.xml": `<?xml version="1.0"?>
<phpunit>
  <php>
    <env name="APP_ENV" value="testing"/>
    <!-- <env name="DB_DATABASE" value="testing"/> -->
  </php>
</phpunit>`,
	})

	if nome := BancoDeTestes(dir, "meu_app"); nome != "" {
		t.Errorf("banco = %q, esperava nenhum", nome)
	}
}

func TestSuiteEmSQLiteNaoPedeBanco(t *testing.T) {
	casos := map[string]string{
		"conexão sqlite": `<?xml version="1.0"?>
<phpunit><php>
  <env name="DB_CONNECTION" value="sqlite"/>
  <env name="DB_DATABASE" value=":memory:"/>
</php></phpunit>`,
		"banco em memória": fmt.Sprintf(phpunitCom, ":memory:"),
		"arquivo sqlite":   fmt.Sprintf(phpunitCom, "database/testing.sqlite"),
	}

	for nome, xml := range casos {
		t.Run(nome, func(t *testing.T) {
			dir := comArquivos(t, map[string]string{"phpunit.xml": xml})
			if banco := BancoDeTestes(dir, "meu_app"); banco != "" {
				t.Errorf("banco = %q, esperava nenhum", banco)
			}
		})
	}
}

// Mesmo banco da aplicação não é banco de testes: não há o que criar.
func TestMesmoBancoDaAplicacaoNaoContaComoTestes(t *testing.T) {
	dir := comArquivos(t, map[string]string{"phpunit.xml": fmt.Sprintf(phpunitCom, "meu_app")})

	if nome := BancoDeTestes(dir, "meu_app"); nome != "" {
		t.Errorf("banco = %q, esperava nenhum", nome)
	}
}

// Sem phpunit.xml, o .env.testing ainda descreve a suíte.
func TestEnvDeTestesEntraQuandoNaoHaPHPUnit(t *testing.T) {
	dir := comArquivos(t, map[string]string{".env.testing": "DB_DATABASE=meu_app_testes\n"})

	if nome := BancoDeTestes(dir, "meu_app"); nome != "meu_app_testes" {
		t.Errorf("banco = %q", nome)
	}
}

func TestProjetoSemSuiteConfigurada(t *testing.T) {
	if nome := BancoDeTestes(t.TempDir(), "meu_app"); nome != "" {
		t.Errorf("banco = %q, esperava nenhum", nome)
	}
}
