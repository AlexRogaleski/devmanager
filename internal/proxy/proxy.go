package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// Portas padrão e alternativas.
//
// As alternativas existem porque 80 e 443 são portas privilegiadas: sem
// CAP_NET_BIND_SERVICE no binário, o kernel recusa o bind e o daemon inteiro
// falharia. Cair para portas altas mantém o proxy funcionando — com URLs
// menos bonitas — e o comando para corrigir fica impresso.
const (
	PortaHTTPPadrao    = 80
	PortaHTTPSPadrao   = 443
	PortaHTTPFallback  = 8080
	PortaHTTPSFallback = 8443
)

// Proxy roteia domínios locais para os servidores dos projetos.
type Proxy struct {
	Tabela *Tabela
	CA     *CA
	Saida  io.Writer

	// Portas efetivamente em uso, preenchidas por Escutar.
	PortaHTTP  int
	PortaHTTPS int

	// SemPrivilegio indica que caímos para as portas alternativas.
	SemPrivilegio bool

	// MotivoDaQueda explica por que as portas padrão não foram usadas.
	MotivoDaQueda string

	lnHTTP, lnHTTPS   net.Listener
	srvHTTP, srvHTTPS *http.Server
}

func Novo(tabela *Tabela, ca *CA, saida io.Writer) *Proxy {
	if saida == nil {
		saida = io.Discard
	}
	return &Proxy{Tabela: tabela, CA: ca, Saida: saida}
}

// Escutar abre as portas, caindo para as alternativas se faltar privilégio.
func (p *Proxy) Escutar() error {
	var err error

	var motivo string

	p.lnHTTP, p.PortaHTTP, motivo, err = escutarCom(PortaHTTPPadrao, PortaHTTPFallback)
	if err != nil {
		return fmt.Errorf("abrindo a porta HTTP: %w", err)
	}

	p.lnHTTPS, p.PortaHTTPS, _, err = escutarCom(PortaHTTPSPadrao, PortaHTTPSFallback)
	if err != nil {
		p.lnHTTP.Close()
		return fmt.Errorf("abrindo a porta HTTPS: %w", err)
	}

	p.SemPrivilegio = p.PortaHTTP != PortaHTTPPadrao
	p.MotivoDaQueda = motivo
	p.montarServidores()
	return nil
}

// montarServidores cria os http.Server AQUI, e não dentro de Servir.
//
// A diferença é de concorrência, não de estilo. O daemon chama Escutar na
// própria goroutine e só depois lança `go p.Servir(ctx)`; Encerrar, por sua
// vez, é chamado de uma terceira. Com a construção dentro de Servir, um
// cancelamento logo após a partida fazia Encerrar LER p.srvHTTP enquanto
// Servir o ESCREVIA — corrida de dados de verdade, detectada pelo -race.
//
// E o defeito era pior que a corrida: se Encerrar chegasse primeiro, via
// nil, não encerrava nada, e Servir seguia adiante servindo — o proxy
// continuava de pé depois de mandado parar, segurando as portas 80 e 443.
//
// Montando aqui, os campos são escritos uma vez só, antes de qualquer
// goroutine existir, e depois só são lidos. A ordem entre Encerrar e Servir
// deixa de importar: Shutdown antes de Serve faz o Serve devolver
// ErrServerClosed na hora, que ignorarFechamento trata como fim normal.
func (p *Proxy) montarServidores() {
	handler := http.HandlerFunc(p.atender)

	p.srvHTTP = &http.Server{
		Handler: handler,
		// Sem ReadHeaderTimeout, uma conexão que abre e não fala segura um
		// descritor de arquivo indefinidamente.
		ReadHeaderTimeout: 20 * time.Second,
	}

	p.srvHTTPS = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// GetCertificate é chamado no handshake, com o domínio que o
			// cliente pediu via SNI. É isso que permite emitir o certificado
			// sob demanda: um projeto novo funciona no primeiro acesso, sem
			// nenhum passo de configuração.
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				nome := hello.ServerName
				if nome == "" {
					// Cliente sem SNI (IP direto, ferramenta antiga): não há
					// domínio para certificar.
					return nil, fmt.Errorf("conexão TLS sem indicação de domínio")
				}
				return p.CA.CertificadoPara(nome)
			},
		},
	}
}

// escutarCom tenta a porta desejada e cai para a alternativa se ela não der.
//
// Cai nos DOIS casos — sem permissão e porta ocupada — e devolve o motivo
// para que a mensagem ao usuário seja específica, porque os consertos são
// diferentes: falta de permissão se resolve com setcap, porta ocupada se
// resolve parando quem está lá.
//
// A primeira versão desistia quando a porta estava ocupada, com o argumento
// de que cair para a alternativa esconderia um conflito real. O argumento
// estava errado na prática: numa máquina de desenvolvimento é comum outra
// ferramenta legitimamente ocupar a 80 — um Sail, um Apache do sistema — e
// perder o proxy INTEIRO por causa disso é desproporcional. Reportar a queda
// resolve o risco de esconder o conflito sem custar a funcionalidade.
func escutarCom(desejada, alternativa int) (net.Listener, int, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", desejada))
	if err == nil {
		return ln, desejada, "", nil
	}

	motivo := fmt.Sprintf("a porta %d está ocupada", desejada)
	if errors.Is(err, os.ErrPermission) {
		motivo = fmt.Sprintf("sem permissão para a porta %d", desejada)
	}

	alt, err := net.Listen("tcp", fmt.Sprintf(":%d", alternativa))
	if err != nil {
		// As duas falharam: aí sim não há o que fazer, e o erro precisa
		// mencionar as duas portas para o diagnóstico ser possível.
		return nil, 0, "", fmt.Errorf("%s, e a alternativa %d também falhou: %w", motivo, alternativa, err)
	}
	return alt, alternativa, motivo, nil
}

// Servir atende até o contexto ser cancelado.
func (p *Proxy) Servir(ctx context.Context) error {
	if p.lnHTTP == nil || p.srvHTTP == nil {
		return errors.New("Escutar precisa ser chamado antes de Servir")
	}

	erros := make(chan error, 2)
	go func() { erros <- ignorarFechamento(p.srvHTTP.Serve(p.lnHTTP)) }()
	go func() { erros <- ignorarFechamento(p.srvHTTPS.ServeTLS(p.lnHTTPS, "", "")) }()

	p.logf("proxy ouvindo em http://:%d e https://:%d", p.PortaHTTP, p.PortaHTTPS)
	if p.MotivoDaQueda != "" {
		p.logf("%s — usando %d e %d", p.MotivoDaQueda, p.PortaHTTP, p.PortaHTTPS)
	}

	select {
	case err := <-erros:
		return err
	case <-ctx.Done():
		return p.Encerrar()
	}
}

func (p *Proxy) Encerrar() error {
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()

	if p.srvHTTP != nil {
		_ = p.srvHTTP.Shutdown(ctx)
	}
	if p.srvHTTPS != nil {
		_ = p.srvHTTPS.Shutdown(ctx)
	}
	return nil
}

// Handler devolve o roteador, sem abrir portas.
//
// Exposto para que testes exercitem o roteamento contra um backend de
// mentira, sem precisar de portas reais nem de privilégio.
func (p *Proxy) Handler() http.Handler { return http.HandlerFunc(p.atender) }

// atender roteia uma requisição para o projeto correspondente.
func (p *Proxy) atender(w http.ResponseWriter, r *http.Request) {
	rota, ok := p.Tabela.Buscar(r.Host)
	if !ok {
		p.semRota(w, r)
		return
	}

	destino := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", rota.Porta)}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(destino)

			// SetURL troca o Host de saída pelo do destino. Repor o
			// original é essencial: o Laravel monta URLs absolutas a partir
			// dele, e sem isso todo link gerado apontaria para
			// 127.0.0.1:39431 em vez de fapcen.test.
			pr.Out.Host = pr.In.Host

			// X-Forwarded-For, -Host e -Proto. O Proto é o que faz o
			// Laravel gerar https:// quando o acesso veio por TLS — sem
			// ele, um site atrás de HTTPS geraria links http e o navegador
			// bloquearia como conteúdo misto.
			pr.SetXForwarded()
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// O servidor do projeto morreu, mas a rota continua na tabela.
			// Um 502 cru deixaria a pessoa achando que o Dev Manager quebrou.
			p.logf("erro ao falar com %s (%s): %v", rota.Projeto, destino.Host, err)
			paginaErro(w, http.StatusBadGateway, fmt.Sprintf(
				"O projeto %s não respondeu em %s.", rota.Projeto, destino.Host),
				"O ambiente pode ter caído. Verifique com `devm ps` e os logs com `devm logs "+rota.Projeto+"`.")
		},
	}

	rp.ServeHTTP(w, r)
}

// semRota responde a um domínio que não conhecemos.
func (p *Proxy) semRota(w http.ResponseWriter, r *http.Request) {
	rotas := p.Tabela.Listar()

	if len(rotas) == 0 {
		paginaErro(w, http.StatusNotFound,
			fmt.Sprintf("Nenhum projeto atende %s.", normalizar(r.Host)),
			"Nenhum ambiente está rodando. Suba um com `devm start -d` na pasta de um projeto.")
		return
	}

	var b strings.Builder
	b.WriteString("Projetos disponíveis:\n\n")
	for _, rota := range rotas {
		b.WriteString("  " + rota.Dominio + "\n")
	}

	paginaErro(w, http.StatusNotFound,
		fmt.Sprintf("Nenhum projeto atende %s.", normalizar(r.Host)), b.String())
}

// paginaErro responde em texto puro.
//
// Texto, e não HTML: a resposta é lida tanto no navegador quanto num curl,
// e um HTML estilizado ficaria ilegível no terminal — que é onde quem
// desenvolve costuma estar quando algo não responde.
func paginaErro(w http.ResponseWriter, status int, titulo, detalhe string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)

	fmt.Fprintf(w, "Dev Manager\n\n%s\n\n%s\n", titulo, detalhe)
}

func ignorarFechamento(err error) error {
	// ErrServerClosed é o retorno NORMAL de um Shutdown, não uma falha.
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (p *Proxy) logf(formato string, args ...any) {
	fmt.Fprintf(p.Saida, "%s  proxy: %s\n",
		time.Now().Format(time.RFC3339), fmt.Sprintf(formato, args...))
}
