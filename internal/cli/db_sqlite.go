package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CaminhoSQLite devolve o arquivo de banco de um projeto SQLite.
//
// O valor de DB_DATABASE tem três formas no Laravel: vazio (o padrão
// database/database.sqlite), um caminho relativo à raiz do projeto, ou um
// caminho absoluto. O ":memory:" não tem arquivo nenhum — e não há o que
// copiar de um banco que morre com o processo.
func caminhoSQLite(dirProjeto, dbDatabase string) (string, bool) {
	valor := strings.TrimSpace(dbDatabase)

	switch {
	case valor == ":memory:":
		return "", false
	case valor == "":
		return filepath.Join(dirProjeto, "database", "database.sqlite"), true
	case filepath.IsAbs(valor):
		return valor, true
	default:
		return filepath.Join(dirProjeto, valor), true
	}
}

// dumpSQLite copia o banco para o destino.
//
// A cópia é feita com VACUUM INTO, pelo PHP do projeto, e não com um simples
// copiar de arquivo. A diferença aparece quando o banco está em modo WAL: as
// escritas recentes vivem no arquivo -wal ao lado, e copiar só o .sqlite
// produziria um backup sem elas — silenciosamente. O VACUUM INTO é a forma
// que o próprio SQLite recomenda para isso, e entrega um arquivo já
// compactado e consistente.
func dumpSQLite(ctx context.Context, stdio IO, origem, destino string) error {
	if _, err := os.Stat(origem); err != nil {
		return fmt.Errorf("o banco %s não existe ainda\n  crie com:  devm artisan migrate", origem)
	}

	// O VACUUM INTO se recusa a escrever por cima de um arquivo existente.
	if err := os.Remove(destino); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := vacuumInto(ctx, stdio, origem, destino); err != nil {
		// Sem PHP resolvido não dá para usar o VACUUM; a cópia crua ainda é
		// melhor que nada, desde que quem a fez saiba do risco.
		fmt.Fprintf(stdio.Out, "aviso: %v\n", err)
		fmt.Fprintln(stdio.Out, "copiando o arquivo direto; pare a aplicação antes se ela estiver escrevendo")
		return copiarArquivo(origem, destino)
	}
	return nil
}

// vacuumInto pede ao PHP do projeto uma cópia consistente do banco.
func vacuumInto(ctx context.Context, stdio IO, origem, destino string) error {
	_, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}
	r.Stdout, r.Stderr = stdio.Out, stdio.Err

	// Os caminhos vão como argumentos do script, não interpolados nele: um
	// diretório com aspas no nome quebraria o PHP, e o mesmo buraco serve
	// para injetar código.
	script := `$o = $argv[1]; $d = $argv[2];` +
		`$pdo = new PDO("sqlite:" . $o, null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);` +
		`$q = $pdo->prepare("VACUUM INTO ?"); $q->execute([$d]);`

	return r.RunPHP(ctx, "-r", script, "--", origem, destino)
}

// restoreSQLite põe o arquivo no lugar do banco do projeto.
func restoreSQLite(stdio IO, origem, destino string) error {
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return err
	}
	return copiarArquivo(origem, destino)
}

// copiarArquivo grava por cima de forma atômica.
//
// O temporário fica no diretório do destino para que o rename funcione: entre
// sistemas de arquivos diferentes ele falha, e um banco meio copiado é pior
// que um banco não copiado.
func copiarArquivo(origem, destino string) error {
	entrada, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer entrada.Close()

	tmp, err := os.CreateTemp(filepath.Dir(destino), ".devm-sqlite-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, entrada); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), destino)
}
