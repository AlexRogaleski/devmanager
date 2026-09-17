package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// projetoFalso monta um projeto em disco a partir de um mapa
// "nome do arquivo" -> "conteúdo".
//
// t.TempDir() cria um diretório temporário e o apaga automaticamente quando o
// teste acaba — inclusive se ele falhar. Testar contra o disco de verdade, em
// vez de abstrair o sistema de arquivos, mantém o código de produção simples
// e ainda testa o que realmente importa: nossa leitura de arquivos reais.
func projetoFalso(t *testing.T, arquivos map[string]string) string {
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
	return dir
}

func TestDetect(t *testing.T) {
	casos := []struct {
		nome      string
		arquivos  map[string]string
		kind      Kind
		php       string
		require   string
		locked    string
		pronto    bool
		qtdFaltas int
	}{
		{
			nome: "laravel instalado e configurado",
			arquivos: map[string]string{
				"composer.json":       `{"require":{"php":"^8.3","laravel/framework":"^13.17"}}`,
				"composer.lock":       `{"packages":[{"name":"laravel/framework","version":"v13.19.0"}]}`,
				"artisan":             "#!/usr/bin/env php",
				".env":                "APP_KEY=base64:x",
				"vendor/autoload.php": "<?php",
			},
			kind:    KindLaravel,
			php:     "^8.3",
			require: "^13.17",
			locked:  "v13.19.0",
			pronto:  true,
		},
		{
			nome: "laravel recém-clonado",
			arquivos: map[string]string{
				"composer.json": `{"require":{"php":"^8.4","laravel/framework":"^13.0"}}`,
				"artisan":       "#!/usr/bin/env php",
			},
			kind:      KindLaravel,
			php:       "^8.4",
			require:   "^13.0",
			pronto:    false,
			qtdFaltas: 3, // composer install, .env, key:generate
		},
		{
			nome: "php puro, sem laravel",
			arquivos: map[string]string{
				"composer.json": `{"require":{"php":"^8.1","symfony/console":"^7.0"}}`,
			},
			kind:      KindPHP,
			php:       "^8.1",
			qtdFaltas: 2,
		},
		{
			nome: "artisan presente mas framework não declarado",
			arquivos: map[string]string{
				"composer.json": `{"require":{"php":"^8.2"}}`,
				"artisan":       "#!/usr/bin/env php",
			},
			kind:      KindLaravel,
			php:       "^8.2",
			qtdFaltas: 3,
		},
		{
			nome:      "pasta comum",
			arquivos:  map[string]string{"README.md": "oi"},
			kind:      KindUnknown,
			qtdFaltas: 2,
		},
	}

	for _, c := range casos {
		// t.Run cria um subteste com nome próprio: a saída do -v fica
		// legível e dá para rodar um caso isolado com -run.
		t.Run(c.nome, func(t *testing.T) {
			dir := projetoFalso(t, c.arquivos)

			p, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect devolveu erro inesperado: %v", err)
			}

			if p.Kind != c.kind {
				t.Errorf("Kind = %q, esperava %q", p.Kind, c.kind)
			}
			if p.PHPConstraint != c.php {
				t.Errorf("PHPConstraint = %q, esperava %q", p.PHPConstraint, c.php)
			}
			if p.LaravelRequire != c.require {
				t.Errorf("LaravelRequire = %q, esperava %q", p.LaravelRequire, c.require)
			}
			if p.LaravelLocked != c.locked {
				t.Errorf("LaravelLocked = %q, esperava %q", p.LaravelLocked, c.locked)
			}
			if p.Ready() != c.pronto {
				t.Errorf("Ready() = %v, esperava %v", p.Ready(), c.pronto)
			}
			if got := len(p.Missing()); got != c.qtdFaltas {
				t.Errorf("Missing() tem %d itens (%v), esperava %d", got, p.Missing(), c.qtdFaltas)
			}
			if p.Path != dir {
				t.Errorf("Path = %q, esperava o caminho absoluto %q", p.Path, dir)
			}
			if p.Name != filepath.Base(dir) {
				t.Errorf("Name = %q, esperava o nome da pasta %q", p.Name, filepath.Base(dir))
			}
		})
	}
}

// Detect devolve erro só quando a INSPEÇÃO falha — não quando o projeto
// simplesmente não é reconhecido.
func TestDetectErros(t *testing.T) {
	t.Run("caminho inexistente", func(t *testing.T) {
		_, err := Detect("/caminho/que/nao/existe/mesmo")
		if err == nil {
			t.Fatal("esperava erro, veio nil")
		}
		// errors.Is atravessa os erros embrulhados com %w e encontra o
		// os.ErrNotExist original lá no fundo. É isso que o %w compra:
		// mensagem com contexto sem perder o erro de origem.
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("erro = %v, esperava embrulhar os.ErrNotExist", err)
		}
	})

	t.Run("caminho é arquivo, não pasta", func(t *testing.T) {
		dir := projetoFalso(t, map[string]string{"arquivo.txt": "oi"})
		if _, err := Detect(filepath.Join(dir, "arquivo.txt")); err == nil {
			t.Fatal("esperava erro ao apontar para um arquivo")
		}
	})

	t.Run("composer.json malformado", func(t *testing.T) {
		dir := projetoFalso(t, map[string]string{"composer.json": `{ isto não é json`})
		if _, err := Detect(dir); err == nil {
			t.Fatal("esperava erro para composer.json inválido")
		}
	})

	t.Run("composer.lock malformado", func(t *testing.T) {
		dir := projetoFalso(t, map[string]string{
			"composer.json": `{"require":{"php":"^8.3"}}`,
			"composer.lock": `{{{`,
		})
		if _, err := Detect(dir); err == nil {
			t.Fatal("esperava erro para composer.lock inválido")
		}
	})
}
