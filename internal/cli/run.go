package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// runCmd executa um comando arbitrário com o PHP do projeto no PATH.
//
//	devm run composer install
//	devm run npm run dev
//	devm run ./vendor/bin/pest
//
// Nenhum argumento é interpretado pelo devm: tudo depois de "run" vai
// verbatim para o processo filho. É o que impede `devm run npm run dev --host`
// de tentar entender o --host como flag do devm.
func runCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: devm run <comando> [argumentos]")
	}

	r, err := runnerDoDiretorioAtual()
	if err != nil {
		return err
	}
	r.Stdin, r.Stdout, r.Stderr = stdio.In, stdio.Out, stdio.Err

	return r.Run(context.Background(), args[0], args[1:]...)
}

// artisanCmd roda `php artisan` com o PHP correto do projeto.
func artisanCmd(stdio IO, args []string) error {
	p, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}
	if !p.HasArtisan {
		return fmt.Errorf("%s não tem artisan — não parece um projeto Laravel", p.Path)
	}
	r.Stdin, r.Stdout, r.Stderr = stdio.In, stdio.Out, stdio.Err

	// RunPHP chama o interpretador diretamente, sem procurar no PATH.
	// O primeiro argumento vira o script a executar.
	return r.RunPHP(context.Background(), append([]string{p.ArtisanPath()}, args...)...)
}

// composerCmd roda o composer com o PHP correto do projeto.
//
// Isso resolve um problema real: o composer instalado globalmente roda com o
// PHP do sistema e resolve dependências para a versão ERRADA. Um projeto que
// exige ^8.2 num sistema com 8.5 pode receber pacotes incompatíveis.
func composerCmd(stdio IO, args []string) error {
	_, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}
	r.Stdin, r.Stdout, r.Stderr = stdio.In, stdio.Out, stdio.Err

	return r.Run(context.Background(), "composer", args...)
}

// ambienteDoProjeto é o caminho comum de todos os comandos de execução:
// achar o projeto, escolher o PHP e montar o Runner apontando para o shim
// persistente daquele projeto.
func ambienteDoProjeto() (*project.Project, *runner.Runner, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("lendo o diretório atual: %w", err)
	}

	p, err := project.Find(cwd)
	if err != nil {
		return nil, nil, err
	}

	rt, err := resolverRuntime(p)
	if err != nil {
		return nil, nil, err
	}

	shim, err := paths.ShimDir(p.Path)
	if err != nil {
		return nil, nil, err
	}

	return p, &runner.Runner{
		Runtime: rt,
		Dir:     p.Path,
		ShimDir: shim,
	}, nil
}

// localizarProjeto acha o projeto SEM resolver o runtime.
//
// Existe por causa de um bug concreto: `devm php use --clear` é a saída de
// emergência de uma versão fixada que não existe na máquina. Se ele dependesse
// de resolver o runtime primeiro, falharia justamente na situação em que é
// necessário. Uma saída de emergência não pode depender daquilo de que ela
// serve para escapar.
func localizarProjeto() (*project.Project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("lendo o diretório atual: %w", err)
	}
	return project.Find(cwd)
}

func runnerDoDiretorioAtual() (*runner.Runner, error) {
	_, r, err := ambienteDoProjeto()
	return r, err
}

// resolverRuntime escolhe o PHP do projeto.
//
// Sem require.php declarado, cai para a versão mais nova disponível: é a
// escolha menos surpreendente, e o projeto pode fixar a versão depois no
// arquivo de configuração.
func resolverRuntime(p *project.Project) (rt runtimes.Runtime, err error) {
	exigencia, origem := p.PHPRequirement()

	c := semver.Any
	if exigencia != "" {
		c, err = semver.ParseConstraint(exigencia)
		if err != nil {
			return rt, fmt.Errorf("versão de PHP inválida em %s: %w", origem, err)
		}
	}
	return defaultManager().Resolve(context.Background(), "php", c)
}
