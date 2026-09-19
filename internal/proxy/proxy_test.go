package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// backendFalso sobe um servidor que devolve o que recebeu, para o teste
// inspecionar os cabeçalhos que o proxy montou.
func backendFalso(t *testing.T) (*httptest.Server, int) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Recebeu-Host", r.Host)
		w.Header().Set("X-Recebeu-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Recebeu-Fwd-Host", r.Header.Get("X-Forwarded-Host"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	porta, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return srv, porta
}

func proxyDeTeste(t *testing.T) (*Proxy, int) {
	t.Helper()

	_, porta := backendFalso(t)

	tab := NovaTabela()
	tab.Definir(Rota{Dominio: "app.test", Porta: porta, Projeto: "app"})

	ca, err := CarregarOuCriar(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Novo(tab, ca, nil), porta
}

func TestProxyRoteiaPorHost(t *testing.T) {
	p, _ := proxyDeTeste(t)

	req := httptest.NewRequest(http.MethodGet, "http://app.test/qualquer", nil)
	req.Host = "app.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperava 200. Corpo: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ok" {
		t.Errorf("corpo = %q", rec.Body.String())
	}
}

// A garantia mais importante do proxy: o Laravel monta URLs absolutas a
// partir do Host. Sem preservá-lo, todo link gerado apontaria para
// 127.0.0.1:39431 em vez do domínio — foi o que este teste trava.
func TestProxyPreservaOHostOriginal(t *testing.T) {
	p, porta := proxyDeTeste(t)

	req := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
	req.Host = "app.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Recebeu-Host"); got != "app.test" {
		t.Errorf("o backend recebeu Host %q, esperava app.test", got)
	}
	if strings.Contains(rec.Header().Get("X-Recebeu-Host"), strconv.Itoa(porta)) {
		t.Error("o Host foi trocado pelo destino interno")
	}
}

// Sem X-Forwarded-Proto, um site atrás de HTTPS geraria links http e o
// navegador bloquearia como conteúdo misto.
func TestProxyAnunciaOProtocolo(t *testing.T) {
	p, _ := proxyDeTeste(t)

	req := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
	req.Host = "app.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Recebeu-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q, esperava http", got)
	}
	if got := rec.Header().Get("X-Recebeu-Fwd-Host"); got != "app.test" {
		t.Errorf("X-Forwarded-Host = %q, esperava app.test", got)
	}
}

func TestProxyDominioDesconhecido(t *testing.T) {
	p, _ := proxyDeTeste(t)

	req := httptest.NewRequest(http.MethodGet, "http://nao-existe.test/", nil)
	req.Host = "nao-existe.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, esperava 404", rec.Code)
	}
	// A resposta precisa ajudar: listar o que existe é mais útil que só
	// dizer que não achou.
	corpo := rec.Body.String()
	if !strings.Contains(corpo, "app.test") {
		t.Errorf("a resposta deveria listar os domínios conhecidos:\n%s", corpo)
	}
}

func TestProxySemNenhumaRota(t *testing.T) {
	ca, err := CarregarOuCriar(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Novo(NovaTabela(), ca, nil)

	req := httptest.NewRequest(http.MethodGet, "http://qualquer.test/", nil)
	req.Host = "qualquer.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "devm start -d") {
		t.Errorf("a resposta deveria orientar o próximo passo:\n%s", rec.Body.String())
	}
}

// Backend caído com rota ainda na tabela: um 502 cru deixaria a pessoa
// achando que o Dev Manager quebrou.
func TestProxyBackendCaido(t *testing.T) {
	tab := NovaTabela()
	// Porta onde ninguém atende.
	tab.Definir(Rota{Dominio: "morto.test", Porta: 1, Projeto: "morto"})

	ca, err := CarregarOuCriar(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Novo(tab, ca, nil)

	req := httptest.NewRequest(http.MethodGet, "http://morto.test/", nil)
	req.Host = "morto.test"
	rec := httptest.NewRecorder()

	p.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, esperava 502", rec.Code)
	}
	corpo := rec.Body.String()
	if !strings.Contains(corpo, "morto") {
		t.Errorf("a resposta deveria citar o projeto:\n%s", corpo)
	}
	if !strings.Contains(corpo, "devm logs") {
		t.Errorf("a resposta deveria orientar como investigar:\n%s", corpo)
	}
}

// O roteamento não pode depender de como o Host chegou.
func TestProxyRoteiaComVariacoesDeHost(t *testing.T) {
	p, _ := proxyDeTeste(t)

	for _, host := range []string{"app.test", "APP.test", "app.test:8080", "app.test."} {
		req := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
		req.Host = host
		rec := httptest.NewRecorder()

		p.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Host %q: status %d, esperava 200", host, rec.Code)
		}
	}
}

// TestEncerrarAntesDeServir fixa a correção de uma corrida de dados real,
// encontrada pelo -race no CI e não reproduzível localmente.
//
// O daemon chama Escutar na sua goroutine, lança `go p.Servir(ctx)` e, ao
// receber o cancelamento, chama Encerrar de uma terceira. Enquanto os
// http.Server eram criados dentro de Servir, um cancelamento logo após a
// partida fazia Encerrar lê-los enquanto Servir os escrevia.
//
// O teste não tenta recriar o entrelaçamento — corrida não se testa por
// repetição, se testa eliminando a escrita concorrente. O que ele fixa é a
// propriedade que passou a valer: depois de montados, os servidores existem
// independentemente de Servir ter rodado, e a ordem entre parar e servir
// deixou de importar.
//
// Nada de portas fixas aqui: o teste do daemon já ocupa a 8080, e os pacotes
// rodam em paralelo — um segundo teste disputando a mesma porta trocaria uma
// intermitência por outra.
func TestEncerrarAntesDeServir(t *testing.T) {
	p := Novo(NovaTabela(), nil, nil)
	p.montarServidores()

	if p.srvHTTP == nil || p.srvHTTPS == nil {
		t.Fatal("montarServidores deixou algum servidor nulo")
	}

	if err := p.Encerrar(); err != nil {
		t.Fatalf("Encerrar antes de Servir devolveu erro: %v", err)
	}

	// A premissa da correção: servir DEPOIS de encerrar não pode ficar
	// atendendo. Se isto mudasse no Go, o proxy voltaria a segurar a porta
	// depois de mandado parar — e é melhor descobrir aqui.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	pronto := make(chan error, 1)
	go func() { pronto <- p.srvHTTP.Serve(ln) }()

	select {
	case err := <-pronto:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve devolveu %v, esperava ErrServerClosed", err)
		}
		if ignorarFechamento(err) != nil {
			t.Errorf("ignorarFechamento deveria tratar isso como fim normal: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve continuou atendendo depois do Shutdown")
	}
}

// TestServirEEncerrarConcorrentes exercita a partida e a parada ao mesmo
// tempo, que é o que o daemon faz: Escutar na goroutine dele, `go Servir` em
// outra, e Encerrar numa terceira quando o contexto cai.
//
// Ele complementa o TestEncerrarAntesDeServir, que fixa a invariante. Este
// aqui é o que DETECTA: se alguém voltar a escrever estado dentro de Servir,
// o -race acusa nestas iterações, e não meses depois num runner de CI.
//
// Os listeners são injetados em vez de vir de Escutar porque Escutar abre as
// portas 80 e 443 de verdade — as efêmeras exercitam o mesmo caminho sem
// disputar porta com ninguém.
func TestServirEEncerrarConcorrentes(t *testing.T) {
	for i := 0; i < 200; i++ {
		p := Novo(NovaTabela(), nil, nil)

		var err error
		if p.lnHTTP, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		if p.lnHTTPS, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		p.montarServidores()

		ctx, cancelar := context.WithCancel(context.Background())
		servindo := make(chan struct{})
		go func() {
			defer close(servindo)
			_ = p.Servir(ctx)
		}()

		if err := p.Encerrar(); err != nil {
			t.Fatalf("Encerrar concorrente falhou: %v", err)
		}
		cancelar()
		<-servindo

		p.lnHTTP.Close()
		p.lnHTTPS.Close()
	}
}

// TestProxyNaoAceitaConexaoDaRede abre o listener do proxy e tenta chegar
// nele pelo IP de rede da máquina, como faria alguém na mesma rede.
//
// O teste confere o comportamento, não a string do endereço: um "127.0.0.1"
// na constante não vale nada se alguém passar a usar outra função para
// escutar. Até a v0.1.0 o proxy abria todas as interfaces, e esta conexão
// era atendida.
func TestProxyNaoAceitaConexaoDaRede(t *testing.T) {
	ipDeRede := primeiroIPv4DeRede()
	if ipDeRede == nil {
		t.Skip("a máquina não tem interface de rede além do loopback")
	}

	ln, err := escutar(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	porta := ln.Addr().(*net.TCPAddr).Port

	// Pelo loopback, conecta — senão o teste abaixo passaria por o
	// listener estar simplesmente quebrado.
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(porta)), time.Second); err != nil {
		t.Fatalf("o proxy precisa aceitar pelo loopback: %v", err)
	} else {
		c.Close()
	}

	alvo := net.JoinHostPort(ipDeRede.String(), strconv.Itoa(porta))
	if c, err := net.DialTimeout("tcp", alvo, time.Second); err == nil {
		c.Close()
		t.Fatalf("o proxy aceitou conexão por %s — está exposto na rede", alvo)
	}
}

func primeiroIPv4DeRede() net.IP {
	enderecos, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, e := range enderecos {
		rede, ok := e.(*net.IPNet)
		if !ok {
			continue
		}
		if ip := rede.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip
		}
	}
	return nil
}

// TestSomenteLoopbackFechaQuemVemDaRede exercita o filtro que o macOS usa
// quando o kernel nega a porta 80 no 127.0.0.1: escutar em 0.0.0.0 e recusar
// no Accept o que não vier do loopback.
//
// A proteção fica no nosso código, e não no kernel, então precisa de teste
// próprio — o TestProxyNaoAceitaConexaoDaRede cobre o caso em que é o kernel
// quem recusa.
func TestSomenteLoopbackFechaQuemVemDaRede(t *testing.T) {
	ipDeRede := primeiroIPv4DeRede()
	if ipDeRede == nil {
		t.Skip("a máquina não tem interface de rede além do loopback")
	}

	todas, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := somenteLoopback{todas}
	defer ln.Close()
	porta := strconv.Itoa(todas.Addr().(*net.TCPAddr).Port)

	aceitas := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(aceitas)
				return
			}
			aceitas <- c
		}
	}()

	// Pela rede: o handshake completa (é o kernel quem responde), mas a
	// conexão é fechada sem um byte — a leitura termina, sem dados.
	deFora, err := net.DialTimeout("tcp", net.JoinHostPort(ipDeRede.String(), porta), time.Second)
	if err != nil {
		t.Fatalf("o handshake pelo IP de rede deveria completar: %v", err)
	}
	defer deFora.Close()
	deFora.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := deFora.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("a conexão de fora deveria ser fechada sem dados (n=%d, err=%v)", n, err)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("a conexão de fora ficou aberta: o filtro não a fechou")
	}

	// Pelo loopback: chega ao Accept.
	deDentro, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", porta), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer deDentro.Close()

	select {
	case c := <-aceitas:
		defer c.Close()
		if !c.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
			t.Errorf("o Accept entregou uma conexão de fora: %v", c.RemoteAddr())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a conexão do loopback não chegou ao Accept")
	}

	// E a de fora nunca chegou: só a do loopback está no canal.
	select {
	case c := <-aceitas:
		t.Errorf("uma segunda conexão chegou ao Accept: %v", c.RemoteAddr())
	default:
	}
}

// No Linux o recurso do macOS nunca entra: lá o loopback é liberado pelo
// sysctl, e a proteção pelo kernel é a mais forte.
func TestEscutarNoLinuxNuncaUsaOFiltro(t *testing.T) {
	ln, err := escutarEm("linux", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, filtrado := ln.(somenteLoopback); filtrado {
		t.Error("no Linux o listener deveria ser o do loopback, não o filtrado")
	}
	if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Errorf("endereço = %v, esperava loopback", ln.Addr())
	}
}
