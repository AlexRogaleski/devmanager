package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/config"
)

// composerJSON espelha só os campos do composer.json que nos interessam.
//
// O decodificador de JSON do Go ignora silenciosamente toda chave do arquivo
// que não tenha campo correspondente aqui. Por isso não precisamos mapear as
// 30 chaves de um composer.json real — declaramos as 2 que usamos e pronto.
// As tags `json:"..."` fazem a ponte entre o nome da chave e o nome do campo.
type composerJSON struct {
	Name    string            `json:"name"`
	Require map[string]string `json:"require"`
}

// composerLock espelha a lista de pacotes instalados do composer.lock.
//
// O struct anônimo dentro do slice evita criar um tipo com nome só para
// carregar dois campos que ninguém mais usa.
type composerLock struct {
	Packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"packages"`
}

const laravelPackage = "laravel/framework"

// Detect inspeciona um diretório e devolve o projeto encontrado.
//
// Regra de projeto: "não é um projeto conhecido" NÃO é erro — devolvemos um
// Project com Kind desconhecido e erro nil. Erro fica reservado para o que
// impede a inspeção de acontecer: caminho inexistente, permissão negada,
// JSON corrompido. Essa distinção evita que a CLI trate "pasta comum" como falha.
func Detect(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolvendo caminho %q: %w", dir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		// %w embrulha o erro original em vez de achatá-lo numa string.
		// Quem chamar pode usar errors.Is(err, os.ErrNotExist) para
		// distinguir "não existe" de "sem permissão" mais acima na pilha.
		return nil, fmt.Errorf("acessando %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s não é um diretório", abs)
	}

	p := &Project{
		Name:       filepath.Base(abs),
		Path:       abs,
		Kind:       KindUnknown,
		HasArtisan: exists(filepath.Join(abs, "artisan")),
		HasVendor:  exists(filepath.Join(abs, "vendor", "autoload.php")),
		HasEnv:     exists(filepath.Join(abs, ".env")),
	}

	if err := readComposerJSON(p, abs); err != nil {
		return nil, err
	}
	if err := readComposerLock(p, abs); err != nil {
		return nil, err
	}

	// O devmanager.yaml é lido por último porque é ele quem tem a palavra
	// final: o que estiver aqui sobrepõe tudo que foi detectado acima.
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, err
	}
	p.Config = cfg

	return p, nil
}

// readComposerJSON preenche o que o projeto DECLARA precisar.
func readComposerJSON(p *Project, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // sem composer.json: simplesmente não é projeto PHP
		}
		return fmt.Errorf("lendo composer.json: %w", err)
	}

	var cj composerJSON
	if err := json.Unmarshal(data, &cj); err != nil {
		return fmt.Errorf("composer.json inválido em %s: %w", dir, err)
	}

	p.Kind = KindPHP
	p.PHPConstraint = cj.Require["php"]

	// A "vírgula ok" de maps: o segundo valor diz se a chave existia.
	// Sem ele não dá para distinguir "laravel/framework ausente" de
	// "presente com versão vazia" — ambos devolveriam string vazia.
	if v, ok := cj.Require[laravelPackage]; ok {
		p.Kind = KindLaravel
		p.LaravelRequire = v
	} else if p.HasArtisan {
		// composer.json customizado pode não listar o framework direto,
		// mas artisan na raiz é sinal forte o bastante.
		p.Kind = KindLaravel
	}

	return nil
}

// readComposerLock preenche o que o projeto REALMENTE tem instalado.
func readComposerLock(p *Project, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "composer.lock"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // lock ausente é normal em projeto recém-clonado
		}
		return fmt.Errorf("lendo composer.lock: %w", err)
	}

	var cl composerLock
	if err := json.Unmarshal(data, &cl); err != nil {
		return fmt.Errorf("composer.lock inválido em %s: %w", dir, err)
	}

	for _, pkg := range cl.Packages {
		if pkg.Name == laravelPackage {
			p.LaravelLocked = pkg.Version
			p.Kind = KindLaravel
			break
		}
	}
	return nil
}

// exists é um atalho para "este caminho existe?".
//
// Ignorar o erro é aceitável aqui de propósito: para os nossos sinais
// (vendor, .env, artisan) "não consegui ler" e "não existe" levam à mesma
// decisão — tratar como ausente.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
