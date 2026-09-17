package ide

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// PhpStorm configura o .idea/php.xml do projeto.
//
// Este arquivo é o exemplo de por que Editor é uma interface e não uma função
// com um switch: o VS Code guarda configuração em JSONC, o PhpStorm em XML, e
// as duas implementações não compartilham uma linha de código. O que elas
// compartilham é o contrato — e é só isso que o resto do sistema conhece.
type PhpStorm struct{}

func (p *PhpStorm) Name() string { return "phpstorm" }

func (p *PhpStorm) Detected(projectPath string) bool {
	if info, err := os.Stat(filepath.Join(projectPath, ".idea")); err == nil && info.IsDir() {
		return true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, marca := range []string{
		filepath.Join(".config", "JetBrains"),
		filepath.Join(".local", "share", "JetBrains"),
		filepath.Join("Library", "Application Support", "JetBrains"), // macOS
	} {
		if entradas, err := os.ReadDir(filepath.Join(home, marca)); err == nil {
			for _, e := range entradas {
				// Só conta se houver um PhpStorm de fato instalado; um IntelliJ
				// ou um PyCharm não configuram PHP.
				if len(e.Name()) >= 8 && e.Name()[:8] == "PhpStorm" {
					return true
				}
			}
		}
	}
	return false
}

// atributoNivel casa o atributo que define a versão de linguagem do projeto.
var atributoNivel = regexp.MustCompile(`php_language_level="[^"]*"`)

// componenteCompartilhado casa a tag do componente, com ou sem o atributo.
var componenteCompartilhado = regexp.MustCompile(`<component name="PhpProjectSharedConfiguration"`)

const modeloPhpXML = `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="PhpProjectSharedConfiguration" php_language_level="%s">
    <option name="suggestChangeDefaultLanguageLevel" value="false" />
  </component>
</project>
`

// Configure ajusta o nível de linguagem PHP do projeto.
//
// LIMITAÇÃO CONHECIDA, e documentada de propósito: o PhpStorm guarda o
// CAMINHO do interpretador na configuração global da IDE (que muda de lugar a
// cada versão), não no projeto. Mexer lá seria frágil e invasivo. O que dá
// para fazer com segurança no nível do projeto é fixar a versão da linguagem,
// que já resolve o problema principal — o editor parar de aceitar sintaxe que
// o PHP do projeto não entende.
func (p *PhpStorm) Configure(cfg Config, dryRun bool) (Result, error) {
	dir := filepath.Join(cfg.ProjectPath, ".idea")
	arquivo := filepath.Join(dir, "php.xml")

	res := Result{File: arquivo, Changes: map[string]string{}}
	nivel := cfg.PHPVersion.MajorMinor()

	bruto, err := os.ReadFile(arquivo)
	if err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("lendo %s: %w", arquivo, err)
	}

	var saida []byte
	switch {
	case len(bruto) == 0:
		saida = []byte(fmt.Sprintf(modeloPhpXML, nivel))

	case atributoNivel.Match(bruto):
		novo := fmt.Sprintf(`php_language_level="%s"`, nivel)
		if string(atributoNivel.Find(bruto)) == novo {
			res.NoChange = true
			return res, nil
		}
		// ReplaceAll com a primeira ocorrência basta: o atributo é único.
		saida = atributoNivel.ReplaceAll(bruto, []byte(novo))

	case componenteCompartilhado.Match(bruto):
		// Componente existe mas sem o atributo: acrescenta na abertura da tag.
		saida = componenteCompartilhado.ReplaceAll(bruto,
			[]byte(fmt.Sprintf(`<component name="PhpProjectSharedConfiguration" php_language_level="%s"`, nivel)))

	default:
		return res, fmt.Errorf(
			"%s existe mas não tem o componente PhpProjectSharedConfiguration —\n"+
				"  abra as configurações de PHP no PhpStorm uma vez e rode de novo", arquivo)
	}

	res.Changes["php_language_level"] = nivel
	if dryRun {
		return res, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("criando %s: %w", dir, err)
	}
	if err := escreverAtomico(arquivo, saida); err != nil {
		return res, err
	}
	return res, nil
}
