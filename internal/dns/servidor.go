// Package dns resolve os domínios locais do Dev Manager.
//
// Com portas alocadas automaticamente, o domínio é o ÚNICO nome estável de um
// projeto: ninguém decora http://127.0.0.1:38501. É o DNS que fecha a
// experiência que o resto da ferramenta preparou.
//
// A implementação usa github.com/miekg/dns, e a escolha merece explicação
// porque contradiz aparentemente a decisão do proxy, onde recusamos o Caddy.
// O critério é o mesmo nos dois casos: a stdlib tem httputil.ReverseProxy,
// completo para o que precisávamos, então uma dependência ali seria peso
// gratuito. Ela NÃO tem servidor DNS — e escrever um à mão significa acertar
// compressão de nomes, EDNS e casos de borda do formato binário, onde um erro
// sutil vira falha de resolução intermitente. São 3 dependências (a
// biblioteca, golang.org/x/net e x/sys), contra as 142 do Caddy.
package dns

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// PortaPadrao é onde o servidor escuta.
//
// 5354, e não 5353: esta última é do mDNS/Avahi, que costuma estar ativo em
// desktops Linux e brigaria pela porta.
const PortaPadrao = 5354

// TLDPadrao é o domínio de topo servido.
//
// ".test" é reservado pela RFC 6761 exatamente para isto: nunca será delegado
// na internet, então não há risco de um dia colidir com um domínio real. É o
// motivo de ".dev" ter deixado de servir — o Google comprou o TLD e passou a
// forçar HTTPS nele.
const TLDPadrao = "test"

// Servidor responde consultas dos domínios locais.
type Servidor struct {
	Porta int
	TLD   string
	Saida io.Writer

	// TTL das respostas. Curto de propósito: os domínios entram e saem a cada
	// start e stop, e um resolvedor guardando a resposta por horas mostraria
	// um projeto que já foi derrubado.
	TTL uint32

	mu       sync.Mutex
	udp, tcp *dns.Server
}

func Novo(porta int, saida io.Writer) *Servidor {
	if porta == 0 {
		porta = PortaPadrao
	}
	if saida == nil {
		saida = io.Discard
	}
	return &Servidor{Porta: porta, TLD: TLDPadrao, Saida: saida, TTL: 10}
}

// Endereco devolve onde o servidor atende.
func (s *Servidor) Endereco() string {
	return fmt.Sprintf("127.0.0.1:%d", s.Porta)
}

// Servir escuta em UDP e TCP até o contexto ser cancelado.
//
// Os dois protocolos são obrigatórios: UDP atende a esmagadora maioria das
// consultas, mas o resolvedor troca para TCP quando a resposta passa de 512
// bytes ou quando o bit de truncamento aparece. Servir só UDP funciona em
// teste e falha em uso real.
func (s *Servidor) Servir(ctx context.Context) error {
	handler := dns.HandlerFunc(s.atender)
	addr := s.Endereco()

	s.mu.Lock()
	s.udp = &dns.Server{Addr: addr, Net: "udp", Handler: handler}
	s.tcp = &dns.Server{Addr: addr, Net: "tcp", Handler: handler}
	udp, tcp := s.udp, s.tcp
	s.mu.Unlock()

	erros := make(chan error, 2)
	go func() { erros <- udp.ListenAndServe() }()
	go func() { erros <- tcp.ListenAndServe() }()

	s.logf("dns ouvindo em %s (udp e tcp), servindo .%s", addr, s.TLD)

	select {
	case err := <-erros:
		return err
	case <-ctx.Done():
		return s.Encerrar()
	}
}

func (s *Servidor) Encerrar() error {
	s.mu.Lock()
	udp, tcp := s.udp, s.tcp
	s.mu.Unlock()

	ctx, cancelar := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelar()

	if udp != nil {
		_ = udp.ShutdownContext(ctx)
	}
	if tcp != nil {
		_ = tcp.ShutdownContext(ctx)
	}
	return nil
}

// atender responde uma consulta.
//
// TODO domínio sob o TLD é resolvido para o loopback, inclusive os que não
// correspondem a projeto nenhum. É deliberado: quem digita um domínio errado
// recebe a página do proxy dizendo quais projetos existem, em vez de um
// NXDOMAIN que o navegador traduz como "servidor não encontrado". A mensagem
// útil vale mais que a exatidão.
func (s *Servidor) atender(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true

	for _, q := range req.Question {
		nome := strings.ToLower(strings.TrimSuffix(q.Name, "."))

		if !s.atende(nome) {
			// Fora do nosso TLD: recusamos em vez de mentir. Responder
			// NXDOMAIN para um domínio real seria sequestrar a resolução.
			resp.Rcode = dns.RcodeRefused
			continue
		}

		switch q.Qtype {
		case dns.TypeA:
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: s.TTL},
				A:   net.IPv4(127, 0, 0, 1),
			})

		default:
			// Os demais tipos recebem NOERROR sem resposta — a forma correta
			// de dizer "o nome existe, mas não tem esse registro". NXDOMAIN
			// aqui faria o cliente concluir que o domínio inteiro não existe.
			//
			// AAAA entra neste grupo de propósito. O proxy escuta só em
			// 127.0.0.1; responder ::1 mandaria o navegador tentar primeiro
			// uma porta fechada em IPv6, e só depois cair para o IPv4.
		}
	}

	if err := w.WriteMsg(resp); err != nil {
		s.logf("erro respondendo consulta: %v", err)
	}
}

// atende informa se o nome pertence ao TLD servido.
func (s *Servidor) atende(nome string) bool {
	tld := strings.ToLower(s.TLD)
	return nome == tld || strings.HasSuffix(nome, "."+tld)
}

func (s *Servidor) logf(formato string, args ...any) {
	fmt.Fprintf(s.Saida, "%s  %s\n",
		time.Now().Format(time.RFC3339), fmt.Sprintf("dns: "+formato, args...))
}
