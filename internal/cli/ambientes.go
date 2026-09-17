package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/client"
	"github.com/AlexRogaleski/devmanager/internal/daemon"
	"github.com/AlexRogaleski/devmanager/internal/paths"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/registry"
)

// psCmd mostra os ambientes rodando sob o daemon.
//
// Diferente do `devm list`, que lista o que está REGISTRADO lendo o disco,
// este mostra o que está EM EXECUÇÃO — informação que só o daemon tem.
func psCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(w)
	comoJSON := fs.Bool("json", false, "imprime o resultado em JSON")

	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	lista, err := c.Listar(context.Background())
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

	if len(lista) == 0 {
		fmt.Fprintln(w, "nenhum ambiente rodando")
		fmt.Fprintln(w, "\nsuba um com `devm start -d` na pasta de um projeto")
		return nil
	}

	fmt.Fprintf(w, "%-20s %-8s %-10s %-9s %s\n", "PROJETO", "PHP", "PROCESSOS", "TEMPO", "ENDEREÇO")
	for _, amb := range lista {
		fmt.Fprintf(w, "%-20s %-8s %-10s %-9s %s\n",
			amb.Projeto, amb.PHP, resumoProcessos(amb), desde(amb.DesdeQue), endereco(amb))
	}
	return nil
}

func resumoProcessos(amb daemon.Ambiente) string {
	var rodando int
	for _, p := range amb.Processos {
		if p.Estado == daemon.ProcRodando {
			rodando++
		}
	}
	if rodando == 0 {
		return "parado"
	}
	return fmt.Sprintf("%d/%d", rodando, len(amb.Processos))
}

func endereco(amb daemon.Ambiente) string {
	if amb.Porta == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", amb.Porta)
}

// desde formata a duração de forma curta e legível.
func desde(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// stopCmd derruba um ambiente.
func stopCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(w)
	todos := fs.Bool("all", false, "derruba todos os ambientes")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}
	ctx := context.Background()

	if *todos {
		lista, err := c.Listar(ctx)
		if err != nil {
			return err
		}
		if len(lista) == 0 {
			fmt.Fprintln(w, "nenhum ambiente rodando")
			return nil
		}
		for _, amb := range lista {
			if _, err := c.Stop(ctx, amb.Projeto); err != nil {
				fmt.Fprintf(w, "%s: %v\n", amb.Projeto, err)
				continue
			}
			fmt.Fprintf(w, "%s parado\n", amb.Projeto)
		}
		return nil
	}

	nome, err := nomeDoAmbiente(posicionais)
	if err != nil {
		return err
	}

	if _, err := c.Stop(ctx, nome); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s parado\n", nome)
	return nil
}

// logsCmd mostra os logs de um ambiente.
func logsCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(w)
	seguir := fs.Bool("f", false, "acompanha novos logs até Ctrl+C")
	apenas := fs.String("only", "", "filtra por processo (ex.: serve)")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	nome, err := nomeDoAmbiente(posicionais)
	if err != nil {
		return err
	}

	c, err := client.Padrao()
	if err != nil {
		return err
	}

	filtro := strings.TrimSpace(*apenas)
	largura := 6

	return c.Logs(context.Background(), nome, *seguir, func(l daemon.Linha) error {
		if filtro != "" && l.Processo != filtro {
			return nil
		}
		if len(l.Processo) > largura {
			largura = len(l.Processo)
		}
		_, err := fmt.Fprintf(w, "%-*s | %s\n", largura, l.Processo, l.Texto)
		return err
	})
}

// nomeDoAmbiente descobre de qual projeto o comando fala.
//
// Sem argumento, usa o projeto da pasta atual — é o que a pessoa espera
// quando já está dentro dele. Com argumento, aceita o nome registrado, para
// operar em qualquer projeto de qualquer lugar.
func nomeDoAmbiente(posicionais []string) (string, error) {
	if len(posicionais) > 0 {
		return posicionais[0], nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("lendo o diretório atual: %w", err)
	}

	p, err := project.Find(cwd)
	if err != nil {
		return "", fmt.Errorf("%w\n  informe o nome do projeto, ou rode de dentro dele", err)
	}

	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	reg, err := registry.Carregar(registry.Path(dir))
	if err != nil {
		return "", err
	}

	// O nome REGISTRADO pode diferir do nome da pasta, quando o projeto foi
	// adicionado com --name. Buscar pelo caminho evita essa divergência.
	if registrado, ok := reg.BuscarPorCaminho(p.Path); ok {
		return registrado.Nome, nil
	}
	return p.Name, nil
}

// registrarSeNecessario garante que o projeto da pasta atual está no registro.
//
// O daemon opera por NOME, então um projeto não registrado não poderia ser
// iniciado. Registrar sozinho — anunciando — é menos atrito que exigir um
// `devm add` que a pessoa não tem motivo para saber que precisa.
func registrarSeNecessario(w io.Writer, p *project.Project) (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}

	reg, err := registry.Carregar(registry.Path(dir))
	if err != nil {
		return "", err
	}

	if registrado, ok := reg.BuscarPorCaminho(p.Path); ok {
		return registrado.Nome, nil
	}

	if err := reg.Adicionar(registry.Projeto{Caminho: p.Path}); err != nil {
		return "", err
	}
	if err := reg.Salvar(); err != nil {
		return "", err
	}

	registrado, _ := reg.BuscarPorCaminho(p.Path)
	fmt.Fprintf(w, "registrado: %s\n", registrado.Nome)
	return registrado.Nome, nil
}
