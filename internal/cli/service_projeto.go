package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/services"
)

// serviceAddCmd declara serviços no devmanager.yaml do projeto.
//
//	devm service add postgres:17 redis
//
// Separado do `service start`, que sobe um contêiner avulso na máquina: este
// muda o que o PROJETO precisa, e é versionado junto com o código. Um mexe
// no arquivo, o outro no Docker.
func serviceAddCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service add", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm service add <serviço[:versão]>...   (ex.: devm service add postgres:17 redis)")
	}

	novos := make([]services.Spec, 0, len(posicionais))
	for _, texto := range posicionais {
		spec, err := services.ParseSpec(texto)
		if err != nil {
			return err
		}
		novos = append(novos, spec)
	}

	atual, err := servicosDeclarados(".")
	if err != nil {
		return err
	}

	lista := atual
	for _, spec := range novos {
		lista = substituirOuIncluir(lista, spec)
	}

	if err := config.SetLista(".", "services", lista); err != nil {
		return err
	}

	for _, spec := range novos {
		fmt.Fprintf(w, "%s declarado em %s\n", textoDaSpec(spec), config.FileName)
	}
	fmt.Fprintln(w, "\nrode `devm up` para subir e ajustar o .env")
	return nil
}

// serviceDropCmd tira serviços do devmanager.yaml.
//
//	devm service drop redis
//
// O contêiner continua na máquina: parar ou apagar dados é decisão separada,
// com `devm service stop` e `devm service remove`. Um projeto deixar de usar
// o Redis não significa que os outros também deixaram.
func serviceDropCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("service drop", flag.ContinueOnError)
	fs.SetOutput(w)

	posicionais, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(posicionais) == 0 {
		return fmt.Errorf("uso: devm service drop <serviço>...   (ex.: devm service drop redis)")
	}

	atual, err := servicosDeclarados(".")
	if err != nil {
		return err
	}
	if len(atual) == 0 {
		return fmt.Errorf("este projeto não declara serviços em %s", config.FileName)
	}

	lista := atual
	for _, texto := range posicionais {
		nome, _, _ := strings.Cut(texto, ":")
		restante, saiu := semOServico(lista, nome)
		if !saiu {
			return fmt.Errorf("%s não está declarado (declarados: %s)", nome, strings.Join(atual, ", "))
		}
		lista = restante
		fmt.Fprintf(w, "%s não é mais exigido por este projeto\n", nome)
	}

	// Lista vazia vira remoção da chave: um `services: []` no arquivo diria
	// a mesma coisa com mais ruído.
	if len(lista) == 0 {
		if err := config.SetChave(".", "services", ""); err != nil {
			return err
		}
	} else if err := config.SetLista(".", "services", lista); err != nil {
		return err
	}

	fmt.Fprintln(w, "\no contêiner continua na máquina — use `devm service stop` se quiser desligá-lo")
	return nil
}

// servicosDeclarados lê a lista atual do devmanager.yaml.
func servicosDeclarados(dir string) ([]string, error) {
	c, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	return c.Services, nil
}

// substituirOuIncluir troca a entrada do mesmo serviço, ou acrescenta.
//
// Um projeto usa UM PostgreSQL: pedir postgres:17 onde já havia postgres:16
// é trocar de versão, não somar um segundo banco.
func substituirOuIncluir(lista []string, spec services.Spec) []string {
	texto := textoDaSpec(spec)

	for i, declarado := range lista {
		nome, _, _ := strings.Cut(declarado, ":")
		if nome == spec.Nome {
			lista[i] = texto
			return lista
		}
	}
	return append(lista, texto)
}

func semOServico(lista []string, nome string) ([]string, bool) {
	saida := make([]string, 0, len(lista))
	achou := false

	for _, declarado := range lista {
		if declaradoNome, _, _ := strings.Cut(declarado, ":"); declaradoNome == nome {
			achou = true
			continue
		}
		saida = append(saida, declarado)
	}
	return saida, achou
}

// textoDaSpec escreve a spec como ela vai para o arquivo.
//
// "latest" não é versão: declarar `mailpit` diz a mesma coisa sem fingir
// precisão que não existe.
func textoDaSpec(spec services.Spec) string {
	if spec.Versao == "" || spec.Versao == "latest" {
		return spec.Nome
	}
	return spec.Nome + ":" + spec.Versao
}
