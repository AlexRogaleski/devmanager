package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/runtimes"
)

// extensoesEsperadas são as que um projeto Laravel normalmente precisa.
//
// A lista está aqui, e não no pacote runtimes, de propósito: é conhecimento
// sobre Laravel, não sobre runtimes. O pacote que baixa PHP não deveria ter
// opinião sobre frameworks.
var extensoesEsperadas = []struct {
	nome   string
	porque string
}{
	{"pdo_sqlite", "banco padrão de um Laravel novo"},
	{"pdo_mysql", "MySQL/MariaDB"},
	{"pdo_pgsql", "PostgreSQL"},
	{"mbstring", "strings multibyte — exigida pelo framework"},
	{"openssl", "criptografia e HTTPS"},
	{"tokenizer", "exigida pelo framework"},
	{"xml", "exigida pelo framework"},
	{"curl", "cliente HTTP"},
	{"zip", "composer e pacotes"},
	{"fileinfo", "upload de arquivos"},
	{"bcmath", "aritmética de precisão"},
	{"gd", "manipulação de imagens"},
	{"intl", "formatação e localização"},
	{"readline", "php artisan tinker interativo"},
}

// mostrarExtensoes roda `php -m` e aponta o que falta para Laravel.
//
// Isso existe porque nenhum build estático disponível é completo: o common
// tem os drivers de banco mas não tem intl nem readline; o bulk é o inverso.
// Esconder essa diferença faria o dev descobrir o problema só quando um
// comando quebrasse, longe da causa.
func mostrarExtensoes(ctx context.Context, w io.Writer, rt runtimes.Runtime) error {
	presentes, err := extensoesDe(ctx, rt.Bin)
	if err != nil {
		// Não conseguir listar extensões não invalida a instalação —
		// o PHP já foi verificado antes de ser movido para o lugar final.
		fmt.Fprintf(w, "aviso: não consegui listar as extensões: %v\n", err)
		return nil
	}

	var faltando []string
	for _, e := range extensoesEsperadas {
		if !presentes[strings.ToLower(e.nome)] {
			faltando = append(faltando, fmt.Sprintf("  %-12s %s", e.nome, e.porque))
		}
	}

	fmt.Fprintf(w, "%d extensões disponíveis\n", len(presentes))
	if len(faltando) == 0 {
		return nil
	}

	fmt.Fprintln(w, "\nnão incluídas neste build:")
	for _, l := range faltando {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w, "\nDEVMANAGER_PHP_VARIANT troca o conjunto de extensões:")
	fmt.Fprintln(w, "  bulk      o padrão: drivers de banco, intl, readline, opcache")
	fmt.Fprintln(w, "  gnu-bulk  o mesmo conjunto, ligado à glibc (114 MB)")
	fmt.Fprintln(w, "  common    só o essencial, sem intl nem readline (12 MB)")
	return nil
}

// extensoesDe devolve o que um binário PHP realmente oferece.
//
// Perguntamos ao próprio PHP em vez de parsear `php -m`, e a diferença não é
// estilo: o `php -m` NÃO lista os drivers compilados dentro da extensão PDO.
// Lendo aquela saída, um build com pdo_pgsql e pdo_sqlite embutidos parece
// não tê-los — conclusão falsa que já levou à escolha errada de variante
// padrão neste projeto.
//
// PDO::getAvailableDrivers() é a fonte correta para drivers de banco;
// get_loaded_extensions() para o resto.
func extensoesDe(ctx context.Context, bin string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	const script = `$e = array_map("strtolower", get_loaded_extensions());
foreach (class_exists("PDO") ? PDO::getAvailableDrivers() : [] as $d) { $e[] = "pdo_" . $d; }
echo json_encode(array_values(array_unique($e)));`

	saida, err := exec.CommandContext(ctx, bin, "-n", "-r", script).Output()
	if err != nil {
		return nil, err
	}

	var lista []string
	if err := json.Unmarshal(saida, &lista); err != nil {
		return nil, fmt.Errorf("lendo a lista de extensões: %w", err)
	}

	presentes := make(map[string]bool, len(lista))
	for _, e := range lista {
		presentes[strings.ToLower(e)] = true
	}
	return presentes, nil
}
