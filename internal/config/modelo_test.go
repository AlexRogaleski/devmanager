package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Toda chave que o Config entende tem de aparecer no modelo — ativa ou como
// exemplo comentado.
//
// O teste lê as tags `yaml:"..."` por reflexão em vez de ter a lista escrita
// à mão: assim quem adicionar uma opção nova ao struct e esquecer de
// documentá-la descobre aqui, e não meses depois quando alguém perguntar
// "dá para configurar isso?".
func TestModeloDocumentaTodasAsChaves(t *testing.T) {
	texto := Modelo(Config{})

	tipo := reflect.TypeOf(Config{})
	for i := range tipo.NumField() {
		tag := tipo.Field(i).Tag.Get("yaml")
		chave, _, _ := strings.Cut(tag, ",")
		if chave == "" || chave == "-" {
			continue
		}

		if !strings.Contains(texto, chave+":") {
			t.Errorf("a chave %q não aparece no modelo gerado", chave)
		}
	}
}

// Um modelo sem nada preenchido tem de ser um arquivo YAML válido e VAZIO:
// todas as opções continuam comentadas, nenhuma entra em vigor por acidente.
func TestModeloSemValoresNaoAtivaNada(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte(Modelo(Config{})), &c); err != nil {
		t.Fatalf("o modelo gerado não é YAML válido: %v", err)
	}

	if !reflect.DeepEqual(c, Config{}) {
		t.Errorf("o modelo vazio ativou alguma chave: %+v", c)
	}
}

// O que entra no modelo tem de voltar igual na leitura. É o teste que pega
// erro de aspas — um comando com {{port}} no lugar errado quebraria o YAML.
func TestModeloVoltaIgualNaLeitura(t *testing.T) {
	original := Config{
		PHP:      "8.4",
		Node:     "22",
		Services: []string{"postgres:17", "redis", "mailpit"},
		Processes: map[string]string{
			"serve": "php artisan serve --host=127.0.0.1 --port={{port}}",
			"queue": "php artisan queue:listen --tries=1",
			"vite":  "npm run dev -- --port {{port:vite}}",
		},
		PHPIni: map[string]string{
			"max_execution_time":  "120",
			"upload_max_filesize": "200M",
		},
	}

	texto := Modelo(original)

	var lido Config
	if err := yaml.Unmarshal([]byte(texto), &lido); err != nil {
		t.Fatalf("o modelo preenchido não é YAML válido: %v\n\n%s", err, texto)
	}
	if !reflect.DeepEqual(lido, original) {
		t.Errorf("leitura diferente da escrita:\n  lido:     %+v\n  original: %+v\n\n%s", lido, original, texto)
	}
}

// Um valor que começa com { abriria um mapeamento em fluxo sem as aspas.
func TestValorPerigosoGanhaAspas(t *testing.T) {
	texto := Modelo(Config{Processes: map[string]string{"x": "{{port}} solto"}})

	var lido Config
	if err := yaml.Unmarshal([]byte(texto), &lido); err != nil {
		t.Fatalf("YAML inválido: %v\n\n%s", err, texto)
	}
	if lido.Processes["x"] != "{{port}} solto" {
		t.Errorf("valor lido = %q", lido.Processes["x"])
	}
}

func TestCriarGravaOModelo(t *testing.T) {
	dir := t.TempDir()

	if err := Criar(dir, Config{PHP: "8.4"}); err != nil {
		t.Fatal(err)
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.PHP != "8.4" {
		t.Fatalf("config lida: %+v", c)
	}

	dados, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	// O exemplo do marcador precisa estar lá: é a única documentação que a
	// pessoa vai ver na hora de declarar processes.
	if !strings.Contains(string(dados), "{{port}}") {
		t.Error("o arquivo gerado não explica o {{port}}")
	}
}

// SetPHP num diretório sem arquivo cria o modelo inteiro, não uma linha solta.
func TestSetPHPNovoTrazAsOpcoesComentadas(t *testing.T) {
	dir := t.TempDir()

	if err := SetPHP(dir, "8.3"); err != nil {
		t.Fatal(err)
	}

	dados, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	texto := string(dados)

	if !strings.Contains(texto, `php: "8.3"`) {
		t.Errorf("versão não ficou ativa:\n%s", texto)
	}
	for _, chave := range []string{"# services:", "# processes:", "# php_ini:"} {
		if !strings.Contains(texto, chave) {
			t.Errorf("faltou o exemplo %q:\n%s", chave, texto)
		}
	}
}
