package dns

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// servidorDeTeste sobe o servidor numa porta livre e devolve o endereço.
func servidorDeTeste(t *testing.T) string {
	t.Helper()

	// Pede uma porta livre ao kernel e libera: a mesma técnica usada para os
	// servidores dos projetos.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	porta := l.Addr().(*net.TCPAddr).Port
	l.Close()

	s := Novo(porta, nil)
	ctx, parar := context.WithCancel(context.Background())
	pronto := make(chan struct{})

	go func() {
		defer close(pronto)
		_ = s.Servir(ctx)
	}()

	t.Cleanup(func() {
		parar()
		select {
		case <-pronto:
		case <-time.After(5 * time.Second):
			t.Error("o servidor DNS não encerrou a tempo")
		}
	})

	endereco := s.Endereco()
	esperarDNS(t, endereco)
	return endereco
}

func esperarDNS(t *testing.T, endereco string) {
	t.Helper()

	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if _, err := consultar(endereco, "x.test.", dns.TypeA, "udp"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("o servidor DNS não respondeu")
}

func consultar(endereco, nome string, tipo uint16, rede string) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(nome, tipo)

	c := &dns.Client{Net: rede, Timeout: 3 * time.Second}
	resp, _, err := c.Exchange(m, endereco)
	return resp, err
}

func TestRespondeA(t *testing.T) {
	endereco := servidorDeTeste(t)

	resp, err := consultar(endereco, "fapcen.test.", dns.TypeA, "udp")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("respostas = %d, esperava 1", len(resp.Answer))
	}

	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("resposta = %T, esperava *dns.A", resp.Answer[0])
	}
	if !a.A.IsLoopback() {
		t.Errorf("A = %v, esperava loopback", a.A)
	}
}

func TestRespondeAAAA(t *testing.T) {
	endereco := servidorDeTeste(t)

	resp, err := consultar(endereco, "app.test.", dns.TypeAAAA, "udp")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("respostas = %d, esperava 1", len(resp.Answer))
	}

	aaaa, ok := resp.Answer[0].(*dns.AAAA)
	if !ok {
		t.Fatalf("resposta = %T, esperava *dns.AAAA", resp.Answer[0])
	}
	if !aaaa.AAAA.IsLoopback() {
		t.Errorf("AAAA = %v, esperava loopback", aaaa.AAAA)
	}
}

// TCP não é opcional: o resolvedor troca para ele quando a resposta passa de
// 512 bytes ou vem truncada. Servir só UDP funciona em teste e falha em uso.
func TestRespondeEmTCP(t *testing.T) {
	endereco := servidorDeTeste(t)

	resp, err := consultar(endereco, "app.test.", dns.TypeA, "tcp")
	if err != nil {
		t.Fatalf("consulta TCP falhou: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Errorf("respostas = %d, esperava 1", len(resp.Answer))
	}
}

// Responder por um domínio real seria sequestrar a resolução da máquina.
func TestRecusaDominiosDeFora(t *testing.T) {
	endereco := servidorDeTeste(t)

	for _, nome := range []string{"google.com.", "exemplo.com.br.", "localhost."} {
		resp, err := consultar(endereco, nome, dns.TypeA, "udp")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Rcode != dns.RcodeRefused {
			t.Errorf("%s: rcode = %s, esperava REFUSED", nome, dns.RcodeToString[resp.Rcode])
		}
		if len(resp.Answer) != 0 {
			t.Errorf("%s: respondeu por um domínio de fora", nome)
		}
	}
}

// Domínio do nosso TLD sem projeto ainda resolve: o usuário recebe a página
// do proxy listando os projetos, em vez de um "servidor não encontrado".
func TestResolveMesmoSemProjeto(t *testing.T) {
	endereco := servidorDeTeste(t)

	resp, err := consultar(endereco, "nunca-existiu.test.", dns.TypeA, "udp")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Answer) != 1 {
		t.Errorf("respostas = %d — todo .test deve resolver para o loopback", len(resp.Answer))
	}
}

// Tipo sem registro devolve NOERROR vazio, não NXDOMAIN: o nome existe, só
// não tem MX. NXDOMAIN faria o cliente concluir que o domínio não existe.
func TestTipoSemRegistro(t *testing.T) {
	endereco := servidorDeTeste(t)

	resp, err := consultar(endereco, "app.test.", dns.TypeMX, "udp")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("rcode = %s, esperava NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 0 {
		t.Errorf("respondeu MX: %v", resp.Answer)
	}
}

func TestAtendeReconheceOTLD(t *testing.T) {
	s := Novo(0, nil)

	dentro := []string{"test", "app.test", "sub.app.test", "APP.TEST"}
	for _, n := range dentro {
		if !s.atende(strings.ToLower(n)) {
			t.Errorf("atende(%q) = false, esperava true", n)
		}
	}

	fora := []string{"google.com", "test.com", "meutest", "app.testing"}
	for _, n := range fora {
		if s.atende(n) {
			t.Errorf("atende(%q) = true, esperava false", n)
		}
	}
}

// O "~" transforma o domínio em ROTEAMENTO. Sem ele, o .test viraria sufixo
// de BUSCA e o sistema tentaria resolver "google.com.test".
func TestDropInUsaDominioDeRoteamento(t *testing.T) {
	conteudo := ConteudoDropIn("127.0.0.1:5354", "test")

	if !strings.Contains(conteudo, "Domains=~test") {
		t.Errorf("faltou o prefixo ~ no domínio:\n%s", conteudo)
	}
	if !strings.Contains(conteudo, "DNS=127.0.0.1:5354") {
		t.Errorf("faltou o endereço do servidor:\n%s", conteudo)
	}
	// O arquivo precisa dizer como desfazer: quem o encontrar daqui a um ano
	// não vai lembrar de onde veio.
	if !strings.Contains(conteudo, "rm "+CaminhoDropIn) {
		t.Errorf("o arquivo deveria explicar como removê-lo:\n%s", conteudo)
	}
}

func TestComandosDeInstalacao(t *testing.T) {
	cmds := ComandosDeInstalacao("127.0.0.1:5354", "test")

	for _, esperado := range []string{
		"mkdir -p /etc/systemd/resolved.conf.d",
		"tee " + CaminhoDropIn,
		"systemctl restart systemd-resolved",
	} {
		if !strings.Contains(cmds, esperado) {
			t.Errorf("faltou %q:\n%s", esperado, cmds)
		}
	}
}
