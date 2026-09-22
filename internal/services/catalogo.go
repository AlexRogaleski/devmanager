package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Porta mapeia uma porta do contêiner para o host.
type Porta struct {
	Host    int
	Interna int
	Rotulo  string // "smtp", "web" — para serviços com mais de uma porta
}

// TipoBanco identifica o dialeto de banco de dados de um serviço.
//
// É um enum, e não uma função guardada no catálogo, para que a Definicao
// continue sendo dado puro — serializável e inspecionável. A lógica de cada
// dialeto fica no código que provisiona.
type TipoBanco string

const (
	BancoNenhum   TipoBanco = ""
	BancoPostgres TipoBanco = "postgres"
	BancoMySQL    TipoBanco = "mysql"
	BancoMariaDB  TipoBanco = "mariadb"
)

// Definicao descreve um serviço que o Dev Manager sabe subir.
type Definicao struct {
	Nome         string
	Imagem       string // sem a tag; a versão vira a tag
	VersaoPadrao string
	Portas       []Porta
	Env          map[string]string

	// VolumeInterno é o caminho, dentro do contêiner, que guarda os dados.
	// Vazio significa serviço sem estado, como o Mailpit.
	VolumeInterno string

	// Banco indica o dialeto, quando o serviço é um banco de dados.
	Banco TipoBanco

	// Prontidao é o comando rodado DENTRO do contêiner para saber se o
	// serviço já aceita conexões.
	//
	// Existe porque `run --detach` retorna assim que o contêiner INICIA, não
	// quando o serviço está pronto: um PostgreSQL recém-criado leva alguns
	// segundos inicializando o cluster, e um CREATE DATABASE nesse intervalo
	// falha com "connection refused".
	//
	// As sondas de banco checam por TCP (-h 127.0.0.1), nunca pelo socket
	// Unix, e isso NÃO é detalhe. As imagens oficiais de PostgreSQL e MySQL
	// sobem um servidor TEMPORÁRIO durante a inicialização para rodar os
	// scripts de init, e ele escuta apenas no socket local. Uma sonda por
	// socket aprova esse servidor provisório; o comando seguinte então
	// esbarra no desligamento dele:
	//
	//	FATAL: the database system is shutting down
	//
	// O listener TCP só aparece quando o servidor definitivo sobe — por isso
	// ele é o sinal certo.
	Prontidao []string

	// Descricao aparece no `devm service catalog`.
	Descricao string
}

// catalogo são os serviços conhecidos.
//
// As credenciais padrão são deliberadamente óbvias e iguais às que o Laravel
// já espera no .env.example. Isto é um ambiente de DESENVOLVIMENTO local,
// acessível só pelo loopback: segredo forte aqui não protege de nada e só
// adiciona atrito. Quando os serviços puderem ser expostos na rede, a
// primeira coisa a mudar é isto.
var catalogo = map[string]Definicao{
	"postgres": {
		Nome:         "postgres",
		Imagem:       "docker.io/library/postgres",
		VersaoPadrao: "17",
		Portas:       []Porta{{Host: 5432, Interna: 5432}},
		Env: map[string]string{
			"POSTGRES_USER":     "laravel",
			"POSTGRES_PASSWORD": "secret",
			"POSTGRES_DB":       "laravel",
		},
		VolumeInterno: "/var/lib/postgresql/data",
		Banco:         BancoPostgres,
		Prontidao:     []string{"pg_isready", "-h", "127.0.0.1", "-U", "laravel", "-d", "postgres", "-q"},
		Descricao:     "banco de dados PostgreSQL",
	},
	"mysql": {
		Nome:         "mysql",
		Imagem:       "docker.io/library/mysql",
		VersaoPadrao: "8.4",
		Portas:       []Porta{{Host: 3306, Interna: 3306}},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "secret",
			"MYSQL_DATABASE":      "laravel",
			"MYSQL_USER":          "laravel",
			"MYSQL_PASSWORD":      "secret",
		},
		VolumeInterno: "/var/lib/mysql",
		Banco:         BancoMySQL,
		Prontidao:     []string{"mysqladmin", "ping", "-h", "127.0.0.1", "-uroot", "-psecret", "--silent"},
		Descricao:     "banco de dados MySQL",
	},
	"mariadb": {
		Nome:         "mariadb",
		Imagem:       "docker.io/library/mariadb",
		VersaoPadrao: "11",
		Portas:       []Porta{{Host: 3306, Interna: 3306}},
		Env: map[string]string{
			"MARIADB_ROOT_PASSWORD": "secret",
			"MARIADB_DATABASE":      "laravel",
			"MARIADB_USER":          "laravel",
			"MARIADB_PASSWORD":      "secret",
		},
		VolumeInterno: "/var/lib/mysql",
		Banco:         BancoMariaDB,
		Prontidao:     []string{"mariadb-admin", "ping", "-h", "127.0.0.1", "-uroot", "-psecret", "--silent"},
		Descricao:     "banco de dados MariaDB",
	},
	"redis": {
		Nome:          "redis",
		Imagem:        "docker.io/library/redis",
		VersaoPadrao:  "8",
		Portas:        []Porta{{Host: 6379, Interna: 6379}},
		VolumeInterno: "/data",
		Prontidao:     []string{"redis-cli", "ping"},
		Descricao:     "cache e filas Redis",
	},
	"mailpit": {
		Nome:         "mailpit",
		Imagem:       "docker.io/axllent/mailpit",
		VersaoPadrao: "latest",
		Portas: []Porta{
			{Host: 1025, Interna: 1025, Rotulo: "smtp"},
			{Host: 8025, Interna: 8025, Rotulo: "web"},
		},
		Descricao: "captura de e-mails em desenvolvimento",
	},
}

// Catalogo devolve os serviços conhecidos, em ordem estável.
func Catalogo() []Definicao {
	defs := make([]Definicao, 0, len(catalogo))
	for _, d := range catalogo {
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Nome < defs[j].Nome })
	return defs
}

// Spec é um pedido concreto: um serviço numa versão.
type Spec struct {
	Definicao
	Versao string
	Portas []Porta // resolvidas, podendo diferir das padrão
}

// Container devolve o nome do contêiner desta spec.
//
// O nome carrega serviço e versão porque os serviços são COMPARTILHADOS entre
// projetos, não replicados por projeto. Subir um PostgreSQL por projeto
// custaria ~100 MB de RAM cada, e nenhum time de desenvolvimento precisa
// disso: bancos diferentes no mesmo servidor resolvem o isolamento.
func (s Spec) Container() string {
	return fmt.Sprintf("%s%s-%s", prefixoContainer, s.Nome, versaoSegura(s.Versao))
}

// Volume devolve o nome do volume nomeado desta spec.
//
// Usamos volume NOMEADO, gerenciado pelo engine, e não bind mount de uma
// pasta do host. Isso evita de uma vez o problema de rótulo SELinux em
// Fedora e derivados, e a confusão de UID entre host e contêiner — dois
// clássicos de "funciona na minha máquina".
func (s Spec) Volume() string {
	return s.Container() + "-dados"
}

// ImagemCompleta devolve imagem:tag.
func (s Spec) ImagemCompleta() string {
	return s.Imagem + ":" + s.Versao
}

const prefixoContainer = "devm-"

// ParseSpec interpreta "postgres:17", "redis" ou "mailpit:latest".
func ParseSpec(texto string) (Spec, error) {
	nome, versao, temVersao := strings.Cut(strings.TrimSpace(texto), ":")
	nome = strings.ToLower(strings.TrimSpace(nome))

	def, ok := catalogo[nome]
	if !ok {
		return Spec{}, &ServicoDesconhecidoError{Nome: nome}
	}

	if !temVersao || strings.TrimSpace(versao) == "" {
		versao = def.VersaoPadrao
	}
	versao = strings.TrimSpace(versao)

	// As portas começam nas padrão. Resolver conflitos entre versões
	// diferentes do mesmo serviço é responsabilidade de quem vai subir,
	// que é quem enxerga o que já está rodando.
	portas := make([]Porta, len(def.Portas))
	copy(portas, def.Portas)

	spec := Spec{Definicao: def, Versao: versao, Portas: portas}
	spec.VolumeInterno = volumeDaVersao(nome, versao, def.VolumeInterno)
	return spec, nil
}

// volumeDaVersao corrige o ponto de montagem quando a imagem mudou de layout
// entre versões.
//
// O PostgreSQL 18 passou a guardar os dados em subdiretório por versão
// (/var/lib/postgresql/18/docker) e a esperar o volume um nível acima. Montar
// no caminho antigo faz o contêiner recusar-se a subir: ele encontra um
// diretório de dados onde não deveria haver um e sai com erro, em vez de
// arriscar corromper o que estiver lá.
//
// Ver docker-library/postgres#1259.
func volumeDaVersao(nome, versao, padrao string) string {
	if nome != "postgres" {
		return padrao
	}

	major, _, _ := strings.Cut(versao, ".")
	if n, err := strconv.Atoi(major); err == nil && n >= 18 {
		return "/var/lib/postgresql"
	}
	return padrao
}

// versaoSegura transforma a versão num pedaço de nome válido para contêiner.
func versaoSegura(v string) string {
	limpo := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return '-'
		default:
			return -1 // descarta
		}
	}, v)

	if limpo == "" {
		return "latest"
	}
	return limpo
}

// ServicoDesconhecidoError lista o que existe, em vez de só recusar.
type ServicoDesconhecidoError struct {
	Nome string
}

func (e *ServicoDesconhecidoError) Error() string {
	nomes := make([]string, 0, len(catalogo))
	for n := range catalogo {
		nomes = append(nomes, n)
	}
	sort.Strings(nomes)

	return fmt.Sprintf("serviço desconhecido: %q (disponíveis: %s)", e.Nome, strings.Join(nomes, ", "))
}
