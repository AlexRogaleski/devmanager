package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// remocao reúne o que muda entre `devm php remove` e `devm node remove`.
//
// Os dois comandos fazem exatamente a mesma coisa; o que difere é a
// linguagem, qual Provider apaga, e onde mora a exigência de cada projeto.
// Em vez de duplicar 80 linhas com "php" trocado por "node" — e deixar as
// duas cópias divergirem na primeira correção — os pontos de variação viram
// campos, e a lógica fica escrita uma vez só.
type remocao struct {
	Lingua   string // "php" ou "node"
	Comando  string // como o comando é escrito, para as mensagens
	Provider runtimes.Removedor

	// Todos lista os runtimes de TODAS as origens, não só os do Provider.
	// É o que permite responder "depois de apagar, ainda sobra algum que
	// sirva?" — se o PHP do sistema atende o projeto, apagar o nosso é
	// inofensivo.
	Todos func(context.Context) ([]runtimes.Runtime, error)

	// Exigencia extrai a versão que um projeto pede.
	Exigencia func(*project.Project) (string, project.Origem)
}

// removerRuntime apaga uma versão instalada pelo Dev Manager.
//
// O comando existe porque `install` sem `remove` é uma via de mão única:
// cada versão testada fica no disco para sempre, e em um ano ninguém sabe
// mais quais ainda servem para alguma coisa.
func removerRuntime(w io.Writer, r remocao, args []string) error {
	fs := flag.NewFlagSet(r.Comando, flag.ContinueOnError)
	fs.SetOutput(w)
	forcar := fs.Bool("force", false, "remove mesmo que algum projeto dependa desta versão")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: %s <versão>   (ex.: %s 8.3.32)", r.Comando, r.Comando)
	}

	c, err := semver.ParseConstraint(posicionais[0])
	if err != nil {
		return fmt.Errorf("versão inválida %q: %w", posicionais[0], err)
	}

	ctx := context.Background()

	// Só as versões DESTE Provider entram na escolha. É o que garante que
	// um `devm php remove 8.4` jamais mire o PHP que a distro instalou:
	// ele nunca aparece nesta lista.
	gerenciados, err := r.Provider.List(ctx)
	if err != nil {
		return err
	}
	if len(gerenciados) == 0 {
		fmt.Fprintf(w, "nenhuma versão de %s instalada pelo Dev Manager\n", r.Lingua)
		return nil
	}

	alvos := versoesQueCasam(gerenciados, c)
	switch len(alvos) {
	case 0:
		return fmt.Errorf("nenhuma versão instalada satisfaz %q\n  instaladas: %s",
			posicionais[0], listarVersoes(gerenciados))
	case 1:
		// caso normal
	default:
		// Ambiguidade não vira escolha automática: apagar a versão errada
		// é irreversível, e adivinhar qual das duas o usuário quis dizer
		// seria apostar com o disco dele.
		return fmt.Errorf("%q casa com mais de uma versão instalada: %s\n  informe a versão exata",
			posicionais[0], listarVersoes(alvos))
	}

	alvo := alvos[0]
	dir := r.Provider.DirDaVersao(alvo.Version)
	tamanho := runtimes.TamanhoEmDisco(dir)

	orfaos, err := projetosOrfaos(ctx, r, alvo.Version)
	if err != nil {
		return err
	}
	if len(orfaos) > 0 && !*forcar {
		return fmt.Errorf("%s %s é a única versão que atende %s:\n%s\n"+
			"  instale outra antes, ou use --force para apagar assim mesmo",
			r.Lingua, alvo.Version, plural(len(orfaos), "este projeto", "estes projetos"),
			strings.Join(orfaos, "\n"))
	}
	for _, o := range orfaos {
		fmt.Fprintf(w, "aviso: %s\n", strings.TrimSpace(o))
	}

	if err := r.Provider.Remove(alvo.Version); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s removido (%s liberados)\n",
		r.Lingua, alvo.Version, runtimes.FormatarTamanho(tamanho))
	return nil
}

// projetosOrfaos devolve os projetos registrados que ficariam sem runtime.
//
// A pergunta certa não é "algum projeto fixou esta versão?", e sim "algum
// projeto deixa de ter QUALQUER versão que sirva?". Um projeto que pede ^8.3
// com 8.3 e 8.4 instalados não se importa de perder um dos dois; um que fixa
// 8.4 exatamente, sim. A checagem por disponibilidade cobre os dois casos
// sem precisar distinguir entre eles.
func projetosOrfaos(ctx context.Context, r remocao, apagada semver.Version) ([]string, error) {
	reg, err := carregarRegistro()
	if err != nil {
		return nil, err
	}

	todos, err := r.Todos(ctx)
	if err != nil {
		return nil, err
	}

	// A lista como ficaria DEPOIS da remoção.
	var sobram []runtimes.Runtime
	for _, rt := range todos {
		if rt.Version != apagada {
			sobram = append(sobram, rt)
		}
	}

	var orfaos []string
	for _, entrada := range reg.Projetos {
		p, err := project.Detect(entrada.Caminho)
		if err != nil {
			continue // pasta sumiu: `devm prune` resolve, não este comando
		}

		exigencia, origem := r.Exigencia(p)
		if exigencia == "" {
			continue // sem exigência, qualquer versão serve
		}

		c, err := semver.ParseConstraint(exigencia)
		if err != nil {
			continue
		}
		if !c.Allows(apagada) {
			continue // nem usava esta versão
		}
		if _, ok := runtimes.Escolher(sobram, c); ok {
			continue // outra versão cobre
		}

		orfaos = append(orfaos, fmt.Sprintf("  %-24s exige %s (de %s)",
			entrada.Nome, exigencia, origem))
	}
	return orfaos, nil
}

func versoesQueCasam(rts []runtimes.Runtime, c semver.Constraint) []runtimes.Runtime {
	var casam []runtimes.Runtime
	for _, rt := range rts {
		if c.Allows(rt.Version) {
			casam = append(casam, rt)
		}
	}
	return casam
}

func listarVersoes(rts []runtimes.Runtime) string {
	textos := make([]string, len(rts))
	for i, rt := range rts {
		textos[i] = rt.Version.String()
	}
	return strings.Join(textos, ", ")
}

// tamanhoDoRuntime devolve quanto uma instalação ocupa, ou "" para as que não
// gerenciamos.
//
// O binário fica em <raiz>/<versão>/bin/<linguagem>: subir dois níveis a
// partir dele dá a pasta da versão. Para o PHP do sistema isso apontaria para
// /usr, e medir /usr inteiro além de errado seria lento — por isso a origem
// é conferida antes.
func tamanhoDoRuntime(rt runtimes.Runtime) string {
	switch rt.Source {
	case runtimes.SourceStatic, runtimes.SourceNodeOfic:
		return runtimes.FormatarTamanho(
			runtimes.TamanhoEmDisco(filepath.Dir(filepath.Dir(rt.Bin))))
	default:
		return "-"
	}
}
