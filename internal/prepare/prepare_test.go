package prepare

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/project"
)

// executorFalso registra o que seria executado, sem executar nada.
//
// É toda a razão de Executor ser uma interface: dá para testar o plano
// inteiro sem PHP, sem composer e sem npm instalados.
type executorFalso struct {
	chamadas []string
	falhaEm  string
}

func (e *executorFalso) Run(_ context.Context, nome string, args ...string) error {
	linha := strings.TrimSpace(nome + " " + strings.Join(args, " "))
	e.chamadas = append(e.chamadas, linha)

	if e.falhaEm != "" && strings.Contains(linha, e.falhaEm) {
		return errors.New("falha simulada")
	}
	return nil
}

func projetoEm(t *testing.T, arquivos map[string]string) *project.Project {
	t.Helper()

	dir := t.TempDir()
	for nome, conteudo := range arquivos {
		caminho := filepath.Join(dir, nome)
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p, err := project.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nomes(passos []Passo) []string {
	out := make([]string, len(passos))
	for i, p := range passos {
		out[i] = p.Nome
	}
	return out
}

func TestPlanoDeCloneLimpo(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json": `{"require":{"php":"^8.3","laravel/framework":"^12.0"}}`,
		"artisan":       "#!/usr/bin/env php",
		".env.example":  "APP_KEY=\nDB_CONNECTION=sqlite\n",
		"package.json":  `{"name":"app"}`,
	})

	passos := Pendentes(Plano(p, &executorFalso{}, Opcoes{}))

	esperado := []string{
		"composer install",
		"cp .env.example .env",
		"php artisan key:generate",
		"npm install",
		"touch database/database.sqlite",
	}
	got := nomes(passos)
	if len(got) != len(esperado) {
		t.Fatalf("passos = %v, esperava %v", got, esperado)
	}
	for i := range esperado {
		if got[i] != esperado[i] {
			t.Errorf("posição %d = %q, esperava %q", i, got[i], esperado[i])
		}
	}
}

func TestPlanoDeProjetoPronto(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json":       `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":             "#!/usr/bin/env php",
		".env":                "APP_KEY=base64:abc\nDB_CONNECTION=mysql\n",
		".env.example":        "APP_KEY=\n",
		"vendor/autoload.php": "<?php",
	})

	if faltando := Pendentes(Plano(p, &executorFalso{}, Opcoes{})); len(faltando) != 0 {
		t.Errorf("projeto pronto não deveria ter pendências: %v", nomes(faltando))
	}
}

// O gerenciador de frontend vem do lockfile: gerar um package-lock.json num
// projeto pnpm sujaria o repositório e confundiria o time.
func TestDetectaGerenciadorDeNode(t *testing.T) {
	casos := map[string]string{
		"package-lock.json": "npm install",
		"pnpm-lock.yaml":    "pnpm install",
		"yarn.lock":         "yarn install",
		"bun.lockb":         "bun install",
	}

	for lock, esperado := range casos {
		t.Run(lock, func(t *testing.T) {
			p := projetoEm(t, map[string]string{
				"composer.json": `{"require":{}}`,
				"package.json":  `{"name":"app"}`,
				lock:            "",
			})

			var achou bool
			for _, passo := range Plano(p, &executorFalso{}, Opcoes{}) {
				if passo.Nome == esperado {
					achou = true
				}
			}
			if !achou {
				t.Errorf("com %s esperava o passo %q, veio %v", lock, esperado, nomes(Plano(p, &executorFalso{}, Opcoes{})))
			}
		})
	}
}

func TestSemPackageJsonNaoTemPassoDeNode(t *testing.T) {
	p := projetoEm(t, map[string]string{"composer.json": `{"require":{}}`})

	for _, passo := range Plano(p, &executorFalso{}, Opcoes{}) {
		if strings.Contains(passo.Nome, "install") && !strings.HasPrefix(passo.Nome, "composer") {
			t.Errorf("passo de node não deveria existir: %q", passo.Nome)
		}
	}
}

// migrate escreve no banco: só entra quando pedido explicitamente.
func TestMigrateSoComOpcao(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json": `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":       "#!/usr/bin/env php",
	})

	for _, passo := range Plano(p, &executorFalso{}, Opcoes{}) {
		if strings.Contains(passo.Nome, "migrate") {
			t.Error("migrate não deveria entrar no plano padrão")
		}
	}

	var achou bool
	for _, passo := range Plano(p, &executorFalso{}, Opcoes{Migrate: true}) {
		if strings.Contains(passo.Nome, "migrate") {
			achou = true
		}
	}
	if !achou {
		t.Error("com Migrate: true o passo deveria entrar")
	}
}

func TestComposerCustomizado(t *testing.T) {
	p := projetoEm(t, map[string]string{"composer.json": `{"require":{}}`})
	ex := &executorFalso{}

	passos := Plano(p, ex, Opcoes{Composer: []string{"/bin/php", "/x/composer.phar"}})
	if err := passos[0].Executar(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ex.chamadas) != 1 || ex.chamadas[0] != "/bin/php /x/composer.phar install" {
		t.Errorf("chamadas = %v", ex.chamadas)
	}
}

// Copiar o .env é feito em Go, sem shell — e nunca sobrescreve um existente.
func TestCopiaEnvNaoSobrescreve(t *testing.T) {
	p := projetoEm(t, map[string]string{
		"composer.json": `{"require":{"laravel/framework":"^12.0"}}`,
		"artisan":       "#!/usr/bin/env php",
		".env.example":  "APP_KEY=\nFOO=exemplo\n",
	})

	passos := Plano(p, &executorFalso{}, Opcoes{})
	var passoEnv Passo
	for _, passo := range passos {
		if strings.HasPrefix(passo.Nome, "cp .env") {
			passoEnv = passo
		}
	}

	if err := passoEnv.Executar(context.Background()); err != nil {
		t.Fatalf("Executar falhou: %v", err)
	}

	envPath := filepath.Join(p.Path, ".env")
	if err := os.WriteFile(envPath, []byte("APP_KEY=segredo-real\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Segunda execução não pode destruir o .env com credenciais reais.
	if err := passoEnv.Executar(context.Background()); err != nil {
		t.Fatalf("segunda execução falhou: %v", err)
	}

	dados, _ := os.ReadFile(envPath)
	if !strings.Contains(string(dados), "segredo-real") {
		t.Errorf(".env foi sobrescrito:\n%s", dados)
	}
}

func TestPlanoSemExecutorNaoExecuta(t *testing.T) {
	p := projetoEm(t, map[string]string{"composer.json": `{"require":{}}`})

	passos := Plano(p, nil, Opcoes{})
	if err := passos[0].Executar(context.Background()); !errors.Is(err, ErrSemExecutor) {
		t.Errorf("erro = %v, esperava ErrSemExecutor", err)
	}
}

func TestLerEnv(t *testing.T) {
	dir := t.TempDir()
	conteudo := `# comentário
APP_KEY=base64:abc=def=
DB_CONNECTION="sqlite"
VAZIA=
  ESPACADA = valor
linha-sem-igual
`
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}

	env := lerEnv(filepath.Join(dir, ".env"))
	esperado := map[string]string{
		"APP_KEY":       "base64:abc=def=", // o "=" do base64 sobrevive
		"DB_CONNECTION": "sqlite",          // aspas removidas
		"VAZIA":         "",
		"ESPACADA":      "valor",
	}
	for k, v := range esperado {
		if env[k] != v {
			t.Errorf("%s = %q, esperava %q", k, env[k], v)
		}
	}
}
