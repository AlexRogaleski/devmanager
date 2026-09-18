package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// serviceCmd despacha os subcomandos de `devm service`.
func serviceCmd(stdio IO, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(stdio.Out, "uso: devm service <list|catalog|start|stop|logs|remove|engine> [argumentos]\n")
		return nil
	}

	switch args[0] {
	case "catalog", "cat":
		return serviceCatalogCmd(stdio.Out)
	case "list", "ls":
		return serviceListCmd(stdio.Out, args[1:])
	case "start", "up":
		return serviceStartCmd(stdio.Out, args[1:])
	case "stop":
		return serviceStopCmd(stdio.Out, args[1:])
	case "remove", "rm":
		return serviceRemoveCmd(stdio.Out, args[1:])
	case "logs":
		return serviceLogsCmd(stdio.Out, args[1:])
	case "engine":
		return serviceEngineCmd(stdio.Out, args[1:])
	default:
		return fmt.Errorf("subcomando desconhecido: service %q", args[0])
	}
}

// serviceCatalogCmd lista o que o Dev Manager sabe subir.
//
// Não precisa de engine: é conhecimento nosso, não estado da máquina. Por
// isso funciona mesmo sem podman nem docker instalados — útil para decidir o
// que instalar.
func serviceCatalogCmd(w io.Writer) error {
	fmt.Fprintf(w, "%-10s %-8s %-8s %s\n", "SERVIÇO", "PADRÃO", "PORTA", "DESCRIÇÃO")
	for _, d := range services.Catalogo() {
		porta := ""
		if len(d.Portas) > 0 {
			porta = fmt.Sprintf("%d", d.Portas[0].Host)
		}
		fmt.Fprintf(w, "%-10s %-8s %-8s %s\n", d.Nome, d.VersaoPadrao, porta, d.Descricao)
	}
	fmt.Fprintln(w, "\nex.: devm service start postgres:17")
	return nil
}

// gerenciadorDeServicos detecta o engine e monta o Manager.
func gerenciadorDeServicos(ctx context.Context, w io.Writer) (*services.Manager, error) {
	engine, err := services.DetectarConfigurado(ctx)
	if err != nil {
		return nil, err
	}
	return &services.Manager{Engine: engine, Saida: w}, nil
}

func serviceListCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service list", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	ctx := context.Background()
	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return err
	}

	lista, err := m.List(ctx)
	if err != nil {
		return err
	}

	if *comoJSON {
		saida, err := json.MarshalIndent(lista, "", "  ")
		if err != nil {
			return fmt.Errorf("serializando resultado: %w", err)
		}
		fmt.Fprintf(w, "%s\n", saida)
		return nil
	}

	fmt.Fprintf(w, "engine: %s %s", m.Engine.Nome(), m.Engine.Versao)
	if m.Engine.Rootless {
		fmt.Fprint(w, " (rootless)")
	}
	fmt.Fprint(w, "\n\n")

	if len(lista) == 0 {
		fmt.Fprintln(w, "nenhum serviço criado pelo Dev Manager")
		fmt.Fprintln(w, "\nveja o que dá para subir com `devm service catalog`")
		return nil
	}

	fmt.Fprintf(w, "%-10s %-8s %-10s %s\n", "SERVIÇO", "VERSÃO", "ESTADO", "PORTAS")
	for _, s := range lista {
		fmt.Fprintf(w, "%-10s %-8s %-10s %s\n", s.Nome, s.Versao, s.Estado, formatarPortas(s))
	}
	return nil
}

func formatarPortas(s services.Servico) string {
	texto := ""
	for i, p := range s.Portas {
		if i > 0 {
			texto += ", "
		}
		texto += fmt.Sprintf("%d", p.Host)
		if p.Rotulo != "" {
			texto += " (" + p.Rotulo + ")"
		}
	}
	return texto
}

func serviceStartCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service start", flag.ContinueOnError)
	fs.SetOutput(w)
	porta := fs.Int("port", 0, "porta do host (padrão: a porta oficial do serviço)")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm service start <serviço[:versão]> [--port N]")
	}

	spec, err := services.ParseSpec(posicionais[0])
	if err != nil {
		return err
	}

	// --port muda a porta PRINCIPAL. Serviços com mais de uma porta, como o
	// mailpit, mantêm as demais no padrão — sobrepor todas a partir de um
	// número só seria adivinhação.
	if *porta != 0 && len(spec.Portas) > 0 {
		spec.Portas[0].Host = *porta
	}

	ctx := context.Background()
	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return err
	}

	s, err := m.Start(ctx, spec)
	if err != nil {
		return enriquecerErroDePorta(err, spec)
	}

	fmt.Fprintf(w, "%s %s rodando\n", s.Nome, s.Versao)
	for _, p := range spec.Portas {
		rotulo := p.Rotulo
		if rotulo == "" {
			rotulo = "porta"
		}
		fmt.Fprintf(w, "  %-6s 127.0.0.1:%d\n", rotulo, p.Host)
	}
	if len(spec.Env) > 0 {
		fmt.Fprintln(w, "\ncredenciais de desenvolvimento:")
		for _, k := range chavesOrdenadas(spec.Env) {
			fmt.Fprintf(w, "  %s=%s\n", k, spec.Env[k])
		}
	}
	return nil
}

func serviceStopCmd(w io.Writer, args []string) error {
	spec, _, err := specDosArgs(w, args, "stop")
	if err != nil {
		return err
	}

	ctx := context.Background()
	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return err
	}
	if err := m.Stop(ctx, spec); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s parado (os dados foram preservados)\n", spec.Nome, spec.Versao)
	return nil
}

func serviceRemoveCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service remove", flag.ContinueOnError)
	fs.SetOutput(w)
	// Apagar dados é irreversível, então exige uma flag própria e explícita:
	// nunca vem junto por engano ao recriar um contêiner.
	apagarDados := fs.Bool("data", false, "APAGA TAMBÉM o volume de dados (irreversível)")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm service remove <serviço[:versão]> [--data]")
	}

	spec, err := services.ParseSpec(posicionais[0])
	if err != nil {
		return err
	}

	ctx := context.Background()
	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return err
	}
	if err := m.Remove(ctx, spec, *apagarDados); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s removido\n", spec.Nome, spec.Versao)
	if *apagarDados {
		fmt.Fprintln(w, "o volume de dados também foi apagado")
	} else if spec.VolumeInterno != "" {
		fmt.Fprintf(w, "o volume %s foi preservado (use --data para apagá-lo)\n", spec.Volume())
	}
	return nil
}

func serviceLogsCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service logs", flag.ContinueOnError)
	fs.SetOutput(w)
	seguir := fs.Bool("f", false, "acompanha novos logs até Ctrl+C")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm service logs <serviço[:versão]> [-f]")
	}

	spec, err := services.ParseSpec(posicionais[0])
	if err != nil {
		return err
	}

	ctx := context.Background()
	m, err := gerenciadorDeServicos(ctx, w)
	if err != nil {
		return err
	}
	return m.Logs(ctx, spec, w, *seguir)
}

// specDosArgs interpreta o argumento posicional comum a vários subcomandos.
func specDosArgs(w io.Writer, args []string, comando string) (services.Spec, []string, error) {
	fs := flag.NewFlagSet("service "+comando, flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return services.Spec{}, nil, err
	}
	if len(posicionais) == 0 {
		return services.Spec{}, nil, fmt.Errorf(
			"uso: devm service %s <serviço[:versão]>   (ex.: devm service %s postgres:17)", comando, comando)
	}

	spec, err := services.ParseSpec(posicionais[0])
	if err != nil {
		return services.Spec{}, nil, err
	}
	return spec, posicionais[1:], nil
}

// enriquecerErroDePorta transforma "porta ocupada" num comando pronto.
//
// Dizer "a porta 5432 está em uso" deixa o trabalho para o usuário; sugerir
// uma porta livre concreta resolve o problema na mesma linha. Instalações
// nativas de PostgreSQL e MySQL ocupam essas portas com frequência.
func enriquecerErroDePorta(err error, spec services.Spec) error {
	var ocupada *services.PortaOcupadaError
	if !errors.As(err, &ocupada) {
		return err
	}

	livre, errPorta := environment.PortaLivre()
	if errPorta != nil {
		return err
	}

	return fmt.Errorf("%w\n\n  tente:  devm service start %s:%s --port %d",
		err, spec.Nome, spec.Versao, livre)
}

func chavesOrdenadas(m map[string]string) []string {
	chaves := make([]string, 0, len(m))
	for k := range m {
		chaves = append(chaves, k)
	}
	slices.Sort(chaves)
	return chaves
}

// serviceEngineCmd mostra ou define o runtime de contêiner usado.
//
//	devm service engine           mostra o escolhido e o detectado
//	devm service engine docker    fixa o docker
//	devm service engine auto      volta à detecção automática
//
// A escolha existe porque a detecção automática tenta podman primeiro — o que
// é certo em sistemas imutáveis e errado para quem tem podman apenas por
// causa do distrobox e trabalha com docker.
func serviceEngineCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service engine", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(posicionais) > 0 {
		if err := config.SetEngine(posicionais[0]); err != nil {
			return err
		}
		caminho, _ := config.GlobalPath()
		fmt.Fprintf(w, "engine definido como %q em %s\n", posicionais[0], caminho)
		fmt.Fprintln(w, "\nreinicie o daemon para aplicar:")
		fmt.Fprintln(w, "  devm daemon stop && devm daemon start")
		return nil
	}

	g, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "configurado  %s\n", g.Engine)

	engine, err := services.DetectarConfigurado(context.Background())
	if err != nil {
		fmt.Fprintf(w, "detectado    nenhum\n\n%v\n", err)
		return nil
	}

	fmt.Fprintf(w, "detectado    %s %s", engine.Nome(), engine.Versao)
	if engine.Rootless {
		fmt.Fprint(w, " (rootless)")
	}
	fmt.Fprintln(w)

	if g.Engine == config.EngineAuto {
		fmt.Fprintln(w, "\nfixe com `devm service engine docker` ou `devm service engine podman`")
	}
	return nil
}
