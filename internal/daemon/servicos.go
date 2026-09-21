package daemon

import (
	"context"
	"slices"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// registrarSubido anota um serviço que o daemon colocou de pé.
//
// Só o que NÓS subimos entra aqui. Um contêiner que já estava rodando quando
// o ambiente começou foi decisão de outra pessoa — um `devm service start`
// deliberado, um banco aberto no cliente gráfico — e derrubá-lo depois seria
// o Dev Manager desfazendo escolha alheia.
func (s *Servidor) registrarSubido(spec services.Spec) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subidos == nil {
		s.subidos = make(map[string]services.Spec)
	}
	s.subidos[spec.Container()] = spec
}

// pararServicosOciosos desliga os serviços que o daemon subiu e que nenhum
// ambiente ativo usa mais.
//
// É o que faz "serviço ligado" significar "algum projeto precisa dele agora".
// Sem isso, subir um projeto PostgreSQL de manhã e um MySQL à tarde deixa os
// dois bancos consumindo memória até o próximo reboot — e o reboot também
// não resolvia, porque o contêiner voltava sozinho.
func (s *Servidor) pararServicosOciosos(ctx context.Context) []string {
	if g, err := config.LoadGlobal(); err == nil && g.ManterServicos {
		return nil
	}

	ociosos := s.selecionarOciosos()
	if len(ociosos) == 0 {
		return nil
	}

	// Contexto próprio, e não o da requisição: quem pediu o stop pode ter
	// desistido de esperar, e parar o contêiner pela metade não é opção.
	ctx, cancelar := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancelar()

	engine, err := services.DetectarConfigurado(ctx)
	if err != nil {
		s.logf("aviso: serviços ociosos não puderam ser parados — %v", err)
		return nil
	}
	m := &services.Manager{Engine: engine}

	parados := make([]string, 0, len(ociosos))
	for _, spec := range ociosos {
		if err := m.Stop(ctx, spec); err != nil {
			s.logf("aviso: %s não parou — %v", spec.Container(), err)
			continue
		}
		s.logf("serviço %s parado (nenhum projeto ativo usa)", spec.Container())
		parados = append(parados, spec.Nome+":"+spec.Versao)
	}

	slices.Sort(parados)
	return parados
}

// selecionarOciosos decide o que desligar e já esquece esses serviços.
//
// Separada de quem executa o stop para poder ser testada sem docker: a
// decisão é toda de contabilidade — quem subimos menos quem ainda usa — e é
// nela que mora o risco de derrubar um serviço de um projeto vivo.
func (s *Servidor) selecionarOciosos() []services.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()

	emUso := make(map[string]bool)
	for _, amb := range s.ambientes {
		for _, spec := range amb.servicos {
			emUso[spec.Container()] = true
		}
	}

	var ociosos []services.Spec
	for container, spec := range s.subidos {
		if !emUso[container] {
			ociosos = append(ociosos, spec)
			// Sai da lista aqui: se o stop falhar, não insistimos a cada
			// ambiente que cair — o usuário resolve com `devm service stop`.
			delete(s.subidos, container)
		}
	}
	return ociosos
}

// containersRodando devolve o conjunto de contêineres de pé agora.
func containersRodando(ctx context.Context, m *services.Manager) map[string]bool {
	lista, err := m.List(ctx)
	if err != nil {
		return nil
	}

	set := make(map[string]bool, len(lista))
	for _, s := range lista {
		if s.Estado == services.EstadoRodando {
			set[s.Container] = true
		}
	}
	return set
}
