package ide

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestStripCommentsPreservaOffsets(t *testing.T) {
	entrada := []byte(`{
    // comentário de linha
    "a": 1, /* bloco */
    "url": "http://exemplo.test", // não é comentário dentro da string
    "barra": "a\"b // c"
}`)

	limpo := stripComments(entrada)

	if len(limpo) != len(entrada) {
		t.Fatalf("tamanho mudou: %d vs %d — os offsets deixariam de valer", len(limpo), len(entrada))
	}
	if strings.Contains(string(limpo), "comentário") || strings.Contains(string(limpo), "bloco") {
		t.Errorf("comentário sobreviveu:\n%s", limpo)
	}
	// O "//" dentro de strings tem que permanecer intacto.
	if !strings.Contains(string(limpo), "http://exemplo.test") {
		t.Errorf("URL dentro de string foi mutilada:\n%s", limpo)
	}
	if !strings.Contains(string(limpo), `a\"b // c`) {
		t.Errorf("string com aspas escapadas foi mutilada:\n%s", limpo)
	}
}

func TestRemoverVirgulasFinais(t *testing.T) {
	entrada := []byte(`{"a": [1, 2, 3,], "b": {"c": 1,},}`)

	var m map[string]any
	if err := json.Unmarshal(removerVirgulasFinais(entrada), &m); err != nil {
		t.Fatalf("ainda inválido depois da limpeza: %v", err)
	}
	if len(m) != 2 {
		t.Errorf("esperava 2 chaves, veio %d", len(m))
	}
}

// O teste mais importante do arquivo: a edição não pode tocar em nada
// que não seja o valor da chave pedida.
func TestDefinirChavePreservaComentarios(t *testing.T) {
	entrada := []byte(`{
    // ---- Formatação ----
    "editor.formatOnSave": true,

    // Intelephense
    "intelephense.environment.phpVersion": "8.2.0",

    // Ortografia
    "cSpell.language": "en,pt,pt_BR"
}
`)

	saida, err := definirChave(entrada, "intelephense.environment.phpVersion", strconv.Quote("8.4.3"))
	if err != nil {
		t.Fatalf("definirChave falhou: %v", err)
	}

	texto := string(saida)
	for _, preservar := range []string{
		"// ---- Formatação ----",
		"// Intelephense",
		"// Ortografia",
		`"editor.formatOnSave": true`,
		`"cSpell.language": "en,pt,pt_BR"`,
	} {
		if !strings.Contains(texto, preservar) {
			t.Errorf("perdeu %q:\n%s", preservar, texto)
		}
	}
	if !strings.Contains(texto, `"intelephense.environment.phpVersion": "8.4.3"`) {
		t.Errorf("valor não foi atualizado:\n%s", texto)
	}
	if strings.Contains(texto, "8.2.0") {
		t.Errorf("valor antigo ficou para trás:\n%s", texto)
	}
}

func TestDefinirChaveInsereQuandoAusente(t *testing.T) {
	casos := map[string]string{
		"objeto vazio":      "{}",
		"objeto com espaço": "{\n}\n",
		"com uma chave":     "{\n    \"a\": 1\n}\n",
		"com vírgula final": "{\n    \"a\": 1,\n}\n",
		"com comentário":    "{\n    \"a\": 1 // nota\n}\n",
	}

	for nome, entrada := range casos {
		t.Run(nome, func(t *testing.T) {
			saida, err := definirChave([]byte(entrada), "php.validate.executablePath", strconv.Quote("/x/php"))
			if err != nil {
				t.Fatalf("definirChave falhou: %v", err)
			}

			var m map[string]any
			if err := json.Unmarshal(removerVirgulasFinais(stripComments(saida)), &m); err != nil {
				t.Fatalf("resultado não é JSON válido (%v):\n%s", err, saida)
			}
			if m["php.validate.executablePath"] != "/x/php" {
				t.Errorf("chave não inserida corretamente: %v\n%s", m, saida)
			}
		})
	}
}

// Chaves de mesmo nome dentro de objetos aninhados não podem ser confundidas
// com as da raiz — o settings.json do VS Code é cheio de blocos "[php]": {...}.
func TestDefinirChaveIgnoraChavesAninhadas(t *testing.T) {
	entrada := []byte(`{
    "[php]": {
        "editor.defaultFormatter": "pint",
        "alvo": "aninhado"
    },
    "alvo": "raiz"
}
`)

	saida, err := definirChave(entrada, "alvo", strconv.Quote("novo"))
	if err != nil {
		t.Fatalf("definirChave falhou: %v", err)
	}

	texto := string(saida)
	if !strings.Contains(texto, `"alvo": "aninhado"`) {
		t.Errorf("a chave aninhada foi alterada indevidamente:\n%s", texto)
	}
	if !strings.Contains(texto, `"alvo": "novo"`) {
		t.Errorf("a chave da raiz não foi alterada:\n%s", texto)
	}
}

func TestDefinirChaveComValoresComplexos(t *testing.T) {
	entrada := []byte(`{
    "eslint.validate": ["javascript", "typescript"],
    "editor.codeActionsOnSave": {
        "source.fixAll.eslint": "explicit"
    },
    "alvo": "antigo"
}
`)

	saida, err := definirChave(entrada, "alvo", strconv.Quote("novo"))
	if err != nil {
		t.Fatalf("definirChave falhou: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(removerVirgulasFinais(stripComments(saida)), &m); err != nil {
		t.Fatalf("resultado inválido (%v):\n%s", err, saida)
	}
	if m["alvo"] != "novo" {
		t.Errorf("alvo = %v, esperava \"novo\"", m["alvo"])
	}
	if arr, ok := m["eslint.validate"].([]any); !ok || len(arr) != 2 {
		t.Errorf("o array vizinho foi corrompido: %v", m["eslint.validate"])
	}
}
