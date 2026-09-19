// Package setup diagnostica e descreve a configuração de sistema que o Dev
// Manager precisa.
//
// São três ajustes que exigem root, cada um feito UMA vez por máquina. O
// pacote existe para que o usuário não precise decorá-los nem caçá-los na
// documentação: `devm setup` diz o que falta e entrega os comandos prontos.
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	devmdns "github.com/AlexRogaleski/devmanager/internal/dns"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
	"github.com/AlexRogaleski/devmanager/internal/shell"
)

// PortaMinimaDesejada é a menor porta que queremos poder abrir sem root.
const PortaMinimaDesejada = 80

// arquivoSysctl persiste o ajuste de portas privilegiadas.
const arquivoSysctl = "/etc/sysctl.d/60-devmanager.conf"

// Passo é um ajuste de sistema.
type Passo struct {
	Nome     string   `json:"name"`
	Porque   string   `json:"why"`
	Feito    bool     `json:"done"`
	Detalhe  string   `json:"detail,omitempty"`
	Comandos []string `json:"commands,omitempty"`
}

// Plano é o conjunto de ajustes e seu estado atual.
type Plano struct {
	Passos []Passo `json:"steps"`
}

// Pendentes devolve só o que falta fazer.
func (p Plano) Pendentes() []Passo {
	var faltando []Passo
	for _, passo := range p.Passos {
		if !passo.Feito {
			faltando = append(faltando, passo)
		}
	}
	return faltando
}

// Script monta um shell script com tudo que falta.
func (p Plano) Script() string {
	var b strings.Builder

	b.WriteString("#!/bin/sh\nset -e\n")
	for _, passo := range p.Pendentes() {
		fmt.Fprintf(&b, "\n# %s — %s\n", passo.Nome, passo.Porque)
		for _, c := range passo.Comandos {
			b.WriteString(c + "\n")
		}
	}
	return b.String()
}

// Entrada é o que o diagnóstico precisa saber além da própria máquina.
type Entrada struct {
	EnderecoDNS string
	TLD         string

	// CertCA é o certificado da CA local; vazio se ela ainda não existe.
	CertCA string

	// DaemonAtivo muda o diagnóstico do DNS: quem responde pelos domínios
	// é o servidor dentro do daemon, e com ele parado nenhuma configuração
	// de sistema faz o .test resolver.
	DaemonAtivo bool
}

// Diagnosticar monta o plano a partir do estado atual da máquina.
func Diagnosticar(ctx context.Context, e Entrada) Plano {
	return Plano{Passos: []Passo{
		decidirDNS(devmdns.Verificar(ctx, e.EnderecoDNS, e.TLD), e),
		passoPortas(),
		passoCA(ctx, e.CertCA),
	}}
}

// decidirDNS transforma o estado observado num passo.
//
// Separada de Verificar de propósito: Verificar olha a máquina — arquivos,
// serviços, uma consulta de DNS de verdade —, e isto aqui só decide. Uma
// decisão que recebe os fatos prontos se testa com um struct literal, sem
// precisar de systemd-resolved, de /etc nem de rede.
func decidirDNS(estado devmdns.Estado, e Entrada) Passo {
	p := Passo{
		Nome:   "resolução de ." + e.TLD,
		Porque: "sem isso, ." + e.TLD + " não resolve e os projetos só abrem por 127.0.0.1:porta",
		Feito:  estado.Resolvendo,
	}

	if p.Feito {
		return p
	}
	if !estado.Suportado {
		p.Detalhe = estado.Motivo
		return p
	}
	if !estado.MecanismoAtivo {
		p.Detalhe = estado.Motivo + "; configure o DNS manualmente"
		return p
	}

	// Configurado e sem resolver, com o daemon parado: o que falta é o
	// servidor, não a configuração. Oferecer regravar o arquivo e reiniciar
	// o resolvedor custaria a senha do sudo para não mudar nada.
	if estado.Configurado && !e.DaemonAtivo {
		p.Detalhe = "configurado em " + estado.Arquivo +
			", mas quem responde é o daemon, que está parado: devm daemon start"
		return p
	}

	// A lista já vem com um comando por elemento. Ela era montada dividindo
	// um texto por linha, o que partia um heredoc em pedaços executados
	// separadamente pelo --apply.
	comandos, err := devmdns.ComandosDeInstalacao(e.EnderecoDNS, e.TLD)
	if err != nil {
		p.Detalhe = err.Error()
		return p
	}
	p.Comandos = comandos
	return p
}

// passoPortas permite abrir as portas 80 e 443 sem root.
//
// Usamos sysctl, e não setcap, por uma razão prática que só aparece com o
// tempo: capacidades são atributos do ARQUIVO, e toda atualização do binário
// as apaga em silêncio. O sintoma é o proxy voltar para as portas
// alternativas depois de um update, sem ninguém entender por quê.
//
// O sysctl é do sistema e sobrevive a qualquer troca de binário. O custo é
// que ele vale para TODO processo sem privilégio, não só para o nosso — numa
// máquina de desenvolvimento pessoal isso é aceitável, e é exatamente o que
// as configurações de contêiner rootless já fazem.
func passoPortas() Passo {
	p := Passo{
		Nome:   "portas 80 e 443 sem root",
		Porque: "permite os domínios sem porta na URL, sem rodar o daemon como root",
	}

	if runtime.GOOS != "linux" {
		return sondarPortas(p, sondarPorta80)
	}

	atual, err := portaMinimaAtual()
	if err != nil {
		p.Detalhe = "não consegui ler net.ipv4.ip_unprivileged_port_start"
		return p
	}
	if atual <= PortaMinimaDesejada {
		p.Feito = true
		p.Detalhe = fmt.Sprintf("já permitido a partir da porta %d", atual)
		return p
	}

	p.Detalhe = fmt.Sprintf("hoje só a partir da porta %d", atual)
	p.Comandos = []string{
		fmt.Sprintf("echo 'net.ipv4.ip_unprivileged_port_start=%d' | sudo tee %s > /dev/null",
			PortaMinimaDesejada, arquivoSysctl),
		fmt.Sprintf("sudo sysctl -p %s", arquivoSysctl),
	}
	return p
}

// sondarPortas decide o passo fora do Linux perguntando ao próprio kernel.
//
// Fora do Linux não há sysctl para ler, e afirmar a regra de cor seria
// frágil. A sonda é o proxy.PodeAbrir, que abre a porta pelo mesmo caminho
// que o proxy — no macOS, inclusive o recurso de escutar em 0.0.0.0 com
// filtro. Um diagnóstico calculado por outro caminho acabaria discordando
// do que o proxy de fato consegue.
func sondarPortas(p Passo, sondar func() error) Passo {
	err := sondar()

	switch {
	case err == nil:
		p.Feito = true
		p.Detalhe = "o sistema permite"

	case errors.Is(err, syscall.EADDRINUSE):
		// Em uso implica permitido: o kernel confere a permissão antes de
		// procurar conflito. Quem está lá costuma ser o próprio daemon.
		p.Feito = true
		p.Detalhe = "a porta 80 já está aberta (pelo proxy do daemon, se ele estiver rodando)"

	case errors.Is(err, os.ErrPermission):
		p.Detalhe = "o sistema não libera a porta 80 sem root; " +
			"o proxy usa 8080 e 8443, e as URLs levam a porta"

	default:
		p.Detalhe = "não consegui testar a porta 80: " + err.Error()
	}
	return p
}

func sondarPorta80() error { return proxy.PodeAbrir(80) }

func portaMinimaAtual() (int, error) {
	dados, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(dados)))
}

// passoCA torna a CA local uma autoridade confiável.
func passoCA(ctx context.Context, cert string) Passo {
	p := Passo{
		Nome:   "confiar no certificado local",
		Porque: "sem isso, o HTTPS dos domínios locais aparece como inseguro",
	}

	if cert == "" {
		p.Detalhe = "a CA é criada na primeira vez que o daemon sobe: devm daemon start"
		return p
	}

	conf, ok := confiancaPara(runtime.GOOS, cert)
	if !ok {
		p.Detalhe = "instale " + cert + " manualmente como autoridade confiável"
		return p
	}

	if conf.instalada(ctx) {
		p.Feito = true
		p.Detalhe = "confiável: " + conf.onde
		return p
	}

	p.Comandos = conf.comandos
	return p
}

// confianca descreve como tornar a CA confiável num sistema.
//
// É a ÚNICA descrição disso no projeto. Havia duas — uma aqui, só de Linux,
// e outra no `devm proxy ca`, que já conhecia o macOS —, e as duas
// divergiam: o setup não sabia configurar o Mac que o proxy ca explicava.
type confianca struct {
	// onde nomeia o armazenamento, para as mensagens.
	onde string

	comandos []string

	// instalada responde se a CA ATUAL é confiável — não uma CA qualquer
	// com o mesmo nome, que ficou de uma instalação anterior.
	instalada func(ctx context.Context) bool
}

func confiancaPara(goos, cert string) (*confianca, bool) {
	switch goos {
	case "linux":
		return confiancaLinux(ancorasLinux, cert)
	case "darwin":
		return confiancaMacOS(cert), true
	}
	return nil, false
}

// ancora é um diretório de autoridades confiáveis de uma família de
// distribuições.
type ancora struct {
	dir, atualizar string
}

// ancorasLinux, na ordem de preferência.
//
// Detectamos pelo diretório que existe, e não pelo /etc/os-release: derivadas
// como Aurora, Bazzite e Nobara reportam nomes próprios, e uma lista de
// distribuições conhecidas ficaria desatualizada a cada nova. O diretório de
// âncoras, esse, não muda.
var ancorasLinux = []ancora{
	{"/etc/pki/ca-trust/source/anchors", "sudo update-ca-trust"},        // Fedora, RHEL
	{"/usr/local/share/ca-certificates", "sudo update-ca-certificates"}, // Debian, Ubuntu
}

func confiancaLinux(ancoras []ancora, cert string) (*confianca, bool) {
	for _, a := range ancoras {
		if info, err := os.Stat(a.dir); err != nil || !info.IsDir() {
			continue
		}
		destino := filepath.Join(a.dir, "devmanager.crt")

		return &confianca{
			onde: destino,
			comandos: []string{
				"sudo cp " + shell.Aspas(cert) + " " + shell.Aspas(destino),
				a.atualizar,
			},

			// Compara o CONTEÚDO, não só a existência. Se a CA for
			// recriada — apagando ~/.local/share/devmanager/ca, por
			// exemplo —, a âncora antiga continua em /etc com o mesmo
			// nome. Conferir só a existência diria "feito" enquanto o
			// navegador recusa todo certificado emitido pela CA nova.
			instalada: func(context.Context) bool {
				instalado, err := os.ReadFile(destino)
				if err != nil {
					return false
				}
				atual, err := os.ReadFile(cert)
				return err == nil && bytes.Equal(instalado, atual)
			},
		}, true
	}
	return nil, false
}

// confiancaMacOS usa o Keychain do sistema.
//
// -d grava a confiança no domínio de administração, que vale para todos os
// usuários e é o que o Safari e o Chrome consultam; -r trustRoot marca o
// certificado como raiz confiável, e não só como presente no chaveiro.
func confiancaMacOS(cert string) *confianca {
	return &confianca{
		onde: "Keychain do sistema",
		comandos: []string{
			"sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain " +
				shell.Aspas(cert),
		},

		// verify-cert avalia o certificado contra a confiança do sistema, que
		// é a pergunta certa: não "está no chaveiro?", e sim "o sistema
		// confia nele?". Uma CA antiga com o mesmo nome não passa, porque o
		// que se avalia é o arquivo atual.
		instalada: func(ctx context.Context) bool {
			ctx, cancelar := context.WithTimeout(ctx, 5*time.Second)
			defer cancelar()
			return exec.CommandContext(ctx, "security", "verify-cert", "-c", cert).Run() == nil
		},
	}
}

// CAConfiavel diz se o sistema já confia na CA atual.
func CAConfiavel(ctx context.Context, cert string) bool {
	conf, ok := confiancaPara(runtime.GOOS, cert)
	return ok && conf.instalada(ctx)
}

// ComandosDeConfianca devolve como confiar na CA neste sistema.
//
// É a porta de entrada do `devm proxy ca`, para que ele mostre exatamente os
// comandos que o `devm setup --apply` executaria.
func ComandosDeConfianca(cert string) (onde string, comandos []string, ok bool) {
	conf, ok := confiancaPara(runtime.GOOS, cert)
	if !ok {
		return "", nil, false
	}
	return conf.onde, conf.comandos, true
}

// AvisoFirefox explica o armazenamento próprio do navegador.
//
// O Firefox ignora o armazenamento de certificados do sistema e mantém o
// dele. Não há comando para isso — é interface gráfica — então a única coisa
// honesta a fazer é avisar.
const AvisoFirefox = `o Firefox mantém um armazenamento de certificados próprio e ignora o do sistema:
  Configurações → Privacidade e Segurança → Certificados → Ver certificados
  → Autoridades → Importar → marque "confiar para identificar sites"`
