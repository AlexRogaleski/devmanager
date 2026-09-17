package project

import (
	"fmt"
	"os"
	"path/filepath"
)

// marcadores são os arquivos que identificam a raiz de um projeto.
// A ordem não importa: basta um deles existir.
var marcadores = []string{"composer.json", "artisan"}

// Find sobe na árvore de diretórios até encontrar a raiz de um projeto.
//
// É o comportamento que todo dev já espera: `git status` funciona de qualquer
// subpasta, `php artisan` também. Sem isso, `devm artisan migrate` só
// funcionaria exatamente na raiz — e ninguém trabalha assim.
//
// Devolve ErrNoProject se chegar na raiz do sistema sem encontrar nada.
func Find(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolvendo caminho %q: %w", dir, err)
	}

	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("acessando %s: %w", abs, err)
	}

	atual := abs
	for {
		if temMarcador(atual) {
			return Detect(atual)
		}

		// filepath.Dir("/") devolve "/": quando o pai é igual ao filho,
		// chegamos na raiz do sistema de arquivos. É assim que se detecta
		// o topo de forma portátil, sem comparar com "/" na mão.
		pai := filepath.Dir(atual)
		if pai == atual {
			return nil, &NoProjectError{StartDir: abs}
		}
		atual = pai
	}
}

func temMarcador(dir string) bool {
	for _, m := range marcadores {
		if exists(filepath.Join(dir, m)) {
			return true
		}
	}
	return false
}

// NoProjectError diz de onde a busca partiu, o que torna a mensagem acionável.
type NoProjectError struct {
	StartDir string
}

func (e *NoProjectError) Error() string {
	return fmt.Sprintf("nenhum projeto PHP encontrado em %s nem nos diretórios acima", e.StartDir)
}
