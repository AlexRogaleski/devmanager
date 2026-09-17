package supervisor

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// coresANSI são as cores usadas para distinguir os processos na saída.
// Evitamos vermelho puro, que o olho associa a erro.
var coresANSI = []string{
	"\033[36m", // ciano
	"\033[32m", // verde
	"\033[35m", // magenta
	"\033[33m", // amarelo
	"\033[34m", // azul
	"\033[96m", // ciano claro
}

const resetANSI = "\033[0m"

// escritorComPrefixo escreve linhas prefixadas pelo nome do processo.
//
// Dois problemas que ele resolve, e que só aparecem com vários processos:
//
//  1. Escrita parcial. Um processo pode escrever "Server ru" numa chamada e
//     "nning\n" na seguinte. Prefixar cada Write produziria duas linhas
//     quebradas, então acumulamos num buffer e só emitimos ao ver "\n".
//
//  2. Entrelaçamento. Todos os processos escrevem no MESMO terminal. Sem um
//     mutex compartilhado entre eles, duas linhas podem se misturar no meio.
//     Por isso o mutex vem de fora, não é criado aqui.
type escritorComPrefixo struct {
	destino io.Writer
	prefixo string
	mu      *sync.Mutex

	// buf guarda o pedaço de linha ainda sem quebra.
	buf bytes.Buffer
}

func novoEscritor(destino io.Writer, mu *sync.Mutex, nome string, largura, indice int, cores bool) *escritorComPrefixo {
	prefixo := fmt.Sprintf("%-*s | ", largura, nome)
	if cores {
		prefixo = coresANSI[indice%len(coresANSI)] + prefixo + resetANSI
	}
	return &escritorComPrefixo{destino: destino, prefixo: prefixo, mu: mu}
}

func (e *escritorComPrefixo) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.buf.Write(p)

	for {
		linha, err := e.buf.ReadBytes('\n')
		if err != nil {
			// Sem "\n" ainda: devolve o pedaço ao buffer e espera o resto.
			e.buf.Write(linha)
			break
		}
		if _, err := fmt.Fprintf(e.destino, "%s%s", e.prefixo, linha); err != nil {
			return 0, err
		}
	}

	// Sempre reportamos ter consumido tudo: o que ficou no buffer não é perda,
	// é linha incompleta. Devolver menos faria o io.Copy do os/exec abortar
	// com ErrShortWrite.
	return len(p), nil
}

// Flush emite o que sobrou sem quebra de linha, no fim do processo.
func (e *escritorComPrefixo) Flush() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.buf.Len() == 0 {
		return
	}
	fmt.Fprintf(e.destino, "%s%s\n", e.prefixo, e.buf.String())
	e.buf.Reset()
}
