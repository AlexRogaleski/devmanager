// Package prepare descreve e executa o que falta para um projeto rodar.
//
// Ele é a ÚNICA fonte de verdade sobre isso: o `devm detect` lista os passos
// pendentes e o `devm up` executa os mesmos passos, do mesmo plano. Se as duas
// coisas fossem calculadas em lugares diferentes, elas divergiriam — e o
// usuário veria um diagnóstico que não corresponde ao que a ferramenta faz.
package prepare

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/project"
)

// Executor roda um comando no ambiente do projeto.
//
// É uma interface de um método só, satisfeita por *runner.Runner. Existe para
// que este pacote não dependa do runner: assim dá para montar um plano só para
// INSPECIONAR (o caso do detect) sem ter um runtime resolvido, e para testar
// os passos com um executor falso que apenas registra o que seria chamado.
type Executor interface {
	Run(ctx context.Context, nome string, args ...string) error
}

// ErrSemExecutor sai quando se tenta executar um plano montado só para leitura.
var ErrSemExecutor = errors.New("plano montado sem executor")

// Passo é uma etapa de preparação.
type Passo struct {
	// Nome é o comando equivalente, como a pessoa escreveria no terminal.
	// Mostrar isso — em vez de "Instalando dependências..." — mantém a
	// ferramenta auditável: dá para reproduzir à mão o que ela faz.
	Nome string

	// Porque explica a necessidade, para quando o passo aparece no detect.
	Porque string

	// Pendente indica se ainda precisa ser executado.
	Pendente bool

	executar func(ctx context.Context) error
}

// Executar roda o passo.
func (p Passo) Executar(ctx context.Context) error {
	if p.executar == nil {
		return ErrSemExecutor
	}
	return p.executar(ctx)
}

// Opcoes ajusta o que entra no plano.
type Opcoes struct {
	// Migrate inclui `artisan migrate`. Fica fora por padrão porque migração
	// é escrita em banco: num projeto apontado para um Postgres compartilhado,
	// rodar sem pedir seria destrutivo. O usuário opta explicitamente.
	Migrate bool

	// SemNode pula a instalação de dependências de frontend.
	SemNode bool

	// Composer é o comando a usar, já com eventuais argumentos iniciais —
	// por exemplo ["/caminho/php", "/caminho/composer.phar"]. Vazio cai para
	// o "composer" do PATH, que funciona mas não garante a versão de PHP.
	Composer []string
}

// Plano monta a lista ordenada de passos para preparar o projeto.
//
// ex pode ser nil: nesse caso o plano serve só para inspeção, e chamar
// Executar devolve ErrSemExecutor. É como o detect usa.
func Plano(p *project.Project, ex Executor, opts Opcoes) []Passo {
	var passos []Passo

	if p.Kind != project.KindUnknown {
		passos = append(passos, passoComposer(p, ex, opts))
	}

	if p.IsLaravel() {
		passos = append(passos, passoEnv(p))
		passos = append(passos, passoAppKey(p, ex))
	}

	if !opts.SemNode {
		if passo, ok := passoNode(p, ex); ok {
			passos = append(passos, passo)
		}
	}

	if p.IsLaravel() {
		if passo, ok := passoSQLite(p); ok {
			passos = append(passos, passo)
		}
		if opts.Migrate {
			passos = append(passos, passoMigrate(p, ex))
		}
	}

	return passos
}

// Pendentes filtra só o que ainda falta fazer.
func Pendentes(passos []Passo) []Passo {
	var faltando []Passo
	for _, p := range passos {
		if p.Pendente {
			faltando = append(faltando, p)
		}
	}
	return faltando
}

func passoComposer(p *project.Project, ex Executor, opts Opcoes) Passo {
	cmd := opts.Composer
	if len(cmd) == 0 {
		cmd = []string{"composer"}
	}
	args := append(append([]string{}, cmd[1:]...), "install")

	return Passo{
		Nome:     "composer install",
		Porque:   "instala as dependências PHP",
		Pendente: !p.HasVendor,
		executar: comando(ex, cmd[0], args...),
	}
}

// passoEnv copia o .env.example.
//
// Fazemos a cópia em Go em vez de rodar `cp`: menos um processo, funciona
// igual no macOS, e nada depende de um shell estar presente.
func passoEnv(p *project.Project) Passo {
	origem := filepath.Join(p.Path, ".env.example")
	destino := filepath.Join(p.Path, ".env")

	return Passo{
		Nome:     "cp .env.example .env",
		Porque:   "cria o arquivo de ambiente",
		Pendente: !p.HasEnv && existe(origem),
		executar: func(context.Context) error {
			dados, err := os.ReadFile(origem)
			if err != nil {
				return fmt.Errorf("lendo .env.example: %w", err)
			}
			// O_EXCL faz a criação falhar se o arquivo já existir. É a
			// proteção contra sobrescrever um .env com credenciais reais
			// por causa de uma corrida ou de um estado lido antes.
			f, err := os.OpenFile(destino, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				if os.IsExist(err) {
					return nil // alguém criou no meio do caminho: tudo bem
				}
				return fmt.Errorf("criando .env: %w", err)
			}
			defer f.Close()

			if _, err := f.Write(dados); err != nil {
				return fmt.Errorf("escrevendo .env: %w", err)
			}
			return nil
		},
	}
}

func passoAppKey(p *project.Project, ex Executor) Passo {
	return Passo{
		Nome:     "php artisan key:generate",
		Porque:   "gera a chave de criptografia da aplicação",
		Pendente: precisaDeAppKey(p.Path),
		executar: comando(ex, "php", filepath.Join(p.Path, "artisan"), "key:generate"),
	}
}

func passoMigrate(p *project.Project, ex Executor) Passo {
	return Passo{
		Nome:     "php artisan migrate",
		Porque:   "cria as tabelas do banco",
		Pendente: true, // só entra no plano quando pedido explicitamente
		executar: comando(ex, "php", p.ArtisanPath(), "migrate", "--force"),
	}
}

// comando devolve a função que executa um comando, ou nil se não há executor.
func comando(ex Executor, nome string, args ...string) func(context.Context) error {
	if ex == nil {
		return nil
	}
	return func(ctx context.Context) error {
		return ex.Run(ctx, nome, args...)
	}
}

func existe(caminho string) bool {
	_, err := os.Stat(caminho)
	return err == nil
}
