package runtimes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// Removedor é a capacidade OPCIONAL de apagar uma versão instalada.
//
// É a simétrica de Installer, e existe pelo mesmo motivo: nem todo Provider
// pode. O SystemProvider enxerga o PHP que o dnf instalou em /usr/bin, mas
// quem instalou foi o gerenciador de pacotes, e apagá-lo por fora seria
// corromper o sistema do usuário. Deixar Remove fora de Provider é o que
// impede, por construção, que `devm php remove` aponte para /usr/bin/php:
// a type assertion simplesmente falha para quem não implementa.
type Removedor interface {
	Provider

	// DirDaVersao devolve a pasta que guarda uma versão.
	DirDaVersao(v semver.Version) string

	// Remove apaga a instalação. Remover o que não existe é erro: o usuário
	// pediu para apagar algo, e o silêncio esconderia um engano de digitação.
	Remove(v semver.Version) error
}

// removerDir apaga a pasta de uma versão, com as duas checagens que separam
// "apagar uma instalação" de "apagar a pasta errada".
//
// A segunda é a que importa: DirDaVersao monta o caminho a partir de uma
// versão que veio do usuário, e um RemoveAll é irreversível. Confirmar que o
// alvo está mesmo DENTRO da raiz do Provider custa três linhas e transforma
// um bug de montagem de caminho em erro, não em perda de dados.
func removerDir(raiz, alvo string) error {
	raizAbs, err := filepath.Abs(raiz)
	if err != nil {
		return fmt.Errorf("resolvendo %s: %w", raiz, err)
	}
	alvoAbs, err := filepath.Abs(alvo)
	if err != nil {
		return fmt.Errorf("resolvendo %s: %w", alvo, err)
	}

	if alvoAbs == raizAbs || !strings.HasPrefix(alvoAbs, raizAbs+string(filepath.Separator)) {
		return fmt.Errorf("recusando apagar %s: fora de %s", alvoAbs, raizAbs)
	}

	if _, err := os.Stat(alvoAbs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s não está instalado", filepath.Base(alvoAbs))
		}
		return err
	}

	return os.RemoveAll(alvoAbs)
}

// TamanhoEmDisco soma os arquivos de uma pasta, em bytes.
//
// Serve para a listagem responder a pergunta que motiva a remoção — "o que
// está ocupando o disco?" — sem obrigar o usuário a sair para o du.
//
// Erros em arquivos individuais são ignorados de propósito: um link quebrado
// no meio da árvore não pode transformar um número aproximado em falha.
func TamanhoEmDisco(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// FormatarTamanho escreve bytes em unidade legível.
func FormatarTamanho(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// Asserções em tempo de compilação: se algum dia alguém mudar a assinatura
// de Remove, o erro aparece aqui, ao compilar o pacote — e não lá na frente,
// quando a type assertion da CLI falhar calada e o comando disser
// "não sei remover esta versão" sem motivo aparente.
//
// O `_` descarta o valor: o que interessa é só o compilador conferir que o
// tipo cabe na interface.
var (
	_ Removedor = (*StaticProvider)(nil)
	_ Removedor = (*NodeOficialProvider)(nil)
)
