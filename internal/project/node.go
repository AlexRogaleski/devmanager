package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// lerExigenciaDeNode descobre qual Node o projeto espera.
func lerExigenciaDeNode(p *Project, dir string) {
	if v := lerNvmrc(filepath.Join(dir, ".nvmrc")); v != "" {
		p.NodeConstraint, p.NodeOrigem = v, OrigemNvmrc
		return
	}
	if v := lerEnginesNode(filepath.Join(dir, "package.json")); v != "" {
		p.NodeConstraint, p.NodeOrigem = v, OrigemPackage
	}
}

// lerNvmrc lê o arquivo do nvm.
//
// O conteúdo é uma linha só, geralmente "22" ou "v22.11.0". Apelidos como
// "lts/*" e "node" existem no nvm mas não são versões — devolvemos vazio,
// porque resolvê-los exigiria consultar a lista de releases, e o resultado
// mudaria com o tempo sem o projeto ter mudado.
func lerNvmrc(caminho string) string {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return ""
	}

	linha := strings.TrimSpace(string(dados))
	if linha == "" || strings.ContainsAny(linha, "/*") {
		return ""
	}
	if linha == "node" || linha == "stable" || linha == "unstable" {
		return ""
	}
	return linha
}

// lerEnginesNode lê engines.node do package.json.
func lerEnginesNode(caminho string) string {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return ""
	}

	var pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(dados, &pkg); err != nil {
		return ""
	}
	return strings.TrimSpace(pkg.Engines.Node)
}
