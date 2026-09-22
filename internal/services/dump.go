package services

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Dump escreve o conteúdo do banco no destino.
//
// A saída é transmitida, não acumulada em memória: um dump de
// desenvolvimento passa fácil de algumas centenas de megabytes, e guardar
// tudo em []byte antes de gravar seria desperdício com risco de estourar.
func (m *Manager) Dump(ctx context.Context, spec Spec, banco string, destino io.Writer) error {
	if !nomeDeBancoValido.MatchString(banco) {
		return fmt.Errorf("nome de banco inválido: %q", banco)
	}

	args, err := comandoDeDump(spec, banco)
	if err != nil {
		return err
	}
	return m.streamNoContainer(ctx, spec.Container(), nil, destino, args...)
}

// Restore lê um dump e o aplica no banco.
//
// Não apaga nada: recriar o banco antes é decisão de quem chama, e há casos
// legítimos de restaurar por cima (um dump só de dados, por exemplo).
func (m *Manager) Restore(ctx context.Context, spec Spec, banco string, origem io.Reader) error {
	if !nomeDeBancoValido.MatchString(banco) {
		return fmt.Errorf("nome de banco inválido: %q", banco)
	}

	args, err := comandoDeRestore(spec, banco)
	if err != nil {
		return err
	}
	return m.streamNoContainer(ctx, spec.Container(), origem, io.Discard, args...)
}

// RecriarBanco apaga e cria de novo, deixando o banco vazio.
//
// Existe por causa do restore: aplicar um dump sobre um banco que já tem as
// tabelas produz uma cascata de "already exists", e no meio dela um COPY que
// falha por chave duplicada passa despercebido. Começar do zero torna o
// resultado previsível.
func (m *Manager) RecriarBanco(ctx context.Context, spec Spec, banco string) error {
	if !nomeDeBancoValido.MatchString(banco) {
		return fmt.Errorf("nome de banco inválido: %q", banco)
	}

	switch spec.Banco {
	case BancoPostgres:
		usuario := spec.Env["POSTGRES_USER"]
		if usuario == "" {
			usuario = "postgres"
		}
		// O dropdb --force derruba as conexões abertas; sem ele, um tinker
		// esquecido noutro terminal impede a operação inteira.
		if err := m.execNoContainer(ctx, spec.Container(),
			"dropdb", "-U", usuario, "--if-exists", "--force", banco); err != nil {
			return fmt.Errorf("apagando o banco %q: %w", banco, err)
		}
		if err := m.execNoContainer(ctx, spec.Container(),
			"createdb", "-U", usuario, banco); err != nil {
			return fmt.Errorf("recriando o banco %q: %w", banco, err)
		}
		return nil

	case BancoMySQL, BancoMariaDB:
		cliente, senha := clienteMySQL(spec)
		sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s`;", banco, banco)
		if usuario := usuarioMySQL(spec); usuario != "" {
			sql += fmt.Sprintf(" GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'; FLUSH PRIVILEGES;", banco, usuario)
		}
		if err := m.execNoContainer(ctx, spec.Container(), cliente, "-uroot", "-p"+senha, "-e", sql); err != nil {
			return fmt.Errorf("recriando o banco %q: %w", banco, err)
		}
		return nil
	}
	return fmt.Errorf("%s não é um banco de dados", spec.Nome)
}

// Shell abre o cliente interativo do banco, ligado ao terminal de quem chamou.
//
// O -it é o que faz o psql desenhar o prompt e aceitar Ctrl+C sem matar o
// contêiner; sem ele o cliente abre em modo não interativo e sai na hora.
func (m *Manager) Shell(ctx context.Context, spec Spec, banco string, entrada io.Reader, saida, erros io.Writer) error {
	if !nomeDeBancoValido.MatchString(banco) {
		return fmt.Errorf("nome de banco inválido: %q", banco)
	}

	args, err := comandoDeShell(spec, banco)
	if err != nil {
		return err
	}

	completo := append([]string{"exec", "-it", spec.Container()}, args...)
	cmd := exec.CommandContext(ctx, m.Engine.Bin, completo...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = entrada, saida, erros
	return cmd.Run()
}

func comandoDeShell(spec Spec, banco string) ([]string, error) {
	switch spec.Banco {
	case BancoPostgres:
		usuario := spec.Env["POSTGRES_USER"]
		if usuario == "" {
			usuario = "postgres"
		}
		return []string{"psql", "-U", usuario, "-d", banco}, nil

	case BancoMySQL, BancoMariaDB:
		cliente, senha := clienteMySQL(spec)
		return []string{cliente, "-uroot", "-p" + senha, banco}, nil
	}
	return nil, fmt.Errorf("%s não é um banco de dados", spec.Nome)
}

func comandoDeDump(spec Spec, banco string) ([]string, error) {
	switch spec.Banco {
	case BancoPostgres:
		usuario := spec.Env["POSTGRES_USER"]
		if usuario == "" {
			usuario = "postgres"
		}
		return []string{"pg_dump", "-U", usuario, banco}, nil

	case BancoMySQL, BancoMariaDB:
		_, senha := clienteMySQL(spec)

		// O utilitário do MariaDB tem hífen — "mariadb-dump" —, o do MySQL
		// não. Colar "dump" no nome do cliente produz "mariadbdump", que não
		// existe.
		ferramenta := "mysqldump"
		if spec.Banco == BancoMariaDB {
			ferramenta = "mariadb-dump"
		}

		// --single-transaction evita travar as tabelas durante a cópia.
		return []string{ferramenta, "-uroot", "-p" + senha, "--single-transaction", banco}, nil
	}
	return nil, fmt.Errorf("%s não é um banco de dados", spec.Nome)
}

func comandoDeRestore(spec Spec, banco string) ([]string, error) {
	switch spec.Banco {
	case BancoPostgres:
		usuario := spec.Env["POSTGRES_USER"]
		if usuario == "" {
			usuario = "postgres"
		}
		// ON_ERROR_STOP transforma o primeiro erro em falha do comando. Sem
		// ele, o psql segue até o fim e termina com código 0 mesmo tendo
		// recusado metade do arquivo.
		return []string{"psql", "-U", usuario, "-d", banco, "-q", "-v", "ON_ERROR_STOP=1"}, nil

	case BancoMySQL, BancoMariaDB:
		cliente, senha := clienteMySQL(spec)
		return []string{cliente, "-uroot", "-p" + senha, banco}, nil
	}
	return nil, fmt.Errorf("%s não é um banco de dados", spec.Nome)
}

func clienteMySQL(spec Spec) (cliente, senha string) {
	cliente = "mysql"
	if spec.Banco == BancoMariaDB {
		cliente = "mariadb"
	}

	senha = spec.Env["MYSQL_ROOT_PASSWORD"]
	if senha == "" {
		senha = spec.Env["MARIADB_ROOT_PASSWORD"]
	}
	return cliente, senha
}

func usuarioMySQL(spec Spec) string {
	if u := spec.Env["MYSQL_USER"]; u != "" {
		return u
	}
	return spec.Env["MARIADB_USER"]
}

// streamNoContainer roda um comando no contêiner ligando stdin e stdout.
func (m *Manager) streamNoContainer(ctx context.Context, container string, entrada io.Reader, saida io.Writer, args ...string) error {
	completo := []string{"exec"}
	if entrada != nil {
		completo = append(completo, "--interactive")
	}
	completo = append(completo, container)
	completo = append(completo, args...)

	cmd := exec.CommandContext(ctx, m.Engine.Bin, completo...)
	cmd.Stdin = entrada
	cmd.Stdout = saida

	var erros strings.Builder
	cmd.Stderr = &erros

	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(erros.String()); msg != "" {
			return fmt.Errorf("%s: %s", args[0], msg)
		}
		return fmt.Errorf("%s: %w", args[0], err)
	}
	return nil
}
