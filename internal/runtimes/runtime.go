// Package runtimes descobre e escolhe interpretadores de linguagem.
//
// O resto do Dev Manager nunca pergunta "onde está o PHP 8.3?" — ele pede
// "um runtime que satisfaça ^8.3" e recebe um Runtime pronto para executar.
// De onde ele veio (pacote do sistema, binário estático baixado, container)
// é problema dos Providers, e some atrás desta interface.
package runtimes

import (
	"context"
	"fmt"
	"slices"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// Runtime é uma instalação concreta e executável de uma linguagem.
type Runtime struct {
	Language string         `json:"language"` // "php", futuramente "node"
	Version  semver.Version `json:"version"`
	Bin      string         `json:"bin"`    // caminho absoluto do executável
	Source   string         `json:"source"` // qual Provider forneceu
}

func (r Runtime) String() string {
	return fmt.Sprintf("%s %s (%s)", r.Language, r.Version, r.Source)
}

// Provider é uma estratégia de fornecimento de runtimes.
//
// Esta é a interface mais importante do projeto. Note que ela tem só DOIS
// métodos: em Go, interfaces pequenas são as fáceis de implementar, e
// implementações fáceis são o que mantém a arquitetura realmente plugável.
//
// Diferente de PHP, nenhum tipo declara "implements Provider". Qualquer struct
// que tenha esses dois métodos já satisfaz a interface — inclusive um mock de
// teste definido dentro do próprio arquivo de teste.
type Provider interface {
	// Name identifica a estratégia: "system", "static", "nix".
	Name() string

	// List devolve os runtimes que já estão disponíveis nesta máquina.
	// Recebe context para poder ser cancelada: descobrir runtimes envolve
	// executar binários, e um binário travado não pode pendurar a CLI.
	List(ctx context.Context) ([]Runtime, error)
}

// Installer é uma capacidade OPCIONAL: só os Providers que sabem baixar
// versões novas a implementam. O Provider do sistema, por exemplo, não
// implementa — ele só enxerga o que o pacote da distro instalou.
//
// Este é um padrão muito comum na stdlib do Go: em vez de uma interface
// gorda com métodos que metade das implementações deixaria vazios, define-se
// uma interface extra e pergunta-se em tempo de execução:
//
//	if inst, ok := provider.(Installer); ok { inst.Install(ctx, v) }
type Installer interface {
	Provider

	// Installable lista versões que este Provider consegue baixar.
	Installable(ctx context.Context) ([]semver.Version, error)

	// Install baixa e instala uma versão, devolvendo o runtime pronto.
	// prog pode ser nil quando não há interesse no progresso.
	Install(ctx context.Context, v semver.Version, prog Progresso) (Runtime, error)
}

// Manager reúne todos os Providers e responde as perguntas do Dev Manager.
type Manager struct {
	providers []Provider
}

// NewManager recebe os Providers já montados em vez de criá-los internamente.
// Isso é injeção de dependência: nos testes passamos um Provider falso, sem
// precisar de PHP instalado na máquina que roda o CI.
func NewManager(providers ...Provider) *Manager {
	return &Manager{providers: providers}
}

// List devolve todos os runtimes de uma linguagem, do mais novo para o mais antigo.
//
// Se um Provider falhar, os outros continuam valendo: a falta do PHP estático
// não pode esconder o PHP do sistema. Por isso acumulamos os erros em vez de
// abortar no primeiro.
func (m *Manager) List(ctx context.Context, language string) ([]Runtime, error) {
	var todos []Runtime
	var problemas []error

	for _, p := range m.providers {
		encontrados, err := p.List(ctx)
		if err != nil {
			problemas = append(problemas, fmt.Errorf("provider %s: %w", p.Name(), err))
			continue
		}
		for _, r := range encontrados {
			if language == "" || r.Language == language {
				todos = append(todos, r)
			}
		}
	}

	// slices.SortFunc é genérico (Go 1.21+): a função de comparação devolve
	// negativo, zero ou positivo — o mesmo contrato do nosso Version.Compare.
	// O sinal invertido ordena do maior para o menor.
	slices.SortFunc(todos, func(a, b Runtime) int {
		return b.Version.Compare(a.Version)
	})

	if len(todos) == 0 && len(problemas) > 0 {
		// errors.Join junta vários erros num só, preservando todos para
		// errors.Is. Só reportamos falha se NADA foi encontrado.
		return nil, fmt.Errorf("nenhum runtime encontrado: %w", joinErros(problemas))
	}
	return todos, nil
}

// Resolve escolhe o melhor runtime que satisfaz a constraint do projeto.
func (m *Manager) Resolve(ctx context.Context, language string, c semver.Constraint) (Runtime, error) {
	disponiveis, err := m.List(ctx, language)
	if err != nil {
		return Runtime{}, err
	}

	// Extrai só as versões para consultar a constraint...
	versoes := make([]semver.Version, 0, len(disponiveis))
	for _, r := range disponiveis {
		versoes = append(versoes, r.Version)
	}

	melhor, ok := c.Best(versoes)
	if !ok {
		return Runtime{}, &NotFoundError{Language: language, Constraint: c, Disponiveis: versoes}
	}

	// ...e depois encontra o runtime correspondente. A lista já está ordenada
	// do maior para o menor, então o primeiro match é o de maior precedência
	// quando dois Providers oferecem a mesma versão.
	for _, r := range disponiveis {
		if r.Version == melhor {
			return r, nil
		}
	}
	return Runtime{}, &NotFoundError{Language: language, Constraint: c, Disponiveis: versoes}
}

// NotFoundError é um erro com dados, não só uma mensagem.
//
// Definir um tipo de erro permite que a camada de cima use errors.As para
// recuperar a constraint e as versões disponíveis, e então decidir o que fazer:
// a CLI imprime "rode devm php install 8.3"; o daemon pode instalar sozinho.
type NotFoundError struct {
	Language    string
	Constraint  semver.Constraint
	Disponiveis []semver.Version
}

// Ter o método Error() é tudo que um tipo precisa para ser um error em Go —
// error é apenas uma interface com esse único método.
func (e *NotFoundError) Error() string {
	if len(e.Disponiveis) == 0 {
		return fmt.Sprintf("nenhum %s instalado (o projeto exige %s)", e.Language, e.Constraint)
	}
	return fmt.Sprintf("nenhum %s instalado satisfaz %s (disponíveis: %s)",
		e.Language, e.Constraint, formatarVersoes(e.Disponiveis))
}

func formatarVersoes(vs []semver.Version) string {
	textos := make([]string, len(vs))
	for i, v := range vs {
		textos[i] = v.String()
	}
	return joinStrings(textos, ", ")
}

// Escolher seleciona o melhor runtime de uma lista JÁ obtida.
//
// Resolve faz List a cada chamada, o que significa executar todos os binários
// de PHP da máquina. Para um comando que resolve N projetos — como o
// `devm list` — isso seria N vezes o mesmo trabalho. Com esta função a lista
// é obtida uma vez e reaproveitada.
//
// A lista precisa vir ordenada do maior para o menor, como Manager.List devolve.
func Escolher(disponiveis []Runtime, c semver.Constraint) (Runtime, bool) {
	for _, r := range disponiveis {
		if c.Allows(r.Version) {
			return r, true
		}
	}
	return Runtime{}, false
}
