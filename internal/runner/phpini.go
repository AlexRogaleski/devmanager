package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/shell"
)

// padraoPHPIni são os ajustes que o Dev Manager aplica a todo projeto.
//
// O PHP estático não carrega php.ini nenhum — "Loaded Configuration File =>
// (none)" —, então valem os padrões compilados. Três deles quebram o uso
// diário, e a comparação com a imagem do Laravel Sail mostra o tamanho do
// buraco:
//
//	                      Sail    PHP estático sem ini
//	memory_limit          -1      128M
//	upload_max_filesize   100M    2M
//	post_max_size         100M    8M
//
// Os 128M derrubam PHPStan, Pest com cobertura e Rector; os 2M aparecem
// depois, no primeiro teste de upload, como um erro que não parece ter
// relação. Quem vem do Sail não mudou nada no projeto: perdeu configuração
// na migração, sem aviso.
//
// As outras diferenças para o Sail NÃO são copiadas de propósito. Lá o
// display_errors está desligado e as assertions compiladas fora, porque o ini
// da imagem deriva do php.ini-production; aqui o ambiente é de
// desenvolvimento, e os padrões do PHP sem ini — erros visíveis, assertions
// ativas — são melhores para quem está desenvolvendo.
var padraoPHPIni = map[string]string{
	"memory_limit":        "-1",
	"upload_max_filesize": "100M",
	"post_max_size":       "100M",
}

// NomeDoPHPIni é o arquivo gerado dentro do shim.
const NomeDoPHPIni = "php.ini"

// ConteudoPHPIni monta o arquivo, com os extras do projeto por cima.
//
// As chaves saem ordenadas para o conteúdo ser o mesmo a cada chamada: é o
// que permite não reescrever o arquivo quando nada mudou, e o que faz um
// diff entre duas execuções ser vazio.
func ConteudoPHPIni(extras map[string]string) string {
	valores := make(map[string]string, len(padraoPHPIni)+len(extras))
	for k, v := range padraoPHPIni {
		valores[k] = v
	}
	for k, v := range extras {
		valores[k] = v
	}

	chaves := make([]string, 0, len(valores))
	for k := range valores {
		chaves = append(chaves, k)
	}
	sort.Strings(chaves)

	var b strings.Builder
	b.WriteString("; Gerado pelo Dev Manager — este arquivo é reescrito, não edite.\n")
	b.WriteString(";\n")
	b.WriteString("; O PHP estático não carrega php.ini nenhum. Sem isto, valeriam os\n")
	b.WriteString("; padrões compilados: 128M de memória e 2M de upload.\n")
	b.WriteString(";\n")
	b.WriteString("; Para mudar algo neste projeto, use php_ini no devmanager.yaml.\n\n")
	for _, k := range chaves {
		fmt.Fprintf(&b, "%s = %s\n", k, valores[k])
	}
	return b.String()
}

// escreverPHPIni grava o arquivo no shim e devolve o caminho.
func escreverPHPIni(shimDir string, extras map[string]string) (string, error) {
	destino := filepath.Join(shimDir, NomeDoPHPIni)
	conteudo := ConteudoPHPIni(extras)

	if atual, err := os.ReadFile(destino); err == nil && string(atual) == conteudo {
		return destino, nil
	}
	if err := os.WriteFile(destino, []byte(conteudo), 0o644); err != nil {
		return "", fmt.Errorf("gravando %s: %w", destino, err)
	}
	return destino, nil
}

// candidatosDeTerminfo são os lugares onde a base de terminfo costuma morar.
//
// A ordem é por probabilidade, não por preferência: qualquer uma serve, e a
// primeira que existir é usada.
var candidatosDeTerminfo = []string{
	"/usr/share/terminfo", // Fedora, Arch, macOS
	"/lib/terminfo",       // Debian, Ubuntu
	"/etc/terminfo",
	"/usr/lib/terminfo",
}

// baseDeTerminfo devolve a primeira base existente, ou "" se não houver.
//
// Vazio é um resultado legítimo: num sistema sem terminfo não há o que
// apontar, e o shim simplesmente não menciona a variável.
func baseDeTerminfo(candidatos []string) string {
	for _, dir := range candidatos {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// escreverShimDoPHP cria o "php" do shim como script, e não como link.
//
// Um link não consegue levar configuração junto, e é por aí que o PHPRC
// entra. A variável sozinha cobriria o que passa pelo devm — o runner já
// monta o ambiente dos processos —, mas não o editor: o Intelephense e o
// PHPStan do VS Code executam este caminho DIRETO, com o ambiente deles.
// Foi assim que o estouro de memória do PHPStan apareceu.
//
// O ${PHPRC} existente é respeitado: dá para rodar um comando com outro ini
// sem mexer no shim.
//
// O exec substitui o processo do shell em vez de criar um filho, então sinais
// e código de saída chegam direto ao PHP. E como o PHPRC é exportado, todo
// subprocesso que o PHP criar — o PHPStan que o composer chama, por exemplo —
// herda a mesma configuração.
func escreverShimDoPHP(shimDir, phpBin, ini string) error {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Gerado pelo Dev Manager — este arquivo é reescrito, não edite.\n")

	fmt.Fprintf(&b, `if [ -z "${PHPRC:-}" ]; then
	PHPRC=%s
	export PHPRC
fi
`, shell.Aspas(ini))

	// O readline do PHP estático não sabe onde fica a base de terminfo do
	// sistema, e todo `artisan tinker` abre com duas linhas de reclamação:
	// "Cannot read termcap database; using dumb terminal settings". A edição
	// de linha e o histórico continuam funcionando — medido —, então isto é
	// barulho, não defeito. Apontar a base cala o aviso.
	if base := baseDeTerminfo(candidatosDeTerminfo); base != "" {
		fmt.Fprintf(&b, `if [ -z "${TERMINFO:-}" ]; then
	TERMINFO=%s
	export TERMINFO
fi
`, shell.Aspas(base))
	}

	fmt.Fprintf(&b, "exec %s \"$@\"\n", shell.Aspas(phpBin))
	conteudo := b.String()

	destino := filepath.Join(shimDir, "php")
	if atual, err := os.ReadFile(destino); err == nil && string(atual) == conteudo {
		return nil
	}

	// Versões anteriores criavam um link aqui. os.WriteFile num symlink
	// escreveria no ALVO — o binário do PHP —, então ele sai primeiro.
	_ = os.Remove(destino)

	if err := os.WriteFile(destino, []byte(conteudo), 0o755); err != nil {
		return fmt.Errorf("criando o shim do php: %w", err)
	}
	return nil
}
