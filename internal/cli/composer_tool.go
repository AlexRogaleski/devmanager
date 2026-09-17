package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/tools"
)

// garantirComposer baixa (se preciso) o composer.phar oficial e instala o
// wrapper no shim do projeto, devolvendo o caminho do phar.
//
// É chamado só pelos comandos que realmente precisam do composer. Colocar
// isso em ambienteDoProjeto faria todo `devm artisan` consultar a rede.
func garantirComposer(ctx context.Context, w io.Writer, shimDir, phpBin string) (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}

	c := &tools.Composer{Dir: filepath.Join(dir, "tools", "composer")}

	// Se já temos um, nem consultamos a rede: o caminho comum fica instantâneo.
	if phar, ok := c.QualquerInstalado(); ok {
		return phar, runner.EnsureComposerShim(shimDir, phpBin, phar)
	}

	fmt.Fprintln(w, "baixando o composer.phar oficial (uma vez só)...")

	phar, err := c.Ensure(ctx, tools.Progresso(progressoNoTerminal(w)))
	if err != nil {
		return "", fmt.Errorf("obtendo o composer: %w", err)
	}

	fmt.Fprintf(w, "\rcomposer em %s\n", phar)
	return phar, runner.EnsureComposerShim(shimDir, phpBin, phar)
}
