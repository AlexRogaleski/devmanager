package prepare

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

func TestEnvParaServicoPostgres(t *testing.T) {
	spec, _ := services.ParseSpec("postgres:17")
	portas := []services.Porta{{Host: 5433, Interna: 5432}}

	env := envParaServico(spec, portas, "meu_projeto", "meu-projeto")

	esperado := map[string]string{
		"DB_CONNECTION": "pgsql",
		"DB_HOST":       "127.0.0.1",
		"DB_PORT":       "5433", // a porta REAL, não a do catálogo
		"DB_DATABASE":   "meu_projeto",
		"DB_USERNAME":   "laravel",
		"DB_PASSWORD":   "secret",
	}
	for k, v := range esperado {
		if env[k] != v {
			t.Errorf("%s = %q, esperava %q", k, env[k], v)
		}
	}
}

func TestEnvParaServicoMySQL(t *testing.T) {
	spec, _ := services.ParseSpec("mysql")
	env := envParaServico(spec, spec.Portas, "app", "app")

	if env["DB_CONNECTION"] != "mysql" {
		t.Errorf("DB_CONNECTION = %q", env["DB_CONNECTION"])
	}
	// Precisa ser o usuário da aplicação, não root: o Laravel não deveria
	// conectar como root nem em desenvolvimento.
	if env["DB_USERNAME"] != "laravel" {
		t.Errorf("DB_USERNAME = %q, esperava laravel", env["DB_USERNAME"])
	}
}

// O Redis tem só 16 índices numerados — pouco para muitos projetos. O
// isolamento vem do prefixo de chave.
func TestEnvParaServicoRedisUsaPrefixo(t *testing.T) {
	spec, _ := services.ParseSpec("redis")
	env := envParaServico(spec, spec.Portas, "", "appmake-erp")

	if env["REDIS_PREFIX"] != "appmake_erp_" {
		t.Errorf("REDIS_PREFIX = %q, esperava appmake_erp_", env["REDIS_PREFIX"])
	}
	if env["REDIS_PORT"] != "6379" {
		t.Errorf("REDIS_PORT = %q", env["REDIS_PORT"])
	}
}

func TestEnvParaServicoMailpitUsaPortaSMTP(t *testing.T) {
	spec, _ := services.ParseSpec("mailpit")
	portas := []services.Porta{
		{Host: 9025, Interna: 8025, Rotulo: "web"},
		{Host: 2025, Interna: 1025, Rotulo: "smtp"},
	}

	env := envParaServico(spec, portas, "", "app")

	// A porta do MAIL_PORT é a de SMTP, não a da interface web — e a ordem
	// da lista não pode influenciar isso.
	if env["MAIL_PORT"] != "2025" {
		t.Errorf("MAIL_PORT = %q, esperava 2025 (a de smtp)", env["MAIL_PORT"])
	}
	if env["MAIL_MAILER"] != "smtp" {
		t.Errorf("MAIL_MAILER = %q", env["MAIL_MAILER"])
	}
}

func TestSpecsDoProjeto(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		"devmanager.yaml": "services:\n  - postgres:17\n  - redis\n",
	})

	specs, err := SpecsDoProjeto(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("esperava 2 specs, veio %d", len(specs))
	}
	if specs[0].Nome != "postgres" || specs[0].Versao != "17" {
		t.Errorf("spec[0] = %+v", specs[0])
	}
}

func TestSpecsDoProjetoServicoInvalido(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{}}`,
		"devmanager.yaml": "services:\n  - mongodb\n",
	})

	if _, err := SpecsDoProjeto(p); err == nil {
		t.Fatal("esperava erro para serviço fora do catálogo")
	}
}

// Projeto que declarou PostgreSQL não deve receber o passo do arquivo SQLite:
// seriam duas configurações de banco e uma ignorada.
func TestPlanoSemSQLiteQuandoTemBanco(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		".env.example":    "APP_KEY=\nDB_CONNECTION=sqlite\n",
		"devmanager.yaml": "services:\n  - postgres:17\n",
	})

	for _, passo := range Plano(p, &executorFalso{}, Opcoes{}) {
		if strings.Contains(passo.Nome, "sqlite") {
			t.Errorf("passo do SQLite não deveria existir: %q", passo.Nome)
		}
	}
}

func TestPlanoComSQLiteQuandoNaoTemBanco(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		".env.example":    "APP_KEY=\nDB_CONNECTION=sqlite\n",
		"devmanager.yaml": "services:\n  - redis\n",
	})

	var achou bool
	for _, passo := range Plano(p, &executorFalso{}, Opcoes{}) {
		if strings.Contains(passo.Nome, "sqlite") {
			achou = true
		}
	}
	if !achou {
		t.Error("com só redis declarado, o passo do SQLite deveria existir")
	}
}

// Sem gerenciador de serviços, o passo fica BLOQUEADO — não pendente e
// tampouco silenciosamente ausente.
func TestPassoServicoBloqueadoSemEngine(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		"devmanager.yaml": "services:\n  - postgres:17\n",
	})

	passos := Plano(p, &executorFalso{}, Opcoes{})

	var servico Passo
	for _, passo := range passos {
		if strings.HasPrefix(passo.Nome, "serviço ") {
			servico = passo
		}
	}
	if servico.Nome == "" {
		t.Fatal("o passo de serviço não entrou no plano")
	}
	if servico.Bloqueado == "" {
		t.Error("sem engine, o passo deveria estar bloqueado")
	}
	if !servico.Pendente {
		t.Error("bloqueado continua pendente: o diagnóstico precisa mostrá-lo")
	}

	// Executar um passo bloqueado tem que dar mensagem útil, não
	// "plano montado sem executor".
	err := servico.Executar(context.Background())
	if err == nil {
		t.Fatal("esperava erro")
	}
	if !strings.Contains(err.Error(), "podman") {
		t.Errorf("a mensagem deveria orientar: %v", err)
	}
}

// Executaveis é o que permite ao devm up fazer o que dá e reportar o resto.
func TestExecutaveisSeparaBloqueados(t *testing.T) {
	passos := []Passo{
		{Nome: "a"},
		{Nome: "b", Bloqueado: "motivo"},
		{Nome: "c"},
	}

	executaveis, bloqueados := Executaveis(passos)
	if len(executaveis) != 2 || len(bloqueados) != 1 {
		t.Errorf("executáveis = %d, bloqueados = %d", len(executaveis), len(bloqueados))
	}
	if bloqueados[0].Nome != "b" {
		t.Errorf("bloqueado errado: %q", bloqueados[0].Nome)
	}
}

// envJaAponta é o que torna o passo idempotente: sem ele, cada devm up
// reescreveria o .env e o git mostraria alteração fantasma.
func TestEnvJaAponta(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		"devmanager.yaml": "services:\n  - postgres:17\n",
	})

	spec, _ := services.ParseSpec("postgres:17")

	if envJaAponta(p, spec, nil) {
		t.Error("sem .env, não deveria considerar configurado")
	}

	envPath := filepath.Join(p.Path, ".env")
	if err := os.WriteFile(envPath, []byte("APP_KEY=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if envJaAponta(p, spec, nil) {
		t.Error(".env sem as chaves de banco não deveria contar")
	}

	// Sem a porta ainda não conta: é a correção do MAIL_PORT vazio.
	if _, err := dotenv.Set(envPath, map[string]string{
		"DB_CONNECTION": "pgsql",
		"DB_DATABASE":   services.NomeDeBanco(p.Name),
	}); err != nil {
		t.Fatal(err)
	}
	if envJaAponta(p, spec, nil) {
		t.Error("sem DB_PORT não deveria contar como configurado")
	}

	// O usuário do serviço é o que discrimina a nossa configuração da de
	// outra ferramenta — ver TestEnvJaApontaExigeOUsuarioDoServico.
	if _, err := dotenv.Set(envPath, map[string]string{
		"DB_PORT":     "5432",
		"DB_USERNAME": spec.Env["POSTGRES_USER"],
	}); err != nil {
		t.Fatal(err)
	}
	if !envJaAponta(p, spec, nil) {
		t.Error("com todas as chaves, deveria contar como configurado")
	}
}

// Regressão: um .env com host correto e PORTA VAZIA era considerado
// configurado, e o passo nunca rodava de novo para corrigir. O sintoma foi
// o MAIL_PORT vazio no segundo projeto a reaproveitar o mailpit.
func TestEnvJaApontaExigeAPorta(t *testing.T) {
	casos := []struct {
		servico string
		valores map[string]string
		querOK  bool
	}{
		{
			servico: "mailpit",
			valores: map[string]string{"MAIL_HOST": "127.0.0.1", "MAIL_MAILER": "smtp"},
			querOK:  false, // sem MAIL_PORT
		},
		{
			servico: "mailpit",
			valores: map[string]string{"MAIL_HOST": "127.0.0.1", "MAIL_MAILER": "smtp", "MAIL_PORT": "1025"},
			querOK:  true,
		},
		{
			servico: "redis",
			valores: map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "app_"},
			querOK:  false, // sem REDIS_PORT
		},
		{
			servico: "mysql",
			valores: map[string]string{"DB_CONNECTION": "mysql", "DB_DATABASE": "app"},
			querOK:  false, // sem DB_PORT
		},
	}

	for _, c := range casos {
		t.Run(c.servico, func(t *testing.T) {
			p := projetoEm(t, map[string]string{
				"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
				"artisan":         "#!/usr/bin/env php",
				"devmanager.yaml": "services:\n  - " + c.servico + "\n",
			})

			if _, err := dotenv.Set(filepath.Join(p.Path, ".env"), c.valores); err != nil {
				t.Fatal(err)
			}

			spec, err := services.ParseSpec(c.servico)
			if err != nil {
				t.Fatal(err)
			}
			if got := envJaAponta(p, spec, nil); got != c.querOK {
				t.Errorf("envJaAponta = %v, esperava %v (valores: %v)", got, c.querOK, c.valores)
			}
		})
	}
}

// Regressão: o instalador do Laravel escreve DB_CONNECTION, DB_PORT e
// DB_DATABASE com exatamente os valores que esperávamos, e a checagem
// concluía "já configurado" POR COINCIDÊNCIA — pulando a criação do banco e
// deixando as credenciais do instalador (root, senha vazia) no .env.
func TestEnvJaApontaExigeOUsuarioDoServico(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		"devmanager.yaml": "services:\n  - mysql\n",
	})

	spec, err := services.ParseSpec("mysql")
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(p.Path, ".env")

	// Exatamente o que o instalador do Laravel deixa.
	if _, err := dotenv.Set(envPath, map[string]string{
		"DB_CONNECTION": "mysql",
		"DB_HOST":       "127.0.0.1",
		"DB_PORT":       "3306",
		"DB_DATABASE":   services.NomeDeBanco(p.Name),
		"DB_USERNAME":   "root",
		"DB_PASSWORD":   "",
	}); err != nil {
		t.Fatal(err)
	}
	if envJaAponta(p, spec, nil) {
		t.Error("credenciais do instalador não podem passar como configuração nossa")
	}

	// Com o usuário do nosso catálogo, aí sim.
	if _, err := dotenv.Set(envPath, map[string]string{
		"DB_USERNAME": spec.Env["MYSQL_USER"],
	}); err != nil {
		t.Fatal(err)
	}
	if !envJaAponta(p, spec, nil) {
		t.Error("com o usuário do serviço, deveria contar como configurado")
	}
}

// O caso que apareceu ao migrar um projeto de postgres:17 para :18 — o
// contêiner novo publica noutra porta, e o .env continuava apontando para o
// antigo. A conferência aprovava qualquer número, então o passo se dava por
// feito e o projeto seguia falando com o banco velho.
func TestEnvNaoApontaQuandoAPortaEDeOutroServico(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":   `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":         "#!/usr/bin/env php",
		"devmanager.yaml": "services:\n  - postgres:18\n",
	})
	spec, _ := services.ParseSpec("postgres:18")
	envPath := filepath.Join(p.Path, ".env")

	base := map[string]string{
		"DB_CONNECTION": "pgsql",
		"DB_HOST":       "127.0.0.1",
		"DB_DATABASE":   services.NomeDeBanco(p.Name),
		"DB_USERNAME":   "laravel",
		"DB_PORT":       "5432", // a porta do contêiner ANTIGO
	}
	if _, err := dotenv.Set(envPath, base); err != nil {
		t.Fatal(err)
	}

	emUso := []services.Porta{{Host: 36101, Interna: 5432}}
	if envJaAponta(p, spec, emUso) {
		t.Error("deu por configurado um .env que aponta para a porta do serviço antigo")
	}

	// Com a porta certa, o passo não se repete à toa.
	if _, err := dotenv.Set(envPath, map[string]string{"DB_PORT": "36101"}); err != nil {
		t.Fatal(err)
	}
	if !envJaAponta(p, spec, emUso) {
		t.Error("marcou como pendente um .env que já aponta para o serviço certo")
	}

	// Sem portas conhecidas — contêiner parado, engine ausente — vale o
	// comportamento antigo: não reescrever o .env por palpite.
	if _, err := dotenv.Set(envPath, map[string]string{"DB_PORT": "5432"}); err != nil {
		t.Fatal(err)
	}
	if !envJaAponta(p, spec, nil) {
		t.Error("sem portas conhecidas, o .env preenchido devia ser aceito")
	}
}
