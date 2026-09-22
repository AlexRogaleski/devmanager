package prepare

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
)

// BancoDeTestes descobre o banco que a suíte do projeto espera encontrar.
//
// O `devm up` cria o banco da aplicação e parava aí. Mas o phpunit.xml do
// Laravel costuma apontar `DB_DATABASE` para outro banco — "testing" —, e
// `artisan test` contra PostgreSQL ou MySQL falha na conexão enquanto ele não
// existir. Criar um banco vazio é barato; descobrir por que a suíte não roda,
// não.
//
// Devolve "" quando não há o que criar: suíte em SQLite, banco em memória, ou
// projeto que usa o mesmo banco da aplicação.
func BancoDeTestes(dirProjeto, bancoDaAplicacao string) string {
	nome := doPHPUnit(dirProjeto)
	if nome == "" {
		nome = doEnvDeTestes(dirProjeto)
	}

	nome = strings.TrimSpace(nome)
	switch {
	case nome == "", nome == bancoDaAplicacao:
		return ""
	// Um banco em memória ou em arquivo é criado pelo próprio SQLite.
	case nome == ":memory:", strings.Contains(nome, "/"), strings.HasSuffix(nome, ".sqlite"):
		return ""
	}
	return nome
}

// doPHPUnit lê as variáveis declaradas no phpunit.xml.
//
// O parser de XML ignora comentários, e isso é exatamente o que queremos: o
// phpunit.xml que o Laravel gera traz as linhas de banco COMENTADAS, e lê-las
// faria o devm criar um banco que o projeto não usa.
func doPHPUnit(dir string) string {
	for _, nome := range []string{"phpunit.xml", "phpunit.xml.dist"} {
		dados, err := os.ReadFile(filepath.Join(dir, nome))
		if err != nil {
			continue
		}

		var doc struct {
			PHP struct {
				Env []struct {
					Nome  string `xml:"name,attr"`
					Valor string `xml:"value,attr"`
				} `xml:"env"`
			} `xml:"php"`
		}
		if err := xml.Unmarshal(dados, &doc); err != nil {
			continue
		}

		valores := make(map[string]string, len(doc.PHP.Env))
		for _, env := range doc.PHP.Env {
			valores[env.Nome] = env.Valor
		}

		// Uma suíte em SQLite não precisa de banco no serviço, mesmo que o
		// DB_DATABASE esteja preenchido.
		if strings.EqualFold(valores["DB_CONNECTION"], "sqlite") {
			return ""
		}
		if banco := valores["DB_DATABASE"]; banco != "" {
			return banco
		}
	}
	return ""
}

// doEnvDeTestes lê o .env.testing, que o Laravel carrega quando APP_ENV=testing.
func doEnvDeTestes(dir string) string {
	env, err := dotenv.Load(filepath.Join(dir, ".env.testing"))
	if err != nil {
		return ""
	}
	if strings.EqualFold(env["DB_CONNECTION"], "sqlite") {
		return ""
	}
	return env["DB_DATABASE"]
}
