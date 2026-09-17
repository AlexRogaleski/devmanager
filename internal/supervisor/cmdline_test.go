package supervisor

import (
	"slices"
	"testing"
)

func TestDividirComando(t *testing.T) {
	casos := map[string][]string{
		`php artisan serve`:             {"php", "artisan", "serve"},
		`npm run dev`:                   {"npm", "run", "dev"},
		`php artisan serve --port=8000`: {"php", "artisan", "serve", "--port=8000"},
		`  espaços    extras  `:         {"espaços", "extras"},
		`echo "um argumento só"`:        {"echo", "um argumento só"},
		`echo 'aspas simples'`:          {"echo", "aspas simples"},
		`echo "misto 'interno'"`:        {"echo", "misto 'interno'"},
		`cmd --msg=""`:                  {"cmd", "--msg="},
		`caminho/com\ espaço`:           {"caminho/com espaço"},
		`echo "aspa \" escapada"`:       {"echo", `aspa " escapada`},
		`echo 'sem \escape aqui'`:       {"echo", `sem \escape aqui`},
	}

	for linha, esperado := range casos {
		t.Run(linha, func(t *testing.T) {
			got, err := DividirComando(linha)
			if err != nil {
				t.Fatalf("DividirComando(%q) falhou: %v", linha, err)
			}
			if !slices.Equal(got, esperado) {
				t.Errorf("= %q, esperava %q", got, esperado)
			}
		})
	}
}

func TestDividirComandoInvalido(t *testing.T) {
	for _, linha := range []string{``, `   `, `echo "não fecha`, `echo 'nem esta`} {
		if _, err := DividirComando(linha); err == nil {
			t.Errorf("DividirComando(%q) deveria falhar", linha)
		}
	}
}
