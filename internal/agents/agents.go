// Package agents escreve instruções de uso do Dev Manager para assistentes
// de IA que trabalham no projeto.
//
// Não é conveniência: é correção. Um projeto migrado do Sail costuma ter, no
// CLAUDE.md, uma linha dizendo "o php não está no PATH, use vendor/bin/sail".
// Depois da migração essa instrução está ERRADA, e o assistente vai continuar
// chamando o Sail — subindo contêineres que o Dev Manager acabou de tornar
// desnecessários, ou falhando porque o Sail está parado.
//
// O conteúdo vai num arquivo PRÓPRIO, e não num bloco dentro do CLAUDE.md:
// nada escrito à mão corre risco, remover é `rm`, e regenerar não exige
// cuidado com o que está em volta. Em troca, os assistentes não leem esse
// arquivo sozinhos — por isso existe o comando de ligação, que acrescenta uma
// linha de referência aos documentos que eles já leem.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Servico descreve um serviço do projeto para as instruções.
type Servico struct {
	Nome   string
	Versao string
	Portas []int
}

// Contexto é o estado do projeto, do qual as instruções são geradas.
//
// Geramos a partir do estado REAL, e não de um modelo fixo: dizer ao
// assistente "o PHP é 8.4.23 e o banco é MySQL na 3306" é útil; dizer "use o
// devm" sem os detalhes só transfere a dúvida.
type Contexto struct {
	Projeto    string
	PHP        string
	PHPOrigem  string
	Node       string
	Dominio    string
	TemArtisan bool
	TemNode    bool
	Servicos   []Servico
}

// Documento monta o arquivo de instruções.
func Documento(c Contexto) string {
	var b strings.Builder

	b.WriteString("# Dev Manager\n\n")
	b.WriteString("> Gerado por `devm agents`. Não edite: o conteúdo é substituído a cada\n")
	b.WriteString("> execução. Instruções próprias vão nos seus arquivos de sempre.\n\n")
	b.WriteString("Este projeto roda sob o Dev Manager (`devm`), **não** sob Laravel Sail,\n")
	b.WriteString("Docker Compose ou o PHP do sistema.\n\n")

	b.WriteString("## Executar comandos\n\n")
	b.WriteString("O `php` e o `composer` deste projeto não estão no PATH do seu shell.\n")
	b.WriteString("Use sempre os comandos abaixo — eles já usam a versão correta:\n\n")
	b.WriteString("```sh\n")
	if c.TemArtisan {
		b.WriteString("devm artisan <comando>     # em vez de php artisan / sail artisan\n")
	}
	b.WriteString("devm composer <comando>    # em vez de composer / sail composer\n")
	b.WriteString("devm run <comando>         # qualquer outro, com o ambiente do projeto\n")
	b.WriteString("```\n\n")
	b.WriteString("Exemplos: `devm artisan migrate`, `devm composer require x/y`,\n")
	b.WriteString("`devm run ./vendor/bin/pest`, `devm run npm run build`.\n\n")

	b.WriteString("## Ambiente\n\n")
	if c.PHP != "" {
		origem := ""
		if c.PHPOrigem != "" {
			origem = fmt.Sprintf(" (definido em %s)", c.PHPOrigem)
		}
		fmt.Fprintf(&b, "- **PHP %s**%s\n", c.PHP, origem)
	}
	if c.TemNode && c.Node != "" {
		fmt.Fprintf(&b, "- **Node %s** — use `devm run npm ...`, não o npm do sistema\n", c.Node)
	}
	if c.Dominio != "" {
		fmt.Fprintf(&b, "- Acessível em **https://%s** quando o ambiente está de pé\n", c.Dominio)
	}

	if len(c.Servicos) > 0 {
		b.WriteString("\n### Serviços\n\n")
		b.WriteString("Rodam em contêineres **compartilhados** entre projetos; este projeto tem\n")
		b.WriteString("bancos próprios dentro deles, e as credenciais já estão no `.env`.\n\n")
		for _, s := range c.Servicos {
			portas := make([]string, len(s.Portas))
			for i, p := range s.Portas {
				portas[i] = fmt.Sprintf("%d", p)
			}
			linha := fmt.Sprintf("- %s %s", s.Nome, s.Versao)
			if len(portas) > 0 {
				linha += " em 127.0.0.1:" + strings.Join(portas, ", ")
			}
			b.WriteString(linha + "\n")
		}
		b.WriteString("\nNão suba esses serviços com `docker compose` nem `sail`: use\n")
		b.WriteString("`devm service list` para ver o estado e `devm up` para garantir que estão de pé.\n")
	}

	b.WriteString("\n## Ciclo de vida\n\n")
	b.WriteString("```sh\n")
	b.WriteString("devm up            # prepara: dependências, .env, chave, serviços\n")
	b.WriteString("devm start -d      # sobe servidor e frontend em segundo plano\n")
	b.WriteString("devm ps            # o que está rodando\n")
	b.WriteString("devm logs " + nomeOuPadrao(c.Projeto) + " -f   # acompanha\n")
	b.WriteString("devm stop " + nomeOuPadrao(c.Projeto) + "      # derruba\n")
	b.WriteString("```\n\n")

	b.WriteString("## O que NÃO fazer\n\n")
	b.WriteString("- Não use `sail`, `docker compose up` nem `./vendor/bin/sail` neste projeto.\n")
	b.WriteString("- Não chame `php`, `composer`, `npm` ou `npx` diretamente: a versão seria\n")
	b.WriteString("  a do sistema, diferente da que o projeto declara.\n")
	b.WriteString("- Não edite as chaves de banco, Redis ou e-mail do `.env` à mão: elas são\n")
	b.WriteString("  geradas pelo `devm up` e serão sobrescritas.\n")

	return b.String()
}

func nomeOuPadrao(nome string) string {
	if nome == "" {
		return "<projeto>"
	}
	return nome
}

// Arquivo é o documento gerado, inteiramente nosso.
//
// Um arquivo PRÓPRIO em vez de um bloco dentro do CLAUDE.md: assim nada
// escrito à mão corre risco, remover é `rm` e o conteúdo pode ser regenerado
// sem cuidado especial. O custo é que os assistentes não o leem sozinhos —
// daí o comando de ligação abaixo.
const Arquivo = "DEVMANAGER.md"

// arquivosDeInstrucao são os documentos que os assistentes leem por conta
// própria. Não há padrão único: AGENTS.md é o que mais se aproxima, mas
// Claude, Gemini e Copilot têm os seus.
var arquivosDeInstrucao = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"GEMINI.md",
	filepath.Join(".github", "copilot-instructions.md"),
}

// LinhaDeLigacao é o que aponta o assistente para o nosso arquivo.
//
// A sintaxe "@caminho" é a de importação do Claude Code; as demais
// ferramentas tratam a linha como texto comum e seguem a instrução literal.
// Uma linha que funciona nos dois modos evita ter um formato por ferramenta.
const LinhaDeLigacao = "Leia @./" + Arquivo + " para saber como executar comandos neste projeto."

// Resultado descreve o que foi (ou seria) alterado.
type Resultado struct {
	Arquivo  string `json:"file"`
	Criado   bool   `json:"created"`
	NoChange bool   `json:"unchanged"`
	Ligacao  bool   `json:"link"`
}

// Escrever grava o arquivo de instruções.
func Escrever(dirProjeto string, c Contexto, dryRun bool) (Resultado, error) {
	caminho := filepath.Join(dirProjeto, Arquivo)
	conteudo := Documento(c)

	original, err := os.ReadFile(caminho)
	if err != nil && !os.IsNotExist(err) {
		return Resultado{}, fmt.Errorf("lendo %s: %w", caminho, err)
	}

	r := Resultado{Arquivo: Arquivo, Criado: os.IsNotExist(err)}
	if string(original) == conteudo {
		r.NoChange = true
		return r, nil
	}
	if dryRun {
		return r, nil
	}

	if err := escreverAtomico(caminho, []byte(conteudo)); err != nil {
		return Resultado{}, err
	}
	return r, nil
}

// AlvosDeLigacao lista os arquivos de instrução que ainda não apontam para o
// nosso.
func AlvosDeLigacao(dirProjeto string) []string {
	var faltando []string

	for _, nome := range arquivosDeInstrucao {
		caminho := filepath.Join(dirProjeto, nome)

		dados, err := os.ReadFile(caminho)
		if err != nil {
			continue // só ligamos a partir de arquivos que já existem
		}
		if strings.Contains(string(dados), Arquivo) {
			continue // já aponta
		}
		faltando = append(faltando, nome)
	}
	return faltando
}

// Ligar acrescenta a linha de referência aos arquivos de instrução.
//
// É a ÚNICA escrita deste pacote fora do arquivo próprio, e é uma linha só —
// acrescentada no fim, sem tocar em nada existente. Fica atrás de uma opção
// explícita porque mexer no documento que a equipe escreveu deveria ser
// escolha, não efeito colateral.
func Ligar(dirProjeto string, dryRun bool) ([]Resultado, error) {
	var resultados []Resultado

	for _, nome := range AlvosDeLigacao(dirProjeto) {
		caminho := filepath.Join(dirProjeto, nome)

		dados, err := os.ReadFile(caminho)
		if err != nil {
			return nil, fmt.Errorf("lendo %s: %w", caminho, err)
		}

		resultados = append(resultados, Resultado{Arquivo: nome, Ligacao: true})
		if dryRun {
			continue
		}

		novo := strings.TrimRight(string(dados), "\n") + "\n\n" + LinhaDeLigacao + "\n"
		if err := escreverAtomico(caminho, []byte(novo)); err != nil {
			return nil, err
		}
	}
	return resultados, nil
}

func escreverAtomico(destino string, dados []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(destino), ".agents-*")
	if err != nil {
		return fmt.Errorf("criando arquivo temporário: %w", err)
	}
	nome := tmp.Name()
	defer os.Remove(nome)

	if _, err := tmp.Write(dados); err != nil {
		tmp.Close()
		return fmt.Errorf("escrevendo %s: %w", destino, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", nome, err)
	}
	if err := os.Chmod(nome, 0o644); err != nil {
		return fmt.Errorf("ajustando permissões: %w", err)
	}
	if err := os.Rename(nome, destino); err != nil {
		return fmt.Errorf("gravando %s: %w", destino, err)
	}
	return nil
}
