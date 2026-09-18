package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/agents"
	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// agentsCmd escreve instruções de uso do Dev Manager para assistentes de IA.
//
//	devm agents         mostra o que seria escrito e onde
//	devm agents apply   grava
//
// Existe porque um projeto migrado do Sail costuma trazer, no CLAUDE.md, uma
// linha dizendo para usar `vendor/bin/sail`. Depois da migração ela está
// errada, e o assistente continua chamando o Sail.
func agentsCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	mostrar := fs.Bool("show", false, "imprime o conteúdo gerado")
	ligar := fs.Bool("link", false, "acrescenta uma linha de referência aos arquivos de instrução existentes")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	aplicar := len(posicionais) > 0 && (posicionais[0] == "apply" || posicionais[0] == "write")

	w := stdio.Out

	p, err := localizarProjeto()
	if err != nil {
		return err
	}

	ctx := montarContexto(context.Background(), p)

	if *mostrar {
		fmt.Fprint(w, agents.Documento(ctx))
		return nil
	}

	arquivo, err := agents.Escrever(p.Path, ctx, !aplicar)
	if err != nil {
		return err
	}

	var ligacoes []agents.Resultado
	if *ligar {
		if ligacoes, err = agents.Ligar(p.Path, !aplicar); err != nil {
			return err
		}
	}

	fmt.Fprintf(w, "projeto  %s\n", p.Name)
	if ctx.PHP != "" {
		fmt.Fprintf(w, "PHP      %s\n", ctx.PHP)
	}
	fmt.Fprintln(w)

	mudou := !arquivo.NoChange || len(ligacoes) > 0
	switch {
	case arquivo.NoChange:
		fmt.Fprintf(w, "  = %s (já atualizado)\n", arquivo.Arquivo)
	case arquivo.Criado:
		fmt.Fprintf(w, "  + %s\n", arquivo.Arquivo)
	default:
		fmt.Fprintf(w, "  ~ %s\n", arquivo.Arquivo)
	}
	for _, l := range ligacoes {
		fmt.Fprintf(w, "  ~ %s (acrescenta a linha de referência)\n", l.Arquivo)
	}

	if !aplicar {
		if mudou {
			fmt.Fprintln(w, "\n(simulação — rode `devm agents apply` para gravar)")
		}
		if !*ligar {
			sugerirLigacao(w, p.Path)
		}
		return nil
	}

	if mudou {
		fmt.Fprintln(w, "\ninstruções gravadas")
	}
	if !*ligar {
		sugerirLigacao(w, p.Path)
	}
	return nil
}

// sugerirLigacao explica como fazer os assistentes lerem o arquivo.
//
// Sem a referência, o arquivo existe mas ninguém o abre. Mostramos a linha em
// vez de acrescentá-la por padrão: mexer no documento que a equipe escreveu
// deveria ser escolha explícita.
func sugerirLigacao(w io.Writer, dirProjeto string) {
	faltando := agents.AlvosDeLigacao(dirProjeto)
	if len(faltando) == 0 {
		return
	}

	fmt.Fprintf(w, "\nos assistentes não leem %s sozinhos.\n", agents.Arquivo)
	fmt.Fprintf(w, "acrescente esta linha a %s:\n\n", strings.Join(faltando, ", "))
	fmt.Fprintf(w, "  %s\n\n", agents.LinhaDeLigacao)
	fmt.Fprintln(w, "ou rode `devm agents apply --link` para fazer isso")
}

// montarContexto lê o estado real do projeto.
//
// Geramos as instruções a partir do que o projeto É agora — versão de PHP,
// serviços, portas — e não de um modelo fixo. Dizer ao assistente "o banco é
// MySQL 8.4 na 3306" é útil; dizer "use o devm" sem detalhes só transfere a
// dúvida para ele.
func montarContexto(ctx context.Context, p *project.Project) agents.Contexto {
	c := agents.Contexto{
		Projeto:    p.Name,
		TemArtisan: p.HasArtisan,
	}
	if p.IsLaravel() {
		c.Dominio = p.Domain()
	}

	if rt, err := environment.ResolverPHP(ctx, p); err == nil {
		c.PHP = rt.Version.String()
		if _, origem := p.PHPRequirement(); origem != "" {
			c.PHPOrigem = string(origem)
		}
	}

	if node, ok := environment.ResolverNode(ctx, p); ok {
		c.TemNode, c.Node = true, node.Version.String()
	}

	c.Servicos = servicosDoContexto(ctx, p)
	return c
}

// servicosDoContexto descreve os serviços declarados, com as portas reais.
func servicosDoContexto(ctx context.Context, p *project.Project) []agents.Servico {
	specs, err := prepare.SpecsDoProjeto(p)
	if err != nil || len(specs) == 0 {
		return nil
	}

	// Sem engine disponível, descrevemos as portas do catálogo: é melhor que
	// omitir os serviços, e o número certo aparece na próxima execução.
	var emExecucao map[string]services.Servico
	if engine, err := services.DetectarConfigurado(ctx); err == nil {
		m := &services.Manager{Engine: engine}
		if lista, err := m.List(ctx); err == nil {
			emExecucao = make(map[string]services.Servico, len(lista))
			for _, s := range lista {
				emExecucao[s.Container] = s
			}
		}
	}

	saida := make([]agents.Servico, 0, len(specs))
	for _, spec := range specs {
		portas := spec.Portas
		if s, ok := emExecucao[spec.Container()]; ok && len(s.Portas) > 0 {
			portas = s.Portas
		}

		numeros := make([]int, 0, len(portas))
		for _, porta := range portas {
			numeros = append(numeros, porta.Host)
		}
		saida = append(saida, agents.Servico{Nome: spec.Nome, Versao: spec.Versao, Portas: numeros})
	}
	return saida
}
