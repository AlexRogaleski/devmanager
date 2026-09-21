package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/shell"
)

// comandosGerenciados lista o que o shim pode criar para cada linguagem.
//
// Existe para saber o que REMOVER. Um shim que só acrescenta links mantém
// para sempre o que já colocou: desfixar a versão de Node num projeto
// deixaria o link antigo no lugar, e o projeto continuaria usando a versão
// que a pessoa acabou de remover da configuração — sem nenhum sinal do
// motivo.
//
// O composer não entra aqui de propósito: ele é gerenciado à parte, por
// EnsureComposerShim, e não deve ser apagado por esta limpeza.
var comandosGerenciados = map[string][]string{
	"php":  {"php"},
	"node": {"node", "npm", "npx"},
}

// EnsureShim cria (ou atualiza) o diretório de shim de um projeto e devolve
// o caminho dele.
//
// O shim é um diretório com o "php" do projeto e os comandos do Node. Ele
// existe por dois motivos, e os dois importam:
//
//  1. Subprocessos. O comando que rodamos vai criar outros processos: o
//     composer chama "php", o artisan chama "php" em comandos que rodam em
//     background, scripts npm chamam "php". Colocando o shim na frente do
//     PATH, TODA a árvore de processos usa a versão certa — não só o primeiro.
//
//  2. Editores. O Intelephense e o PHP Debug do VS Code precisam de um caminho
//     fixo para o interpretador, gravado no .vscode/settings.json. Esse
//     caminho tem que continuar válido com o devm parado, o que descarta um
//     diretório temporário.
//
// O Node entra como link; o PHP, como script, porque é por ele que o php.ini
// do projeto é apontado. Ver escreverShimDoPHP.
//
// A função é idempotente: chamar várias vezes com o mesmo runtime não muda
// nada, e chamar com um runtime diferente reaponta o shim.
//
// phpIni são os ajustes do projeto, sobrepostos aos padrões do Dev Manager.
// Nil usa só os padrões.
func EnsureShim(shimDir string, phpIni map[string]string, runtimesDoProjeto ...runtimes.Runtime) (string, error) {
	// MkdirAll não reclama se o diretório já existe — é o mkdir -p.
	// O modo 0o755 dá leitura e execução a todos, escrita só ao dono.
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", fmt.Errorf("criando shim em %s: %w", shimDir, err)
	}

	presentes := make(map[string]bool, len(runtimesDoProjeto))

	for _, rt := range runtimesDoProjeto {
		presentes[rt.Language] = true
		for nome, alvo := range rt.Executaveis() {
			// O php é o único que vira script: é por ele que o php.ini
			// entra, e um link não carrega configuração junto.
			if nome == "php" {
				ini, err := escreverPHPIni(shimDir, phpIni)
				if err != nil {
					return "", err
				}
				if err := escreverShimDoPHP(shimDir, alvo, ini); err != nil {
					return "", err
				}
				continue
			}
			if err := ligar(shimDir, nome, alvo); err != nil {
				return "", err
			}
		}
	}

	// Remove o que sobrou de uma configuração anterior.
	for linguagem, comandos := range comandosGerenciados {
		if presentes[linguagem] {
			continue
		}
		for _, nome := range comandos {
			_ = os.Remove(filepath.Join(shimDir, nome))
		}
	}
	if !presentes["php"] {
		_ = os.Remove(filepath.Join(shimDir, NomeDoPHPIni))
	}

	return shimDir, nil
}

// ligar cria ou reaponta um link do shim.
func ligar(shimDir, nome, destino string) error {
	link := filepath.Join(shimDir, nome)

	// Se o link já aponta para o lugar certo, não há nada a fazer.
	// Readlink lê o ALVO do link sem segui-lo, que é o que queremos comparar.
	if alvo, err := os.Readlink(link); err == nil && alvo == destino {
		return nil
	}

	// Symlink falha se o destino já existe, então removemos antes.
	// os.Remove num caminho inexistente devolve erro, que ignoramos de
	// propósito: "já não existia" é exatamente o estado que queremos.
	_ = os.Remove(link)

	if err := os.Symlink(destino, link); err != nil {
		return fmt.Errorf("apontando %s para %s: %w", link, destino, err)
	}
	return nil
}

// PHPPath devolve o caminho do interpretador dentro de um shim.
// É o valor que vai para o .vscode/settings.json.
func PHPPath(shimDir string) string {
	return filepath.Join(shimDir, "php")
}

// EnsureComposerShim cria um wrapper "composer" dentro do shim.
//
// O wrapper é um script de shell que executa o phar com o PHP do projeto:
//
//	#!/bin/sh
//	exec "<shim>/php" "<composer.phar>" "$@"
//
// Com ele no PATH, qualquer coisa que chame "composer" — um script do
// package.json, um comando do artisan, o próprio dev no terminal — usa o par
// correto de PHP e composer, sem saber que existe um Dev Manager no meio.
//
// Chama o PHP DO SHIM, e não o binário direto: é o shim que aponta o php.ini.
// Pelo binário, um composer executado de fora do devm — uma tarefa do VS
// Code, por exemplo — rodaria com 128M de memória, e o PHPStan que ele chama
// morreria sem explicação.
//
// O exec substitui o processo do shell em vez de criar um filho, então sinais
// e código de saída chegam direto ao composer, sem intermediário.
func EnsureComposerShim(shimDir, phar string) error {
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return fmt.Errorf("criando shim em %s: %w", shimDir, err)
	}

	conteudo := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n",
		shell.Aspas(PHPPath(shimDir)), shell.Aspas(phar))
	destino := filepath.Join(shimDir, "composer")

	// Se já está exatamente assim, não reescreve: evita mexer no mtime a cada
	// comando, o que confundiria ferramentas que observam o diretório.
	if atual, err := os.ReadFile(destino); err == nil && string(atual) == conteudo {
		return nil
	}

	if err := os.WriteFile(destino, []byte(conteudo), 0o755); err != nil {
		return fmt.Errorf("criando wrapper do composer: %w", err)
	}
	return nil
}
