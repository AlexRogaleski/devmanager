package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// initCmd gera o devmanager.yaml do projeto.
//
//	devm init          cria o arquivo com o que der para deduzir
//	devm init --print  só mostra o modelo, sem gravar nada
//
// É o comando de adoção: um projeto que vinha do Sail tem tudo descrito no
// compose.yaml, e um que nunca usou contêiner tem as pistas no .env. Ler os
// dois poupa a tradução manual, que era como os primeiros projetos vieram
// para cá — arquivo por arquivo, à mão.
//
// Nada é adivinhado em silêncio: cada linha do resumo diz de onde veio, para
// a pessoa conferir antes de versionar.
func initCmd(stdio IO, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stdio.Out)
	imprimir := fs.Bool("print", false, "mostra o modelo sem gravar nada")

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	dir := "."
	if len(posicionais) > 0 {
		dir = posicionais[0]
	}

	w := stdio.Out
	novo, origemDoPHP, achados := deduzir(dir)

	if *imprimir {
		fmt.Fprint(w, config.Modelo(novo))
		return nil
	}

	if _, err := os.Stat(config.Path(dir)); err == nil {
		return jaExiste(dir, achados)
	}

	if err := config.Criar(dir, novo); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s criado\n\n", config.Path(dir))
	if novo.PHP != "" {
		fmt.Fprintf(w, "  %-16s %s\n", "php "+novo.PHP, origemDoPHP)
	}
	jaNaMaquina := versoesNaMaquina()
	for _, a := range achados {
		fmt.Fprintf(w, "  %-16s %s\n", textoDaSpec(a.Spec), a.Origem)

		// Versão diferente da que já existe aqui significa um contêiner a mais.
		for _, versao := range jaNaMaquina[a.Spec.Nome] {
			if versao != a.Spec.Versao {
				fmt.Fprintf(w, "  %-16s ↳ a máquina já tem %s:%s; a mesma versão evita um segundo contêiner\n",
					"", a.Spec.Nome, versao)
			}
		}
	}

	fmt.Fprintln(w, "\nas demais opções estão no arquivo, comentadas")
	if len(achados) > 0 {
		fmt.Fprintln(w, "confira os serviços antes de versionar; `devm up` sobe o que estiver declarado")
	}
	return nil
}

// jaExiste explica o que fazer quando o projeto já tem configuração.
//
// Sobrescrever seria inaceitável: o arquivo é do projeto, costuma ter
// comentários explicando decisões, e está versionado. Mas recusar sem dizer
// nada desperdiça o que acabamos de descobrir — daí a lista do que o projeto
// usa e não declara.
func jaExiste(dir string, achados []services.Achado) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s já existe\n", config.Path(dir))

	if faltando := naoDeclarados(dir, achados); len(faltando) > 0 {
		b.WriteString("\n  o projeto parece usar, mas não declara:\n")
		for _, a := range faltando {
			fmt.Fprintf(&b, "    %-16s %s\n", textoDaSpec(a.Spec), a.Origem)
		}

		nomes := make([]string, 0, len(faltando))
		for _, a := range faltando {
			nomes = append(nomes, textoDaSpec(a.Spec))
		}
		fmt.Fprintf(&b, "\n  declare com:  devm service add %s\n", strings.Join(nomes, " "))
	}

	b.WriteString("\n  para ver todas as opções:  devm init --print")
	return fmt.Errorf("%s", b.String())
}

// naoDeclarados filtra os achados que o devmanager.yaml ainda não lista.
func naoDeclarados(dir string, achados []services.Achado) []services.Achado {
	declarados, err := servicosDeclarados(dir)
	if err != nil {
		return nil
	}

	ja := make(map[string]bool, len(declarados))
	for _, texto := range declarados {
		nome, _, _ := strings.Cut(texto, ":")
		ja[nome] = true
	}

	var faltando []services.Achado
	for _, a := range achados {
		if !ja[a.Spec.Nome] {
			faltando = append(faltando, a)
		}
	}
	return faltando
}

// deduzir monta a configuração a partir do que o projeto já tem.
func deduzir(dir string) (config.Config, string, []services.Achado) {
	php, origem := phpDoProjeto(dir)
	c := config.Config{PHP: php}

	achados := services.Descobrir(dir)
	for _, a := range achados {
		c.Services = append(c.Services, textoDaSpec(a.Spec))
	}
	return c, origem, achados
}

// versoesNaMaquina lista, por serviço, as versões que já existem aqui.
//
// Serve a uma dica, não a uma decisão: um contêiner por versão atende todos
// os projetos, então declarar mysql:8.0 onde a máquina já roda mysql:8.4
// dobra o consumo de memória para nada. Quem escolhe continua sendo o dono
// do projeto — pode haver motivo real para a versão antiga.
func versoesNaMaquina() map[string][]string {
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()

	engine, err := services.DetectarConfigurado(ctx)
	if err != nil {
		return nil // sem engine não há o que comparar, e isso não é erro
	}

	lista, err := (&services.Manager{Engine: engine}).List(ctx)
	if err != nil {
		return nil
	}

	versoes := make(map[string][]string, len(lista))
	for _, s := range lista {
		versoes[s.Nome] = append(versoes[s.Nome], s.Versao)
	}
	return versoes
}

// phpDoProjeto escolhe a versão a fixar e explica de onde ela veio.
//
// O compose do Sail vence a detecção: ele diz em que versão o projeto
// REALMENTE rodava, enquanto o composer.json costuma declarar só o piso
// ("^8.2" num projeto que roda há anos em 8.4). Fixar o piso mudaria o
// ambiente de execução de quem está migrando — exatamente o que a migração
// não pode fazer.
func phpDoProjeto(dir string) (versao, origem string) {
	if versao, arquivo, ok := phpDoSail(dir); ok {
		return versao, arquivo + " (runtime do Sail)"
	}
	return phpDetectado(dir), "a maior instalada que atende o composer.json"
}

// runtimeDoSail captura a versão no caminho do runtime que o Sail constrói:
// ./vendor/laravel/sail/runtimes/8.4
var runtimeDoSail = regexp.MustCompile(`sail/runtimes/(\d+\.\d+)`)

func phpDoSail(dir string) (versao, arquivo string, ok bool) {
	nome, dados, existe := services.LerCompose(dir)
	if !existe {
		return "", "", false
	}

	achado := runtimeDoSail.FindSubmatch(dados)
	if achado == nil {
		return "", "", false
	}
	return string(achado[1]), nome, true
}

// phpDetectado devolve a versão a fixar, ou "" se não der para saber.
//
// Fixar a versão RESOLVIDA, e não a exigência do composer.json, é de
// propósito: "^8.2" num projeto que roda em 8.4 fixaria o piso, e o projeto
// passaria a rodar numa versão diferente da que estava em uso.
func phpDetectado(dir string) string {
	p, err := project.Detect(dir)
	if err != nil {
		return ""
	}

	res := resolverPHP(p)
	if !res.Achou {
		return ""
	}
	return res.Runtime.Version.MajorMinor()
}
