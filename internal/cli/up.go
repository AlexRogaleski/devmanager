package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/AlexRogaleski/devmanager/internal/prepare"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// upCmd prepara o projeto para rodar.
//
//	devm up                deixa o projeto pronto
//	devm up --migrate      também roda as migrações
//	devm up --dry-run      só mostra o que faria
//
// É o comando que fecha a experiência "clonar → abrir → rodar": tudo que o
// `devm detect` aponta como pendente, ele executa — com o PHP correto do
// projeto, o que importa porque `composer install` resolve dependências
// contra a versão do interpretador que estiver rodando.
func upCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)

	migrate := fs.Bool("migrate", false, "também roda php artisan migrate")
	semNode := fs.Bool("no-node", false, "não instala dependências de frontend")
	dryRun := fs.Bool("dry-run", false, "mostra o que seria feito, sem executar")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	p, r, err := ambienteDoProjeto()
	if err != nil {
		return err
	}
	r.Stdin, r.Stdout, r.Stderr = stdio.In, stdio.Out, stdio.Err

	rt, err := resolverRuntime(p)
	if err != nil {
		return err
	}

	ctx := context.Background()
	w := stdio.Out

	fmt.Fprintf(w, "%s  (%s)\n", p.Name, p.Kind)
	fmt.Fprintf(w, "PHP     %s  (%s)\n\n", rt.Version, rt.Source)

	opts := prepare.Opcoes{
		Migrate:  *migrate,
		SemNode:  *semNode,
		Saida:    w,
		Servicos: servicosDoProjeto(ctx, w, p),
	}

	// Garante o composer ANTES de montar o plano: o passo precisa saber qual
	// comando vai executar. Sem isso, cairíamos no composer do PATH — que é
	// justamente o que não funciona com PHP estático.
	if !*dryRun {
		phar, err := garantirComposer(ctx, w, r.ShimDir, rt.Bin)
		if err != nil {
			return err
		}
		opts.Composer = []string{rt.Bin, phar}
	}

	passos := prepare.Plano(p, r, opts)
	if len(passos) == 0 {
		fmt.Fprintln(w, "nada a preparar neste projeto")
		return nil
	}

	// Mostra o plano inteiro antes de executar qualquer coisa: o que já está
	// feito e o que falta. Ver o todo antes evita a sensação de caixa-preta.
	for _, passo := range passos {
		marca := "✓"
		sufixo := "  (já feito)"
		if passo.Pendente {
			marca, sufixo = "→", ""
		}
		fmt.Fprintf(w, "  %s %s%s\n", marca, passo.Nome, sufixo)
	}

	pendentes := prepare.Pendentes(passos)
	if len(pendentes) == 0 {
		fmt.Fprintf(w, "\nprojeto já está pronto\n")
		return nil
	}

	if *dryRun {
		fmt.Fprintf(w, "\n(simulação — rode `devm up` para executar)\n")
		return nil
	}

	executaveis, bloqueados := prepare.Executaveis(pendentes)

	for i, passo := range executaveis {
		fmt.Fprintf(w, "\n[%d/%d] %s\n", i+1, len(executaveis), passo.Nome)

		if err := passo.Executar(ctx); err != nil {
			// Para no primeiro erro: os passos são ordenados por dependência,
			// então continuar depois de uma falha só produziria erros em
			// cascata que escondem a causa original.
			return fmt.Errorf("falhou em %q: %w", passo.Nome, err)
		}
	}

	if len(bloqueados) > 0 {
		fmt.Fprintln(w, "\nficou pendente:")
		for _, passo := range bloqueados {
			fmt.Fprintf(w, "  %s — %s\n", passo.Nome, passo.Bloqueado)
		}
		fmt.Fprintln(w, "\nprojeto preparado, mas não completo")
		return nil
	}

	fmt.Fprintln(w, "\nprojeto pronto")
	if p.IsLaravel() {
		fmt.Fprintln(w, "  devm start   sobe servidor e frontend")
	}
	return nil
}

// resumoPendencias devolve os passos pendentes, para o detect.
//
// Usa o MESMO Plano do devm up, com executor nil. É isso que impede o
// diagnóstico e a ação de divergirem com o tempo.
//
// O gerenciador de serviços é consultado aqui também, e em silêncio: sem ele
// o plano não sabe quais contêineres já estão rodando, e o detect reportaria
// como pendente um serviço que o up considera pronto. Um diagnóstico que
// discorda da ação é pior que nenhum diagnóstico.
func resumoPendencias(p *project.Project) []prepare.Passo {
	return prepare.Pendentes(prepare.Plano(p, nil, prepare.Opcoes{
		Servicos: servicosSilencioso(p),
	}))
}

// servicosSilencioso detecta o engine sem imprimir avisos.
//
// O detect é um relatório: encher a saída com "instale o podman" no meio das
// informações do projeto atrapalharia a leitura. Quem avisa é o up, que é
// quem ia usar o engine.
func servicosSilencioso(p *project.Project) *services.Manager {
	specs, err := prepare.SpecsDoProjeto(p)
	if err != nil || len(specs) == 0 {
		return nil
	}

	engine, err := services.Detectar(context.Background())
	if err != nil {
		return nil
	}
	return &services.Manager{Engine: engine}
}
