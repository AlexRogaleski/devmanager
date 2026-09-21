package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/environment"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/registry"
	"github.com/AlexRogaleski/devmanager/internal/runner"
	"github.com/AlexRogaleski/devmanager/internal/runtimes"
	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// newCmd cria um projeto Laravel do zero.
//
//	devm new minha-app                  usa o instalador do Laravel
//	devm new minha-app --php 8.4        escolhe a versão de PHP
//	devm new minha-app --plain          composer create-project, sem o instalador
//
// Existe por um problema de ovo e galinha: todo comando de execução do devm
// encontra o projeto pela pasta atual, e numa pasta vazia não há projeto.
// Sem este comando, criar um Laravel novo exigiria ter PHP e composer no
// sistema — justamente o que a ferramenta existe para evitar.
func newCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)

	versaoPHP := fs.String("php", "", "versão de PHP (padrão: a mais nova instalada)")
	versaoNode := fs.String("node", "", "versão de Node a fixar no projeto")
	banco := fs.String("database", "", "banco do projeto: mysql, mariadb, pgsql ou sqlite")
	plain := fs.Bool("plain", false, "usa composer create-project em vez do instalador do Laravel")
	semRegistro := fs.Bool("no-register", false, "não registra o projeto no devm")

	// Tudo depois de "--" vai VERBATIM para o instalador. Ele tem dezenas de
	// opções próprias — starter kits, WorkOS, GitHub — e replicá-las aqui
	// seria uma lista para manter desatualizada.
	nossos, doInstalador := separarEm(args, "--")

	posicionais, err := parseArgs(fs, nossos)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm new <nome-do-projeto> [--php 8.4] [--plain]")
	}

	nome := posicionais[0]
	if err := registry.ValidarNome(nome); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("lendo o diretório atual: %w", err)
	}
	destino := filepath.Join(cwd, nome)

	// Conferimos ANTES de resolver runtime e baixar composer: falhar no fim
	// de um preparo de um minuto, por causa de uma pasta que já existia,
	// seria desnecessariamente irritante.
	if _, err := os.Stat(destino); err == nil {
		return fmt.Errorf("%s já existe", destino)
	}

	w := stdio.Out
	ctx := context.Background()

	php, err := escolherPHPParaNovo(ctx, *versaoPHP)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "PHP %s\n", php.Version)

	// Shim TEMPORÁRIO: o projeto ainda não existe, então não há shim
	// persistente onde colocá-lo. O que importa é que php, composer e node
	// estejam no PATH enquanto o instalador roda — ele chama os três.
	shimDir, err := os.MkdirTemp("", "devmanager-new-")
	if err != nil {
		return fmt.Errorf("criando shim temporário: %w", err)
	}
	defer os.RemoveAll(shimDir)

	runtimesDoShim := []runtimes.Runtime{php}
	if *versaoNode != "" {
		node, err := escolherNodeParaNovo(ctx, *versaoNode)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Node %s\n", node.Version)
		runtimesDoShim = append(runtimesDoShim, node)
	}

	if _, err := runner.EnsureShim(shimDir, nil, runtimesDoShim...); err != nil {
		return err
	}

	phar, err := garantirComposer(ctx, w, shimDir, php.Bin)
	if err != nil {
		return err
	}

	r := &runner.Runner{
		Runtime: php,
		Dir:     cwd,
		ShimDir: shimDir,
		Stdin:   stdio.In, // o instalador é interativo: pergunta starter kit, testes, banco
		Stdout:  stdio.Out,
		Stderr:  stdio.Err,
	}

	extras := doInstalador
	if *banco != "" {
		extras = append(extras, "--database="+*banco)
	}

	fmt.Fprintln(w)
	if err := criarProjeto(ctx, r, w, nome, phar, *plain, extras); err != nil {
		return err
	}

	return finalizarProjetoNovo(w, destino, php, *versaoNode, *banco, *semRegistro)
}

// escolherPHPParaNovo resolve a versão de PHP do projeto que vai nascer.
//
// Sem --php, a mais nova instalada: é a escolha que um projeto novo quer, e
// difere da regra usada em projeto existente, onde o composer.json manda.
func escolherPHPParaNovo(ctx context.Context, versao string) (runtimes.Runtime, error) {
	c := semver.Any
	if versao != "" {
		parsed, err := semver.ParseConstraint(versao)
		if err != nil {
			return runtimes.Runtime{}, fmt.Errorf("versão de PHP inválida %q: %w", versao, err)
		}
		c = parsed
	}

	rt, err := environment.Runtimes().Resolve(ctx, "php", c)
	if err != nil {
		return runtimes.Runtime{}, fmt.Errorf("%w\n  instale uma com `devm php install 8.4`", err)
	}
	return rt, nil
}

func escolherNodeParaNovo(ctx context.Context, versao string) (runtimes.Runtime, error) {
	c, err := semver.ParseConstraint(versao)
	if err != nil {
		return runtimes.Runtime{}, fmt.Errorf("versão de Node inválida %q: %w", versao, err)
	}

	rt, err := environment.NodeRuntimes().Resolve(ctx, "node", c)
	if err != nil {
		return runtimes.Runtime{}, fmt.Errorf("%w\n  instale uma com `devm node install %s`", err, versao)
	}
	return rt, nil
}

// criarProjeto executa o instalador ou o composer.
func criarProjeto(ctx context.Context, r *runner.Runner, w io.Writer, nome, phar string, plain bool, extras []string) error {
	if !plain {
		if caminho, err := exec.LookPath("laravel"); err == nil {
			fmt.Fprintf(w, "criando com o instalador do Laravel (%s)\n\n", caminho)
			// O instalador tem shebang "#!/usr/bin/env php", e o Runner
			// desvia scripts PHP para o interpretador do projeto — então
			// ele roda no PHP que escolhemos, não no do sistema.
			return r.Run(ctx, caminho, append([]string{"new", nome}, extras...)...)
		}
		fmt.Fprintln(w, "instalador do Laravel não encontrado; usando composer create-project")
		fmt.Fprintln(w, "  para instalá-lo: devm run composer global require laravel/installer")
		fmt.Fprintln(w)
	}

	return r.RunPHP(ctx, phar, "create-project", "laravel/laravel", nome)
}

// separarEm divide os argumentos no primeiro separador encontrado.
func separarEm(args []string, sep string) (antes, depois []string) {
	for i, a := range args {
		if a == sep {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// servicoDoBanco traduz a escolha de banco do instalador num serviço nosso.
//
// O instalador aceita mysql, mariadb, pgsql, sqlite e sqlsrv. Os quatro
// primeiros temos; sqlsrv não, e sqlite não precisa de contêiner nenhum.
func servicoDoBanco(banco string) string {
	switch banco {
	case "mysql":
		return "mysql:8.4"
	case "mariadb":
		return "mariadb:11"
	case "pgsql":
		return "postgres:17"
	default:
		return "" // sqlite não precisa de serviço; sqlsrv não está no catálogo
	}
}

// finalizarProjetoNovo escreve a configuração e registra o projeto.
func finalizarProjetoNovo(w io.Writer, destino string, php runtimes.Runtime, versaoNode, banco string, semRegistro bool) error {
	if _, err := os.Stat(destino); err != nil {
		// O instalador pode ter sido cancelado no meio de uma pergunta.
		// Não é erro nosso, e não há o que configurar.
		fmt.Fprintf(w, "\n%s não foi criado\n", filepath.Base(destino))
		return nil
	}

	// Fixamos a versão MAIOR.MENOR, não a exata: o projeto deve acompanhar
	// os patches sem precisar editar o arquivo a cada atualização.
	if err := config.SetChave(destino, "php", php.Version.MajorMinor()); err != nil {
		return err
	}
	if versaoNode != "" {
		if err := config.SetChave(destino, "node", versaoNode); err != nil {
			return err
		}
	}

	// A escolha de banco feita no instalador vira um serviço declarado: sem
	// isso o .env apontaria para um MySQL que ninguém subiu, e o primeiro
	// `artisan migrate` falharia sem explicação.
	servico := servicoDoBanco(banco)
	if servico != "" {
		if err := config.SetLista(destino, "services", []string{servico}); err != nil {
			return err
		}
	}

	fmt.Fprintf(w, "\n%s criado\n", filepath.Base(destino))
	fmt.Fprintf(w, "  devmanager.yaml com php %s\n", php.Version.MajorMinor())
	if servico != "" {
		fmt.Fprintf(w, "  serviço %s declarado\n", servico)
	}

	nome := filepath.Base(destino)
	if !semRegistro {
		registrado, err := registrarCaminho(destino)
		if err != nil {
			fmt.Fprintf(w, "  aviso: não consegui registrar: %v\n", err)
		} else {
			nome = registrado
			fmt.Fprintf(w, "  registrado como %s\n", registrado)
		}
	}

	fmt.Fprintf(w, "\npróximos passos:\n")
	fmt.Fprintf(w, "  cd %s\n", filepath.Base(destino))
	fmt.Fprintf(w, "  devm up          # serviços e banco, se você declarar algum\n")
	fmt.Fprintf(w, "  devm start -d    # sobe o ambiente\n")

	if p, err := project.Detect(destino); err == nil && p.IsLaravel() {
		fmt.Fprintf(w, "\ndepois: https://%s.test\n", nome)
	}
	return nil
}

// registrarCaminho acrescenta o projeto ao registro e devolve o nome usado.
func registrarCaminho(caminho string) (string, error) {
	reg, err := carregarRegistro()
	if err != nil {
		return "", err
	}
	if err := reg.Adicionar(registry.Projeto{Caminho: caminho}); err != nil {
		return "", err
	}
	if err := reg.Salvar(); err != nil {
		return "", err
	}

	registrado, _ := reg.BuscarPorCaminho(caminho)
	return registrado.Nome, nil
}
