package cli

import (
	"context"
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
	fmt.Fprintln(w, "\nDEVMANAGER_PHP_VARIANT=bulk troca o conjunto de extensões")
	fmt.Fprintln(w, "(bulk tem intl/readline/opcache, mas não tem pdo_sqlite nem pdo_pgsql)")
	return nil
}

// extensoesDe devolve as extensões compiladas num binário PHP.
func extensoesDe(ctx context.Context, bin string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	saida, err := exec.CommandContext(ctx, bin, "-n", "-m").Output()
	if err != nil {
		return nil, err
	}

	presentes := map[string]bool{}
	for _, linha := range strings.Split(string(saida), "\n") {
		linha = strings.TrimSpace(linha)
		// Pula os cabeçalhos de seção como "[PHP Modules]".
		if linha == "" || strings.HasPrefix(linha, "[") {
			continue
		}
		presentes[strings.ToLower(linha)] = true
	}
	return presentes, nil
}
