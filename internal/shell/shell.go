// Package shell monta comandos para serem lidos por gente e executados por sh.
//
// O Dev Manager nunca roda nada com sudo por conta própria: ele MOSTRA os
// comandos, e o `devm setup --apply` executa cada um num `sh -c`. Isso faz do
// texto do comando a interface — ele precisa ser legível para quem vai colar
// no terminal e exato para o shell que vai executá-lo.
package shell

import "strings"

// Aspas protege um valor para o shell com aspas simples.
//
// Dentro de aspas simples nada é interpretado — nem $, nem crase, nem barra
// invertida —, exceto a própria aspa, que não pode ser escapada lá dentro. O
// jeito é fechar as aspas, inserir uma aspa escapada e reabrir. A palavra
// it's vira:
//
//	'it'\''s'
//
// (Em bloco de código porque o gofmt reescreve duas aspas simples seguidas
// como aspas tipográficas no texto de um comentário de documentação.)
func Aspas(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
