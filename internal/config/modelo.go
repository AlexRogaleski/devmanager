package config

import (
	"fmt"
	"slices"
	"strings"
)

// cabecalho abre todo devmanager.yaml gerado.
const cabecalho = `# Dev Manager — configuração deste projeto.
# Versione este arquivo junto com o código: ele descreve o ambiente
# necessário para rodar o projeto.
#
# O que estiver escrito aqui vence qualquer detecção automática.
`

// secaoModelo é um bloco do arquivo: a explicação, o exemplo e como
// escrever o valor quando o projeto tem um.
//
// Manter as três coisas juntas é o ponto. A documentação de uma opção fica
// no arquivo que a pessoa está editando, e não num README que ela teria de
// lembrar de abrir — e o exemplo comentado é o próprio formato correto,
// pronto para descomentar.
type secaoModelo struct {
	doc     string // sem o "# " na frente: a função comentar cuida disso
	exemplo string
	ativo   func(Config) string // "" quando o projeto não define a chave
}

var secoesDoModelo = []secaoModelo{
	{
		doc: `Versão do PHP. Sobrepõe o require.php do composer.json.
  "8.4"     qualquer 8.4.x
  "8.4.12"  exatamente essa
  "^8.4"    8.4 ou superior, abaixo de 9.0`,
		exemplo: `php: "8.4"`,
		ativo: func(c Config) string {
			if c.PHP == "" {
				return ""
			}
			return fmt.Sprintf("php: %q", c.PHP)
		},
	},
	{
		doc: `Versão do Node, na mesma gramática do PHP. Sem esta chave, vale
o .nvmrc ou engines.node do package.json.`,
		exemplo: `node: "22"`,
		ativo: func(c Config) string {
			if c.Node == "" {
				return ""
			}
			return fmt.Sprintf("node: %q", c.Node)
		},
	},
	{
		doc: `Serviços em contêiner que o projeto usa. Veja o catálogo com
` + "`devm service catalog`" + `.

Uma versão significa UM contêiner servindo todos os projetos que
pedirem a mesma: declarar "postgres:17" em três projetos não sobe
três bancos.`,
		exemplo: `services:
  - postgres:17
  - redis
  - mailpit`,
		ativo: func(c Config) string {
			if len(c.Services) == 0 {
				return ""
			}
			linhas := []string{"services:"}
			for _, s := range c.Services {
				linhas = append(linhas, "  - "+escalarYAML(s))
			}
			return strings.Join(linhas, "\n")
		},
	},
	{
		doc: `Processos que ` + "`devm start`" + ` sobe.

Sem esta chave, o padrão é ` + "`php artisan serve`" + ` mais o script de
frontend do package.json. Declarar aqui substitui o padrão INTEIRO —
inclusive o servidor.

{{port}} recebe a porta que o ambiente ganhou, e é nela que o proxy
publica <projeto>.test. Use {{port:nome}} para um segundo processo que
também escute: a porta é sorteada uma vez e vale para o ambiente todo.

Os processos recebem as portas no ambiente: DEVM_PORT para a principal
e DEVM_PORT_VITE para {{port:vite}}. É por aí que o vite.config.js
descobre onde o backend subiu.`,
		exemplo: `processes:
  serve: php artisan serve --host=127.0.0.1 --port={{port}}
  queue: php artisan queue:listen --tries=1
  vite: npm run dev
  logs: php artisan pail --timeout=0`,
		ativo: func(c Config) string {
			if len(c.Processes) == 0 {
				return ""
			}
			linhas := []string{"processes:"}
			for _, nome := range ordenadas(c.Processes) {
				linhas = append(linhas, fmt.Sprintf("  %s: %s", nome, escalarYAML(c.Processes[nome])))
			}
			return strings.Join(linhas, "\n")
		},
	},
	{
		doc: `Diretivas do php.ini que o Dev Manager gera para este projeto.

O padrão já é generoso para desenvolvimento: memory_limit sem limite e
uploads de 100M. Sobreponha o que o projeto precisar.`,
		exemplo: `php_ini:
  max_execution_time: "120"
  upload_max_filesize: 200M`,
		ativo: func(c Config) string {
			if len(c.PHPIni) == 0 {
				return ""
			}
			linhas := []string{"php_ini:"}
			for _, chave := range ordenadas(c.PHPIni) {
				linhas = append(linhas, fmt.Sprintf("  %s: %q", chave, c.PHPIni[chave]))
			}
			return strings.Join(linhas, "\n")
		},
	},
}

// Modelo devolve um devmanager.yaml completo: todas as opções documentadas,
// as que o projeto define ativas e as demais comentadas como exemplo.
func Modelo(c Config) string {
	var b strings.Builder
	b.WriteString(cabecalho)

	for _, s := range secoesDoModelo {
		b.WriteString("\n")
		b.WriteString(comentar(s.doc))

		if valor := s.ativo(c); valor != "" {
			b.WriteString(valor)
			b.WriteString("\n")
			continue
		}
		b.WriteString(comentar(s.exemplo))
	}
	return b.String()
}

// comentar prefixa cada linha com "# ", deixando as vazias como "#".
func comentar(texto string) string {
	linhas := strings.Split(texto, "\n")
	for i, l := range linhas {
		if l == "" {
			linhas[i] = "#"
			continue
		}
		linhas[i] = "# " + l
	}
	return strings.Join(linhas, "\n") + "\n"
}

// escalarYAML devolve o valor pronto para ir ao arquivo, com aspas quando
// elas são necessárias.
//
// Sem isso, um processo declarado como "{{port}} algo" abriria um mapeamento
// em fluxo aos olhos do YAML e o arquivo ficaria inválido — um erro que só
// apareceria na próxima leitura, longe de quem o causou.
func escalarYAML(v string) string {
	if v == "" {
		return `""`
	}
	if strings.TrimSpace(v) != v || strings.Contains(v, ": ") || strings.Contains(v, " #") {
		return fmt.Sprintf("%q", v)
	}
	if strings.ContainsAny(v[:1], "{}[]&*#?|>%@`!\"',-") {
		return fmt.Sprintf("%q", v)
	}
	return v
}

func ordenadas[V any](m map[string]V) []string {
	chaves := make([]string, 0, len(m))
	for k := range m {
		chaves = append(chaves, k)
	}
	slices.Sort(chaves)
	return chaves
}
