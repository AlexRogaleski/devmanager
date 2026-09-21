package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// pararOciosos desliga todo serviço rodando que nenhum ambiente ativo usa.
//
// O daemon já faz isso sozinho quando um ambiente cai, mas só alcança os
// contêineres que ele mesmo subiu nesta execução. Sobra o resto: o que ficou
// de uma sessão anterior, o que voltou sozinho num reboot de versões antigas,
// o que alguém subiu à mão e esqueceu. É o que este comando limpa.
func pararOciosos(ctx context.Context, w io.Writer, m *services.Manager) error {
	emUso, err := containersEmUso(ctx)
	if err != nil {
		return err
	}

	lista, err := m.List(ctx)
	if err != nil {
		return err
	}

	parados := 0
	for _, s := range lista {
		if s.Estado != services.EstadoRodando || emUso[s.Container] {
			continue
		}

		// Nome e versão bastam para achar o contêiner: é assim que o nome
		// dele é formado.
		spec := services.Spec{Definicao: services.Definicao{Nome: s.Nome}, Versao: s.Versao}
		if err := m.Stop(ctx, spec); err != nil {
			fmt.Fprintf(w, "%s: %v\n", s.Container, err)
			continue
		}
		fmt.Fprintf(w, "%s %s parado\n", s.Nome, s.Versao)
		parados++
	}

	if parados == 0 {
		fmt.Fprintln(w, "nenhum serviço ocioso")
		return nil
	}
	fmt.Fprintln(w, "\nos dados foram preservados; o próximo `devm up` religa o que o projeto pedir")
	return nil
}

// containersEmUso devolve os contêineres exigidos pelos ambientes ativos.
//
// Sem daemon rodando não há ambiente ativo, e portanto nenhum serviço em uso.
// Isso é resposta, não erro: é justamente o estado em que a limpeza faz mais
// sentido.
func containersEmUso(ctx context.Context) (map[string]bool, error) {
	c, err := client.Padrao()
	if err != nil {
		return nil, err
	}

	ambientes, err := c.Listar(ctx)
	if err != nil {
		var semDaemon *client.SemDaemonError
		if errors.As(err, &semDaemon) {
			return nil, nil
		}
		return nil, err
	}

	emUso := make(map[string]bool)
	for _, amb := range ambientes {
		p, err := project.Detect(amb.Caminho)
		if err != nil {
			continue
		}
		specs, err := prepare.SpecsDoProjeto(p)
		if err != nil {
			continue
		}
		for _, spec := range specs {
			emUso[spec.Container()] = true
		}
	}
	return emUso, nil
}
