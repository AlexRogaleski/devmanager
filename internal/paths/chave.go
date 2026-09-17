package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// chaveDoProjeto gera um identificador legível e único para um caminho.
//
// O formato é "<nome-da-pasta>-<8 chars de hash>", por exemplo
// "appmake-erp-3f2a91c4". O nome na frente mantém o diretório navegável a
// olho nu; o hash do caminho completo garante que ~/a/api e ~/b/api não
// compartilhem shim.
//
// sha256 aqui não é segurança, é só uma função de dispersão estável: o mesmo
// caminho sempre produz a mesma chave, entre execuções e entre máquinas.
func chaveDoProjeto(projectPath string) string {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		abs = projectPath
	}

	soma := sha256.Sum256([]byte(abs))
	return filepath.Base(abs) + "-" + hex.EncodeToString(soma[:])[:8]
}
