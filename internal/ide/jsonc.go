package ide

import (
	"bytes"
	"fmt"
	"strconv"
)

// Este arquivo implementa edição cirúrgica de JSONC — o "JSON com comentários"
// que o VS Code usa em settings.json.
//
// Por que não usar encoding/json e regravar? Porque o ciclo
// decodificar → mapa → recodificar DESTRÓI tudo que não é dado: comentários,
// ordem das chaves, formatação, linhas em branco. Para um arquivo que o
// desenvolvedor escreveu e comenta à mão, isso é vandalismo.
//
// A estratégia aqui é outra: localizar no TEXTO o intervalo de bytes ocupado
// pelo valor de uma chave e substituir só aquele trecho. Todo o resto do
// arquivo sai idêntico, byte a byte.

// stripComments troca comentários por espaços, PRESERVANDO os offsets.
//
// Preservar o tamanho é o truque central: como cada byte de comentário vira um
// espaço, uma posição encontrada na versão limpa vale exatamente para o texto
// original. Assim podemos analisar sem comentários e editar com eles.
func stripComments(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)

	const (
		normal = iota
		emString
		emLinha
		emBloco
	)

	estado := normal
	for i := 0; i < len(src); i++ {
		c := src[i]

		switch estado {
		case normal:
			if c == '"' {
				estado = emString
				continue
			}
			if c == '/' && i+1 < len(src) {
				switch src[i+1] {
				case '/':
					estado, out[i], out[i+1] = emLinha, ' ', ' '
					i++
				case '*':
					estado, out[i], out[i+1] = emBloco, ' ', ' '
					i++
				}
			}

		case emString:
			if c == '\\' {
				i++ // pula o caractere escapado: \" não fecha a string
				continue
			}
			if c == '"' {
				estado = normal
			}

		case emLinha:
			if c == '\n' {
				estado = normal // a quebra de linha fica, para não juntar tokens
				continue
			}
			out[i] = ' '

		case emBloco:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				estado = normal
				continue
			}
			if c != '\n' {
				out[i] = ' '
			}
		}
	}
	return out
}

// removerVirgulasFinais tira vírgulas antes de } ou ], que o JSONC aceita e o
// encoding/json rejeita. Usada só para PARSEAR — nunca para gravar.
func removerVirgulasFinais(src []byte) []byte {
	out := make([]byte, 0, len(src))

	for i := 0; i < len(src); i++ {
		if src[i] == '"' {
			fim, ok := fimDaString(src, i)
			if !ok {
				out = append(out, src[i:]...)
				break
			}
			out = append(out, src[i:fim]...)
			i = fim - 1
			continue
		}

		if src[i] == ',' {
			if j := proximoNaoEspaco(src, i+1); j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue // vírgula pendurada: descarta
			}
		}
		out = append(out, src[i])
	}
	return out
}

// definirChave devolve src com a chave de nível raiz ajustada para valorJSON.
// Se a chave não existir, ela é inserida antes do fechamento do objeto.
func definirChave(src []byte, chave, valorJSON string) ([]byte, error) {
	limpo := stripComments(src)

	if ini, fim, ok := localizarValorRaiz(limpo, chave); ok {
		// Substituição no lugar: o resto do arquivo nem é tocado.
		novo := make([]byte, 0, len(src)+len(valorJSON))
		novo = append(novo, src[:ini]...)
		novo = append(novo, valorJSON...)
		novo = append(novo, src[fim:]...)
		return novo, nil
	}

	fecha, ok := fechamentoRaiz(limpo)
	if !ok {
		return nil, fmt.Errorf("não encontrei o objeto raiz do JSON")
	}

	// Último byte de conteúdo REAL antes do "}". Calculado sobre o texto
	// limpo, onde comentários viraram espaços — mas o offset vale para o
	// texto original, porque stripComments preserva o tamanho.
	anterior := ultimoNaoEspaco(limpo, fecha)

	// Começo da linha onde o "}" está. Inserimos a chave nova imediatamente
	// antes dela, para não empurrar o fechamento para o meio de outra linha.
	inicioLinha := fecha
	for inicioLinha > 0 && src[inicioLinha-1] != '\n' {
		inicioLinha--
	}
	if inicioLinha < anterior+1 {
		inicioLinha = anterior + 1
	}

	var b bytes.Buffer
	b.Write(src[:anterior+1])

	// A vírgula vai logo após o último conteúdo real — ANTES de qualquer
	// comentário de fim de linha. Escrevê-la depois produziria
	//     "a": 1 // nota,
	// com a vírgula comentada e o JSON quebrado. Foi o bug que o teste pegou.
	if anterior >= 0 && limpo[anterior] != '{' && limpo[anterior] != ',' {
		b.WriteByte(',')
	}

	// Tudo entre o fim do conteúdo e a linha do "}" é devolvido intacto:
	// comentários de fim de linha, linhas em branco, o que houver.
	b.Write(src[anterior+1 : inicioLinha])

	if b.Len() > 0 && b.Bytes()[b.Len()-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("    ")
	b.WriteString(strconv.Quote(chave))
	b.WriteString(": ")
	b.WriteString(valorJSON)
	b.WriteByte('\n')
	b.Write(src[inicioLinha:])

	return b.Bytes(), nil
}

// localizarValorRaiz devolve o intervalo [ini, fim) do valor de uma chave que
// esteja no nível raiz do objeto — chaves aninhadas são ignoradas.
func localizarValorRaiz(limpo []byte, chave string) (int, int, bool) {
	profundidade := 0

	for i := 0; i < len(limpo); {
		c := limpo[i]

		switch {
		case c == '"':
			fimChave, ok := fimDaString(limpo, i)
			if !ok {
				return 0, 0, false
			}

			// Só interessa string seguida de ":" e no nível 1.
			j := proximoNaoEspaco(limpo, fimChave)
			if profundidade != 1 || j >= len(limpo) || limpo[j] != ':' {
				i = fimChave
				continue
			}

			vi := proximoNaoEspaco(limpo, j+1)
			vf, ok := fimDoValor(limpo, vi)
			if !ok {
				return 0, 0, false
			}

			nome, err := strconv.Unquote(string(limpo[i:fimChave]))
			if err == nil && nome == chave {
				return vi, vf, true
			}

			// Pula o valor inteiro: assim as chaves de objetos aninhados
			// nunca são confundidas com chaves da raiz.
			i = vf
			continue

		case c == '{' || c == '[':
			profundidade++

		case c == '}' || c == ']':
			profundidade--
		}
		i++
	}
	return 0, 0, false
}

// fechamentoRaiz devolve a posição do "}" que fecha o objeto principal.
func fechamentoRaiz(limpo []byte) (int, bool) {
	profundidade := 0

	for i := 0; i < len(limpo); i++ {
		switch limpo[i] {
		case '"':
			fim, ok := fimDaString(limpo, i)
			if !ok {
				return 0, false
			}
			i = fim - 1
		case '{':
			profundidade++
		case '}':
			profundidade--
			if profundidade == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// fimDaString devolve a posição logo após a aspa de fechamento.
func fimDaString(b []byte, i int) (int, bool) {
	for j := i + 1; j < len(b); j++ {
		if b[j] == '\\' {
			j++
			continue
		}
		if b[j] == '"' {
			return j + 1, true
		}
	}
	return 0, false
}

// fimDoValor devolve a posição logo após o fim de um valor JSON.
func fimDoValor(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return 0, false
	}

	switch b[i] {
	case '"':
		return fimDaString(b, i)

	case '{', '[':
		abre := b[i]
		fecha := byte('}')
		if abre == '[' {
			fecha = ']'
		}

		profundidade := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				fim, ok := fimDaString(b, j)
				if !ok {
					return 0, false
				}
				j = fim - 1
			case abre:
				profundidade++
			case fecha:
				profundidade--
				if profundidade == 0 {
					return j + 1, true
				}
			}
		}
		return 0, false

	default:
		// Escalar: número, true, false, null. Termina no delimitador.
		j := i
		for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != '\n' {
			j++
		}
		for j > i && ehEspaco(b[j-1]) {
			j--
		}
		return j, true
	}
}

func proximoNaoEspaco(b []byte, i int) int {
	for i < len(b) && ehEspaco(b[i]) {
		i++
	}
	return i
}

func ultimoNaoEspaco(b []byte, antesDe int) int {
	for i := antesDe - 1; i >= 0; i-- {
		if !ehEspaco(b[i]) {
			return i
		}
	}
	return -1
}

func ehEspaco(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
