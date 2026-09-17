package semver

import (
	"fmt"
	"strings"
)

// operador é o tipo de comparação de um único termo.
//
// iota gera constantes sequenciais automaticamente: opGTE=0, opGT=1, e assim
// por diante. É o "enum" do Go — um tipo próprio impede que um int solto
// seja usado como operador por engano.
type operador int

const (
	opGTE operador = iota // >=
	opGT                  // >
	opLTE                 // <=
	opLT                  // <
	opEQ                  // ==
)

func (o operador) String() string {
	return [...]string{">=", ">", "<=", "<", "=="}[o]
}

// termo é uma comparação isolada, como ">= 8.3.0".
type termo struct {
	op operador
	v  Version
}

func (t termo) permite(v Version) bool {
	c := v.Compare(t.v)
	switch t.op {
	case opGTE:
		return c >= 0
	case opGT:
		return c > 0
	case opLTE:
		return c <= 0
	case opLT:
		return c < 0
	default:
		return c == 0
	}
}

// Constraint é uma faixa de versões aceitas, como "^8.3" ou ">=8.2 <9.0".
//
// A estrutura interna é uma OR de ANDs: grupos separados por "||", e dentro de
// cada grupo, termos que precisam valer todos ao mesmo tempo. É assim que o
// Composer modela, e cobre qualquer constraint real:
//
//	"^8.2 || ^8.3"  ->  [[>=8.2.0, <9.0.0], [>=8.3.0, <9.0.0]]
//	">=8.2 <9.0"    ->  [[>=8.2.0, <9.0.0]]
//
// Os campos são minúsculos: quem usa o pacote não deveria depender do formato
// interno, só de Allows, Best e String.
type Constraint struct {
	texto  string
	grupos [][]termo
}

// Any é a constraint que aceita qualquer versão — o valor zero útil,
// devolvido quando o projeto não declara exigência de PHP.
var Any = Constraint{texto: "*"}

// ParseConstraint lê uma constraint do Composer.
//
// Suporta o que aparece de fato em composer.json:
//
//	^8.3        cuidado com o major: >=8.3.0 <9.0.0
//	~8.3        >=8.3.0 <9.0.0     (dois componentes)
//	~8.3.1      >=8.3.1 <8.4.0     (três componentes)
//	8.3.*       >=8.3.0 <8.4.0
//	>=8.2       >=8.2.0
//	>=8.2 <9.0  os dois ao mesmo tempo
//	^8.2|^8.3   um OU outro
//	*           qualquer versão
func ParseConstraint(s string) (Constraint, error) {
	original := s
	s = strings.TrimSpace(s)

	if s == "" || s == "*" {
		return Any, nil
	}

	c := Constraint{texto: original}

	// "||" e "|" significam a mesma coisa no Composer. Normalizamos para um só
	// separador antes de dividir, em vez de tratar os dois casos no laço.
	for _, parteOR := range strings.Split(strings.ReplaceAll(s, "||", "|"), "|") {
		// Dentro de um grupo, vírgula e espaço são ambos "e também".
		campos := strings.Fields(strings.ReplaceAll(parteOR, ",", " "))
		if len(campos) == 0 {
			return Constraint{}, fmt.Errorf("constraint inválida: %q", original)
		}

		var grupo []termo
		for _, campo := range campos {
			termos, err := expandir(campo)
			if err != nil {
				return Constraint{}, fmt.Errorf("constraint %q: %w", original, err)
			}
			grupo = append(grupo, termos...)
		}
		c.grupos = append(c.grupos, grupo)
	}

	return c, nil
}

// expandir traduz um termo isolado na lista de comparações equivalentes.
// É aqui que "^8.3" deixa de ser açúcar sintático e vira ">=8.3.0 e <9.0.0".
func expandir(campo string) ([]termo, error) {
	if campo == "*" {
		return nil, nil
	}

	// Operadores explícitos: ordem importa, ">=" precisa ser testado antes de ">".
	for _, prefixo := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
		if !strings.HasPrefix(campo, prefixo) {
			continue
		}
		if prefixo == "!=" {
			return nil, fmt.Errorf("operador %q não suportado", prefixo)
		}

		v, _, err := parsePrecisao(strings.TrimPrefix(campo, prefixo))
		if err != nil {
			return nil, err
		}

		ops := map[string]operador{">=": opGTE, ">": opGT, "<=": opLTE, "<": opLT, "=": opEQ, "==": opEQ}
		return []termo{{op: ops[prefixo], v: v}}, nil
	}

	switch {
	case strings.HasPrefix(campo, "^"):
		v, _, err := parsePrecisao(strings.TrimPrefix(campo, "^"))
		if err != nil {
			return nil, err
		}
		return faixa(v, proximoCaret(v)), nil

	case strings.HasPrefix(campo, "~"):
		v, precisao, err := parsePrecisao(strings.TrimPrefix(campo, "~"))
		if err != nil {
			return nil, err
		}
		// ~8.3 libera o minor; ~8.3.1 trava o minor e só libera o patch.
		if precisao >= 3 {
			return faixa(v, Version{Major: v.Major, Minor: v.Minor + 1}), nil
		}
		return faixa(v, Version{Major: v.Major + 1}), nil

	case strings.HasSuffix(campo, ".*"), strings.HasSuffix(campo, ".x"):
		base := campo[:len(campo)-2]
		v, precisao, err := parsePrecisao(base)
		if err != nil {
			return nil, err
		}
		return faixa(v, proximoNivel(v, precisao)), nil
	}

	// Versão nua. "8.3.4" é exata; "8.3" e "8" liberam os componentes omitidos,
	// que é o que as pessoas querem dizer ao escrever "php": "8.3".
	v, precisao, err := parsePrecisao(campo)
	if err != nil {
		return nil, err
	}
	if precisao >= 3 {
		return []termo{{op: opEQ, v: v}}, nil
	}
	return faixa(v, proximoNivel(v, precisao)), nil
}

// faixa monta o par ">=min, <max" usado por quase todos os operadores.
func faixa(min, max Version) []termo {
	return []termo{{op: opGTE, v: min}, {op: opLT, v: max}}
}

// proximoCaret aplica a regra do ^ do Composer: ele protege o componente
// mais à esquerda que for diferente de zero. Antes do 1.0 a API é instável,
// então ^0.3.1 não pode saltar para 0.4.
func proximoCaret(v Version) Version {
	switch {
	case v.Major > 0:
		return Version{Major: v.Major + 1}
	case v.Minor > 0:
		return Version{Minor: v.Minor + 1}
	default:
		return Version{Patch: v.Patch + 1}
	}
}

// proximoNivel devolve o limite superior de um curinga, conforme quantos
// componentes foram escritos: "8.*" fecha em 9.0.0, "8.3.*" fecha em 8.4.0.
func proximoNivel(v Version, precisao int) Version {
	if precisao >= 2 {
		return Version{Major: v.Major, Minor: v.Minor + 1}
	}
	return Version{Major: v.Major + 1}
}

// parsePrecisao devolve a versão E quantos componentes foram escritos.
// A precisão é o que diferencia "~8.3" de "~8.3.0", que expandem diferente.
func parsePrecisao(s string) (Version, int, error) {
	s = strings.TrimSpace(s)
	v, err := Parse(s)
	if err != nil {
		return Version{}, 0, err
	}

	limpo := strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(limpo, "-+"); i >= 0 {
		limpo = limpo[:i]
	}
	return v, len(strings.Split(limpo, ".")), nil
}

// Allows informa se a versão cai dentro da faixa.
func (c Constraint) Allows(v Version) bool {
	if len(c.grupos) == 0 {
		return true // constraint vazia ou "*"
	}

	// OR entre grupos: basta um grupo inteiro passar.
	for _, grupo := range c.grupos {
		ok := true
		for _, t := range grupo {
			if !t.permite(v) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// Best escolhe a MAIOR versão da lista que satisfaz a constraint.
//
// A regra "maior compatível" é a mesma do Composer e a que o desenvolvedor
// espera: um projeto que pede ^8.2 deve rodar no 8.3 se ele estiver instalado,
// sem precisar declarar nada.
//
// O segundo retorno é um bool em vez de erro porque "nenhuma versão serve" é
// um resultado normal, não uma falha — cabe a quem chama decidir se baixa uma
// versão nova ou avisa o usuário.
func (c Constraint) Best(versoes []Version) (Version, bool) {
	var melhor Version
	achou := false

	for _, v := range versoes {
		if !c.Allows(v) {
			continue
		}
		if !achou || melhor.Less(v) {
			melhor, achou = v, true
		}
	}
	return melhor, achou
}

// String devolve o texto original da constraint, preservando o que o
// desenvolvedor escreveu no composer.json.
func (c Constraint) String() string {
	if c.texto == "" {
		return "*"
	}
	return c.texto
}
