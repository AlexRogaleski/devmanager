package dotenv

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func escrever(t *testing.T, conteudo string) string {
	t.Helper()

	caminho := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(caminho, []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
	return caminho
}

func ler(t *testing.T, caminho string) string {
	t.Helper()

	dados, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	return string(dados)
}

func TestLoad(t *testing.T) {
	caminho := escrever(t, `# comentário
APP_NAME=Laravel
APP_KEY=base64:abc=def=
DB_CONNECTION="sqlite"
DB_PASSWORD='com espaço'
export FORCE_HTTPS=true
VAZIA=
  ESPACADA = valor
sem-igual
`)

	valores, err := Load(caminho)
	if err != nil {
		t.Fatal(err)
	}

	esperado := map[string]string{
		"APP_NAME":      "Laravel",
		"APP_KEY":       "base64:abc=def=", // o "=" do base64 sobrevive
		"DB_CONNECTION": "sqlite",          // aspas removidas
		"DB_PASSWORD":   "com espaço",
		"FORCE_HTTPS":   "true", // prefixo export tratado
		"VAZIA":         "",
		"ESPACADA":      "valor",
	}
	for k, v := range esperado {
		if valores[k] != v {
			t.Errorf("%s = %q, esperava %q", k, valores[k], v)
		}
	}
	if _, existe := valores["sem-igual"]; existe {
		t.Error("linha sem = não deveria virar chave")
	}
}

func TestLoadArquivoAusente(t *testing.T) {
	valores, err := Load(filepath.Join(t.TempDir(), "nao-existe"))
	if err != nil {
		t.Fatalf("ausência não deveria ser erro: %v", err)
	}
	if len(valores) != 0 {
		t.Errorf("esperava mapa vazio, veio %v", valores)
	}
}

// A garantia central: comentários, ordem e chaves alheias sobrevivem.
func TestSetPreservaOArquivo(t *testing.T) {
	caminho := escrever(t, `# ---- Aplicação ----
APP_NAME=Laravel
APP_ENV=local

# ---- Banco ----
DB_CONNECTION=sqlite
DB_DATABASE=database/database.sqlite

# ---- Fila ----
QUEUE_CONNECTION=database
`)

	mudadas, err := Set(caminho, map[string]string{
		"DB_CONNECTION": "pgsql",
		"DB_DATABASE":   "meu_projeto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(mudadas, []string{"DB_CONNECTION", "DB_DATABASE"}) {
		t.Errorf("mudadas = %v", mudadas)
	}

	texto := ler(t, caminho)
	for _, preservar := range []string{
		"# ---- Aplicação ----",
		"APP_NAME=Laravel",
		"# ---- Banco ----",
		"# ---- Fila ----",
		"QUEUE_CONNECTION=database",
	} {
		if !strings.Contains(texto, preservar) {
			t.Errorf("perdeu %q:\n%s", preservar, texto)
		}
	}
	if !strings.Contains(texto, "DB_CONNECTION=pgsql") {
		t.Errorf("valor não atualizado:\n%s", texto)
	}
	if strings.Contains(texto, "sqlite") {
		t.Errorf("valor antigo permaneceu:\n%s", texto)
	}

	// A chave alterada tem que ficar NO LUGAR dela, não no fim do arquivo.
	linhas := strings.Split(texto, "\n")
	for i, l := range linhas {
		if strings.HasPrefix(l, "DB_CONNECTION=") && i < 4 {
			t.Errorf("a chave saiu do bloco original (linha %d)", i)
		}
	}
}

func TestSetAcrescentaChavesNovas(t *testing.T) {
	caminho := escrever(t, "APP_NAME=Laravel\n")

	if _, err := Set(caminho, map[string]string{
		"REDIS_HOST":   "127.0.0.1",
		"REDIS_PREFIX": "projeto_",
	}); err != nil {
		t.Fatal(err)
	}

	valores, _ := Load(caminho)
	if valores["REDIS_HOST"] != "127.0.0.1" || valores["REDIS_PREFIX"] != "projeto_" {
		t.Errorf("chaves novas não foram gravadas: %v", valores)
	}
	if valores["APP_NAME"] != "Laravel" {
		t.Error("chave original foi perdida")
	}
}

// Rodar devm up duas vezes não pode alterar o arquivo na segunda.
func TestSetNaoGravaQuandoNadaMuda(t *testing.T) {
	caminho := escrever(t, "DB_CONNECTION=pgsql\nDB_PORT=5432\n")
	antes := ler(t, caminho)

	mudadas, err := Set(caminho, map[string]string{
		"DB_CONNECTION": "pgsql",
		"DB_PORT":       "5432",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mudadas) != 0 {
		t.Errorf("reportou mudanças inexistentes: %v", mudadas)
	}
	if depois := ler(t, caminho); depois != antes {
		t.Errorf("arquivo foi reescrito sem necessidade:\n%q", depois)
	}
}

func TestSetPreservaExport(t *testing.T) {
	caminho := escrever(t, "export DB_PORT=5432\n")

	if _, err := Set(caminho, map[string]string{"DB_PORT": "5433"}); err != nil {
		t.Fatal(err)
	}

	if texto := ler(t, caminho); !strings.Contains(texto, "export DB_PORT=5433") {
		t.Errorf("o prefixo export foi perdido:\n%s", texto)
	}
}

func TestSetCitaQuandoNecessario(t *testing.T) {
	caminho := escrever(t, "A=1\n")

	if _, err := Set(caminho, map[string]string{
		"SIMPLES":    "valor",
		"COM_ESPACO": "dois valores",
		"COM_HASH":   "a#b",
		"COM_ASPAS":  `tem "aspas"`,
	}); err != nil {
		t.Fatal(err)
	}

	texto := ler(t, caminho)
	if !strings.Contains(texto, "SIMPLES=valor") {
		t.Errorf("valor simples não deveria ser citado:\n%s", texto)
	}
	if !strings.Contains(texto, `COM_ESPACO="dois valores"`) {
		t.Errorf("valor com espaço deveria ser citado:\n%s", texto)
	}
	if !strings.Contains(texto, `COM_HASH="a#b"`) {
		t.Errorf("valor com # deveria ser citado:\n%s", texto)
	}

	// E tem que sobreviver ao round-trip.
	valores, _ := Load(caminho)
	if valores["COM_ESPACO"] != "dois valores" {
		t.Errorf("COM_ESPACO = %q", valores["COM_ESPACO"])
	}
	if valores["COM_HASH"] != "a#b" {
		t.Errorf("COM_HASH = %q", valores["COM_HASH"])
	}
}

func TestSetEmArquivoInexistente(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), ".env")

	if _, err := Set(caminho, map[string]string{"DB_PORT": "5432"}); err != nil {
		t.Fatal(err)
	}

	valores, _ := Load(caminho)
	if valores["DB_PORT"] != "5432" {
		t.Errorf("não criou o arquivo com a chave: %v", valores)
	}
}

// O .env tem credenciais: permissão precisa ser restritiva.
func TestSetUsaPermissaoRestrita(t *testing.T) {
	caminho := escrever(t, "A=1\n")

	if _, err := Set(caminho, map[string]string{"B": "2"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if modo := info.Mode().Perm(); modo != 0o600 {
		t.Errorf("permissão = %v, esperava 0600", modo)
	}
}

func TestSetNaoAcumulaLinhasEmBranco(t *testing.T) {
	caminho := escrever(t, "A=1\n")

	for i := 0; i < 3; i++ {
		if _, err := Set(caminho, map[string]string{"B": strings.Repeat("x", i+1)}); err != nil {
			t.Fatal(err)
		}
	}

	texto := ler(t, caminho)
	if strings.Contains(texto, "\n\n\n") {
		t.Errorf("acumulou linhas em branco:\n%q", texto)
	}
}

func TestSetGuardaCopiaDoOriginal(t *testing.T) {
	original := "DB_CONNECTION=sqlite\nDB_PASSWORD=segredo\n"
	caminho := escrever(t, original)

	if _, err := Set(caminho, map[string]string{"DB_CONNECTION": "mysql"}); err != nil {
		t.Fatal(err)
	}

	copia := CaminhoDaCopia(caminho)
	if !Existe(copia) {
		t.Fatalf("a cópia não foi criada em %s", copia)
	}
	if got := ler(t, copia); got != original {
		t.Errorf("a cópia não tem o conteúdo original:\n%q", got)
	}
}

// TestSetNaoSobrescreveACopia é o teste que justifica a regra.
//
// Se a cópia fosse refeita a cada gravação, ela passaria a valer "o estado
// antes da última edição" — e depois do segundo `devm up` o arquivo que o
// usuário escreveu à mão estaria perdido. A cópia precisa ser a ORIGINAL.
func TestSetNaoSobrescreveACopia(t *testing.T) {
	original := "DB_CONNECTION=sqlite\n"
	caminho := escrever(t, original)

	if _, err := Set(caminho, map[string]string{"DB_CONNECTION": "mysql"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(caminho, map[string]string{"DB_CONNECTION": "pgsql"}); err != nil {
		t.Fatal(err)
	}

	if got := ler(t, CaminhoDaCopia(caminho)); got != original {
		t.Errorf("a cópia foi sobrescrita:\n%q", got)
	}
}

func TestSetNaoCriaCopiaQuandoNadaMuda(t *testing.T) {
	caminho := escrever(t, "DB_CONNECTION=mysql\n")

	if _, err := Set(caminho, map[string]string{"DB_CONNECTION": "mysql"}); err != nil {
		t.Fatal(err)
	}

	if Existe(CaminhoDaCopia(caminho)) {
		t.Error("criou cópia sem ter alterado nada")
	}
}

func TestSetNaoCriaCopiaDeArquivoInexistente(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), ".env")

	if _, err := Set(caminho, map[string]string{"APP_ENV": "local"}); err != nil {
		t.Fatal(err)
	}

	if Existe(CaminhoDaCopia(caminho)) {
		t.Error("criou cópia de um .env que não existia")
	}
}

// TestCopiaUsaPermissaoRestrita guarda a correção de um defeito real.
//
// A cópia herdava o modo do original. Num projeto cujo .env estava 0644 — o
// que o umask produz num `cat > .env` qualquer — a cópia saía legível por
// todos, ao lado de um .env que o próprio Set acabara de restringir a 0600.
// Mesmos segredos, dois modos, pelo mesmo caminho de código. O original
// começa 0644 aqui de propósito: é o caso que falhava.
func TestCopiaUsaPermissaoRestrita(t *testing.T) {
	caminho := escrever(t, "DB_PASSWORD=segredo\n")
	if err := os.Chmod(caminho, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Set(caminho, map[string]string{"DB_CONNECTION": "mysql"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(CaminhoDaCopia(caminho))
	if err != nil {
		t.Fatal(err)
	}
	if modo := info.Mode().Perm(); modo != 0o600 {
		t.Errorf("permissão da cópia = %o, queria 600", modo)
	}
}
