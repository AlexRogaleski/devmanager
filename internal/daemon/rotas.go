package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/proxy"
)

// rotas monta o roteador da API.
//
// Os padrões incluem o método e variáveis de caminho — recurso do ServeMux
// desde o Go 1.22. Antes disso seria preciso um roteador de terceiros ou um
// switch dentro de cada handler; hoje a stdlib basta, e uma dependência a
// menos num daemon é uma dependência a menos para auditar.
func (s *Servidor) rotas() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /"+Versao+"/health", s.rotaSaude)
	mux.HandleFunc("GET /"+Versao+"/environments", s.rotaListar)
	mux.HandleFunc("GET /"+Versao+"/proxy", s.rotaProxy)
	mux.HandleFunc("POST /"+Versao+"/environments/{nome}/start", s.rotaStart)
	mux.HandleFunc("POST /"+Versao+"/environments/{nome}/stop", s.rotaStop)
	mux.HandleFunc("GET /"+Versao+"/environments/{nome}/logs", s.rotaLogs)

	// Qualquer outro caminho: 404 com corpo JSON, para o cliente distinguir
	// "rota inexistente" de "daemon mudo".
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		escreverErro(w, http.StatusNotFound, fmt.Errorf("rota desconhecida: %s %s", r.Method, r.URL.Path))
	})

	return mux
}

func (s *Servidor) rotaSaude(w http.ResponseWriter, r *http.Request) {
	escreverJSON(w, http.StatusOK, Saude{
		OK:        true,
		Versao:    s.Versao,
		APIVersao: Versao,
		PID:       os.Getpid(),
		DesdeQue:  s.desdeQue,
	})
}

func (s *Servidor) rotaListar(w http.ResponseWriter, r *http.Request) {
	escreverJSON(w, http.StatusOK, s.listar())
}

func (s *Servidor) rotaProxy(w http.ResponseWriter, r *http.Request) {
	escreverJSON(w, http.StatusOK, s.InfoProxy())
}

func (s *Servidor) rotaStart(w http.ResponseWriter, r *http.Request) {
	nome := r.PathValue("nome")

	var pedido PedidoStart
	// Corpo vazio é legítimo: `devm start -d` sem opções nenhuma. Só um
	// corpo presente e malformado é erro.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&pedido); err != nil {
			escreverErro(w, http.StatusBadRequest, fmt.Errorf("corpo inválido: %w", err))
			return
		}
	}

	// Já rodando: devolvemos 200 com o ambiente atual em vez de erro. Subir
	// algo que já está de pé é a intenção satisfeita, não uma falha.
	s.mu.Lock()
	if amb, existe := s.ambientes[nome]; existe {
		instantaneo := amb.snapshot()
		if instantaneo.Rodando() {
			s.mu.Unlock()
			escreverJSON(w, http.StatusOK, instantaneo)
			return
		}
		// Ambiente morto: sai da tabela para dar lugar ao novo.
		delete(s.ambientes, nome)
	}
	s.mu.Unlock()

	projeto, err := localizarProjeto(nome)
	if err != nil {
		escreverErro(w, http.StatusNotFound, err)
		return
	}

	// Subir pode levar minutos (baixar imagem de contêiner, inicializar
	// banco). O contexto da REQUISIÇÃO não serve como pai do ambiente: ele
	// morre quando a resposta é enviada. Usamos o da requisição apenas para
	// abortar o preparo se o cliente desistir.
	amb, err := s.subir(r.Context(), projeto.Caminho, nome, pedido)
	if err != nil {
		escreverErro(w, http.StatusInternalServerError, err)
		return
	}

	s.mu.Lock()
	s.ambientes[nome] = amb
	s.mu.Unlock()

	// A rota só entra DEPOIS do ambiente estar de pé. Registrá-la antes
	// faria o proxy anunciar um domínio que responderia 502.
	instantaneo := amb.snapshot()
	if instantaneo.Dominio != "" && instantaneo.Porta != 0 {
		s.tabela.Definir(proxy.Rota{
			Dominio: instantaneo.Dominio,
			Porta:   instantaneo.Porta,
			Projeto: nome,
		})
	}

	s.logf("ambiente %q iniciado", nome)
	escreverJSON(w, http.StatusOK, amb.snapshot())
}

func (s *Servidor) rotaStop(w http.ResponseWriter, r *http.Request) {
	nome := r.PathValue("nome")

	s.mu.Lock()
	amb, existe := s.ambientes[nome]
	if existe {
		delete(s.ambientes, nome)
	}
	s.mu.Unlock()

	if !existe {
		escreverErro(w, http.StatusNotFound, fmt.Errorf("o ambiente %q não está rodando", nome))
		return
	}

	if dominio := amb.snapshot().Dominio; dominio != "" {
		s.tabela.Remover(dominio)
	}

	amb.cancelar()

	select {
	case <-amb.encerrado:
	case <-time.After(20 * time.Second):
		s.logf("aviso: %s não encerrou a tempo", nome)
	}
	amb.anel.Fechar()

	s.logf("ambiente %q parado", nome)

	instantaneo := amb.snapshot()
	instantaneo.ServicosParados = s.pararServicosOciosos(r.Context())
	escreverJSON(w, http.StatusOK, instantaneo)
}

// rotaLogs devolve o histórico e, com ?follow=1, continua transmitindo.
//
// O formato é NDJSON — um objeto JSON por linha. Diferente de um array, ele
// pode ser transmitido indefinidamente e consumido incrementalmente: o
// cliente processa cada linha ao chegar, sem esperar um fechamento que nunca
// vem num stream.
func (s *Servidor) rotaLogs(w http.ResponseWriter, r *http.Request) {
	nome := r.PathValue("nome")

	s.mu.Lock()
	amb, existe := s.ambientes[nome]
	s.mu.Unlock()

	if !existe {
		escreverErro(w, http.StatusNotFound, fmt.Errorf("o ambiente %q não está rodando", nome))
		return
	}

	seguir := r.URL.Query().Get("follow") == "1"

	// A inscrição vem ANTES de enviar o histórico. Na ordem inversa, uma
	// linha escrita entre o histórico e a inscrição se perderia — e um log
	// com buraco é pior que um log com linha repetida.
	var novas <-chan Linha
	var cancelarInscricao func()
	if seguir {
		novas, cancelarInscricao = amb.anel.Inscrever(256)
		defer cancelarInscricao()
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	// O Flusher empurra os bytes para o cliente em vez de esperar o buffer
	// encher — sem ele, `devm logs -f` só mostraria algo a cada 4 KB.
	flusher, _ := w.(http.Flusher)

	for _, linha := range amb.anel.Historico() {
		if err := enc.Encode(linha); err != nil {
			return
		}
	}
	if flusher != nil {
		flusher.Flush()
	}

	if !seguir {
		return
	}

	for {
		select {
		case linha, aberto := <-novas:
			if !aberto {
				return // ambiente encerrado
			}
			if err := enc.Encode(linha); err != nil {
				return // cliente desconectou
			}
			if flusher != nil {
				flusher.Flush()
			}

		case <-r.Context().Done():
			// Cliente fechou a conexão: o defer cancela a inscrição e o anel
			// para de guardar um canal que ninguém lê.
			return

		case <-amb.encerrado:
			return
		}
	}
}
