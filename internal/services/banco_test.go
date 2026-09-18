package services

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNomeDeBanco(t *testing.T) {
	casos := map[string]string{
		"fapcen":                 "fapcen",
		"appmake-erp":            "appmake_erp", // hífen exigiria aspas em toda consulta
		"Portal.2024":            "portal_2024",
		"web-rtr":                "web_rtr",
		"meu projeto":            "meu_projeto",
		"2024-app":               "db_2024_app", // identificador SQL não começa com dígito
		"--estranho--":           "estranho",
		"":                       "app",
		"!!!":                    "app",
		strings.Repeat("a", 100): strings.Repeat("a", 63),
	}

	for entrada, esperado := range casos {
		if got := NomeDeBanco(entrada); got != esperado {
			t.Errorf("NomeDeBanco(%q) = %q, esperava %q", entrada, got, esperado)
		}
	}
}

// Todo nome gerado precisa passar pela validação usada no CriarBanco,
// senão haveria projeto impossível de provisionar.
func TestNomeDeBancoSempreValido(t *testing.T) {
	for _, entrada := range []string{
		"fapcen", "appmake-erp", "Portal.2024", "2024", "!!!", "", "A-B_c.d",
		strings.Repeat("x", 200),
	} {
		nome := NomeDeBanco(entrada)
		if !nomeDeBancoValido.MatchString(nome) {
			t.Errorf("NomeDeBanco(%q) = %q, que não passa na validação", entrada, nome)
		}
	}
}

func TestCriarBancoPostgres(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.CriarBanco(ctx, spec, "meu_projeto"); err != nil {
		t.Fatalf("CriarBanco falhou: %v", err)
	}

	log := strings.Join(chamadas(t, estado), "\n")

	// Postgres não tem CREATE DATABASE IF NOT EXISTS: precisamos consultar
	// antes de criar, e o teste trava esses dois passos.
	if !strings.Contains(log, "pg_database WHERE datname = 'meu_projeto'") {
		t.Errorf("não consultou se o banco existia:\n%s", log)
	}
	if !strings.Contains(log, "createdb -U laravel meu_projeto") {
		t.Errorf("não criou o banco:\n%s", log)
	}
}

func TestCriarBancoMySQLConcedePermissao(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("mysql")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.CriarBanco(ctx, spec, "outro_projeto"); err != nil {
		t.Fatalf("CriarBanco falhou: %v", err)
	}

	log := strings.Join(chamadas(t, estado), "\n")
	if !strings.Contains(log, "CREATE DATABASE IF NOT EXISTS `outro_projeto`") {
		t.Errorf("não criou o banco:\n%s", log)
	}
	// Sem o GRANT, a imagem oficial só dá acesso ao MYSQL_DATABASE inicial,
	// e o Laravel receberia "access denied" no banco novo.
	if !strings.Contains(log, "GRANT ALL PRIVILEGES ON `outro_projeto`.* TO 'laravel'@'%'") {
		t.Errorf("não concedeu permissão ao usuário da aplicação:\n%s", log)
	}
}

func TestCriarBancoRecusaNomeInvalido(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")

	// O nome vai dentro de SQL; nomes fora do padrão são recusados em vez de
	// escapados, o que torna injeção impossível por construção.
	for _, nome := range []string{"", "com-hifen", "COM_MAIUSCULA", "drop; --", "1digito"} {
		if err := m.CriarBanco(context.Background(), spec, nome); err == nil {
			t.Errorf("CriarBanco(%q) deveria falhar", nome)
		}
	}
}

func TestCriarBancoEmServicoSemBanco(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("redis")

	if err := m.CriarBanco(context.Background(), spec, "qualquer"); err != nil {
		t.Errorf("serviço sem banco não deveria falhar: %v", err)
	}
}

func TestAguardarProntoSucesso(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.AguardarPronto(ctx, spec, 5*time.Second); err != nil {
		t.Errorf("AguardarPronto falhou: %v", err)
	}
}

// Sonda que nunca responde tem que estourar o prazo com mensagem clara,
// não pendurar o comando.
func TestAguardarProntoEstouraPrazo(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")

	// Contêiner nunca criado: o exec falha sempre.
	err := m.AguardarPronto(context.Background(), spec, 600*time.Millisecond)
	if err == nil {
		t.Fatal("esperava erro de prazo esgotado")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("a mensagem deveria citar o serviço: %v", err)
	}
}

func TestAguardarProntoSemSonda(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("mailpit")

	if err := m.AguardarPronto(context.Background(), spec, time.Second); err != nil {
		t.Errorf("serviço sem sonda deveria passar direto: %v", err)
	}
}

// Porta oficial ocupada não pode impedir o serviço de subir — mas a troca
// precisa ser reportada, senão o usuário aponta o cliente para a porta errada.
func TestAjustarPortasOcupadas(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")

	m.PortaLivre = func(porta int) error {
		if porta == 5432 {
			return context.DeadlineExceeded // qualquer erro serve
		}
		return nil
	}

	ajustada, notas := m.AjustarPortasOcupadas(spec)

	if ajustada.Portas[0].Host == 5432 {
		t.Error("a porta ocupada não foi trocada")
	}
	if len(notas) != 1 {
		t.Fatalf("notas = %v, esperava exatamente uma", notas)
	}
	if !strings.Contains(notas[0], "5432") {
		t.Errorf("a nota deveria citar a porta original: %q", notas[0])
	}

	// A spec original não pode ser modificada: quem chamou pode precisar dela.
	if spec.Portas[0].Host != 5432 {
		t.Error("a spec original foi alterada")
	}
}

func TestAjustarPortasLivresNaoMuda(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("redis")

	ajustada, notas := m.AjustarPortasOcupadas(spec)

	if ajustada.Portas[0].Host != 6379 {
		t.Errorf("porta livre foi trocada sem motivo: %d", ajustada.Portas[0].Host)
	}
	if len(notas) != 0 {
		t.Errorf("notas = %v, esperava nenhuma", notas)
	}
}

// Regressão: a leitura de portas do engine não traz rótulos, e um serviço de
// várias portas perdia a identificação quando um SEGUNDO projeto o
// reaproveitava. O sintoma foi um MAIL_PORT vazio no .env do segundo projeto.
func TestRotularPortasRecuperaOsRotulos(t *testing.T) {
	// Como o comando `port` reporta: sem rótulo.
	lidas := []Porta{
		{Host: 36527, Interna: 8025},
		{Host: 35097, Interna: 1025},
	}

	rotuladas := rotularPortas("mailpit", lidas)

	rotulos := map[int]string{}
	for _, p := range rotuladas {
		rotulos[p.Interna] = p.Rotulo
	}
	if rotulos[1025] != "smtp" {
		t.Errorf("porta 1025 = %q, esperava smtp", rotulos[1025])
	}
	if rotulos[8025] != "web" {
		t.Errorf("porta 8025 = %q, esperava web", rotulos[8025])
	}

	// A porta do HOST precisa ser preservada: é ela que vai para o .env.
	for _, p := range rotuladas {
		if p.Interna == 1025 && p.Host != 35097 {
			t.Errorf("porta do host = %d, esperava 35097", p.Host)
		}
	}
}

func TestRotularPortasNaoSobrescreveOQueJaTemRotulo(t *testing.T) {
	entrada := []Porta{{Host: 1, Interna: 1025, Rotulo: "personalizado"}}

	if got := rotularPortas("mailpit", entrada)[0].Rotulo; got != "personalizado" {
		t.Errorf("Rotulo = %q, esperava preservar o existente", got)
	}
}

func TestRotularPortasDeServicoDesconhecido(t *testing.T) {
	entrada := []Porta{{Host: 1, Interna: 2}}

	if got := rotularPortas("inexistente", entrada); len(got) != 1 {
		t.Errorf("serviço fora do catálogo deveria passar as portas intactas: %v", got)
	}
}
