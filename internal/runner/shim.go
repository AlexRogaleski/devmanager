package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
)

// EnsureShim cria (ou atualiza) o diretório de shim de um projeto e devolve
// o caminho dele.
//
// O shim é um diretório contendo um link chamado "php" que aponta para o
// runtime escolhido. Ele existe por dois motivos, e os dois importam:
//
//  1. Subprocessos. O comando que rodamos vai criar outros processos: o
//     composer chama "php", o artisan chama "php" em comandos que rodam em
//     background, scripts npm chamam "php". Colocando o shim na frente do
//     PATH, TODA a árvore de processos usa a versão certa — não só o primeiro.
//
//  2. Editores. O Intelephense e o PHP Debug do VS Code precisam de um caminho
//     fixo para o interpretador, gravado no .vscode/settings.json. Esse
//     caminho tem que continuar válido com o devm parado, o que descarta um
//     diretório temporário.
//
// A função é idempotente: chamar várias vezes com o mesmo runtime não muda
// nada, e chamar com um runtime diferente reaponta o link.
func EnsureShim(shimDir string, rt runtimes.Runtime) (string, error) {
	// MkdirAll não reclama se o diretório já existe — é o mkdir -p.
	// O modo 0o755 dá leitura e execução a todos, escrita só ao dono.
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", fmt.Errorf("criando shim em %s: %w", shimDir, err)
	}

	link := filepath.Join(shimDir, "php")

	// Se o link já aponta para o lugar certo, não há nada a fazer.
	// Readlink lê o ALVO do link sem segui-lo, que é o que queremos comparar.
	if alvo, err := os.Readlink(link); err == nil && alvo == rt.Bin {
		return shimDir, nil
	}

	// Symlink falha se o destino já existe, então removemos antes.
	// os.Remove num caminho inexistente devolve erro, que ignoramos de
	// propósito: "já não existia" é exatamente o estado que queremos.
	_ = os.Remove(link)

	if err := os.Symlink(rt.Bin, link); err != nil {
		return "", fmt.Errorf("apontando %s para %s: %w", link, rt.Bin, err)
	}
	return shimDir, nil
}

// PHPPath devolve o caminho do interpretador dentro de um shim.
// É o valor que vai para o .vscode/settings.json.
func PHPPath(shimDir string) string {
	return filepath.Join(shimDir, "php")
}
