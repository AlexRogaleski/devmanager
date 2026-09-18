package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/AlexRogaleski/devmanager/internal/client"
	devmdns "github.com/AlexRogaleski/devmanager/internal/dns"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
	"github.com/AlexRogaleski/devmanager/internal/setup"
)

// setupCmd diagnostica e aplica a configuração de sistema.
//
//	devm setup           mostra o que falta e os comandos
//	devm setup --apply   executa, pedindo a senha do sudo
//
// O modo padrão é MOSTRAR, não aplicar. Os três ajustes mudam o sistema além
// do Dev Manager — resolução de nomes, permissão de portas e as autoridades
// certificadoras confiáveis — e quem tem a senha deveria poder ler antes.
// Com --apply, o próprio sudo serve de confirmação: os comandos são impressos
// antes, e a senha é digitada sabendo o que vai rodar.
func setupCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	aplicar := fs.Bool("apply", false, "executa os comandos, pedindo a senha do sudo")
	comoJSON := fs.Bool("json", false, "imprime o diagnóstico em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	w := stdio.Out
	ctx := context.Background()

	plano := setup.Diagnosticar(ctx, dadosDoDaemon(ctx))

	if *comoJSON {
		saida, err := json.MarshalIndent(plano, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	for _, passo := range plano.Passos {
		marca := "→"
		if passo.Feito {
			marca = "✓"
		}
		fmt.Fprintf(w, "  %s %s\n", marca, passo.Nome)
		if passo.Detalhe != "" {
			fmt.Fprintf(w, "      %s\n", passo.Detalhe)
		}
	}

	pendentes := plano.Pendentes()
	if len(pendentes) == 0 {
		fmt.Fprintln(w, "\ntudo configurado")
		fmt.Fprintf(w, "\n%s\n", setup.AvisoFirefox)
		return nil
	}

	// Um passo pendente pode não ter comando nenhum — o DNS configurado
	// com o daemon parado, por exemplo, cujo conserto é `devm daemon
	// start`. O detalhe já foi impresso embaixo do passo; aqui só entra o
	// que o --apply de fato executaria.
	var comComandos []setup.Passo
	for _, p := range pendentes {
		if len(p.Comandos) > 0 {
			comComandos = append(comComandos, p)
		}
	}
	if len(comComandos) == 0 {
		return nil
	}

	if !*aplicar {
		fmt.Fprintln(w, "\ncomandos para configurar:")
		fmt.Fprintln(w)
		for _, passo := range comComandos {
			fmt.Fprintf(w, "  # %s\n", passo.Porque)
			for _, c := range passo.Comandos {
				fmt.Fprintf(w, "  %s\n", c)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "ou rode `devm setup --apply` para executá-los agora")
		return nil
	}

	return aplicarPlano(w, comComandos)
}

// aplicarPlano executa os comandos, um passo por vez.
func aplicarPlano(w io.Writer, pendentes []setup.Passo) error {
	for _, passo := range pendentes {
		if len(passo.Comandos) == 0 {
			fmt.Fprintf(w, "\npulando %q: %s\n", passo.Nome, passo.Detalhe)
			continue
		}

		fmt.Fprintf(w, "\n%s\n", passo.Nome)
		for _, c := range passo.Comandos {
			fmt.Fprintf(w, "  $ %s\n", c)

			// sh -c porque os comandos usam pipe, heredoc e redirecionamento
			// — eles foram escritos para serem lidos e colados no terminal, e
			// executá-los pelo shell mantém as duas formas idênticas. Não há
			// entrada do usuário nessas strings: elas são montadas pelo
			// próprio pacote setup a partir de caminhos que nós controlamos.
			cmd := exec.Command("sh", "-c", c)
			cmd.Stdin = os.Stdin // o sudo precisa do terminal para pedir a senha
			cmd.Stdout = w
			cmd.Stderr = w

			if err := cmd.Run(); err != nil {
				return fmt.Errorf("falhou em %q: %w", c, err)
			}
		}
	}

	fmt.Fprintln(w, "\nconfiguração aplicada")
	fmt.Fprintln(w, "reinicie o daemon para o proxy pegar as portas novas:")
	fmt.Fprintln(w, "  devm daemon stop && devm daemon start")
	fmt.Fprintf(w, "\n%s\n", setup.AvisoFirefox)
	return nil
}

// dadosDoDaemon busca o endereço do DNS e o certificado da CA.
//
// Com o daemon parado caímos nos valores padrão: o diagnóstico continua útil
// — dá para configurar o sistema ANTES de subir o daemon pela primeira vez.
func dadosDoDaemon(ctx context.Context) setup.Entrada {
	e := setup.Entrada{
		EnderecoDNS: fmt.Sprintf("127.0.0.1:%d", devmdns.PortaPadrao),
		TLD:         devmdns.TLDPadrao,

		// O certificado é um arquivo, e existe com o daemon parado. Pedir
		// ao daemon o caminho — como era — fazia o setup dizer "não há
		// certificado" justamente no momento em que alguém configura a
		// máquina antes de subir tudo.
		CertCA: proxy.CertificadoPadrao(),
	}

	c, err := client.Padrao()
	if err != nil {
		return e
	}
	info, err := c.Proxy(ctx)
	if err != nil {
		return e
	}

	e.DaemonAtivo = true
	if info.DNS.Endereco != "" {
		e.EnderecoDNS = info.DNS.Endereco
	}
	if info.DNS.TLD != "" {
		e.TLD = info.DNS.TLD
	}
	if info.CertificadoCA != "" {
		e.CertCA = info.CertificadoCA
	}
	return e
}
