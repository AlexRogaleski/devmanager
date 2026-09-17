package cli

import "flag"

// parseArgs faz o que o flag.FlagSet sozinho não faz: aceitar flags misturadas
// com argumentos posicionais, em qualquer ordem.
//
// O flag da stdlib para de interpretar flags assim que encontra o primeiro
// argumento que não começa com "-". Ou seja, em
//
//	devm detect /caminho --json
//
// o "--json" vira um posicional qualquer e a flag nunca é ligada — um bug
// silencioso, porque nada falha: o comando só ignora a flag.
//
// A saída é chamar Parse em laço: a cada volta, consumimos o primeiro
// posicional encontrado e mandamos o RESTO de volta para o Parse, que então
// volta a enxergar flags. Repetimos até não sobrar nada.
//
// Esse laço também funciona com flags que levam valor separado (-o arquivo),
// porque quem decide quantos argumentos consumir continua sendo o Parse.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var posicionais []string
	restante := args

	for {
		if err := fs.Parse(restante); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return posicionais, nil
		}
		posicionais = append(posicionais, fs.Arg(0))
		restante = fs.Args()[1:]
	}
}
