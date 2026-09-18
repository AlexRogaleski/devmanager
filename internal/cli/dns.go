package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/client"
	devmdns "github.com/AlexRogaleski/devmanager/internal/dns"
)

// dnsCmd despacha os subcomandos de `devm dns`.
func dnsCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		return dnsStatusCmd(stdio.Out, nil)
	}

	switch args[0] {
	case "status":
		return dnsStatusCmd(stdio.Out, args[1:])
	case "install":
		return dnsInstallCmd(stdio.Out, args[1:])
	case "uninstall", "remove":
		return dnsUninstallCmd(stdio.Out, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: dns %q", args[0])
	}
}

// infoDNS junta o que o daemon sabe com o que o sistema mostra.
func infoDNS(ctx context.Context) (endereco, tld string, ativo bool, motivo string, err error) {
	c, err := client.Padrao()
	if err != nil {
		return "", "", false, "", err
	}

	info, err := c.Proxy(ctx)
	if err != nil {
		return "", "", false, "", err
	}

	d := info.DNS
	endereco, tld = d.Endereco, d.TLD
	if endereco == "" {
		endereco = fmt.Sprintf("127.0.0.1:%d", devmdns.PortaPadrao)
	}
	if tld == "" {
		tld = devmdns.TLDPadrao
	}
	return endereco, tld, d.Ativo, d.Motivo, nil
}

func dnsStatusCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("dns status", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	ctx := context.Background()

	endereco, tld, ativo, motivo, err := infoDNS(ctx)
	if err != nil {
		return err
	}

	estado := devmdns.Verificar(ctx, endereco, tld)

	if *comoJSON {
		saida, err := json.MarshalIndent(struct {
			ServidorAtivo bool `json:"server_active"`
			devmdns.Estado
		}{ativo, estado}, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	fmt.Fprintf(w, "servidor     %s\n", simNao(ativo, motivo))
	fmt.Fprintf(w, "endereço     %s\n", endereco)
	fmt.Fprintf(w, "domínio      .%s\n", tld)
	if estado.Suportado {
		fmt.Fprintf(w, "mecanismo    %s  (%s)\n", estado.Mecanismo, simNao(estado.MecanismoAtivo, ""))
		fmt.Fprintf(w, "configurado  %s  (%s)\n", simNao(estado.Configurado, ""), estado.Arquivo)
	}

	// O que realmente importa é o nome RESOLVER. Arquivo gravado sem o
	// restart do resolved não faz nada, e reportar "configurado" nesse
	// estado seria mentira útil para ninguém.
	fmt.Fprintf(w, "resolvendo   %s\n", simNao(estado.Resolvendo, ""))

	if estado.Resolvendo {
		fmt.Fprintf(w, "\nos domínios .%s já funcionam no navegador\n", tld)
		return nil
	}

	if !estado.Suportado {
		fmt.Fprintf(w, "\n%s\n", estado.Motivo)
		return nil
	}
	if !estado.MecanismoAtivo {
		fmt.Fprintf(w, "\n%s — a configuração automática precisa dele\n", estado.Motivo)
		return nil
	}

	fmt.Fprintln(w, "\nrode `devm dns install` para ver como configurar")
	return nil
}

func simNao(v bool, motivo string) string {
	if v {
		return "sim"
	}
	if motivo != "" {
		return "não (" + motivo + ")"
	}
	return "não"
}

// dnsInstallCmd imprime os comandos que configuram a resolução.
//
// Imprime, não executa. Mexer na resolução de nomes afeta TODO o sistema, não
// só o Dev Manager — é o tipo de mudança que quem tem a senha deveria ler
// antes de aplicar. Mesma postura do `devm proxy ca`.
func dnsInstallCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("dns install", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	ctx := context.Background()

	endereco, tld, ativo, motivo, err := infoDNS(ctx)
	if err != nil {
		return err
	}
	if !ativo {
		fmt.Fprintf(w, "aviso: o servidor DNS do daemon não está ativo")
		if motivo != "" {
			fmt.Fprintf(w, " (%s)", motivo)
		}
		fmt.Fprint(w, "\n\n")
	}

	estado := devmdns.Verificar(ctx, endereco, tld)
	if estado.Resolvendo {
		fmt.Fprintf(w, "os domínios .%s já resolvem — nada a fazer\n", tld)
		return nil
	}
	if !estado.Suportado {
		return fmt.Errorf("%s", estado.Motivo)
	}
	if !estado.MecanismoAtivo {
		return fmt.Errorf("%s — a configuração automática depende dele", estado.Motivo)
	}

	comandos, err := devmdns.ComandosDeInstalacao(endereco, tld)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "para resolver os domínios .%s, rode:\n\n", tld)
	for _, c := range comandos {
		fmt.Fprintln(w, c)
	}
	fmt.Fprintf(w, "\ndepois confira com `devm dns status`\n")
	return nil
}

func dnsUninstallCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("dns uninstall", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	_, tld, _, _, err := infoDNS(context.Background())
	if err != nil {
		return err
	}

	comandos, err := devmdns.ComandosDeRemocao(tld)
	if err != nil {
		return err
	}

	fmt.Fprint(w, "para desfazer a configuração de DNS, rode:\n\n")
	for _, c := range comandos {
		fmt.Fprintln(w, c)
	}
	return nil
}
