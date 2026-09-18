package dns

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/shell"
)

// integracao descreve como um sistema operacional encaminha as consultas de
// um domínio para o servidor do Dev Manager.
//
// Cada sistema tem um mecanismo próprio — o systemd-resolved no Linux, o
// diretório /etc/resolver no macOS —, mas os dois cabem na mesma forma: um
// arquivo com certo conteúdo, e um comando que faz o sistema relê-lo.
//
// A escolha é feita por runtime.GOOS passado como parâmetro, e não por build
// tags. Build tag é para código que não COMPILA em outro sistema, como o
// campo Pdeathsig do supervisor, que só existe no Linux. Aqui tudo é caminho,
// texto e exec — compila em qualquer lugar —, e receber o sistema como
// parâmetro permite testar o macOS numa máquina Linux:
// integracaoPara("darwin", ...) é só uma chamada de função.
type integracao struct {
	// mecanismo nomeia o que o sistema usa, para as mensagens.
	mecanismo string

	// arquivo é onde a configuração mora.
	arquivo string

	// conteudo monta as linhas do arquivo a partir do endereço do servidor.
	conteudo func(host, porta, tld string) []string

	// recarregar são os comandos que fazem o sistema reler a configuração.
	recarregar []string

	// notificar avisa o resolvedor de que o servidor voltou. Roda sem
	// privilégio, pelo próprio daemon, a cada vez que o DNS sobe.
	notificar [][]string
}

// integracaoPara devolve a integração de um sistema, ou false se não houver.
func integracaoPara(goos, tld string) (*integracao, bool) {
	switch goos {
	case "linux":
		return &integracao{
			mecanismo: "systemd-resolved",

			// Um arquivo em .conf.d, e não uma edição do resolved.conf: o
			// principal pertence à distribuição e pode ser substituído numa
			// atualização, enquanto o diretório de drop-ins existe
			// exatamente para acréscimos locais.
			arquivo: "/etc/systemd/resolved.conf.d/devmanager.conf",

			conteudo: func(host, porta, tld string) []string {
				return []string{
					"# Gerado pelo Dev Manager: envia ." + tld + " para o servidor local.",
					"# Para desfazer: devm dns uninstall",
					"[Resolve]",
					"DNS=" + net.JoinHostPort(host, porta),
					// O "~" faz de .test um domínio de ROTEAMENTO, não de
					// busca: só as consultas terminadas em .test vêm para
					// cá. Sem ele, .test viraria sufixo de busca e o
					// sistema tentaria resolver "google.com.test".
					"Domains=~" + tld,
				}
			},

			recarregar: []string{"sudo systemctl restart systemd-resolved"},

			// Se algo consulta um .test enquanto o daemon está parado — o
			// navegador reabrindo abas depois de um reboot, por exemplo —,
			// o resolved rebaixa o servidor de UDP+EDNS0 para UDP, depois
			// para TCP, e desiste. Um servidor que nunca respondeu desde o
			// boot não tem período de carência que expire, então ele NÃO
			// volta a tentar quando o daemon sobe: todo .test passa a dar
			// SERVFAIL sem que uma única consulta chegue até nós.
			//
			// Esquecer o que ele aprendeu sobre o servidor resolve. No
			// systemd 259 as duas chamadas não pedem senha; o
			// --no-ask-password garante que, numa versão em que peçam,
			// elas falhem em vez de abrir um diálogo na tela.
			notificar: [][]string{
				{"resolvectl", "--no-ask-password", "reset-server-features"},
				{"resolvectl", "--no-ask-password", "flush-caches"},
			},
		}, true

	case "darwin":
		return &integracao{
			mecanismo: "/etc/resolver",

			// O macOS consulta /etc/resolver/<domínio> antes da configuração
			// geral: um arquivo por domínio, lido pelo mDNSResponder. É o
			// mesmo mecanismo que o Valet e o Docker Desktop usam.
			arquivo: "/etc/resolver/" + tld,

			conteudo: func(host, porta, tld string) []string {
				return []string{
					"# Gerado pelo Dev Manager: envia ." + tld + " para o servidor local.",
					"# Para desfazer: devm dns uninstall",
					"nameserver " + host,
					// A porta vai numa linha própria: o formato do resolver
					// não aceita host:porta, e sem ela a consulta iria para
					// a 53, onde não há ninguém.
					"port " + porta,
				}
			},

			// O mDNSResponder percebe o arquivo novo sozinho, mas guarda em
			// cache as respostas negativas de antes — um .test consultado
			// antes da configuração continuaria falhando por um tempo.
			recarregar: []string{"sudo killall -HUP mDNSResponder"},
		}, true
	}
	return nil, false
}

// linhas monta o conteúdo do arquivo para um endereço host:porta.
func (i *integracao) linhas(endereco, tld string) ([]string, error) {
	host, porta, err := net.SplitHostPort(endereco)
	if err != nil {
		return nil, fmt.Errorf("endereço do DNS inválido %q: %w", endereco, err)
	}
	return i.conteudo(host, porta, tld), nil
}

// comandosDeInstalacao devolve um comando por elemento, e cada um cabe numa
// linha só.
//
// Essa regra é o que corrige um defeito real. A versão anterior gravava o
// arquivo com um heredoc de várias linhas, e o `devm setup --apply` executa
// cada elemento num `sh -c` separado: a linha do tee rodava com o heredoc
// SEM corpo — gravando o arquivo vazio —, os comentários passavam calados, e
// a linha "[Resolve]" era executada como comando. DNS quebrado e um arquivo
// vazio em /etc, pelo caminho que o README apresenta como o fácil.
func (i *integracao) comandosDeInstalacao(endereco, tld string) ([]string, error) {
	linhas, err := i.linhas(endereco, tld)
	if err != nil {
		return nil, err
	}

	comandos := []string{
		"sudo mkdir -p " + shell.Aspas(filepath.Dir(i.arquivo)),
		gravarComSudo(i.arquivo, linhas),
	}
	return append(comandos, i.recarregar...), nil
}

func (i *integracao) comandosDeRemocao() []string {
	return append([]string{"sudo rm -f " + shell.Aspas(i.arquivo)}, i.recarregar...)
}

// gravarComSudo monta UM comando, de uma linha, que grava o arquivo como root.
//
// printf e não echo: o echo interpreta barras invertidas de forma diferente
// em cada shell — o sh do Debian é o dash, o do macOS é o bash em modo POSIX
// —, e o mesmo comando gravaria conteúdos diferentes. O printf '%s\n' imprime
// cada argumento verbatim seguido de uma quebra, em qualquer shell.
//
// As linhas vão como ARGUMENTOS do printf, nunca no formato: um "%" no
// conteúdo seria interpretado como diretiva se estivesse no primeiro
// argumento.
func gravarComSudo(destino string, linhas []string) string {
	args := make([]string, len(linhas))
	for i, l := range linhas {
		args[i] = shell.Aspas(l)
	}
	return fmt.Sprintf("printf '%%s\\n' %s | sudo tee %s > /dev/null",
		strings.Join(args, " "), shell.Aspas(destino))
}

// Estado descreve a situação da resolução de domínios locais.
type Estado struct {
	Suportado      bool   `json:"supported"`
	Mecanismo      string `json:"mechanism,omitempty"`
	MecanismoAtivo bool   `json:"mechanism_active"`
	Arquivo        string `json:"file,omitempty"`
	Configurado    bool   `json:"configured"`
	Resolvendo     bool   `json:"resolving"`
	Endereco       string `json:"address"`
	TLD            string `json:"tld"`
	Motivo         string `json:"reason,omitempty"`
}

// Verificar inspeciona se a resolução de domínios locais está de pé.
func Verificar(ctx context.Context, endereco, tld string) Estado {
	return verificarEm(ctx, runtime.GOOS, endereco, tld)
}

func verificarEm(ctx context.Context, goos, endereco, tld string) Estado {
	e := Estado{Endereco: endereco, TLD: tld}

	integ, ok := integracaoPara(goos, tld)
	if !ok {
		e.Motivo = "a configuração automática de DNS não é suportada em " + goos
		return e
	}
	e.Suportado = true
	e.Mecanismo = integ.mecanismo
	e.Arquivo = integ.arquivo

	e.MecanismoAtivo, e.Motivo = mecanismoAtivo(ctx, goos)

	if _, err := os.Stat(integ.arquivo); err == nil {
		e.Configurado = true
	}

	// A prova real não é o arquivo existir, é o nome resolver. Um drop-in
	// gravado mas sem o restart do resolved não faz nada, e reportar
	// "configurado" nesse estado seria mentira útil para ninguém.
	e.Resolvendo = resolve(ctx, goos, "devmanager-verificacao."+tld)

	return e
}

// mecanismoAtivo diz se o encaminhamento do sistema está disponível.
func mecanismoAtivo(ctx context.Context, goos string) (bool, string) {
	switch goos {
	case "linux":
		if servicoAtivo(ctx, "systemd-resolved") {
			return true, ""
		}
		return false, "systemd-resolved não está ativo"
	case "darwin":
		// O /etc/resolver é lido pelo mDNSResponder, que é parte do sistema:
		// não há serviço a conferir, nem como ele estar desligado.
		return true, ""
	}
	return false, ""
}

// resolve testa se um nome do nosso TLD chega ao loopback.
func resolve(ctx context.Context, goos, nome string) bool {
	ctx, cancelar := context.WithTimeout(ctx, 3*time.Second)
	defer cancelar()

	if goos == "darwin" {
		return resolveNoMacOS(ctx, nome)
	}

	var r net.Resolver
	ips, err := r.LookupHost(ctx, nome)
	if err != nil {
		return false
	}
	return algumLoopback(ips)
}

// resolveNoMacOS pergunta ao próprio sistema, e não ao resolvedor do Go.
//
// Compilado sem cgo, o Go não passa pelo mesmo caminho que o Safari e o
// Chrome usam no macOS, e o /etc/resolver pode não ser consultado. Testar
// pelo Go poderia dizer "não resolve" com o navegador funcionando — ou o
// contrário. O dscacheutil consulta o serviço de diretório do sistema, que é
// a resposta que os navegadores recebem.
func resolveNoMacOS(ctx context.Context, nome string) bool {
	saida, err := exec.CommandContext(ctx, "dscacheutil", "-q", "host", "-a", "name", nome).Output()
	if err != nil {
		return false
	}
	return algumLoopback(enderecosDoDscacheutil(string(saida)))
}

// enderecosDoDscacheutil extrai os IPs da saída do dscacheutil, que tem a
// forma:
//
//	name: app.test
//	ip_address: 127.0.0.1
//
//	name: app.test
//	ipv6_address: ::1
func enderecosDoDscacheutil(saida string) []string {
	var ips []string
	sc := bufio.NewScanner(strings.NewReader(saida))
	for sc.Scan() {
		chave, valor, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(chave) {
		case "ip_address", "ipv6_address":
			ips = append(ips, strings.TrimSpace(valor))
		}
	}
	return ips
}

func algumLoopback(ips []string) bool {
	for _, ip := range ips {
		if addr := net.ParseIP(ip); addr != nil && addr.IsLoopback() {
			return true
		}
	}
	return false
}

func servicoAtivo(ctx context.Context, unidade string) bool {
	ctx, cancelar := context.WithTimeout(ctx, 3*time.Second)
	defer cancelar()

	saida, err := exec.CommandContext(ctx, "systemctl", "is-active", unidade).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(saida)) == "active"
}

// NotificarSistema avisa o resolvedor do sistema de que o servidor DNS está
// de pé. O daemon chama isto logo depois de abrir a porta.
//
// Devolve os comandos executados, para o log mostrar o que foi feito. Um erro
// nomeia o comando que falhou, para ele poder ser copiado e rodado à mão. Em
// sistemas sem nada a notificar, não faz nada e devolve nil.
func NotificarSistema(ctx context.Context, tld string) ([]string, error) {
	integ, ok := integracaoPara(runtime.GOOS, tld)
	if !ok {
		return nil, nil
	}

	ctx, cancelar := context.WithTimeout(ctx, 5*time.Second)
	defer cancelar()

	var feitos []string
	for _, argv := range integ.notificar {
		comando := strings.Join(argv, " ")
		if saida, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput(); err != nil {
			return feitos, fmt.Errorf("%s: %w: %s", comando, err, strings.TrimSpace(string(saida)))
		}
		feitos = append(feitos, comando)
	}
	return feitos, nil
}

// ComandosDeInstalacao devolve o que o usuário precisa rodar como root, um
// comando por elemento.
//
// Não executamos nada com sudo aqui: mexer na resolução de nomes afeta TODO o
// sistema, não só o Dev Manager. Devolver os comandos exatos deixa a decisão
// — e a auditoria do que será feito — com quem tem a senha.
func ComandosDeInstalacao(endereco, tld string) ([]string, error) {
	integ, ok := integracaoPara(runtime.GOOS, tld)
	if !ok {
		return nil, fmt.Errorf("a configuração automática de DNS não é suportada em %s", runtime.GOOS)
	}
	return integ.comandosDeInstalacao(endereco, tld)
}

// ComandosDeRemocao desfaz a configuração.
func ComandosDeRemocao(tld string) ([]string, error) {
	integ, ok := integracaoPara(runtime.GOOS, tld)
	if !ok {
		return nil, fmt.Errorf("a configuração automática de DNS não é suportada em %s", runtime.GOOS)
	}
	return integ.comandosDeRemocao(), nil
}
