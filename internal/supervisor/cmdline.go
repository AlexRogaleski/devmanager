package supervisor

import (
	"fmt"
	"strings"
)

// DividirComando quebra uma linha de comando em argumentos.
//
// Não usamos `sh -c "<linha>"` de propósito, apesar de ser mais curto. Com o
// shell no meio, o processo que iniciamos é o shell — e é nele que os sinais
// chegam. Um Ctrl+C mataria o sh e deixaria o `npm run dev` rodando, órfão,
// segurando a porta. Iniciando o processo diretamente, controlamos quem
// recebe o quê.
//
// O custo é não suportar pipes, redirecionamentos e && na linha. Para
// processos de desenvolvimento — um servidor, um worker, um bundler — isso
// nunca é necessário, e quem precisar pode chamar sh explicitamente.
func DividirComando(linha string) ([]string, error) {
	var (
		args    []string
		atual   strings.Builder
		aspas   byte // 0 = fora de aspas, senão a aspa que abriu
		temAlgo bool
	)

	// Percorremos BYTES, não runes. Em Go, indexar uma string devolve um byte:
	// linha[i] de "ç" devolve 0xC3, metade do caractere. Converter isso para
	// rune produziria lixo — foi o bug que o teste pegou.
	//
	// Iterar por bytes é seguro aqui porque todos os delimitadores que nos
	// interessam (espaço, tabulação, aspas, barra invertida) são ASCII, e
	// nenhum byte de uma sequência UTF-8 multibyte coincide com ASCII. Os
	// bytes de dentro de um caractere apenas atravessam para o Builder,
	// intactos e na ordem.
	for i := 0; i < len(linha); i++ {
		c := linha[i]

		switch {
		case c == '\\' && i+1 < len(linha) && aspas != '\'':
			// Barra invertida escapa o próximo byte, exceto dentro de aspas
			// simples — mesma regra do shell.
			i++
			atual.WriteByte(linha[i])
			temAlgo = true

		case aspas != 0:
			if c == aspas {
				aspas = 0
			} else {
				atual.WriteByte(c)
			}
			temAlgo = true

		case c == '"' || c == '\'':
			aspas = c
			// Marca que houve conteúdo mesmo com aspas vazias: `--msg ""`
			// precisa produzir um argumento vazio, não sumir.
			temAlgo = true

		case c == ' ' || c == '\t':
			if temAlgo {
				args = append(args, atual.String())
				atual.Reset()
				temAlgo = false
			}

		default:
			atual.WriteByte(c)
			temAlgo = true
		}
	}

	if aspas != 0 {
		return nil, fmt.Errorf("aspas não fechadas em %q", linha)
	}
	if temAlgo {
		args = append(args, atual.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("comando vazio")
	}
	return args, nil
}
