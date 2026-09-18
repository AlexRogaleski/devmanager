// Package client conversa com o daemon do Dev Manager.
//
// A CLI passa a ser um cliente magro: ela não sabe supervisionar processos
// nem subir contêineres, só pedir ao daemon que faça isso. É o que torna a
// interface substituível — uma GUI usa exatamente estas chamadas.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/daemon"
)

// Cliente fala HTTP sobre um socket Unix.
type Cliente struct {
	Socket string
	http   *http.Client
}

// Novo monta um cliente para um socket específico.
func Novo(socket string) *Cliente {
	return &Cliente{
		Socket: socket,
		http: &http.Client{
			Transport: &http.Transport{
				// O truque: substituímos o dialer TCP por um que sempre
				// conecta no socket, ignorando o host da URL. O resto da
				// pilha HTTP — métodos, cabeçalhos, streaming — funciona
				// igual, e é por isso que HTTP sobre socket Unix é mais
				// simples que inventar um protocolo binário próprio.
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
			// Sem timeout global: a rota de logs com ?follow=1 fica aberta
			// indefinidamente por projeto. Quem precisa de prazo usa context.
		},
	}
}

// Padrao monta um cliente para o socket padrão do usuário.
func Padrao() (*Cliente, error) {
	socket, err := daemon.CaminhoDoSocket()
	if err != nil {
		return nil, err
	}
	return Novo(socket), nil
}

// url monta o endereço de uma rota.
//
// O host é fictício e ignorado pelo dialer, mas a biblioteca HTTP exige um
// para montar a requisição.
func (c *Cliente) url(caminho string) string {
	return "http://devmanager/" + daemon.Versao + caminho
}

// Rodando informa se há daemon respondendo.
func (c *Cliente) Rodando(ctx context.Context) bool {
	ctx, cancelar := context.WithTimeout(ctx, 2*time.Second)
	defer cancelar()

	_, err := c.Saude(ctx)
	return err == nil
}

func (c *Cliente) Saude(ctx context.Context) (daemon.Saude, error) {
	var s daemon.Saude
	err := c.pedir(ctx, http.MethodGet, "/health", nil, &s)
	return s, err
}

func (c *Cliente) Listar(ctx context.Context) ([]daemon.Ambiente, error) {
	var lista []daemon.Ambiente
	err := c.pedir(ctx, http.MethodGet, "/environments", nil, &lista)
	return lista, err
}

func (c *Cliente) Proxy(ctx context.Context) (daemon.Proxy, error) {
	var p daemon.Proxy
	err := c.pedir(ctx, http.MethodGet, "/proxy", nil, &p)
	return p, err
}

func (c *Cliente) Start(ctx context.Context, nome string, pedido daemon.PedidoStart) (daemon.Ambiente, error) {
	var amb daemon.Ambiente
	err := c.pedir(ctx, http.MethodPost, "/environments/"+nome+"/start", pedido, &amb)
	return amb, err
}

func (c *Cliente) Stop(ctx context.Context, nome string) (daemon.Ambiente, error) {
	var amb daemon.Ambiente
	err := c.pedir(ctx, http.MethodPost, "/environments/"+nome+"/stop", nil, &amb)
	return amb, err
}

// Logs entrega cada linha ao callback, até o fim do stream ou erro.
//
// Um callback, e não um slice devolvido, porque com ?follow=1 o stream não
// tem fim: acumular tudo em memória para devolver no final nunca retornaria.
func (c *Cliente) Logs(ctx context.Context, nome string, seguir bool, cada func(daemon.Linha) error) error {
	caminho := "/environments/" + nome + "/logs"
	if seguir {
		caminho += "?follow=1"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(caminho), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return c.embrulhar(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return erroDaResposta(resp)
	}

	// json.Decoder sobre o corpo lê um objeto por vez, conforme chegam. É
	// exatamente o que NDJSON pede, e não exige separar as linhas à mão.
	dec := json.NewDecoder(resp.Body)
	for {
		var linha daemon.Linha
		if err := dec.Decode(&linha); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Cancelamento do contexto chega aqui como erro de leitura;
			// não é falha, é o usuário apertando Ctrl+C.
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("lendo logs: %w", err)
		}
		if err := cada(linha); err != nil {
			return err
		}
	}
}

func (c *Cliente) pedir(ctx context.Context, metodo, caminho string, corpo, destino any) error {
	var leitor io.Reader
	if corpo != nil {
		dados, err := json.Marshal(corpo)
		if err != nil {
			return fmt.Errorf("serializando pedido: %w", err)
		}
		leitor = bytes.NewReader(dados)
	}

	req, err := http.NewRequestWithContext(ctx, metodo, c.url(caminho), leitor)
	if err != nil {
		return err
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return c.embrulhar(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return erroDaResposta(resp)
	}
	if destino == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(destino); err != nil {
		return fmt.Errorf("lendo resposta: %w", err)
	}
	return nil
}

// embrulhar traduz falha de conexão em erro tipado.
//
// "connection refused" num socket Unix significa, na prática, "o daemon não
// está rodando" — e essa é a informação que o usuário precisa, não o texto
// do syscall.
func (c *Cliente) embrulhar(err error) error {
	return &SemDaemonError{Socket: c.Socket, Causa: err}
}

// erroDaResposta extrai a mensagem do corpo de erro da API.
func erroDaResposta(resp *http.Response) error {
	var e daemon.Erro
	if err := json.NewDecoder(resp.Body).Decode(&e); err == nil && e.Mensagem != "" {
		return errors.New(e.Mensagem)
	}
	return fmt.Errorf("o daemon respondeu %s", resp.Status)
}

// SemDaemonError indica que não há daemon atendendo no socket.
type SemDaemonError struct {
	Socket string
	Causa  error
}

func (e *SemDaemonError) Error() string {
	return fmt.Sprintf("o daemon não está rodando (%s)\n  inicie com `devm daemon start`", e.Socket)
}

func (e *SemDaemonError) Unwrap() error { return e.Causa }
