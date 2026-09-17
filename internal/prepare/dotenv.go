package prepare

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
	"github.com/AlexRogaleski/devmanager/internal/project"
)

// lerEnv delega ao pacote dotenv, ignorando o erro.
//
// Aqui só queremos inspecionar: um .env ilegível significa "não sei o que tem
// dentro", e o plano trata isso como ausência de configuração. Quem grava no
// arquivo — o passo de serviço — usa dotenv.Set direto e propaga o erro.
func lerEnv(caminho string) map[string]string {
	valores, err := dotenv.Load(caminho)
	if err != nil {
		return map[string]string{}
	}
	return valores
}

// precisaDeAppKey informa se falta gerar a chave da aplicação.
//
// Sem .env o passo é necessário, porque ele virá do .env.example — onde
// APP_KEY vem vazia por padrão.
func precisaDeAppKey(dirProjeto string) bool {
	env := lerEnv(filepath.Join(dirProjeto, ".env"))
	return env["APP_KEY"] == ""
}

// passoSQLite cria o arquivo do banco quando o projeto usa SQLite.
//
// Um Laravel novo vem configurado com SQLite, mas o arquivo do banco está no
// .gitignore — então todo clone chega sem ele, e o primeiro `artisan migrate`
// falha. É um atrito pequeno e universal, exatamente o tipo de coisa que a
// ferramenta deveria absorver.
func passoSQLite(p *project.Project) (Passo, bool) {
	env := lerEnv(filepath.Join(p.Path, ".env"))

	conexao := env["DB_CONNECTION"]
	// Sem .env ainda, olhamos o exemplo: o passo precisa aparecer no plano
	// ANTES do .env existir, senão o detect nunca o mostraria.
	if conexao == "" {
		conexao = lerEnv(filepath.Join(p.Path, ".env.example"))["DB_CONNECTION"]
	}
	if conexao != "sqlite" {
		return Passo{}, false
	}

	banco := env["DB_DATABASE"]
	if banco == "" {
		banco = filepath.Join("database", "database.sqlite")
	}
	if !filepath.IsAbs(banco) {
		banco = filepath.Join(p.Path, banco)
	}

	return Passo{
		Nome:     "touch " + mostrarRelativo(p.Path, banco),
		Porque:   "cria o arquivo do banco SQLite",
		Pendente: !existe(banco),
		executar: func(context.Context) error {
			if err := os.MkdirAll(filepath.Dir(banco), 0o755); err != nil {
				return fmt.Errorf("criando diretório do banco: %w", err)
			}
			// O_EXCL de novo: nunca zerar um banco que já exista.
			f, err := os.OpenFile(banco, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				if os.IsExist(err) {
					return nil
				}
				return fmt.Errorf("criando %s: %w", banco, err)
			}
			return f.Close()
		},
	}, true
}

func mostrarRelativo(base, caminho string) string {
	if rel, err := filepath.Rel(base, caminho); err == nil {
		return rel
	}
	return caminho
}
