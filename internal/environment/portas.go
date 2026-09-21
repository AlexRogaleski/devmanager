package environment

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/supervisor"
)

// MarcadorPorta é o que uma linha de `processes` escreve para receber a porta
// que o ambiente ganhou.
//
// Existe porque a porta é decidida em tempo de execução — o daemon pede uma
// livre ao kernel — e o devmanager.yaml é escrito muito antes disso. Sem um
// marcador, quem declarasse `processes` teria de fixar um número, e fixar um
// número quebra o proxy: ele roteia o domínio para a porta que o ambiente
// anunciou, não para a que está escrita no arquivo.
const MarcadorPorta = "{{port}}"

// prefixoExtra abre um marcador de porta NOMEADA: {{port:vite}}.
//
// A porta principal é a do servidor da aplicação, a que o proxy publica em
// <projeto>.test. Um segundo processo que também escute — um frontend em
// modo dev, um servidor de SSR — precisa de outra porta, e precisa que ela
// seja sempre a mesma dentro do ambiente para poder ser referenciada por
// outro processo.
const prefixoExtra = "{{port:"

// portaPadraoDoArtisan é onde `php artisan serve` escuta quando ninguém diz.
const portaPadraoDoArtisan = 8000

// Execucao é a decisão completa do que rodar e em que portas.
//
// Antes isso era só uma lista de processos, e "tem servidor?" era respondido
// procurando a substring "artisan serve" nas linhas. Funcionava enquanto o
// devm escrevia as linhas sozinho; deixou de funcionar quando o projeto pode
// declarar as suas. Com os marcadores, quem resolve as portas já SABE qual
// processo escuta onde, e essa resposta vira um dado em vez de um palpite.
type Execucao struct {
	Processos []supervisor.Processo

	// Porta é onde o servidor da aplicação atende; 0 quando não há servidor.
	Porta int

	// Servidor é o nome do processo que atende na Porta.
	Servidor string

	// Extras são as portas nomeadas, por nome do marcador.
	Extras map[string]int
}

// resolverPortas troca os marcadores pelas portas reais.
//
// livre é um parâmetro, e não uma chamada direta a PortaLivre, para que o
// teste possa entregar portas previsíveis: o resultado precisa ser afirmável,
// e uma porta sorteada pelo kernel não é.
func resolverPortas(procs []supervisor.Processo, principal int, livre func() (int, error)) (Execucao, error) {
	ex := Execucao{Processos: make([]supervisor.Processo, 0, len(procs))}

	for _, proc := range procs {
		linha := proc.Linha

		if strings.Contains(linha, MarcadorPorta) {
			if principal == 0 {
				return Execucao{}, fmt.Errorf("o processo %q pede %s, mas nenhuma porta foi atribuída", proc.Nome, MarcadorPorta)
			}
			linha = strings.ReplaceAll(linha, MarcadorPorta, strconv.Itoa(principal))

			// O primeiro a pedir a porta principal é o servidor. Um segundo
			// processo pedindo a mesma porta é erro de quem escreveu o
			// arquivo — dois servidores não cabem numa porta só.
			if ex.Porta != 0 {
				return Execucao{}, fmt.Errorf(
					"os processos %q e %q pedem os dois a porta principal — use %svite}} no segundo",
					ex.Servidor, proc.Nome, prefixoExtra)
			}
			ex.Porta, ex.Servidor = principal, proc.Nome
		}

		var err error
		if linha, err = trocarExtras(linha, proc.Nome, livre, &ex); err != nil {
			return Execucao{}, err
		}

		proc.Linha = linha
		ex.Processos = append(ex.Processos, proc)
	}

	// Sem nenhum marcador, caímos na leitura do comando. É o caso de quem
	// declarou `processes` antes de este mecanismo existir.
	if ex.Porta == 0 {
		ex.Servidor, ex.Porta = servidorDeclarado(ex.Processos)
	}
	return ex, nil
}

// trocarExtras substitui todos os {{port:nome}} de uma linha.
//
// O mesmo nome sempre recebe a mesma porta dentro do ambiente: é isso que
// permite um processo apontar para o outro, como um SSR que precisa saber
// onde o Vite subiu.
func trocarExtras(linha, processo string, livre func() (int, error), ex *Execucao) (string, error) {
	for {
		abre := strings.Index(linha, prefixoExtra)
		if abre < 0 {
			return linha, nil
		}

		resto := linha[abre+len(prefixoExtra):]
		fecha := strings.Index(resto, "}}")
		if fecha < 0 {
			return "", fmt.Errorf("o processo %q tem um %s sem o }} que fecha", processo, prefixoExtra)
		}

		nome := strings.TrimSpace(resto[:fecha])
		if nome == "" {
			return "", fmt.Errorf("o processo %q usa %s}} sem nome", processo, prefixoExtra)
		}

		porta, ja := ex.Extras[nome]
		if !ja {
			var err error
			if porta, err = livre(); err != nil {
				return "", fmt.Errorf("porta para %q: %w", nome, err)
			}
			if ex.Extras == nil {
				ex.Extras = make(map[string]int)
			}
			ex.Extras[nome] = porta
		}

		linha = linha[:abre] + strconv.Itoa(porta) + resto[fecha+len("}}"):]
	}
}

// servidorDeclarado lê a porta de um `artisan serve` escrito à mão.
//
// Sem isto, um projeto com `processes: {serve: php artisan serve --port=8000}`
// anunciaria ao proxy a porta sorteada pelo daemon, e o domínio responderia
// 502 — o servidor estaria de pé na 8000, e o proxy batendo em outro lugar.
// Ler o que a linha diz é mais honesto que supor.
func servidorDeclarado(procs []supervisor.Processo) (string, int) {
	for _, p := range procs {
		if !strings.Contains(p.Linha, "artisan serve") {
			continue
		}
		if porta, ok := portaDeclarada(p.Linha); ok {
			return p.Nome, porta
		}
		// `artisan serve` sem --port escuta na 8000, não numa porta livre.
		return p.Nome, portaPadraoDoArtisan
	}
	return "", 0
}

// portaDeclarada extrai o valor de --port=N ou --port N.
func portaDeclarada(linha string) (int, bool) {
	campos := strings.Fields(linha)
	for i, campo := range campos {
		var texto string
		switch {
		case strings.HasPrefix(campo, "--port="):
			texto = strings.TrimPrefix(campo, "--port=")
		case campo == "--port" && i+1 < len(campos):
			texto = campos[i+1]
		default:
			continue
		}
		if n, err := strconv.Atoi(texto); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

// Ambiente devolve as portas deste ambiente como variáveis.
//
// A porta muda a cada execução, então um processo não tem como descobrir a do
// outro lendo um arquivo. Com DEVM_PORT e DEVM_PORT_<NOME> no ambiente, o
// vite.config.js faz proxy para o backend, e o backend monta a URL do
// bundler, sem ninguém escrever número nenhum em lugar nenhum.
func (e Execucao) Ambiente() []string {
	var env []string

	if e.Porta != 0 {
		env = append(env, fmt.Sprintf("DEVM_PORT=%d", e.Porta))
	}

	nomes := make([]string, 0, len(e.Extras))
	for nome := range e.Extras {
		nomes = append(nomes, nome)
	}
	slices.Sort(nomes) // ordem estável: o ambiente fica reproduzível
	for _, nome := range nomes {
		env = append(env, fmt.Sprintf("DEVM_PORT_%s=%d", nomeDeVariavel(nome), e.Extras[nome]))
	}
	return env
}

// nomeDeVariavel transforma "vite-ssr" em "VITE_SSR".
//
// Nome de variável de ambiente aceita letras, dígitos e sublinhado; qualquer
// outra coisa produziria uma variável que o shell não consegue ler.
func nomeDeVariavel(nome string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(nome) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Filtrar restringe a execução aos processos pedidos.
func (e Execucao) Filtrar(querido []string) (Execucao, error) {
	if len(querido) == 0 {
		return e, nil
	}

	pedidos := make(map[string]bool, len(querido))
	for _, nome := range querido {
		pedidos[nome] = true
	}

	saida := e
	saida.Processos = nil
	for _, p := range e.Processos {
		if pedidos[p.Nome] {
			saida.Processos = append(saida.Processos, p)
			delete(pedidos, p.Nome)
		}
	}

	if len(pedidos) > 0 {
		faltando := make([]string, 0, len(pedidos))
		for nome := range pedidos {
			faltando = append(faltando, nome)
		}
		slices.Sort(faltando)
		return Execucao{}, fmt.Errorf("processo(s) não configurado(s): %v", faltando)
	}
	return saida.semServidorOrfao(), nil
}

// SemFrontend tira o processo de bundler, para quem sobe só o backend.
func (e Execucao) SemFrontend() Execucao {
	saida := e
	saida.Processos = nil
	for _, p := range e.Processos {
		if p.Nome == "vite" {
			continue
		}
		saida.Processos = append(saida.Processos, p)
	}
	return saida.semServidorOrfao()
}

// semServidorOrfao zera a porta quando o processo que a servia foi filtrado.
//
// Sem isso, `devm start --only vite` anunciaria ao proxy um domínio apontando
// para uma porta onde não há ninguém: o domínio responderia 502 em vez de
// simplesmente não existir.
func (e Execucao) semServidorOrfao() Execucao {
	if e.Servidor == "" {
		return e
	}
	for _, p := range e.Processos {
		if p.Nome == e.Servidor {
			return e
		}
	}
	e.Porta, e.Servidor = 0, ""
	return e
}
