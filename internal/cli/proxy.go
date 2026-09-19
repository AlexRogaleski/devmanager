package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
	"github.com/AlexRogaleski/devmanager/internal/setup"
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
// permissão se resolve configurando o sistema; porta ocupada se resolve
// parando quem está lá. Uma mensagem genérica mandaria metade dos usuários
// rodar um comando que não resolve nada no caso deles.
func avisoDeQueda(motivo string, portaHTTP, portaHTTPS int) string {
	return avisoDeQuedaEm(runtime.GOOS, motivo, portaHTTP, portaHTTPS)
}

func avisoDeQuedaEm(goos, motivo string, portaHTTP, portaHTTPS int) string {
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
		return cabecalho + fmt.Sprintf(`
descubra quem está usando a porta com:

  %s
  docker ps --format '{{.Names}}\t{{.Ports}}' | grep ':80'

pare aquele serviço e reinicie o daemon, ou continue usando as portas
alternativas — os projetos funcionam igual, só com a porta na URL.
`, quemUsaAPorta(goos, 80))
	}

	// No macOS o proxy já tenta tudo o que um usuário comum pode: o
	// loopback e, se o kernel negar, todas as interfaces com filtro. Negado
	// mesmo assim, não há ajuste que o setup possa sugerir.
	if goos == "darwin" {
		return cabecalho + `
o macOS negou a porta 80 mesmo em todas as interfaces, o que só acontece
em versões antigas do sistema. Os projetos funcionam igual, com a porta
na URL.
`
	}

	// Falta de permissão: o `devm setup` sabe o conserto de cada sistema.
	// Esta mensagem recomendava setcap, que foi abandonado justamente
	// porque toda atualização do binário apaga a capacidade em silêncio.
	return cabecalho + `
para usar as portas padrão, rode ` + "`devm setup`" + `, que mostra o ajuste
de sistema necessário, e depois reinicie o daemon.
`
}

// quemUsaAPorta devolve o comando que lista o processo escutando numa porta.
//
// O ss é do Linux (iproute2) e não existe no macOS; lá o equivalente é o
// lsof, que o sistema traz de fábrica.
func quemUsaAPorta(goos string, porta int) string {
	if goos == "darwin" {
		return fmt.Sprintf("lsof -nP -iTCP:%d -sTCP:LISTEN", porta)
	}
	return fmt.Sprintf("ss -ltnp 'sport = :%d'", porta)
}

// proxyCACmd mostra como confiar na autoridade certificadora local.
func proxyCACmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("proxy ca", flag.ContinueOnError)
	fs.SetOutput(w)

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	// O certificado é um arquivo: com o daemon parado ele continua no disco,
	// e confiar nele antes de subir o daemon é um caminho legítimo. O
	// daemon só é necessário na primeira vez, porque é ele quem cria a CA.
	cert := proxy.CertificadoPadrao()
	if cert == "" {
		return fmt.Errorf("a CA ainda não existe — ela é criada na primeira vez que o daemon sobe: devm daemon start")
	}

	fmt.Fprintf(w, "certificado da CA local:\n  %s\n\n", cert)

	// Os comandos continuam aparecendo com a CA já confiável: servem para
	// outra máquina, ou para reinstalar depois de recriar a CA.
	if setup.CAConfiavel(context.Background(), cert) {
		fmt.Fprint(w, "✓ este sistema já confia nela\n\n")
	}
	fmt.Fprint(w, instrucoesDeConfianca(cert))
	return nil
}

// instrucoesDeConfianca explica como instalar a CA neste sistema.
//
// Os comandos vêm do pacote setup, o mesmo que o `devm setup --apply`
// executa. Havia uma segunda descrição aqui, escrita à parte, e as duas
// divergiam — esta conhecia o macOS e o setup não.
//
// Instalar sozinho seria invasivo: mexer nas autoridades confiáveis do
// sistema é uma mudança de segurança que merece ser deliberada. Mostramos os
// comandos exatos e deixamos a decisão com quem vai executá-los.
func instrucoesDeConfianca(cert string) string {
	onde, comandos, ok := setup.ComandosDeConfianca(cert)
	if !ok {
		return fmt.Sprintf("instale %s como autoridade confiável no seu sistema\n", cert)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "para confiar nela (%s):\n\n", onde)
	for _, c := range comandos {
		fmt.Fprintf(&b, "  %s\n", c)
	}
	fmt.Fprintf(&b, "\nou rode `devm setup --apply`, que faz isto e o resto da configuração\n\n%s\n",
		setup.AvisoFirefox)
	return b.String()
}
