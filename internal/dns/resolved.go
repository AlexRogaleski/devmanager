package dns

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// CaminhoDropIn é onde a configuração do systemd-resolved é gravada.
//
// Um arquivo em .conf.d, e não uma edição do resolved.conf: o arquivo
// principal pertence à distribuição e pode ser substituído numa atualização,
// enquanto o diretório de drop-ins existe exatamente para acréscimos locais.
// Desfazer também fica trivial — basta apagar um arquivo.
const CaminhoDropIn = "/etc/systemd/resolved.conf.d/devmanager.conf"

// dropIn é o conteúdo do arquivo de configuração.
//
// O "~" antes do domínio faz dele um domínio de ROTEAMENTO, não de busca:
// consultas terminadas em .test vão para o nosso servidor, e todo o resto
// continua indo para onde ia. Sem o "~", o .test viraria sufixo de busca e
// o sistema tentaria resolver "google.com.test" — quebrando a navegação.
const dropIn = `# Gerado por devm dns install.
#
# Roteia as consultas do domínio .%s para o Dev Manager, que responde
# com o endereço de loopback. Todo o resto da resolução segue inalterado.
#
# Para desfazer: sudo rm %s && sudo systemctl restart systemd-resolved

[Resolve]
DNS=%s
Domains=~%s
`

// Estado descreve a situação da resolução de domínios locais.
type Estado struct {
	Suportado     bool   `json:"supported"`
	ResolvedAtivo bool   `json:"resolved_active"`
	Configurado   bool   `json:"configured"`
	Resolvendo    bool   `json:"resolving"`
	Endereco      string `json:"address"`
	TLD           string `json:"tld"`
	Motivo        string `json:"reason,omitempty"`
}

// Verificar inspeciona se a resolução de domínios locais está de pé.
func Verificar(ctx context.Context, endereco, tld string) Estado {
	e := Estado{Endereco: endereco, TLD: tld}

	if runtime.GOOS != "linux" {
		e.Motivo = "a configuração automática de DNS só é suportada no Linux"
		return e
	}
	e.Suportado = true

	e.ResolvedAtivo = servicoAtivo(ctx, "systemd-resolved")
	if !e.ResolvedAtivo {
		e.Motivo = "systemd-resolved não está ativo"
	}

	if _, err := os.Stat(CaminhoDropIn); err == nil {
		e.Configurado = true
	}

	// A prova real não é o arquivo existir, é o nome resolver. Um drop-in
	// gravado mas sem o restart do resolved não faz nada, e reportar
	// "configurado" nesse estado seria mentira útil para ninguém.
	e.Resolvendo = resolve(ctx, "devmanager-verificacao."+tld)

	return e
}

// resolve testa se um nome do nosso TLD chega ao loopback.
func resolve(ctx context.Context, nome string) bool {
	ctx, cancelar := context.WithTimeout(ctx, 3*time.Second)
	defer cancelar()

	var r net.Resolver
	ips, err := r.LookupHost(ctx, nome)
	if err != nil {
		return false
	}

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

// ConteudoDropIn monta o arquivo de configuração.
func ConteudoDropIn(endereco, tld string) string {
	return fmt.Sprintf(dropIn, tld, CaminhoDropIn, endereco, tld)
}

// ComandosDeInstalacao monta o que o usuário precisa rodar como root.
//
// Não executamos nada com sudo: mexer na resolução de nomes da máquina é uma
// mudança que afeta TODO o sistema, não só o Dev Manager. Imprimir os
// comandos exatos deixa a decisão — e a auditoria do que será feito — com
// quem tem a senha.
func ComandosDeInstalacao(endereco, tld string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "sudo mkdir -p %s\n", dirDe(CaminhoDropIn))
	fmt.Fprintf(&b, "sudo tee %s > /dev/null <<'FIM'\n", CaminhoDropIn)
	b.WriteString(ConteudoDropIn(endereco, tld))
	b.WriteString("FIM\n")
	b.WriteString("sudo systemctl restart systemd-resolved\n")

	return b.String()
}

// ComandosDeRemocao desfaz a configuração.
func ComandosDeRemocao() string {
	return fmt.Sprintf("sudo rm %s\nsudo systemctl restart systemd-resolved\n", CaminhoDropIn)
}

func dirDe(caminho string) string {
	if i := strings.LastIndexByte(caminho, '/'); i > 0 {
		return caminho[:i]
	}
	return "/"
}
