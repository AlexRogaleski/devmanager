package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/dns"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
	"github.com/AlexRogaleski/devmanager/internal/registry"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// NomeDoSocket é o arquivo de socket dentro do diretório de runtime.
const NomeDoSocket = "devmanager.sock"

// Servidor é o daemon.
type Servidor struct {
	Socket string
	Versao string

	// Saida é o log do próprio daemon (não dos projetos).
	Saida io.Writer

	mu        sync.Mutex
	ambientes map[string]*ambiente

	// subidos são os serviços que ESTE daemon colocou de pé, por contêiner.
	// É o que distingue um serviço nosso de um que já estava rodando quando
	// chegamos — só o primeiro pode ser desligado quando fica ocioso.
	subidos map[string]services.Spec

	// tabela é compartilhada com o proxy: o daemon escreve as rotas quando
	// um ambiente sobe, e o proxy as lê a cada requisição.
	tabela *proxy.Tabela
	proxy  *proxy.Proxy
	dns    *dns.Servidor
	dnsErr string

	desdeQue time.Time
	ln       net.Listener
	http     *http.Server
}

// CaminhoDoSocket devolve onde o socket fica.
//
// XDG_RUNTIME_DIR (/run/user/1000) é o lugar certo: é um tmpfs por usuário,
// limpo no logout, e com permissão 0700 pelo próprio systemd. Colocar o
// socket no home o deixaria sobrevivendo a reboots como arquivo órfão, e num
// home compartilhado por rede seria pior ainda.
func CaminhoDoSocket() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, NomeDoSocket), nil
	}

	// Sem XDG_RUNTIME_DIR — sessão não gerenciada por systemd, ou macOS.
	// Cair para o diretório de dados é menos bom, mas funciona.
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, NomeDoSocket), nil
}

func NovoServidor(socket, versao string, saida io.Writer) *Servidor {
	if saida == nil {
		saida = io.Discard
	}
	return &Servidor{
		Socket:    socket,
		Versao:    versao,
		Saida:     saida,
		ambientes: make(map[string]*ambiente),
		tabela:    proxy.NovaTabela(),
		desdeQue:  time.Now(),
	}
}

// iniciarProxy sobe o proxy de domínios locais.
//
// Falhar aqui NÃO derruba o daemon: a porta 80 pode estar ocupada por um
// Apache do sistema, e perder a supervisão de processos por causa disso seria
// desproporcional. O proxy é um recurso a mais, não o coração da ferramenta.
func (s *Servidor) iniciarProxy(ctx context.Context) {
	dir, err := proxy.DirPadrao()
	if err != nil {
		s.logf("proxy indisponível: %v", err)
		return
	}

	ca, err := proxy.CarregarOuCriar(dir)
	if err != nil {
		s.logf("proxy indisponível: %v", err)
		return
	}

	p := proxy.Novo(s.tabela, ca, s.Saida)
	if err := p.Escutar(); err != nil {
		s.logf("proxy indisponível: %v", err)
		return
	}

	s.proxy = p
	go func() {
		if err := p.Servir(ctx); err != nil {
			s.logf("proxy encerrado com erro: %v", err)
		}
	}()

	s.iniciarDNS(ctx)
}

// iniciarDNS sobe o servidor de domínios locais.
//
// Como o proxy, falhar aqui não derruba o daemon: sem DNS os projetos ainda
// respondem por 127.0.0.1:porta, só perdem o nome amigável.
func (s *Servidor) iniciarDNS(ctx context.Context) {
	d := dns.Novo(dns.PortaPadrao, s.Saida)

	// Uma escuta de teste antes de entregar ao servidor detecta porta
	// ocupada aqui, e não numa goroutine cujo erro ninguém veria.
	teste, err := net.Listen("tcp", d.Endereco())
	if err != nil {
		s.dnsErr = err.Error()
		s.logf("dns indisponível: %v", err)
		return
	}
	teste.Close()

	s.dns = d
	go func() {
		if err := d.Servir(ctx); err != nil {
			s.logf("dns encerrado com erro: %v", err)
		}
	}()

	go s.avisarResolvedor(ctx, d)
}

// avisarResolvedor diz ao resolvedor do sistema que o servidor DNS voltou.
//
// Sem isso, depois de um reboot em que algo consultou um .test com o daemon
// parado, o systemd-resolved desiste do servidor e nunca mais tenta: o daemon
// sobe, responde perfeitamente, e nenhuma consulta chega até ele. O sintoma
// é "o .test parou de funcionar depois que reiniciei o computador".
//
// É melhor esforço. Falhar aqui não derruba nada — só registra o comando que
// resolveria, para quem for investigar pelo `devm daemon logs`.
func (s *Servidor) avisarResolvedor(ctx context.Context, d *dns.Servidor) {
	select {
	case <-d.Pronto():
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
		s.logf("dns: o servidor não ficou pronto a tempo; o resolvedor do sistema não foi avisado")
		return
	}

	feitos, err := dns.NotificarSistema(ctx, d.TLD)
	if err != nil {
		s.logf("dns: não consegui avisar o resolvedor do sistema (%v); se o .%s não resolver, rode o comando à mão",
			err, d.TLD)
		return
	}
	if len(feitos) > 0 {
		s.logf("dns: resolvedor do sistema avisado (%s)", strings.Join(feitos, "; "))
	}
}

// InfoProxy descreve o estado do proxy para a API.
func (s *Servidor) InfoProxy() Proxy {
	if s.proxy == nil {
		return Proxy{Ativo: false}
	}

	return Proxy{
		Ativo:         true,
		PortaHTTP:     s.proxy.PortaHTTP,
		PortaHTTPS:    s.proxy.PortaHTTPS,
		SemPrivilegio: s.proxy.SemPrivilegio,
		MotivoDaQueda: s.proxy.MotivoDaQueda,
		CertificadoCA: s.proxy.CA.CaminhoDoCertificado(),
		Dominios:      s.tabela.Dominios(),
		DNS:           s.infoDNS(),
	}
}

func (s *Servidor) infoDNS() DNS {
	if s.dns == nil {
		return DNS{Motivo: s.dnsErr}
	}
	return DNS{Ativo: true, Endereco: s.dns.Endereco(), TLD: s.dns.TLD}
}

// Escutar abre o socket, recusando subir se já há um daemon.
func (s *Servidor) Escutar() error {
	if err := os.MkdirAll(filepath.Dir(s.Socket), 0o700); err != nil {
		return fmt.Errorf("criando diretório do socket: %w", err)
	}

	// Socket existente pode ser de um daemon vivo ou o cadáver de um que
	// morreu sem limpar. A única forma confiável de distinguir é tentar
	// conversar: se alguém responde, há daemon; se não, o arquivo é lixo.
	if _, err := os.Stat(s.Socket); err == nil {
		conn, err := net.DialTimeout("unix", s.Socket, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return &JaRodandoError{Socket: s.Socket}
		}
		if err := os.Remove(s.Socket); err != nil {
			return fmt.Errorf("removendo socket órfão %s: %w", s.Socket, err)
		}
		s.logf("removido socket órfão em %s", s.Socket)
	}

	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return fmt.Errorf("abrindo %s: %w", s.Socket, err)
	}

	// 0600 é obrigatório, não zelo excessivo: esta API executa comandos
	// arbitrários no ambiente do usuário. Um socket legível por outros
	// usuários da máquina seria execução remota de código.
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("ajustando permissões do socket: %w", err)
	}

	s.ln = ln
	return nil
}

// Servir atende requisições até o contexto ser cancelado.
func (s *Servidor) Servir(ctx context.Context) error {
	if s.ln == nil {
		return errors.New("Escutar precisa ser chamado antes de Servir")
	}

	s.iniciarProxy(ctx)

	s.http = &http.Server{Handler: s.rotas()}

	// Uma goroutine espera o cancelamento para encerrar o servidor: o
	// http.Serve bloqueia, e é o Shutdown que o faz retornar.
	erros := make(chan error, 1)
	go func() {
		err := s.http.Serve(s.ln)
		// ErrServerClosed é o retorno NORMAL de um Shutdown, não uma falha.
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		erros <- err
	}()

	s.logf("daemon ouvindo em %s (pid %d)", s.Socket, os.Getpid())

	select {
	case err := <-erros:
		return err
	case <-ctx.Done():
		return s.Encerrar()
	}
}

// Encerrar derruba todos os ambientes e fecha o socket.
func (s *Servidor) Encerrar() error {
	s.logf("encerrando %d ambiente(s)...", len(s.listar()))

	// Cancela todos primeiro, depois espera: cancelar em série faria o tempo
	// total ser a soma dos encerramentos em vez do mais lento deles.
	s.mu.Lock()
	ambientes := make([]*ambiente, 0, len(s.ambientes))
	for _, amb := range s.ambientes {
		ambientes = append(ambientes, amb)
		amb.cancelar()
	}
	// Esquecer os ambientes aqui é o que torna os serviços deles ociosos:
	// enquanto estiverem no mapa, a contabilidade os considera em uso e
	// nada é desligado.
	clear(s.ambientes)
	s.mu.Unlock()

	prazo := time.After(20 * time.Second)
	for _, amb := range ambientes {
		select {
		case <-amb.encerrado:
		case <-prazo:
			s.logf("aviso: %s não encerrou a tempo", amb.nome)
		}
		amb.anel.Fechar()
	}

	// Com todos os ambientes derrubados, nenhum serviço nosso tem mais
	// usuário: o daemon sai sem deixar contêiner ligado para trás.
	s.pararServicosOciosos(context.Background())

	if s.dns != nil {
		_ = s.dns.Encerrar()
	}
	if s.proxy != nil {
		_ = s.proxy.Encerrar()
	}

	if s.http != nil {
		ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelar()
		_ = s.http.Shutdown(ctx)
	}

	// O socket precisa sair do disco: deixá-lo faria o próximo daemon gastar
	// tempo detectando órfão, e um `ls` confundiria quem investigasse.
	_ = os.Remove(s.Socket)
	s.logf("daemon encerrado")
	return nil
}

func (s *Servidor) logf(formato string, args ...any) {
	fmt.Fprintf(s.Saida, "%s  %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(formato, args...))
}

// listar devolve os ambientes conhecidos, ordenados por nome.
func (s *Servidor) listar() []Ambiente {
	s.mu.Lock()
	defer s.mu.Unlock()

	saida := make([]Ambiente, 0, len(s.ambientes))
	for _, amb := range s.ambientes {
		saida = append(saida, amb.snapshot())
	}
	slices.SortFunc(saida, func(a, b Ambiente) int { return strings.Compare(a.Projeto, b.Projeto) })
	return saida
}

// JaRodandoError indica que outro daemon já ocupa o socket.
type JaRodandoError struct {
	Socket string
}

func (e *JaRodandoError) Error() string {
	return fmt.Sprintf("já existe um daemon rodando em %s", e.Socket)
}

// localizarProjeto encontra um projeto registrado pelo nome.
//
// O registro é lido a CADA requisição, sem cache. É barato, e garante que um
// `devm add` feito agora já vale para o próximo comando — um cache aqui
// obrigaria a reiniciar o daemon depois de registrar um projeto.
func localizarProjeto(nome string) (registry.Projeto, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return registry.Projeto{}, err
	}

	reg, err := registry.Carregar(registry.Path(dir))
	if err != nil {
		return registry.Projeto{}, err
	}

	p, ok := reg.Buscar(nome)
	if !ok {
		return registry.Projeto{}, fmt.Errorf("projeto %q não está registrado (rode `devm add`)", nome)
	}
	return p, nil
}

// escreverJSON responde com um corpo JSON.
func escreverJSON(w http.ResponseWriter, status int, corpo any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(corpo)
}

func escreverErro(w http.ResponseWriter, status int, err error) {
	escreverJSON(w, status, Erro{Mensagem: err.Error()})
}
