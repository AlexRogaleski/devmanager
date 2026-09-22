package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/proxy"
)

// NomeDaIntencao é o arquivo que guarda o que deveria estar rodando.
const NomeDaIntencao = "ambientes.json"

// Intencao é um ambiente que o usuário pediu para subir.
//
// O arquivo guarda INTENÇÃO, não estado. A diferença importa: um daemon que
// morreu não tem processo nenhum de pé, e um arquivo dizendo "rodando" seria
// mentira. Dizendo "o usuário queria isto rodando", continua verdade — e é
// exatamente o que permite restaurar.
type Intencao struct {
	Projeto  string      `json:"project"`
	Pedido   PedidoStart `json:"request"`
	DesdeQue time.Time   `json:"since"`
}

// caminhoDaIntencao devolve onde o arquivo mora.
//
// Fica no diretório de DADOS, e não no de runtime: /run/user/1000 é limpo no
// logout, e a restauração precisa sobreviver ao reboot, que é justamente
// quando ela serve para alguma coisa.
func caminhoDaIntencao() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, NomeDaIntencao), nil
}

// lerIntencoes carrega o que estava rodando.
//
// Arquivo ausente ou inválido devolve lista vazia, sem erro: a restauração é
// conveniência, e falhar o start do daemon por causa de um JSON corrompido
// seria trocar um incômodo por um impedimento.
func lerIntencoes() []Intencao {
	caminho, err := caminhoDaIntencao()
	if err != nil {
		return nil
	}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		return nil
	}

	var lista []Intencao
	if err := json.Unmarshal(dados, &lista); err != nil {
		return nil
	}
	return lista
}

// gravarIntencoes escreve a lista atual, de forma atômica.
func gravarIntencoes(lista []Intencao) error {
	caminho, err := caminhoDaIntencao()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}

	dados, err := json.MarshalIndent(lista, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(caminho), ".devm-ambientes-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(append(dados, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), caminho)
}

// anotarIntencao registra que este ambiente deve subir com o daemon.
func (s *Servidor) anotarIntencao(nome string, pedido PedidoStart) {
	lista := semOProjeto(lerIntencoes(), nome)
	lista = append(lista, Intencao{Projeto: nome, Pedido: pedido, DesdeQue: time.Now()})

	slices.SortFunc(lista, func(a, b Intencao) int {
		if a.Projeto < b.Projeto {
			return -1
		}
		if a.Projeto > b.Projeto {
			return 1
		}
		return 0
	})

	if err := gravarIntencoes(lista); err != nil {
		s.logf("aviso: não consegui anotar %q para restaurar — %v", nome, err)
	}
}

// esquecerIntencao tira o ambiente da lista: parar é deliberado, e o daemon
// não deve ressuscitar o que alguém derrubou de propósito.
func (s *Servidor) esquecerIntencao(nome string) {
	if err := gravarIntencoes(semOProjeto(lerIntencoes(), nome)); err != nil {
		s.logf("aviso: não consegui esquecer %q — %v", nome, err)
	}
}

func semOProjeto(lista []Intencao, nome string) []Intencao {
	saida := make([]Intencao, 0, len(lista))
	for _, i := range lista {
		if i.Projeto != nome {
			saida = append(saida, i)
		}
	}
	return saida
}

// Restaurar sobe de novo os ambientes que estavam rodando.
//
// Roda em série, e não em paralelo: subir dois projetos ao mesmo tempo faria
// dois `composer install` disputarem CPU e, pior, dois contêineres do mesmo
// serviço tentarem nascer juntos.
func (s *Servidor) Restaurar(ctx context.Context) {
	lista := lerIntencoes()
	if len(lista) == 0 {
		return
	}

	s.logf("restaurando %d ambiente(s)...", len(lista))

	for _, intencao := range lista {
		projeto, err := localizarProjeto(intencao.Projeto)
		if err != nil {
			// Projeto removido do registro no meio tempo: não é erro, é
			// história. Sai da lista para não tentar de novo amanhã.
			s.logf("%s não está mais registrado; tirando da restauração", intencao.Projeto)
			s.esquecerIntencao(intencao.Projeto)
			continue
		}

		amb, err := s.subir(ctx, projeto.Caminho, intencao.Projeto, intencao.Pedido)
		if err != nil {
			s.logf("aviso: %s não subiu na restauração — %v", intencao.Projeto, err)
			continue
		}

		s.mu.Lock()
		s.ambientes[intencao.Projeto] = amb
		s.mu.Unlock()

		s.registrarRota(intencao.Projeto, amb)
		s.logf("ambiente %q restaurado", intencao.Projeto)
	}
}

// registrarRota publica o domínio do ambiente no proxy.
func (s *Servidor) registrarRota(nome string, amb *ambiente) {
	instantaneo := amb.snapshot()
	if instantaneo.Dominio == "" || instantaneo.Porta == 0 {
		return
	}

	s.tabela.Definir(proxy.Rota{
		Dominio: instantaneo.Dominio,
		Porta:   instantaneo.Porta,
		Projeto: nome,
	})
}
