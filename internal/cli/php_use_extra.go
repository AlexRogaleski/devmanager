package cli

import (
	"os"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
)

// runtimeInfo é um alias local para encurtar assinaturas neste pacote.
type runtimeInfo = runtimes.Runtime

// jaConfigurado informa se o arquivo de configuração do editor já existe.
func jaConfigurado(arquivo string) bool {
	_, err := os.Stat(arquivo)
	return err == nil
}
