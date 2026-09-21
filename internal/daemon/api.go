// Package daemon é o servidor de longa duração do Dev Manager.
//
// Ele existe por três razões, em ordem de importância:
//
//  1. Os projetos precisam continuar rodando com o terminal fechado. Hoje o
//     `devm start` prende um terminal por projeto, e fechá-lo derruba tudo.
//
//  2. O proxy *.test exige alguém segurando as portas 80 e 443
//     permanentemente. Sem um processo de longa duração, não existe domínio
//     local.
//
//  3. Uma GUI precisa de uma API para consumir. Com o daemon expondo o que os
//     comandos já emitem em JSON, a interface passa a ser um cliente magro e
//     substituível.
//
// O daemon NÃO reimplementa nada: ele hospeda os pacotes que já existem —
// supervisor, services, registry, runtimes. O supervisor foi escrito desde o
// começo como ensaio dele.
package daemon

import "time"

// Versao da API. O caminho das rotas carrega a versão (/v1/...) para que um
// cliente antigo receba 404 explícito em vez de comportamento estranho.
const Versao = "v1"

// Saude é a resposta de /v1/health.
type Saude struct {
	OK        bool      `json:"ok"`
	Versao    string    `json:"version"`     // versão do binário
	APIVersao string    `json:"api_version"` // versão do contrato
	PID       int       `json:"pid"`
	DesdeQue  time.Time `json:"started_at"`
}

// EstadoProcesso é a situação de um processo supervisionado.
type EstadoProcesso string

const (
	ProcRodando EstadoProcesso = "rodando"
	ProcParado  EstadoProcesso = "parado"
	ProcFalhou  EstadoProcesso = "falhou"
)

// Processo é um processo de um ambiente.
type Processo struct {
	Nome   string         `json:"name"`
	Linha  string         `json:"command"`
	Estado EstadoProcesso `json:"state"`
	PID    int            `json:"pid,omitempty"`
}

// Ambiente é um projeto rodando sob o daemon.
type Ambiente struct {
	Projeto string `json:"project"`
	Caminho string `json:"path"`
	PHP     string `json:"php,omitempty"`
	Porta   int    `json:"port,omitempty"`
	Dominio string `json:"domain,omitempty"`

	// PortasExtras são as portas nomeadas que o projeto pediu com
	// {{port:nome}} no devmanager.yaml, por nome do marcador.
	PortasExtras map[string]int `json:"extra_ports,omitempty"`

	Processos []Processo `json:"processes"`
	DesdeQue  time.Time  `json:"started_at"`

	// Erro descreve por que o ambiente parou sozinho, quando foi o caso.
	Erro string `json:"error,omitempty"`

	// ServicosParados são os serviços desligados junto com este ambiente,
	// por terem ficado sem nenhum projeto ativo que os usasse. Vem
	// preenchido só na resposta do stop.
	ServicosParados []string `json:"stopped_services,omitempty"`
}

// Rodando informa se ainda há processo de pé neste ambiente.
func (a Ambiente) Rodando() bool {
	for _, p := range a.Processos {
		if p.Estado == ProcRodando {
			return true
		}
	}
	return false
}

// Proxy descreve o proxy de domínios locais.
type Proxy struct {
	Ativo         bool     `json:"active"`
	PortaHTTP     int      `json:"http_port,omitempty"`
	PortaHTTPS    int      `json:"https_port,omitempty"`
	SemPrivilegio bool     `json:"unprivileged,omitempty"`
	MotivoDaQueda string   `json:"fallback_reason,omitempty"`
	CertificadoCA string   `json:"ca_cert,omitempty"`
	Dominios      []string `json:"domains,omitempty"`
	DNS           DNS      `json:"dns"`
}

// DNS descreve o servidor de domínios locais.
type DNS struct {
	Ativo    bool   `json:"active"`
	Endereco string `json:"address,omitempty"`
	TLD      string `json:"tld,omitempty"`
	Motivo   string `json:"reason,omitempty"`
}

// PedidoStart é o corpo de POST /v1/projects/{nome}/start.
type PedidoStart struct {
	// Porta fixa a porta do servidor. Zero deixa o daemon escolher.
	Porta int `json:"port,omitempty"`

	// Apenas restringe quais processos subir. Vazio sobe todos.
	Apenas []string `json:"only,omitempty"`

	// SemNode pula os processos de frontend.
	SemNode bool `json:"no_node,omitempty"`
}

// Linha é uma linha de log de um processo.
type Linha struct {
	Processo string    `json:"process"`
	Texto    string    `json:"text"`
	Em       time.Time `json:"at"`
}

// Erro é o corpo devolvido em qualquer resposta de falha.
//
// Um formato único para erros permite ao cliente distinguir "o daemon
// respondeu que não deu" de "o daemon não respondeu" — situações com
// tratamentos bem diferentes.
type Erro struct {
	Mensagem string `json:"error"`
}
