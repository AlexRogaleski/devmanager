package prepare

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// prazoDeProntidao é quanto esperamos um serviço aceitar conexões.
//
// Um minuto parece muito, mas a primeira subida de um MySQL inicializa o
// diretório de dados e pode passar de trinta segundos numa máquina ocupada.
// Nas vezes seguintes a espera é de milissegundos.
const prazoDeProntidao = time.Minute

// passoServico sobe um serviço e liga o projeto a ele.
//
// O passo é deliberadamente "gordo" — sobe, espera ficar pronto, cria o banco
// do projeto e ajusta o .env — porque essas quatro coisas só fazem sentido
// juntas. Separá-las em quatro passos produziria uma lista confusa onde três
// deles nunca poderiam rodar isolados.
func passoServico(p *project.Project, opts Opcoes, spec services.Spec) Passo {
	pendente := true
	if opts.Servicos != nil {
		if estado, err := opts.Servicos.Estado(context.Background(), spec); err == nil {
			// Já rodando E o .env já apontando para ele: nada a fazer.
			pendente = estado != services.EstadoRodando || !envJaAponta(p, spec)
		}
	}

	passo := Passo{
		Nome:     fmt.Sprintf("serviço %s:%s", spec.Nome, spec.Versao),
		Porque:   spec.Descricao,
		Pendente: pendente,
	}

	if opts.Servicos == nil {
		passo.Bloqueado = "precisa de podman ou docker"
		return passo
	}

	m := opts.Servicos
	passo.executar = func(ctx context.Context) error {
		// Só faz sentido escolher porta na CRIAÇÃO. Um contêiner que já
		// existe tem as portas dele fixadas, e mexer nisso exigiria recriá-lo
		// — o que descartaria a configuração anterior sem pedir.
		aSubir := spec
		if estado, err := m.Estado(ctx, spec); err == nil && estado == services.EstadoAusente {
			ajustada, notas := m.AjustarPortasOcupadas(spec)
			aSubir = ajustada

			for _, nota := range notas {
				if opts.Saida != nil {
					fmt.Fprintf(opts.Saida, "  %s\n", nota)
				}
			}
		}

		servico, err := m.Start(ctx, aSubir)
		if err != nil {
			return err
		}
		spec = aSubir

		if err := m.AguardarPronto(ctx, aSubir, prazoDeProntidao); err != nil {
			return err
		}

		nomeBanco := services.NomeDeBanco(p.Name)
		if err := m.CriarBanco(ctx, aSubir, nomeBanco); err != nil {
			return err
		}

		// As portas vêm do serviço em execução, não da spec: se o contêiner
		// foi criado antes com --port, é a porta real que precisa ir para o
		// .env do projeto.
		valores := envParaServico(aSubir, servico.Portas, nomeBanco, p.Name)
		if len(valores) == 0 {
			return nil
		}

		_, err = dotenv.Set(filepath.Join(p.Path, ".env"), valores)
		return err
	}

	return passo
}

// envParaServico traduz um serviço nas variáveis que o Laravel espera.
//
// Este mapeamento é conhecimento sobre LARAVEL, por isso vive aqui e não no
// pacote services — que não deveria ter opinião sobre frameworks.
func envParaServico(spec services.Spec, portas []services.Porta, nomeBanco, nomeProjeto string) map[string]string {
	porta := func(rotulo string) int {
		for _, p := range portas {
			if p.Rotulo == rotulo {
				return p.Host
			}
		}
		if len(portas) > 0 && rotulo == "" {
			return portas[0].Host
		}
		return 0
	}

	switch spec.Nome {
	case "postgres":
		return map[string]string{
			"DB_CONNECTION": "pgsql",
			"DB_HOST":       "127.0.0.1",
			"DB_PORT":       itoa(porta("")),
			"DB_DATABASE":   nomeBanco,
			"DB_USERNAME":   spec.Env["POSTGRES_USER"],
			"DB_PASSWORD":   spec.Env["POSTGRES_PASSWORD"],
		}

	case "mysql", "mariadb":
		usuario := spec.Env["MYSQL_USER"]
		senha := spec.Env["MYSQL_PASSWORD"]
		if usuario == "" {
			usuario, senha = spec.Env["MARIADB_USER"], spec.Env["MARIADB_PASSWORD"]
		}
		return map[string]string{
			"DB_CONNECTION": "mysql",
			"DB_HOST":       "127.0.0.1",
			"DB_PORT":       itoa(porta("")),
			"DB_DATABASE":   nomeBanco,
			"DB_USERNAME":   usuario,
			"DB_PASSWORD":   senha,
		}

	case "redis":
		return map[string]string{
			"REDIS_HOST": "127.0.0.1",
			"REDIS_PORT": itoa(porta("")),
			// O Redis não tem bancos nomeados, só índices de 0 a 15 — pouco
			// para oito projetos. O isolamento vem do prefixo de chave, que
			// o Laravel já aplica em cache, sessão e filas.
			"REDIS_PREFIX": services.NomeDeBanco(nomeProjeto) + "_",
		}

	case "mailpit":
		return map[string]string{
			"MAIL_MAILER":     "smtp",
			"MAIL_HOST":       "127.0.0.1",
			"MAIL_PORT":       itoa(porta("smtp")),
			"MAIL_USERNAME":   "",
			"MAIL_PASSWORD":   "",
			"MAIL_ENCRYPTION": "",
		}
	}
	return nil
}

// envJaAponta confere se o .env do projeto já está configurado para o serviço.
func envJaAponta(p *project.Project, spec services.Spec) bool {
	atual, err := dotenv.Load(filepath.Join(p.Path, ".env"))
	if err != nil {
		return false
	}

	switch spec.Nome {
	case "postgres":
		return atual["DB_CONNECTION"] == "pgsql" && atual["DB_DATABASE"] == services.NomeDeBanco(p.Name)
	case "mysql", "mariadb":
		return atual["DB_CONNECTION"] == "mysql" && atual["DB_DATABASE"] == services.NomeDeBanco(p.Name)
	case "redis":
		return atual["REDIS_HOST"] == "127.0.0.1" && atual["REDIS_PREFIX"] != ""
	case "mailpit":
		return atual["MAIL_HOST"] == "127.0.0.1" && atual["MAIL_MAILER"] == "smtp"
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// SpecsDoProjeto interpreta a lista services do devmanager.yaml.
func SpecsDoProjeto(p *project.Project) ([]services.Spec, error) {
	if p.Config == nil || len(p.Config.Services) == 0 {
		return nil, nil
	}

	specs := make([]services.Spec, 0, len(p.Config.Services))
	for _, texto := range p.Config.Services {
		spec, err := services.ParseSpec(texto)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// temBancoDeDados informa se algum serviço declarado é um banco.
//
// Usado para não oferecer o passo do arquivo SQLite a um projeto que já
// declarou PostgreSQL: seriam dois bancos configurados e um deles ignorado.
func temBancoDeDados(specs []services.Spec) bool {
	for _, s := range specs {
		if s.Banco != services.BancoNenhum {
			return true
		}
	}
	return false
}
