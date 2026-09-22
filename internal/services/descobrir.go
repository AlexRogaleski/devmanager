package services

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
)

// Achado é um serviço deduzido dos arquivos do projeto, com a pista que o
// revelou.
//
// A origem existe para que a decisão seja auditável. Um comando que declara
// "postgres:17" sem dizer de onde tirou isso obriga quem lê a confiar; com a
// pista, a pessoa confere em dois segundos e corrige se for o caso.
type Achado struct {
	Spec   Spec
	Origem string
}

// arquivosDeCompose são os nomes que o Docker Compose reconhece, na ordem em
// que ele próprio procura.
var arquivosDeCompose = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// imagensConhecidas liga o nome de uma imagem ao serviço do catálogo.
//
// A chave é o nome da imagem sem registro nem tag: "mysql/mysql-server" vira
// "mysql-server". O MailHog entra aqui de propósito — o projeto que o usava
// quer um servidor SMTP de desenvolvimento, e é isso que o Mailpit é.
var imagensConhecidas = map[string]string{
	"postgres":     "postgres",
	"postgis":      "postgres",
	"mysql":        "mysql",
	"mysql-server": "mysql",
	"mariadb":      "mariadb",
	"redis":        "redis",
	"valkey":       "redis",
	"mailpit":      "mailpit",
	"mailhog":      "mailpit",
}

// Descobrir procura, nos arquivos do projeto, os serviços que ele já usa.
//
// Serve à migração: um projeto que vinha do Sail tem tudo descrito no
// compose.yaml, e um que nunca usou contêiner tem as pistas no .env. Ler os
// dois poupa a tradução manual — que foi como os primeiros projetos vieram
// para cá, arquivo por arquivo.
//
// O compose vence o .env: ele diz a VERSÃO, e o .env só diz o dialeto.
func Descobrir(dir string) []Achado {
	achados := make(map[string]Achado)

	for _, a := range doCompose(dir) {
		if _, ja := achados[a.Spec.Nome]; !ja {
			achados[a.Spec.Nome] = a
		}
	}
	for _, a := range doEnv(dir) {
		if _, ja := achados[a.Spec.Nome]; !ja {
			achados[a.Spec.Nome] = a
		}
	}

	nomes := make([]string, 0, len(achados))
	for nome := range achados {
		nomes = append(nomes, nome)
	}
	slices.Sort(nomes)

	saida := make([]Achado, 0, len(nomes))
	for _, nome := range nomes {
		saida = append(saida, achados[nome])
	}
	return saida
}

// LerCompose devolve o arquivo de compose do projeto e o seu conteúdo.
//
// Exportada porque o compose é a melhor fonte sobre um projeto que vinha do
// Sail, e nem tudo que se tira dele é serviço: a versão do PHP, por exemplo,
// está no caminho do runtime que o Dockerfile usa.
func LerCompose(dir string) (nome string, dados []byte, ok bool) {
	nome, dados = primeiroCompose(dir)
	return nome, dados, dados != nil
}

// doCompose lê o compose do projeto, quando existe.
func doCompose(dir string) []Achado {
	arquivo, dados := primeiroCompose(dir)
	if dados == nil {
		return nil
	}

	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(dados, &doc); err != nil {
		// Compose inválido não é problema nosso: seguimos com o .env.
		return nil
	}

	var achados []Achado
	for _, servico := range doc.Services {
		// Sem image o serviço é construído por Dockerfile — é o próprio
		// app, não algo que saibamos subir.
		if servico.Image == "" {
			continue
		}

		nome, versao, ok := doCatalogo(servico.Image)
		if !ok {
			continue
		}

		spec, err := ParseSpec(nome + ":" + versao)
		if err != nil {
			continue
		}

		origem := arquivo
		if tag := tagDe(servico.Image); tag != "" && tag != versao {
			origem = fmt.Sprintf("%s (%s → %s)", arquivo, tag, versao)
		}
		achados = append(achados, Achado{Spec: spec, Origem: origem})
	}
	return achados
}

func primeiroCompose(dir string) (string, []byte) {
	for _, nome := range arquivosDeCompose {
		dados, err := os.ReadFile(filepath.Join(dir, nome))
		if err == nil {
			return nome, dados
		}
	}
	return "", nil
}

// doCatalogo traduz uma imagem de contêiner em serviço e versão do catálogo.
func doCatalogo(imagem string) (nome, versao string, ok bool) {
	caminho, tag, _ := strings.Cut(imagem, ":")
	caminho = strings.TrimSpace(caminho)

	// "docker.io/mysql/mysql-server" → "mysql-server"
	base := caminho
	if i := strings.LastIndex(caminho, "/"); i >= 0 {
		base = caminho[i+1:]
	}

	nome, conhecida := imagensConhecidas[base]
	if !conhecida {
		return "", "", false
	}

	def, existe := catalogo[nome]
	if !existe {
		return "", "", false
	}
	return nome, versaoDaTag(nome, tag, def.VersaoPadrao), true
}

// versaoDaTag decide a versão a declarar a partir da tag da imagem.
//
// Tags como "alpine", "latest" ou "8-bookworm" não são versões que o
// catálogo saiba servir, e o padrão dele é melhor palpite que qualquer
// tentativa de interpretar variantes de imagem.
func versaoDaTag(nome, tag, padrao string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag[0] < '0' || tag[0] > '9' {
		return padrao
	}

	// Só a parte numérica: "8.0.35-debian" vira "8.0.35".
	corte := strings.IndexFunc(tag, func(r rune) bool {
		return r != '.' && (r < '0' || r > '9')
	})
	if corte > 0 {
		tag = tag[:corte]
	}

	// O MariaDB do catálogo é sondado com `mariadb-admin`, que só existe a
	// partir do 11. Declarar o 10.6 de um compose antigo criaria um serviço
	// que nunca é considerado pronto.
	if nome == "mariadb" && maiorQue(11, tag) {
		return padrao
	}
	return tag
}

// maiorQue informa se minimo é maior que o major da versão informada.
func maiorQue(minimo int, versao string) bool {
	major, _, _ := strings.Cut(versao, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n < minimo
}

// tagDe devolve a tag declarada na imagem, se houver.
func tagDe(imagem string) string {
	_, tag, _ := strings.Cut(imagem, ":")
	return tag
}

// doEnv lê o .env (ou o .env.example) em busca do que o projeto conecta.
func doEnv(dir string) []Achado {
	arquivo := ".env"
	env, err := dotenv.Load(filepath.Join(dir, arquivo))
	if err != nil || len(env) == 0 {
		arquivo = ".env.example"
		if env, err = dotenv.Load(filepath.Join(dir, arquivo)); err != nil {
			return nil
		}
	}

	var achados []Achado
	adicionar := func(nome, pista string) {
		spec, err := ParseSpec(nome)
		if err != nil {
			return
		}
		achados = append(achados, Achado{Spec: spec, Origem: fmt.Sprintf("%s (%s)", arquivo, pista)})
	}

	switch conexao := strings.ToLower(env["DB_CONNECTION"]); conexao {
	case "pgsql", "postgres", "postgresql":
		adicionar("postgres", "DB_CONNECTION="+conexao)
	case "mysql":
		adicionar("mysql", "DB_CONNECTION=mysql")
	case "mariadb":
		adicionar("mariadb", "DB_CONNECTION=mariadb")
	}

	if pista, usa := usaRedis(env); usa {
		adicionar("redis", pista)
	}
	if pista, usa := usaMailpit(env); usa {
		adicionar("mailpit", pista)
	}
	return achados
}

// usaRedis procura qualquer subsistema apontado para o Redis.
func usaRedis(env map[string]string) (string, bool) {
	for _, chave := range []string{"CACHE_STORE", "CACHE_DRIVER", "SESSION_DRIVER", "QUEUE_CONNECTION", "BROADCAST_CONNECTION"} {
		if strings.EqualFold(env[chave], "redis") {
			return chave + "=redis", true
		}
	}
	// REDIS_HOST sozinho não basta: o .env.example do Laravel traz a chave
	// preenchida mesmo em projeto que não usa Redis para nada.
	return "", false
}

// usaMailpit reconhece o servidor SMTP de desenvolvimento.
func usaMailpit(env map[string]string) (string, bool) {
	if env["MAIL_PORT"] == "1025" {
		return "MAIL_PORT=1025", true
	}

	host := strings.ToLower(env["MAIL_HOST"])
	for _, marca := range []string{"mailpit", "mailhog"} {
		if strings.Contains(host, marca) {
			return "MAIL_HOST=" + host, true
		}
	}
	return "", false
}
