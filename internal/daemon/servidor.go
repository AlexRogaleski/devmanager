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

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/registry"
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
		desdeQue:  time.Now(),
	}
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
