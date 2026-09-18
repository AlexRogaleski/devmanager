package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/client"
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
			fmt.Fprintf(w, "  %s\n", urlDoDominio("https", d, info.PortaHTTPS))
			// O HTTP entra só quando está numa porta diferente da padrão —
			// nesse caso a URL não é óbvia e vale mostrar as duas.
			if info.PortaHTTP != 80 {
				fmt.Fprintf(w, "  %s\n", urlDoDominio("http", d, info.PortaHTTP))
			}
		}
	}

	if info.PortaHTTP != 80 || info.PortaHTTPS != 443 {
		fmt.Fprint(w, avisoDeQueda(info.MotivoDaQueda, info.PortaHTTP, info.PortaHTTPS))
	}
	return nil
}

// urlDoDominio monta a URL, omitindo a porta quando ela é a padrão.
//
// Imprimir "https://suma.test:443" é tecnicamente correto e praticamente
// ruim: a pessoa lê o 443, conclui que precisa dele, e não percebe que
// "https://suma.test" — que é o que ela digitaria naturalmente — já funciona.
// Foi exatamente o que aconteceu quando o HTTPS pegou a 443 e o HTTP caiu
// para 8080: a saída sugeria uma porta alternativa que não existia.
func urlDoDominio(esquema, dominio string, porta int) string {
	padrao := 80
	if esquema == "https" {
		padrao = 443
	}
	if porta == padrao || porta == 0 {
		return esquema + "://" + dominio
	}
	return fmt.Sprintf("%s://%s:%d", esquema, dominio, porta)
}

// avisoDeQueda explica por que o proxy não está nas portas padrão.
//
// A explicação é específica porque os consertos são diferentes: falta de
// permissão se resolve com setcap; porta ocupada se resolve parando quem está
// lá. Uma mensagem genérica mandaria metade dos usuários rodar um comando que
// não resolve nada no caso deles.
func avisoDeQueda(motivo string, portaHTTP, portaHTTPS int) string {
	if motivo == "" {
		motivo = "as portas padrão não puderam ser usadas"
	}

	// Dizer QUAL protocolo caiu evita a confusão de procurar o HTTPS numa
	// porta alternativa quando foi só o HTTP que mudou — os dois caem de
	// forma independente, e frequentemente só um deles cai.
	var quais string
	switch {
	case portaHTTP != 80 && portaHTTPS != 443:
		quais = fmt.Sprintf("http em :%d e https em :%d", portaHTTP, portaHTTPS)
	case portaHTTP != 80:
		quais = fmt.Sprintf("http em :%d (o https está na 443, como de costume)", portaHTTP)
	default:
		quais = fmt.Sprintf("https em :%d (o http está na 80, como de costume)", portaHTTPS)
	}

	cabecalho := fmt.Sprintf("\n%s — %s\n", motivo, quais)

	// Porta ocupada: o conserto é parar quem está lá, não dar permissão.
	if strings.Contains(motivo, "ocupada") {
		return cabecalho + `
descubra quem está usando a porta com:

  ss -ltnp | grep ':80 '
  docker ps --format '{{.Names}}\t{{.Ports}}' | grep ':80'

pare aquele serviço e reinicie o daemon, ou continue usando as portas
alternativas — os projetos funcionam igual, só com a porta na URL.
`
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "$(which devm)"
	}
	if runtime.GOOS != "linux" {
		return cabecalho
	}

	return cabecalho + fmt.Sprintf(`
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
