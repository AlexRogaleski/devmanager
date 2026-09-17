package ide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

func cfgFalsa(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		ProjectPath: dir,
		PHPBin:      "/shims/projeto/php",
		PHPVersion:  semver.MustParse("8.3.15"),
	}
}

func TestVSCodeCriaArquivoNovo(t *testing.T) {
	cfg := cfgFalsa(t)

	res, err := (&VSCode{}).Configure(cfg, false)
	if err != nil {
		t.Fatalf("Configure falhou: %v", err)
	}
	if len(res.Changes) != 3 {
		t.Errorf("esperava 3 chaves alteradas, veio %d: %v", len(res.Changes), res.Changes)
	}

	m, err := lerSettings(res.File)
	if err != nil {
		t.Fatalf("arquivo gerado é inválido: %v", err)
	}
	if m["php.validate.executablePath"] != cfg.PHPBin {
		t.Errorf("executablePath = %v, esperava %v", m["php.validate.executablePath"], cfg.PHPBin)
	}
	if m["intelephense.environment.phpVersion"] != "8.3.15" {
		t.Errorf("phpVersion = %v, esperava 8.3.15", m["intelephense.environment.phpVersion"])
	}
}

func TestVSCodeEhIdempotente(t *testing.T) {
	cfg := cfgFalsa(t)
	v := &VSCode{}

	if _, err := v.Configure(cfg, false); err != nil {
		t.Fatal(err)
	}
	res, err := v.Configure(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoChange {
		t.Errorf("segunda execução deveria ser no-op, alterou: %v", res.Changes)
	}
}

func TestVSCodeDryRunNaoGrava(t *testing.T) {
	cfg := cfgFalsa(t)

	res, err := (&VSCode{}).Configure(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Error("dry run deveria reportar as mudanças que faria")
	}
	if _, err := os.Stat(res.File); !os.IsNotExist(err) {
		t.Error("dry run gravou o arquivo")
	}
}

// A garantia que mais importa: configuração alheia sobrevive intacta.
func TestVSCodePreservaConfiguracaoExistente(t *testing.T) {
	cfg := cfgFalsa(t)
	vscodeDir := filepath.Join(cfg.ProjectPath, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	original := `{
    // Formatação
    "editor.formatOnSave": true,
    "[php]": {
        "editor.defaultFormatter": "open-southeners.laravel-pint"
    },
    "intelephense.environment.phpVersion": "8.1.0"
}
`
	arquivo := filepath.Join(vscodeDir, "settings.json")
	if err := os.WriteFile(arquivo, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := (&VSCode{}).Configure(cfg, false); err != nil {
		t.Fatalf("Configure falhou: %v", err)
	}

	dados, err := os.ReadFile(arquivo)
	if err != nil {
		t.Fatal(err)
	}
	texto := string(dados)

	for _, preservar := range []string{
		"// Formatação",
		`"editor.formatOnSave": true`,
		"open-southeners.laravel-pint",
	} {
		if !strings.Contains(texto, preservar) {
			t.Errorf("perdeu %q:\n%s", preservar, texto)
		}
	}
	if strings.Contains(texto, "8.1.0") {
		t.Errorf("versão antiga não foi atualizada:\n%s", texto)
	}
	if !strings.Contains(texto, "8.3.15") {
		t.Errorf("versão nova não foi gravada:\n%s", texto)
	}
}

func TestPhpStormCriaEAtualiza(t *testing.T) {
	cfg := cfgFalsa(t)
	ps := &PhpStorm{}

	res, err := ps.Configure(cfg, false)
	if err != nil {
		t.Fatalf("Configure falhou: %v", err)
	}

	dados, err := os.ReadFile(res.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dados), `php_language_level="8.3"`) {
		t.Errorf("nível de linguagem errado:\n%s", dados)
	}

	// Segunda passada com versão diferente deve atualizar o atributo.
	cfg.PHPVersion = semver.MustParse("8.4.1")
	if _, err := ps.Configure(cfg, false); err != nil {
		t.Fatal(err)
	}
	dados, _ = os.ReadFile(res.File)
	if !strings.Contains(string(dados), `php_language_level="8.4"`) {
		t.Errorf("atributo não foi atualizado:\n%s", dados)
	}
	if strings.Contains(string(dados), `"8.3"`) {
		t.Errorf("valor antigo permaneceu:\n%s", dados)
	}
}

// Todo Editor registrado precisa cumprir o contrato mínimo.
func TestTodosOsEditoresRespeitamOContrato(t *testing.T) {
	for _, e := range All() {
		t.Run(e.Name(), func(t *testing.T) {
			cfg := cfgFalsa(t)

			res, err := e.Configure(cfg, true)
			if err != nil {
				t.Fatalf("dry run falhou: %v", err)
			}
			if res.File == "" {
				t.Error("Result.File vazio")
			}
			if _, err := os.Stat(res.File); !os.IsNotExist(err) {
				t.Error("dry run gravou arquivo")
			}
		})
	}
}
