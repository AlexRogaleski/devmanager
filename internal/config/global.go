package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AlexRogaleski/devmanager/internal/paths"
)

// GlobalFileName é a configuração do usuário, valendo para todos os projetos.
const GlobalFileName = "config.yaml"

// Valores aceitos para a escolha de runtime de contêiner.
const (
	EngineAuto   = "auto"
	EnginePodman = "podman"
	EngineDocker = "docker"
)

// Global é a configuração do usuário.
//
// Separada da configuração de projeto porque responde a outra pergunta: o
// devmanager.yaml descreve o que um PROJETO precisa e é versionado com ele;
// isto descreve como ESTA MÁQUINA deve se comportar, e não faz sentido
// compartilhar com o time.
type Global struct {
	// Engine escolhe o runtime de contêiner: auto, docker ou podman.
	//
	// A detecção automática tenta docker antes de podman. Fixar aqui serve a
	// quem quer o contrário — em sistemas imutáveis o podman rootless é a
	// escolha melhor, e nada além desta chave faz a detecção saber disso.
	Engine string `yaml:"engine,omitempty"`
}

// GlobalPath devolve onde a configuração do usuário fica.
func GlobalPath() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, GlobalFileName), nil
}

// LoadGlobal lê a configuração do usuário.
//
// A variável de ambiente vence o arquivo: permite experimentar um engine num
// comando só, sem alterar a configuração — e é como os testes isolam isso.
func LoadGlobal() (*Global, error) {
	g := &Global{Engine: EngineAuto}

	caminho, err := GlobalPath()
	if err != nil {
		return nil, err
	}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("lendo %s: %w", caminho, err)
		}
	} else if err := yaml.Unmarshal(dados, g); err != nil {
		return nil, fmt.Errorf("%s inválido: %w", caminho, err)
	}

	if env := strings.TrimSpace(os.Getenv("DEVMANAGER_ENGINE")); env != "" {
		g.Engine = env
	}
	if g.Engine == "" {
		g.Engine = EngineAuto
	}
	return g, nil
}

// SetEngine grava a escolha de runtime de contêiner.
func SetEngine(valor string) error {
	valor = strings.ToLower(strings.TrimSpace(valor))
	if err := ValidarEngine(valor); err != nil {
		return err
	}

	caminho, err := GlobalPath()
	if err != nil {
		return err
	}

	dados, err := os.ReadFile(caminho)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lendo %s: %w", caminho, err)
	}

	// Mesma disciplina do devmanager.yaml: editamos pelo yaml.Node para
	// preservar comentários e outras chaves que o usuário tenha escrito.
	var doc yaml.Node
	if len(dados) > 0 {
		if err := yaml.Unmarshal(dados, &doc); err != nil {
			return fmt.Errorf("%s inválido: %w", caminho, err)
		}
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}
	definirEscalar(mapa, "engine", valor)

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(caminho), err)
	}
	return escreverAtomico(caminho, saida)
}

// ValidarEngine recusa valores fora do conjunto conhecido.
func ValidarEngine(valor string) error {
	switch valor {
	case EngineAuto, EnginePodman, EngineDocker:
		return nil
	default:
		return fmt.Errorf("engine inválido: %q (use auto, docker ou podman)", valor)
	}
}
