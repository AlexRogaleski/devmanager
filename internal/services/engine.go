// Package services roda os serviços de infraestrutura de um projeto —
// PostgreSQL, MySQL, Redis, Mailpit — em contêineres.
//
// A conversa com o runtime de contêiner é feita pelo BINÁRIO de CLI, não
// pelas bibliotecas Go de cada projeto. O motivo é concreto: containers/podman
// e docker/docker são dependências grandes, com APIs incompatíveis entre si, e
// exigiriam duas implementações inteiras. As CLIs, por outro lado, aceitam
// praticamente os mesmos argumentos para o que precisamos aqui — então uma
// implementação só atende os dois, parametrizada pelas diferenças reais.
package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/config"
)

// Engine descreve um runtime de contêiner concreto e suas peculiaridades.
//
// As diferenças entre Podman e Docker que importam aqui são poucas, mas são
// reais — e cada uma custou horas de "funciona na minha máquina" para alguém.
type Engine struct {
	// Bin é o executável: "podman" ou "docker".
	Bin string

	// Rootless indica execução sem privilégios de root.
	Rootless bool

	// QualificaImagem força o registro completo no nome da imagem.
	//
	// O Podman NÃO assume docker.io: "postgres:17" ou falha, ou pergunta de
	// qual registro baixar — o que trava um comando não interativo. O Docker
	// completa sozinho. Qualificar sempre funciona nos dois.
	QualificaImagem bool

	// RotulaVolumeSELinux acrescenta :Z aos volumes.
	//
	// Em Fedora, Silverblue, Bazzite e derivados — ou seja, no host do
	// projeto — o SELinux impede o contêiner de ler um volume do host sem o
	// rótulo certo. O sintoma é "permission denied" dentro do contêiner com
	// permissões aparentemente corretas no host. É a causa clássica de
	// volume que não funciona em Fedora e funciona em Ubuntu.
	RotulaVolumeSELinux bool

	// Versao é o que o engine reportou, para diagnóstico.
	Versao string
}

func (e *Engine) Nome() string { return e.Bin }

// DetectarConfigurado detecta o engine respeitando a configuração do usuário.
//
// É o ponto de entrada que todo chamador deveria usar: ler a preferência em
// cada lugar que precisa de um engine espalharia a mesma lógica por daemon,
// CLI e testes, e um deles acabaria esquecendo.
func DetectarConfigurado(ctx context.Context) (*Engine, error) {
	g, err := config.LoadGlobal()
	if err != nil {
		return nil, err
	}
	return Detectar(ctx, g.Engine)
}

// Detectar encontra um runtime de contêiner utilizável.
//
// Com preferencia vazia ou "auto", a ordem é Podman antes de Docker: ele roda
// rootless por padrão, o que torna a ferramenta viável em sistemas imutáveis
// sem pedir root.
//
// Mas essa heurística erra num caso comum, e por isso a preferência existe:
// quem tem podman instalado APENAS por causa do distrobox ou do toolbox, e
// trabalha com docker. A presença do binário podman, nesses sistemas, não diz
// nada sobre o que a pessoa usa — e escolher errado colocaria os serviços num
// engine que ela nem abre, invisíveis no `docker ps` dela.
//
// Só a presença do binário não basta: o Docker precisa de um daemon rodando,
// e um binário instalado com o serviço parado falharia depois, longe da causa.
// Por isso perguntamos a versão ao engine, o que exige que ele responda.
func Detectar(ctx context.Context, preferencia string) (*Engine, error) {
	var tentativas []string

	ordem := []string{"podman", "docker"}
	switch preferencia {
	case "podman":
		ordem = []string{"podman"}
	case "docker":
		ordem = []string{"docker"}
	}

	for _, bin := range ordem {
		caminho, err := exec.LookPath(bin)
		if err != nil {
			tentativas = append(tentativas, fmt.Sprintf("%s: não instalado", bin))
			continue
		}

		e, err := inspecionar(ctx, bin, caminho)
		if err != nil {
			tentativas = append(tentativas, fmt.Sprintf("%s: %v", bin, err))
			continue
		}
		return e, nil
	}

	return nil, &EngineIndisponivelError{Tentativas: tentativas, Preferencia: preferencia}
}

func inspecionar(ctx context.Context, bin, caminho string) (*Engine, error) {
	ctx, cancelar := context.WithTimeout(ctx, 10*time.Second)
	defer cancelar()

	// O formato de template é idêntico nos dois, e pedir só a versão evita
	// depender do JSON completo, que difere entre as engines.
	saida, err := exec.CommandContext(ctx, caminho, "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		// Sem o daemon, o docker falha aqui — que é exatamente o que
		// queremos descobrir agora, e não na hora de subir um serviço.
		return nil, fmt.Errorf("não respondeu (o serviço está rodando?)")
	}

	e := &Engine{
		Bin:             bin,
		Versao:          strings.TrimSpace(string(saida)),
		QualificaImagem: bin == "podman",
	}

	e.Rootless = ehRootless(ctx, caminho, bin)
	e.RotulaVolumeSELinux = selinuxAtivo()

	return e, nil
}

// ehRootless descobre se o engine roda sem privilégios de root.
func ehRootless(ctx context.Context, caminho, bin string) bool {
	if bin != "podman" {
		return false
	}

	ctx, cancelar := context.WithTimeout(ctx, 5*time.Second)
	defer cancelar()

	saida, err := exec.CommandContext(ctx, caminho, "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(saida)) == "true"
}

// EngineIndisponivelError lista o que foi tentado e por quê falhou.
//
// Um erro genérico ("nenhum runtime encontrado") deixaria o usuário sem saber
// se falta instalar, se o daemon está parado ou se o binário está quebrado.
// Cada uma dessas situações tem uma correção diferente.
type EngineIndisponivelError struct {
	Tentativas  []string
	Preferencia string
}

func (e *EngineIndisponivelError) Error() string {
	var b strings.Builder
	b.WriteString("nenhum runtime de contêiner disponível")
	for _, t := range e.Tentativas {
		b.WriteString("\n  " + t)
	}

	// Com preferência explícita, só um engine foi tentado. Dizer "instale o
	// podman" a quem escolheu docker seria conselho errado.
	if e.Preferencia == "podman" || e.Preferencia == "docker" {
		b.WriteString("\n\n  a configuração exige " + e.Preferencia +
			"\n  mude com `devm service engine auto` para tentar o outro")
		return b.String()
	}

	b.WriteString("\n\n  instale o podman (roda sem root) ou o docker")
	return b.String()
}
