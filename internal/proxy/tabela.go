// Package proxy serve os projetos em domínios locais como http://fapcen.test.
//
// A implementação é sobre a stdlib — httputil.ReverseProxy e crypto/x509 —
// e não sobre o Caddy embutido, apesar de ele ser a escolha óbvia à primeira
// vista. A medição decidiu: importar caddy/v2 mais o módulo de reverse proxy
// traz 142 dependências e leva o binário de 11 MB para 64 MB. Para um daemon
// que fica sempre rodando, e num projeto onde já recusamos as bibliotecas Go
// de podman e docker pelo mesmo motivo, o custo não se paga.
//
// O que o Caddy daria de graça — HTTPS local com CA interna — são cerca de
// 150 linhas de crypto/x509 que ficam inteiramente sob nosso controle.
package proxy

import (
	"sort"
	"strings"
	"sync"
)

// Rota liga um domínio local a um destino.
type Rota struct {
	Dominio string `json:"domain"`
	Porta   int    `json:"port"`
	Projeto string `json:"project"`
}

// Tabela é o mapa de domínios para destinos, alterável enquanto o proxy roda.
//
// Ela existe separada do proxy porque as duas coisas mudam em ritmos
// diferentes: o servidor sobe uma vez e fica, enquanto as rotas entram e saem
// a cada `devm start` e `devm stop`. Um RWMutex em vez de Mutex porque a
// leitura acontece a cada requisição e a escrita quase nunca.
type Tabela struct {
	mu    sync.RWMutex
	rotas map[string]Rota
}

func NovaTabela() *Tabela {
	return &Tabela{rotas: make(map[string]Rota)}
}

// Definir registra ou atualiza uma rota.
func (t *Tabela) Definir(r Rota) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.rotas[normalizar(r.Dominio)] = r
}

// Remover tira uma rota da tabela.
func (t *Tabela) Remover(dominio string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.rotas, normalizar(dominio))
}

// Buscar encontra a rota de um cabeçalho Host.
func (t *Tabela) Buscar(host string) (Rota, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	r, ok := t.rotas[normalizar(host)]
	return r, ok
}

// Listar devolve as rotas em ordem de domínio.
func (t *Tabela) Listar() []Rota {
	t.mu.RLock()
	defer t.mu.RUnlock()

	saida := make([]Rota, 0, len(t.rotas))
	for _, r := range t.rotas {
		saida = append(saida, r)
	}
	sort.Slice(saida, func(i, j int) bool { return saida[i].Dominio < saida[j].Dominio })
	return saida
}

// Dominios devolve só os nomes, para quem configura DNS.
func (t *Tabela) Dominios() []string {
	rotas := t.Listar()

	nomes := make([]string, len(rotas))
	for i, r := range rotas {
		nomes[i] = r.Dominio
	}
	return nomes
}

// normalizar prepara um Host para comparação.
//
// Três coisas acontecem aqui, e cada uma corresponde a um jeito diferente de
// o mesmo domínio chegar:
//
//	"Fapcen.test"       maiúsculas — DNS não diferencia caixa
//	"fapcen.test:8080"  com porta — o navegador manda assim fora da 80
//	"fapcen.test."      com ponto final — forma absoluta, válida em DNS
func normalizar(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))

	// Corta a porta. Cuidado com IPv6 literal, que tem ":" no meio e vem
	// entre colchetes: "[::1]:8080".
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.HasSuffix(h, "]") {
		if j := strings.LastIndexByte(h, ']'); j < i {
			h = h[:i]
		}
	}

	return strings.TrimSuffix(h, ".")
}
