package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/dotenv"
	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// dbCmd despacha os subcomandos de `devm db`.
//
//	devm db dump [arquivo]      copia o banco do projeto para um arquivo
//	devm db restore <arquivo>   recria o banco e aplica o arquivo
//
// Existe porque a alternativa é decorar a linha do engine: nome do contêiner,
// cliente do dialeto, usuário, senha e redirecionamento. Tudo isso o devm já
// sabe — o projeto declara o serviço, e o .env diz o banco.
func dbCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(stdio.Out, "uso: devm db <dump|restore|shell> [argumentos]\n")
		return nil
	}

	switch args[0] {
	case "dump":
		return dbDumpCmd(stdio, args[1:])
	case "restore":
		return dbRestoreCmd(stdio, args[1:])
	case "shell":
		return dbShellCmd(stdio, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: db %q", args[0])
	}
}

func dbDumpCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("db dump", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	ctx := context.Background()
	alvo, err := bancoDoProjeto(ctx, stdio.Out)
	if err != nil {
		return err
	}

	extensao := ".sql"
	if alvo.ehSQLite() {
		extensao = ".sqlite"
	}

	destino := fmt.Sprintf("%s-%s%s", alvo.projeto, time.Now().Format("2006-01-02-1504"), extensao)
	if len(posicionais) > 0 {
		destino = posicionais[0]
	}

	if alvo.ehSQLite() {
		fmt.Fprintf(stdio.Out, "copiando %s...\n", alvo.arquivo)
		if err := dumpSQLite(ctx, stdio, alvo.arquivo, destino); err != nil {
			return err
		}
		return relatarArquivo(stdio.Out, destino)
	}

	// O arquivo é criado antes do comando rodar, mas só é renomeado no fim:
	// um dump interrompido no meio não pode passar por completo.
	tmp, err := os.CreateTemp(filepath.Dir(destino), ".devm-dump-*")
	if err != nil {
		return fmt.Errorf("criando o arquivo: %w", err)
	}
	defer os.Remove(tmp.Name())

	fmt.Fprintf(stdio.Out, "copiando %s...\n", alvo.descricao())

	if err := alvo.manager.Dump(ctx, alvo.spec, alvo.banco, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), destino); err != nil {
		return fmt.Errorf("gravando %s: %w", destino, err)
	}

	return relatarArquivo(stdio.Out, destino)
}

// relatarArquivo imprime o que foi gravado e o tamanho.
func relatarArquivo(w io.Writer, caminho string) error {
	info, err := os.Stat(caminho)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s  (%s)\n", caminho, tamanhoLegivel(info.Size()))
	return nil
}

func dbRestoreCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("db restore", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	semPerguntar := fs.Bool("force", false, "não pede confirmação")
	manter := fs.Bool("keep", false, "aplica por cima, sem recriar o banco")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm db restore <arquivo> [--keep] [--force]")
	}

	arquivo, err := os.Open(posicionais[0])
	if err != nil {
		return err
	}
	defer arquivo.Close()

	ctx := context.Background()
	alvo, err := bancoDoProjeto(ctx, stdio.Out)
	if err != nil {
		return err
	}

	if alvo.ehSQLite() {
		if *manter {
			return fmt.Errorf("--keep não vale para SQLite: restaurar é substituir o arquivo")
		}
		if !*semPerguntar {
			fmt.Fprintf(stdio.Out, "isto SUBSTITUI o arquivo %s por %s.\n", alvo.arquivo, posicionais[0])
			if !confirmado(stdio) {
				fmt.Fprintln(stdio.Out, "cancelado")
				return nil
			}
		}
		if err := restoreSQLite(stdio, posicionais[0], alvo.arquivo); err != nil {
			return err
		}
		fmt.Fprintln(stdio.Out, "pronto")
		return nil
	}

	if !*manter {
		// Apagar o banco é irreversível, e o comando é curto de digitar:
		// a confirmação é a única coisa entre um engano e o dia perdido.
		if !*semPerguntar {
			fmt.Fprintf(stdio.Out, "isto APAGA o banco %s e aplica %s no lugar.\n",
				alvo.descricao(), posicionais[0])
			if !confirmado(stdio) {
				fmt.Fprintln(stdio.Out, "cancelado")
				return nil
			}
		}
		if err := alvo.manager.RecriarBanco(ctx, alvo.spec, alvo.banco); err != nil {
			return err
		}
	}

	fmt.Fprintf(stdio.Out, "aplicando %s em %s...\n", posicionais[0], alvo.banco)
	if err := alvo.manager.Restore(ctx, alvo.spec, alvo.banco, arquivo); err != nil {
		return err
	}

	fmt.Fprintln(stdio.Out, "pronto")
	return nil
}

// confirmado pergunta e espera um "s" explícito.
func confirmado(stdio IO) bool {
	fmt.Fprint(stdio.Out, "continuar? [s/N] ")

	if stdio.In == nil {
		return false
	}
	linha, err := bufio.NewReader(stdio.In).ReadString('\n')
	if err != nil && linha == "" {
		return false
	}

	resposta := strings.ToLower(strings.TrimSpace(linha))
	return resposta == "s" || resposta == "sim"
}

// dbShellCmd abre o cliente do banco do projeto.
//
//	devm db shell
//
// A alternativa é montar à mão o `docker exec -it`, com nome de contêiner,
// cliente do dialeto, usuário e senha — tudo que o projeto já declara.
func dbShellCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("db shell", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	alvo, err := bancoDoProjeto(ctx, stdio.Out)
	if err != nil {
		return err
	}

	if alvo.ehSQLite() {
		return shellSQLite(stdio, alvo.arquivo)
	}

	fmt.Fprintf(stdio.Out, "conectando em %s  (Ctrl+D para sair)\n", alvo.descricao())
	return alvo.manager.Shell(ctx, alvo.spec, alvo.banco, os.Stdin, os.Stdout, os.Stderr)
}

// shellSQLite abre o sqlite3, quando ele existe na máquina.
//
// Diferente dos outros dialetos, aqui não há contêiner com o cliente dentro:
// o banco é um arquivo local, e quem abre é um programa do sistema.
func shellSQLite(stdio IO, arquivo string) error {
	caminho, err := exec.LookPath("sqlite3")
	if err != nil {
		return fmt.Errorf("o sqlite3 não está instalado nesta máquina\n"+
			"  o banco do projeto é %s\n"+
			"  instale o sqlite3, ou use `devm artisan tinker` para consultar pelo Eloquent", arquivo)
	}

	fmt.Fprintf(stdio.Out, "conectando em %s  (.quit para sair)\n", arquivo)

	cmd := exec.Command(caminho, arquivo)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// alvoDeBanco reúne o que é preciso para falar com o banco do projeto.
//
// São dois mundos: um banco em contêiner, que se acessa pelo cliente do
// dialeto, e um arquivo SQLite, que se copia. O campo preenchido diz qual.
type alvoDeBanco struct {
	projeto string

	banco   string // nome do banco no serviço
	spec    services.Spec
	manager *services.Manager

	arquivo string // caminho do .sqlite, quando o projeto usa SQLite
}

func (a alvoDeBanco) ehSQLite() bool { return a.arquivo != "" }

// descricao é como o alvo aparece nas mensagens.
func (a alvoDeBanco) descricao() string {
	if a.ehSQLite() {
		return a.arquivo
	}
	return fmt.Sprintf("%s de %s:%s", a.banco, a.spec.Nome, a.spec.Versao)
}

// bancoDoProjeto descobre qual serviço e qual banco o projeto atual usa.
//
// O nome sai do .env, não do nome do projeto: quem renomeou o banco à mão
// espera que o dump seja do banco que a aplicação abre, não do que o devm
// teria criado.
func bancoDoProjeto(ctx context.Context, w io.Writer) (alvoDeBanco, error) {
	p, err := project.Detect(".")
	if err != nil {
		return alvoDeBanco{}, err
	}

	specs, err := prepare.SpecsDoProjeto(p)
	if err != nil {
		return alvoDeBanco{}, err
	}

	env, _ := dotenv.Load(filepath.Join(p.Path, ".env"))

	// O .env manda: é ele que a aplicação lê. Um projeto pode ter declarado
	// um serviço e ainda assim estar rodando em SQLite — e o dump tem de ser
	// do banco que a aplicação abre, não do que o devmanager.yaml previa.
	if strings.EqualFold(strings.TrimSpace(env["DB_CONNECTION"]), "sqlite") {
		arquivo, temArquivo := caminhoSQLite(p.Path, env["DB_DATABASE"])
		if !temArquivo {
			return alvoDeBanco{}, fmt.Errorf("este projeto usa SQLite em memória: não há banco a copiar")
		}
		return alvoDeBanco{projeto: p.Name, arquivo: arquivo}, nil
	}

	var escolhida services.Spec
	for _, spec := range specs {
		if spec.Banco != services.BancoNenhum {
			escolhida = spec
			break
		}
	}
	if escolhida.Nome == "" {
		return alvoDeBanco{}, fmt.Errorf(
			"este projeto não declara um banco de dados em %s\n"+
				"  declare um com:  devm service add postgres:18\n"+
				"  ou use SQLite:   DB_CONNECTION=sqlite no .env", "devmanager.yaml")
	}

	banco := services.NomeDeBanco(p.Name)
	if declarado := strings.TrimSpace(env["DB_DATABASE"]); declarado != "" {
		banco = declarado
	}

	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return alvoDeBanco{}, err
	}

	if estado, err := m.Estado(ctx, escolhida); err == nil && estado != services.EstadoRodando {
		return alvoDeBanco{}, fmt.Errorf("%s:%s não está rodando\n  suba com:  devm up",
			escolhida.Nome, escolhida.Versao)
	}

	return alvoDeBanco{projeto: p.Name, banco: banco, spec: escolhida, manager: m}, nil
}

// tamanhoLegivel escreve bytes em unidade humana.
func tamanhoLegivel(n int64) string {
	const unidade = 1024
	if n < unidade {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unidade), 0
	for m := n / unidade; m >= unidade; m /= unidade {
		div *= unidade
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
