package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runner"
)

// shellCmd abre um shell com o ambiente do projeto e histórico próprio.
//
//	devm shell
//
// Duas coisas acontecem aqui, e as duas são o ponto.
//
// A primeira: dentro dele, `php`, `composer`, `node`, `npm` e `npx` são os do
// projeto, sem prefixo nenhum. O `devm run` existe para um comando avulso;
// para uma sessão inteira de trabalho, prefixar tudo cansa.
//
// A segunda: o histórico é do projeto. Cada um guarda o seu, então a seta
// para cima traz o que você digitou NAQUELE projeto — e não o comando de
// outro, com um nome de arquivo que não existe aqui.
//
// Não há mágica no shell do usuário: nada é acrescentado ao .bashrc, e sair
// com `exit` devolve o terminal exatamente como estava.
func shellCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	qual := fs.String("shell", "", "shell a abrir (padrão: o que você está usando)")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	p, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}

	sh := shellDoUsuario(*qual)
	dir, err := paths.HistoricoDir(p.Path)
	if err != nil {
		return err
	}
	// 0700: histórico de terminal costuma guardar caminhos, nomes de
	// servidor e, de vez em quando, um segredo colado sem pensar.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("criando %s: %w", dir, err)
	}

	variaveis := historicoDoShell(sh, dir)
	r.Env = append(os.Environ(), variaveis...)
	r.Stdin, r.Stdout, r.Stderr = stdio.In, stdio.Out, stdio.Err

	apresentar(stdio.Out, p, r, sh, dir)

	// O Run devolve o código de saída do shell, então `devm shell` se
	// comporta como um shell: sair com 1 dá 1.
	return r.Run(context.Background(), sh)
}

func apresentar(w io.Writer, p *project.Project, r *runner.Runner, sh, dir string) {
	fmt.Fprintf(w, "shell do projeto %s  (%s)\n", p.Name, filepath.Base(sh))
	fmt.Fprintf(w, "  PHP        %s\n", r.Runtime.Version)
	for _, extra := range r.Extras {
		fmt.Fprintf(w, "  %-10s %s\n", extra.Language, extra.Version)
	}
	fmt.Fprintf(w, "  histórico  %s\n", encurtarHome(dir))
	fmt.Fprintf(w, "\nsaia com `exit` ou Ctrl+D\n\n")
}

// shellDoUsuario escolhe o shell a abrir.
//
// A ordem é o ponto, e ela não é óbvia: o shell PAI vem antes do $SHELL.
//
// $SHELL é o shell de LOGIN, e ele NÃO muda quando a pessoa roda outro por
// cima. Numa máquina cujo login é bash mas onde o terminal abre zsh — uma
// combinação comum —, $SHELL continua dizendo bash o tempo todo. Abrir bash
// ali seria trocar o shell da pessoa sem avisar, e jogar o histórico num
// arquivo que ela nunca vai ler.
//
// O processo pai é o shell em que `devm shell` foi digitado, que é a
// resposta certa. Quando o pai não é um shell — um agente, uma tarefa do
// editor, um script — caímos para o $SHELL.
func shellDoUsuario(escolhido string) string {
	if escolhido != "" {
		if caminho := normalizarShell(escolhido, exec.LookPath); caminho != "" {
			return caminho
		}
		return escolhido
	}
	if caminho := normalizarShell(shellPai(), exec.LookPath); caminho != "" {
		return caminho
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// shellsConhecidos existe para NÃO abrir o processo pai quando ele não é um
// shell. Sem esta lista, um `devm shell` disparado por um agente abriria o
// próprio agente como se fosse um shell.
var shellsConhecidos = map[string]bool{
	"bash": true, "zsh": true, "fish": true, "sh": true,
	"dash": true, "ksh": true, "mksh": true, "tcsh": true, "csh": true,
}

// normalizarShell transforma o que o sistema reporta num caminho executável.
//
// Um shell de LOGIN aparece com um traço na frente — "-zsh" —, convenção
// antiga que os shells usam para saber que são de login. O traço faz parte do
// nome do processo, não do caminho, então sai antes de procurar o executável.
func normalizarShell(reportado string, procurar func(string) (string, error)) string {
	if reportado == "" {
		return ""
	}
	nome := strings.TrimPrefix(filepath.Base(reportado), "-")
	if !shellsConhecidos[nome] {
		return ""
	}
	if filepath.IsAbs(reportado) && filepath.Base(reportado) == nome {
		return reportado
	}
	if caminho, err := procurar(nome); err == nil {
		return caminho
	}
	return ""
}

// shellPai descobre qual processo chamou o devm.
//
// Cada sistema expõe isso de um jeito: no Linux o /proc tem um link para o
// executável; no macOS não há /proc, e a resposta vem do ps.
func shellPai() string {
	ppid := os.Getppid()

	if runtime.GOOS == "darwin" {
		saida, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(ppid)).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(saida))
	}

	alvo, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", ppid))
	if err != nil {
		return ""
	}
	return alvo
}

// historicoDoShell devolve as variáveis que apontam o histórico do projeto.
//
// Cada shell guarda de um jeito, e os formatos não são compatíveis — o zsh
// grava metadados de tempo nas próprias linhas —, por isso cada um tem seu
// arquivo dentro da pasta do projeto.
//
// O fish é o diferente: ele não aceita um caminho, e sim o NOME de uma
// sessão, que ele resolve dentro do diretório de dados dele.
func historicoDoShell(sh, dir string) []string {
	switch strings.TrimPrefix(filepath.Base(sh), "-") {
	case "fish":
		return []string{"fish_history=" + filepath.Base(dir)}

	case "zsh":
		// O SAVEHIST não é enfeite: o padrão do zsh é ZERO, e com ele o
		// histórico não é gravado. Apontar só o HISTFILE criaria a
		// funcionalidade inteira sem gravar uma linha — e sem erro nenhum
		// para denunciar. Verificado num zsh de verdade.
		//
		// Um .zshrc que defina os seus próprios valores vence: ele roda
		// depois, e a escolha da pessoa deve prevalecer sobre a nossa.
		return []string{
			"HISTFILE=" + filepath.Join(dir, "zsh_history"),
			"SAVEHIST=10000",
			"HISTSIZE=10000",
		}

	default:
		// bash, ksh e parentes usam HISTFILE, e gravam por padrão. Um shell
		// sem histórico simplesmente ignora a variável.
		return []string{"HISTFILE=" + filepath.Join(dir, "bash_history")}
	}
}
