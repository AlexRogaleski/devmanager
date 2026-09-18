// Package config lê e escreve o devmanager.yaml de um projeto.
//
// Este arquivo é a "configuração declarativa" do projeto: o que ele precisa
// para rodar, versionável junto com o código. A regra que o guia é uma só —
// o que está escrito aqui vence qualquer detecção automática.
//
// Detectar requisitos a partir do composer.json é conveniência; poder fixar a
// versão à mão é necessidade. Um projeto pode exigir "^8.2" e estar em
// produção no 8.3: sem poder fixar, o desenvolvimento rodaria numa versão
// diferente da que realmente importa.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName é o nome do arquivo na raiz do projeto.
const FileName = "devmanager.yaml"

// Config é o conteúdo do devmanager.yaml.
//
// As tags `yaml:"..."` funcionam como as de JSON. omitempty evita gravar
// chaves vazias, mantendo o arquivo enxuto enquanto o projeto não usa tudo.
type Config struct {
	// PHP fixa a versão de PHP do projeto. Aceita a mesma gramática do
	// Composer: "8.3" (qualquer 8.3.x), "8.3.15" (exata), "^8.3" (faixa).
	PHP string `yaml:"php,omitempty"`

	// Node fixa a versão de Node do projeto, na mesma gramática do PHP:
	// "22" (qualquer 22.x), "22.11.0" (exata), "^22" (faixa).
	Node string `yaml:"node,omitempty"`

	// Campos abaixo ainda não são usados, mas já definem o formato para os
	// próximos passos. Declará-los agora evita quebrar arquivos existentes
	// quando as features chegarem.
	Services  []string          `yaml:"services,omitempty"`
	Processes map[string]string `yaml:"processes,omitempty"`
}

// Path devolve o caminho do arquivo de configuração de um diretório.
func Path(dir string) string {
	return filepath.Join(dir, FileName)
}

// Load lê o devmanager.yaml de um diretório.
//
// Arquivo ausente devolve (nil, nil), não erro: a maioria dos projetos não
// tem um, e isso é normal. Só YAML inválido é falha — silenciar um arquivo
// mal formado faria o Dev Manager ignorar a escolha explícita do dev, que é
// exatamente o oposto do que ele deveria fazer.
func Load(dir string) (*Config, error) {
	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}

	var c Config
	if err := yaml.Unmarshal(dados, &c); err != nil {
		return nil, fmt.Errorf("%s inválido: %w", caminho, err)
	}
	return &c, nil
}
