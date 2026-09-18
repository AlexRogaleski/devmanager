package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/daemon"
	"github.com/AlexRogaleski/devmanager/internal/paths"
)

// daemonCmd despacha os subcomandos de `devm daemon`.
//
// O daemon é um subcomando do próprio devm, e não um binário devmanagerd
// separado. O motivo é evitar divergência de versão: dois binários instalados
// em momentos diferentes falariam contratos de API diferentes, e o sintoma
// seria um erro obscuro em vez de "atualize o daemon". Com um binário só,
// isso é impossível por construção.
func daemonCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		return daemonStatusCmd(stdio.Out, nil)
	}

	switch args[0] {
	case "run":
		return daemonRunCmd(stdio, args[1:])
	case "start":
		return daemonStartCmd(stdio.Out, args[1:])
	case "stop":
		return daemonStopCmd(stdio.Out, args[1:])
	case "status":
		return daemonStatusCmd(stdio.Out, args[1:])
	case "logs":
		return daemonLogsCmd(stdio.Out, args[1:])
	case "install":
		return daemonInstallCmd(stdio.Out, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: daemon %q", args[0])
	}
}

// daemonRunCmd roda o daemon em primeiro plano.
//
// É o alvo de um unit do systemd: `ExecStart=%h/.local/bin/devm daemon run`.
// Ficar em primeiro plano é o que o systemd espera — um processo que se
// bifurca sozinho confunde o supervisor dele.
func daemonRunCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("daemon run", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	socket := fs.String("socket", "", "caminho do socket (padrão: $XDG_RUNTIME_DIR/devmanager.sock)")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	caminho := *socket
	if caminho == "" {
		var err error
		if caminho, err = daemon.CaminhoDoSocket(); err != nil {
			return err
		}
	}

	s := daemon.NovoServidor(caminho, Version, stdio.Out)
	if err := s.Escutar(); err != nil {
		return err
	}

	// signal.NotifyContext devolve um contexto que cancela no sinal. É a
	// forma curta de "encerre ordenadamente no Ctrl+C ou no systemctl stop",
	// e o Servir já sabe derrubar os ambientes quando o contexto cancela.
	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	return s.Servir(ctx)
}

// daemonStartCmd inicia o daemon em segundo plano.
func daemonStartCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if saude, err := c.Saude(ctx); err == nil {
		fmt.Fprintf(w, "o daemon já está rodando (pid %d, desde %s)\n",
			saude.PID, saude.DesdeQue.Format(time.RFC3339))
		return nil
	}

	if err := iniciarDaemon(w); err != nil {
		return err
	}

	// Espera o socket responder antes de dizer que subiu: reportar sucesso
	// sem confirmar faria o comando seguinte falhar com "daemon não está
	// rodando", num intervalo confuso de milissegundos.
	prazo := time.After(10 * time.Second)
	for {
		if saude, err := c.Saude(ctx); err == nil {
			fmt.Fprintf(w, "daemon iniciado (pid %d)\n", saude.PID)
			fmt.Fprintf(w, "logs em %s\n", caminhoDoLog())
			return nil
		}

		select {
		case <-prazo:
			return fmt.Errorf("o daemon não respondeu em 10s — veja %s", caminhoDoLog())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func daemonStopCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}
	ctx := context.Background()

	saude, err := c.Saude(ctx)
	if err != nil {
		fmt.Fprintln(w, "o daemon não está rodando")
		return nil
	}

	// Parar o daemon derruba TODOS os ambientes junto, e isso não é óbvio
	// para quem só quer aplicar uma configuração nova. Listar o que vai cair
	// evita a surpresa de voltar ao navegador e encontrar tudo fora do ar.
	if ambientes, err := c.Listar(ctx); err == nil && len(ambientes) > 0 {
		fmt.Fprintf(w, "isto vai derrubar %d ambiente(s):\n", len(ambientes))
		for _, a := range ambientes {
			fmt.Fprintf(w, "  %s\n", a.Projeto)
		}
		fmt.Fprintln(w, "\npara subir de novo depois:")
		for _, a := range ambientes {
			fmt.Fprintf(w, "  devm start -d %s\n", a.Projeto)
		}
		fmt.Fprintln(w)
	}

	// SIGTERM ao PID, em vez de uma rota /shutdown: o encerramento já está
	// implementado no tratamento de sinal, que é o mesmo caminho que o
	// systemd usa. Uma rota seria um segundo caminho para a mesma coisa.
	if err := syscall.Kill(saude.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("enviando SIGTERM ao pid %d: %w", saude.PID, err)
	}

	fmt.Fprintf(w, "encerrando o daemon (pid %d)...\n", saude.PID)

	prazo := time.After(30 * time.Second)
	for {
		if !c.Rodando(ctx) {
			fmt.Fprintln(w, "daemon encerrado")
			return nil
		}
		select {
		case <-prazo:
			return fmt.Errorf("o daemon não encerrou em 30s (pid %d)", saude.PID)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func daemonStatusCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	saude, err := c.Saude(context.Background())
	if err != nil {
		fmt.Fprintf(w, "daemon   parado\n")
		fmt.Fprintf(w, "socket   %s\n\n", c.Socket)
		fmt.Fprintln(w, "inicie com `devm daemon start`")
		return nil
	}

	fmt.Fprintf(w, "daemon   rodando\n")
	fmt.Fprintf(w, "pid      %d\n", saude.PID)
	fmt.Fprintf(w, "versão   %s (api %s)\n", saude.Versao, saude.APIVersao)
	fmt.Fprintf(w, "desde    %s\n", saude.DesdeQue.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "socket   %s\n", c.Socket)
	return nil
}

// daemonLogsCmd mostra o log do próprio daemon, não dos projetos.
func daemonLogsCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("daemon logs", flag.ContinueOnError)
	fs.SetOutput(w)
	linhas := fs.Int("n", 50, "quantas linhas finais mostrar")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	dados, err := os.ReadFile(caminhoDoLog())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(w, "nenhum log ainda (%s)\n", caminhoDoLog())
			return nil
		}
		return fmt.Errorf("lendo %s: %w", caminhoDoLog(), err)
	}

	for _, linha := range ultimasLinhas(string(dados), *linhas) {
		fmt.Fprintln(w, linha)
	}
	return nil
}

func caminhoDoLog() string {
	dir, err := paths.DataDir()
	if err != nil {
		return "devmanager-daemon.log"
	}
	return filepath.Join(dir, "daemon.log")
}

func ultimasLinhas(texto string, n int) []string {
	var linhas []string
	inicio := 0
	for i := 0; i < len(texto); i++ {
		if texto[i] == '\n' {
			linhas = append(linhas, texto[inicio:i])
			inicio = i + 1
		}
	}
	if inicio < len(texto) {
		linhas = append(linhas, texto[inicio:])
	}

	if n > 0 && len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return linhas
}

// garantirDaemon devolve um cliente com daemon garantidamente de pé.
//
// Inicia o daemon sozinho quando necessário, de forma visível. Ferramenta de
// desenvolvimento local deve funcionar no primeiro comando, sem exigir que a
// pessoa saiba que existe um daemon — mas avisando, para que ela descubra
// como ele se chama quando precisar depurar.
func garantirDaemon(w io.Writer) (*client.Cliente, error) {
	c, err := client.Padrao()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	if c.Rodando(ctx) {
		return c, nil
	}

	fmt.Fprintln(w, "iniciando o daemon...")
	if err := iniciarDaemon(w); err != nil {
		return nil, err
	}

	prazo := time.After(10 * time.Second)
	for {
		if c.Rodando(ctx) {
			return c, nil
		}
		select {
		case <-prazo:
			return nil, fmt.Errorf("o daemon não respondeu em 10s — veja %s", caminhoDoLog())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// iniciarDaemon bifurca o próprio binário em modo daemon.
func iniciarDaemon(w io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("localizando o próprio binário: %w", err)
	}

	log, err := abrirLog()
	if err != nil {
		return err
	}
	defer log.Close()

	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdout = log
	cmd.Stderr = log
	// stdin nil: o daemon nunca lê do terminal, e herdar o stdin o
	// prenderia ao terminal que o iniciou.
	cmd.Stdin = nil

	desacoplarDoTerminal(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("iniciando o daemon: %w", err)
	}

	// Release solta o processo filho: sem isso o Go manteria a intenção de
	// esperá-lo, e o daemon viraria zumbi quando a CLI terminasse.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("desacoplando o daemon: %w", err)
	}
	return nil
}

func abrirLog() (*os.File, error) {
	caminho := caminhoDoLog()

	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return nil, fmt.Errorf("criando diretório do log: %w", err)
	}

	f, err := os.OpenFile(caminho, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("abrindo %s: %w", caminho, err)
	}
	return f, nil
}

// daemonInstallCmd grava o serviço que faz o daemon subir no login.
//
// Escrevemos o arquivo, mas NÃO habilitamos o serviço: ligar algo para rodar
// em todo login é decisão do usuário, não efeito colateral de um comando de
// instalação. Os comandos a rodar ficam impressos.
func daemonInstallCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("daemon install", flag.ContinueOnError)
	fs.SetOutput(w)
	forcar := fs.Bool("force", false, "sobrescreve um arquivo existente")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("localizando o próprio binário: %w", err)
	}
	// O serviço precisa do caminho REAL: se o binário foi chamado por um
	// symlink que depois muda, o serviço apontaria para o lugar errado.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("localizando o diretório home: %w", err)
	}

	servico, ok := servicoDeLoginPara(runtime.GOOS, dadosDoServico{
		Exe:        exe,
		Home:       home,
		ConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		Path:       os.Getenv("PATH"),
		Log:        caminhoDoLog(),
	})
	if !ok {
		return fmt.Errorf("subir no login não é suportado em %s; use `devm daemon start`", runtime.GOOS)
	}

	if _, err := os.Stat(servico.arquivo); err == nil && !*forcar {
		return fmt.Errorf("%s já existe (use --force para sobrescrever)", servico.arquivo)
	}

	if err := os.MkdirAll(filepath.Dir(servico.arquivo), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(servico.arquivo), err)
	}
	if err := os.WriteFile(servico.arquivo, []byte(servico.conteudo), 0o644); err != nil {
		return fmt.Errorf("gravando %s: %w", servico.arquivo, err)
	}

	fmt.Fprintf(w, "gravado em %s\n\n", servico.arquivo)
	fmt.Fprintln(w, "para subir no login:")
	for _, c := range servico.ligar {
		fmt.Fprintf(w, "  %s\n", c)
	}
	fmt.Fprintln(w, "\npara desligar depois:")
	for _, c := range servico.desligar {
		fmt.Fprintf(w, "  %s\n", c)
	}
	return nil
}
