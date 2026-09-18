package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
)

// proxyCmd despacha os subcomandos de `devm proxy`.
func proxyCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		return proxyStatusCmd(stdio.Out, nil)
	}

	switch args[0] {
	case "status":
		return proxyStatusCmd(stdio.Out, args[1:])
	case "ca":
		return proxyCACmd(stdio.Out, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: proxy %q", args[0])
	}
}

func proxyStatusCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("proxy status", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	info, err := c.Proxy(context.Background())
	if err != nil {
		return err
	}

	if *comoJSON {
		saida, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	if !info.Ativo {
		fmt.Fprintln(w, "proxy   inativo")
		fmt.Fprintln(w, "\nveja o motivo em `devm daemon logs`")
		return nil
	}

	fmt.Fprintf(w, "proxy   ativo\n")
	fmt.Fprintf(w, "http    :%d\n", info.PortaHTTP)
	fmt.Fprintf(w, "https   :%d\n", info.PortaHTTPS)

	if len(info.Dominios) == 0 {
		fmt.Fprintln(w, "\nnenhum domínio ativo (suba um projeto com `devm start -d`)")
	} else {
		fmt.Fprintln(w, "\ndomínios:")
		for _, d := range info.Dominios {
			esquema := "https://"
			sufixo := ""
			if info.SemPrivilegio {
				// Fora da 443, o navegador precisa da porta explícita.
				sufixo = fmt.Sprintf(":%d", info.PortaHTTPS)
			}
			fmt.Fprintf(w, "  %s%s%s\n", esquema, d, sufixo)
		}
	}

	if info.SemPrivilegio {
		fmt.Fprint(w, avisoDePrivilegio())
	}
	return nil
}

// avisoDePrivilegio explica como liberar as portas 80 e 443.
//
// setcap concede ao BINÁRIO a capacidade de abrir portas privilegiadas, sem
// que ele rode como root. É a diferença entre dar uma permissão específica e
// dar todas — e um daemon de desenvolvimento não tem motivo para ser root.
func avisoDePrivilegio() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "$(which devm)"
	}

	if runtime.GOOS != "linux" {
		return fmt.Sprintf(`
o proxy está em portas alternativas porque 80 e 443 exigem privilégio
  os domínios ficam como http://projeto.test:%d
`, proxy.PortaHTTPFallback)
	}

	return fmt.Sprintf(`
o proxy está em portas alternativas porque 80 e 443 exigem privilégio.
para usar as portas padrão, conceda a capacidade ao binário:

  sudo setcap 'cap_net_bind_service=+ep' %s

e reinicie o daemon com `+"`devm daemon stop && devm daemon start`"+`

isso NÃO faz o daemon rodar como root: concede só a permissão de abrir
portas baixas, e nada mais.
`, exe)
}

// proxyCACmd mostra como confiar na autoridade certificadora local.
func proxyCACmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("proxy ca", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	info, err := c.Proxy(context.Background())
	if err != nil {
		return err
	}
	if !info.Ativo {
		return fmt.Errorf("o proxy não está ativo — veja `devm daemon logs`")
	}

	fmt.Fprintf(w, "certificado da CA local:\n  %s\n\n", info.CertificadoCA)
	fmt.Fprint(w, instrucoesDeConfianca(info.CertificadoCA))
	return nil
}

// instrucoesDeConfianca explica como instalar a CA em cada sistema.
//
// Instalar sozinho seria invasivo: mexer no armazenamento de certificados do
// sistema é uma mudança de segurança que merece ser deliberada, e cada
// distribuição faz de um jeito. Imprimimos os comandos exatos e deixamos a
// decisão com quem vai executá-los.
func instrucoesDeConfianca(cert string) string {
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf(`para confiar nela no macOS:

  sudo security add-trusted-cert -d -r trustRoot \
    -k /Library/Keychains/System.keychain %s
`, cert)

	case "linux":
		return fmt.Sprintf(`para confiar nela no sistema:

  # Fedora, RHEL e derivados (inclusive Aurora e Kinoite)
  sudo cp %s /etc/pki/ca-trust/source/anchors/devmanager.crt
  sudo update-ca-trust

  # Debian e Ubuntu
  sudo cp %s /usr/local/share/ca-certificates/devmanager.crt
  sudo update-ca-certificates

o Firefox mantém um armazenamento próprio e ignora o do sistema:
  Configurações → Privacidade e Segurança → Certificados → Ver certificados
  → Autoridades → Importar → marque "confiar para identificar sites"
`, cert, cert)
	}

	return fmt.Sprintf("instale %s como autoridade confiável no seu sistema\n", cert)
}
