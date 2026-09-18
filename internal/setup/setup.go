// Package setup diagnostica e descreve a configuração de sistema que o Dev
// Manager precisa.
//
// São três ajustes que exigem root, cada um feito UMA vez por máquina. O
// pacote existe para que o usuário não precise decorá-los nem caçá-los na
// documentação: `devm setup` diz o que falta e entrega os comandos prontos.
package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	devmdns "github.com/AlexRogaleski/devmanager/internal/dns"
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

// Diagnosticar monta o plano a partir do estado atual da máquina.
func Diagnosticar(ctx context.Context, enderecoDNS, tld, certCA string) Plano {
	return Plano{Passos: []Passo{
		passoDNS(ctx, enderecoDNS, tld),
		passoPortas(),
		passoCA(certCA),
	}}
}

// passoDNS verifica a resolução dos domínios locais.
func passoDNS(ctx context.Context, endereco, tld string) Passo {
	estado := devmdns.Verificar(ctx, endereco, tld)

	p := Passo{
		Nome:   "resolução de " + tld,
		Porque: "sem isso, " + tld + " não resolve e os projetos só abrem por 127.0.0.1:porta",
		Feito:  estado.Resolvendo,
	}

	if p.Feito {
		return p
	}
	if !estado.Suportado {
		p.Detalhe = estado.Motivo
		return p
	}
	if !estado.ResolvedAtivo {
		p.Detalhe = "systemd-resolved não está ativo; configure o DNS manualmente"
		return p
	}

	p.Comandos = strings.Split(strings.TrimSpace(devmdns.ComandosDeInstalacao(endereco, tld)), "\n")
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
		p.Feito = true
		p.Detalhe = "não se aplica fora do Linux"
		return p
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

func portaMinimaAtual() (int, error) {
	dados, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(dados)))
}

// passoCA instala o certificado local como autoridade confiável.
func passoCA(cert string) Passo {
	p := Passo{
		Nome:   "confiar no certificado local",
		Porque: "sem isso, o HTTPS dos domínios locais aparece como inseguro",
	}

	if cert == "" {
		p.Detalhe = "o proxy não está ativo; não há certificado para instalar"
		return p
	}

	destino, comandos := instalacaoDaCA(cert)
	if destino == "" {
		p.Detalhe = "instale " + cert + " manualmente como autoridade confiável"
		return p
	}

	if _, err := os.Stat(destino); err == nil {
		p.Feito = true
		p.Detalhe = "instalado em " + destino
		return p
	}

	p.Comandos = comandos
	return p
}

// instalacaoDaCA devolve o destino e os comandos, conforme a distribuição.
//
// Detectamos pelo diretório que existe, e não pelo /etc/os-release: derivadas
// como Aurora, Bazzite e Nobara reportam nomes próprios, e uma lista de
// distribuições conhecidas ficaria desatualizada a cada nova. O diretório de
// âncoras, esse, não muda.
func instalacaoDaCA(cert string) (string, []string) {
	candidatos := []struct {
		dir, nome, atualizar string
	}{
		{"/etc/pki/ca-trust/source/anchors", "devmanager.crt", "sudo update-ca-trust"},
		{"/usr/local/share/ca-certificates", "devmanager.crt", "sudo update-ca-certificates"},
	}

	for _, c := range candidatos {
		if info, err := os.Stat(c.dir); err != nil || !info.IsDir() {
			continue
		}
		destino := filepath.Join(c.dir, c.nome)
		return destino, []string{
			fmt.Sprintf("sudo cp %s %s", cert, destino),
			c.atualizar,
		}
	}
	return "", nil
}

// AvisoFirefox explica o armazenamento próprio do navegador.
//
// O Firefox ignora o armazenamento de certificados do sistema e mantém o
// dele. Não há comando para isso — é interface gráfica — então a única coisa
// honesta a fazer é avisar.
const AvisoFirefox = `o Firefox mantém um armazenamento de certificados próprio e ignora o do sistema:
  Configurações → Privacidade e Segurança → Certificados → Ver certificados
  → Autoridades → Importar → marque "confiar para identificar sites"`
