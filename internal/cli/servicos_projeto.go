package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// servicosDoProjeto prepara o gerenciador de serviços, se o projeto usa algum.
//
// Só detectamos o engine quando há serviços declarados: um projeto que não
// usa contêiner nenhum não deve pagar o custo — nem receber o erro — de uma
// detecção de podman.
//
// Quando o projeto declara serviços mas não há engine na máquina, devolvemos
// nil com um aviso em vez de falhar. O `devm up` ainda tem valor sem os
// serviços: instalar dependências e gerar a chave continua funcionando, e o
// `devm detect` segue apontando o que falta.
func servicosDoProjeto(ctx context.Context, w io.Writer, p *project.Project) *services.Manager {
	specs, err := prepare.SpecsDoProjeto(p)
	if err != nil {
		fmt.Fprintf(w, "aviso: %v\n\n", err)
		return nil
	}
	if len(specs) == 0 {
		return nil
	}

	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		fmt.Fprintf(w, "aviso: os serviços do projeto não podem subir\n%v\n\n", err)
		return nil
	}
	return m
}
