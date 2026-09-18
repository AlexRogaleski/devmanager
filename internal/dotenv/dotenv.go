// Package dotenv lê e edita arquivos .env preservando o que já existe.
//
// O .env é o arquivo mais sensível de um projeto Laravel: tem credenciais,
// comentários explicando cada bloco e frequentemente chaves que só existem na
// máquina daquele dev. Reescrevê-lo a partir de um mapa destruiria tudo isso.
//
// A edição aqui é por LINHA: mexemos apenas nas linhas das chaves que vamos
// alterar, e acrescentamos no fim as que não existiam. Todo o resto do arquivo
// sai idêntico, byte a byte — mesma disciplina que aplicamos ao
// .vscode/settings.json e ao devmanager.yaml.
package dotenv

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// modoDoEnv é a permissão de qualquer arquivo que este pacote grava.
//
// 0600 mantém as credenciais fora do alcance de outros usuários da máquina.
// É uma constante, e não um literal repetido, porque a cópia de segurança
// precisa da MESMA permissão do .env — e não da permissão que o original
// tinha. A primeira versão herdava o modo da fonte, e num projeto onde o
// .env estava com 0644 o resultado era uma cópia legível por todos ao lado
// de um .env que o próprio devm acabara de restringir. Mesmo segredo, modo
// diferente, pelo mesmo caminho de código.
const modoDoEnv = os.FileMode(0o600)

// SufixoCopia nomeia a cópia de segurança do .env original.
//
// O nome é explícito de propósito. Um ".bak" não diz de onde veio nem por
// que existe, e seis meses depois ninguém sabe se pode apagar.
const SufixoCopia = ".antes-do-devmanager"

// CaminhoDaCopia devolve onde fica a cópia de um .env.
func CaminhoDaCopia(caminho string) string { return caminho + SufixoCopia }

// salvarCopia guarda o arquivo original antes da primeira modificação.
//
// Duas regras, e as duas importam:
//
// Só na PRIMEIRA vez. Se a cópia já existe, ela é preservada — mesmo que o
// .env tenha mudado desde então. Sobrescrever a cada gravação transformaria
// a cópia em "o estado antes da última edição", e depois do segundo `devm up`
// o arquivo original estaria perdido para sempre. O valor de uma cópia de
// segurança é ser a ORIGINAL.
//
// Só quando há algo a copiar. Um projeto sem .env não ganha uma cópia vazia.
//
// A gravação é O_EXCL: se dois processos chegarem juntos, um cria e o outro
// recebe EEXIST em vez de os dois escreverem no mesmo arquivo.
func salvarCopia(caminho string) error {
	destino := CaminhoDaCopia(caminho)

	original, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lendo %s: %w", caminho, err)
	}

	f, err := os.OpenFile(destino, os.O_WRONLY|os.O_CREATE|os.O_EXCL, modoDoEnv)
	if err != nil {
		if os.IsExist(err) {
			return nil // já existe: é a original, não toca
		}
		return fmt.Errorf("criando %s: %w", destino, err)
	}
	defer f.Close()

	if _, err := f.Write(original); err != nil {
		return fmt.Errorf("gravando %s: %w", destino, err)
	}
	return f.Close()
}

// Load lê um .env. Arquivo ausente devolve um mapa vazio, não erro.
func Load(caminho string) (map[string]string, error) {
	valores := map[string]string{}

	f, err := os.Open(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return valores, nil
		}
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		chave, valor, ok := analisarLinha(scanner.Text())
		if ok {
			valores[chave] = valor
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}
	return valores, nil
}

// Set aplica os valores no arquivo e devolve as chaves que mudaram.
//
// Chaves cujo valor já é o desejado não entram na lista de mudanças e não
// causam gravação — assim rodar `devm up` duas vezes não toca no arquivo na
// segunda vez, e o git não mostra alteração fantasma.
func Set(caminho string, valores map[string]string) ([]string, error) {
	original, err := os.ReadFile(caminho)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}

	atuais, err := Load(caminho)
	if err != nil {
		return nil, err
	}

	// Descobre o que realmente precisa mudar antes de tocar no arquivo.
	pendentes := map[string]string{}
	for chave, novo := range valores {
		if atual, existe := atuais[chave]; !existe || atual != novo {
			pendentes[chave] = novo
		}
	}
	if len(pendentes) == 0 {
		return nil, nil
	}

	// A cópia é feita AQUI, depois de saber que algo vai mudar de verdade.
	// Posta antes da checagem, um `devm up` que não altera nada criaria um
	// arquivo novo no projeto sem motivo.
	if err := salvarCopia(caminho); err != nil {
		return nil, err
	}

	linhas := strings.Split(string(original), "\n")

	// Uma quebra de linha final produz um último elemento vazio no Split.
	// Removemos para não acumular linhas em branco a cada gravação, e
	// recolocamos a quebra só no fim.
	terminavaComQuebra := len(linhas) > 0 && linhas[len(linhas)-1] == ""
	if terminavaComQuebra {
		linhas = linhas[:len(linhas)-1]
	}

	aplicadas := map[string]bool{}
	for i, linha := range linhas {
		chave, _, ok := analisarLinha(linha)
		if !ok {
			continue // comentário, linha vazia ou algo que não entendemos: preserva
		}

		novo, querMudar := pendentes[chave]
		if !querMudar || aplicadas[chave] {
			continue
		}

		// Preserva o "export " quando o arquivo usa essa forma.
		prefixo := ""
		if semEspaco := strings.TrimLeft(linha, " \t"); strings.HasPrefix(semEspaco, "export ") {
			prefixo = "export "
		}

		linhas[i] = prefixo + chave + "=" + citar(novo)
		aplicadas[chave] = true
	}

	// As que não existiam no arquivo vão para o fim, em ordem estável.
	var novas []string
	for chave := range pendentes {
		if !aplicadas[chave] {
			novas = append(novas, chave)
		}
	}
	sort.Strings(novas)

	if len(novas) > 0 {
		if len(linhas) > 0 && strings.TrimSpace(linhas[len(linhas)-1]) != "" {
			linhas = append(linhas, "")
		}
		for _, chave := range novas {
			linhas = append(linhas, chave+"="+citar(pendentes[chave]))
		}
	}

	conteudo := strings.Join(linhas, "\n") + "\n"
	if err := escreverAtomico(caminho, []byte(conteudo)); err != nil {
		return nil, err
	}

	mudadas := make([]string, 0, len(pendentes))
	for chave := range pendentes {
		mudadas = append(mudadas, chave)
	}
	sort.Strings(mudadas)
	return mudadas, nil
}

// analisarLinha extrai chave e valor de uma linha de .env.
//
// Devolve ok=false para comentários, linhas vazias e qualquer coisa sem "=" —
// que é o sinal para preservar a linha intacta.
func analisarLinha(linha string) (chave, valor string, ok bool) {
	limpa := strings.TrimSpace(linha)
	if limpa == "" || strings.HasPrefix(limpa, "#") {
		return "", "", false
	}

	limpa = strings.TrimPrefix(limpa, "export ")

	// Cut no PRIMEIRO "=": valores em base64 contêm "=" no fim, e o APP_KEY
	// do Laravel é justamente base64.
	chave, valor, achou := strings.Cut(limpa, "=")
	if !achou {
		return "", "", false
	}

	chave = strings.TrimSpace(chave)
	if chave == "" {
		return "", "", false
	}

	valor = strings.TrimSpace(valor)
	if len(valor) >= 2 {
		if (valor[0] == '"' && valor[len(valor)-1] == '"') ||
			(valor[0] == '\'' && valor[len(valor)-1] == '\'') {
			valor = valor[1 : len(valor)-1]
		}
	}
	return chave, valor, true
}

// citar coloca aspas só quando o valor precisa.
//
// O Laravel lê o .env sem shell, mas espaços e "#" sem aspas quebram vários
// parsers de dotenv — inclusive o que o próprio artisan usa em alguns
// contextos. Citar sempre deixaria o arquivo estranho; citar só o necessário
// mantém a aparência do que um humano escreveria.
func citar(valor string) string {
	if valor == "" {
		return ""
	}
	if !strings.ContainsAny(valor, " \t#\"'$") {
		return valor
	}
	return `"` + strings.ReplaceAll(valor, `"`, `\"`) + `"`
}

func escreverAtomico(destino string, dados []byte) error {
	dir := filepath.Dir(destino)

	tmp, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return fmt.Errorf("criando arquivo temporário: %w", err)
	}
	nome := tmp.Name()
	defer os.Remove(nome)

	if _, err := tmp.Write(dados); err != nil {
		tmp.Close()
		return fmt.Errorf("escrevendo %s: %w", destino, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", nome, err)
	}

	if err := os.Chmod(nome, modoDoEnv); err != nil {
		return fmt.Errorf("ajustando permissões: %w", err)
	}
	if err := os.Rename(nome, destino); err != nil {
		return fmt.Errorf("gravando %s: %w", destino, err)
	}
	return nil
}

// Existe informa se o arquivo está presente.
func Existe(caminho string) bool {
	dados, err := os.ReadFile(caminho)
	return err == nil && len(bytes.TrimSpace(dados)) > 0
}
