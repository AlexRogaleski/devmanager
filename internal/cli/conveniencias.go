package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/daemon"
)

// restartCmd derruba e sobe de novo um ambiente.
//
//	devm restart            o projeto da pasta atual
//	devm restart web-rtr    pelo nome
//
// Não é `stop` seguido de `start`: quem subiu com `--only serve` perderia a
// escolha no caminho. O daemon guarda o pedido original e o repete.
func restartCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	nome, err := nomeDoAmbiente(posicionais)
	if err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	amb, err := c.Restart(context.Background(), nome)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s reiniciado\n", nome)
	if amb.Dominio != "" {
		fmt.Fprintf(w, "  http://%s\n", amb.Dominio)
	}
	for _, p := range amb.Processos {
		fmt.Fprintf(w, "  %-8s %s\n", p.Nome, p.Linha)
	}
	return nil
}

// openCmd abre o projeto no navegador.
//
//	devm open            o projeto da pasta atual
//	devm open web-rtr    pelo nome
func openCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	nome, err := nomeDoAmbiente(posicionais)
	if err != nil {
		return err
	}

	url := "http://" + nome + ".test"

	// Avisar não é impedir: o domínio pode estar servido por um `devm start`
	// em primeiro plano, que o daemon desconhece.
	if c, err := client.Padrao(); err == nil {
		if lista, err := c.Listar(context.Background()); err == nil && !estaNaLista(lista, nome) {
			fmt.Fprintf(w, "aviso: %s não está rodando sob o daemon\n", nome)
		}
	}

	fmt.Fprintf(w, "abrindo %s\n", url)
	return abrirNoNavegador(url)
}

func estaNaLista(lista []daemon.Ambiente, nome string) bool {
	for _, amb := range lista {
		if amb.Projeto == nome {
			return true
		}
	}
	return false
}

// abrirNoNavegador chama o utilitário do sistema.
func abrirNoNavegador(url string) error {
	abridor := "xdg-open"
	if runtime.GOOS == "darwin" {
		abridor = "open"
	}

	cmd := exec.Command(abridor, url)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("abrindo %s com %s: %w", url, abridor, err)
	}
	// Não esperamos: o navegador continua vivo depois que o devm sai.
	return cmd.Process.Release()
}
