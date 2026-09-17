package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// nomeDeBancoValido restringe o que aceitamos como nome de banco.
//
// A restrição é de segurança, não de estilo: o nome vai dentro de uma
// instrução SQL executada no contêiner. Aceitar só letras minúsculas,
// dígitos e sublinhado torna injeção impossível por construção, em vez de
// depender de escapar corretamente — que é onde essas coisas dão errado.
var nomeDeBancoValido = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// NomeDeBanco converte o nome de um projeto num nome de banco válido.
//
//	appmake-erp  ->  appmake_erp
//	Portal.2024  ->  portal_2024
//
// Hífen e ponto viram sublinhado porque, em MySQL e PostgreSQL, um nome com
// hífen exige aspas em toda consulta — atrito garantido para quem for abrir
// um cliente de banco depois.
func NomeDeBanco(nomeProjeto string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(nomeProjeto) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '.', r == '_', r == ' ':
			b.WriteByte('_')
		}
	}

	nome := strings.Trim(b.String(), "_")
	if nome == "" {
		return "app"
	}
	// Identificador SQL não pode começar com dígito.
	if nome[0] >= '0' && nome[0] <= '9' {
		nome = "db_" + nome
	}
	if len(nome) > 63 {
		nome = nome[:63]
	}
	return nome
}

// AguardarPronto espera o serviço aceitar conexões.
//
// Sem essa espera, tudo que vem depois de subir um contêiner novo falha de
// forma intermitente — o clássico "funciona na segunda vez que eu rodo".
func (m *Manager) AguardarPronto(ctx context.Context, spec Spec, prazo time.Duration) error {
	if len(spec.Prontidao) == 0 {
		return nil // serviço sem sonda: assumimos pronto
	}

	ctx, cancelar := context.WithTimeout(ctx, prazo)
	defer cancelar()

	// Intervalo curto no começo: na maioria das vezes o serviço já está de pé
	// e queremos sair na primeira tentativa.
	intervalo := 200 * time.Millisecond

	for tentativa := 1; ; tentativa++ {
		if err := m.execNoContainer(ctx, spec.Container(), spec.Prontidao...); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s não ficou pronto em %s (%d tentativas)", spec.Nome, prazo, tentativa)
		case <-time.After(intervalo):
		}

		// Aumenta o intervalo até um teto: um PostgreSQL inicializando o
		// cluster na primeira vez pode levar bem mais que os outros.
		if intervalo < 2*time.Second {
			intervalo *= 2
		}
	}
}

// CriarBanco garante que um banco existe dentro do serviço.
//
// A operação é idempotente: rodar de novo num banco existente não faz nada e
// não é erro.
func (m *Manager) CriarBanco(ctx context.Context, spec Spec, nome string) error {
	if spec.Banco == BancoNenhum {
		return nil
	}
	if !nomeDeBancoValido.MatchString(nome) {
		return fmt.Errorf("nome de banco inválido: %q", nome)
	}

	switch spec.Banco {
	case BancoPostgres:
		return m.criarBancoPostgres(ctx, spec, nome)
	case BancoMySQL:
		return m.criarBancoMySQL(ctx, spec, nome, "mysql")
	case BancoMariaDB:
		return m.criarBancoMySQL(ctx, spec, nome, "mariadb")
	}
	return nil
}

func (m *Manager) criarBancoPostgres(ctx context.Context, spec Spec, nome string) error {
	usuario := spec.Env["POSTGRES_USER"]
	if usuario == "" {
		usuario = "postgres"
	}

	// O PostgreSQL não tem CREATE DATABASE IF NOT EXISTS, então consultamos
	// antes. É por isso que este dialeto precisa de dois passos e o MySQL não.
	saida, err := m.execCapturando(ctx, spec.Container(),
		"psql", "-U", usuario, "-d", "postgres", "-tAc",
		fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", nome))
	if err != nil {
		return fmt.Errorf("consultando bancos em %s: %w", spec.Nome, err)
	}
	if strings.TrimSpace(saida) == "1" {
		return nil
	}

	if err := m.execNoContainer(ctx, spec.Container(),
		"createdb", "-U", usuario, nome); err != nil {
		return fmt.Errorf("criando o banco %q em %s: %w", nome, spec.Nome, err)
	}
	return nil
}

func (m *Manager) criarBancoMySQL(ctx context.Context, spec Spec, nome, cliente string) error {
	senhaRoot := spec.Env["MYSQL_ROOT_PASSWORD"]
	if senhaRoot == "" {
		senhaRoot = spec.Env["MARIADB_ROOT_PASSWORD"]
	}

	usuario := spec.Env["MYSQL_USER"]
	if usuario == "" {
		usuario = spec.Env["MARIADB_USER"]
	}

	// A imagem oficial concede ao usuário da aplicação acesso APENAS ao banco
	// declarado em MYSQL_DATABASE. Para os bancos que criamos depois, o GRANT
	// é obrigatório — sem ele o Laravel conecta e recebe "access denied".
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", nome)
	if usuario != "" {
		sql += fmt.Sprintf(" GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'; FLUSH PRIVILEGES;", nome, usuario)
	}

	if err := m.execNoContainer(ctx, spec.Container(),
		cliente, "-uroot", "-p"+senhaRoot, "-e", sql); err != nil {
		return fmt.Errorf("criando o banco %q em %s: %w", nome, spec.Nome, err)
	}
	return nil
}

func (m *Manager) execNoContainer(ctx context.Context, container string, args ...string) error {
	_, err := m.execCapturando(ctx, container, args...)
	return err
}

func (m *Manager) execCapturando(ctx context.Context, container string, args ...string) (string, error) {
	completo := append([]string{"exec", container}, args...)
	return m.capturar(ctx, completo...)
}
