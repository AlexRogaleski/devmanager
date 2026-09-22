package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Criar grava um devmanager.yaml novo a partir de uma configuração inteira.
//
// Existe para quem já sabe tudo que vai escrever — o `devm new`, que decidiu
// PHP, Node e banco antes de tocar no disco. Fazer isso com três SetChave
// seguidos deixaria o arquivo com o valor ativo longe do comentário que o
// explica, porque cada edição posterior entra no fim do documento.
//
// Sobrescreve: a decisão de não apagar arquivo alheio é de quem chama.
func Criar(dir string, c Config) error {
	caminho := Path(dir)
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(caminho), err)
	}
	return escreverAtomico(caminho, []byte(Modelo(c)))
}

// SetChave grava (ou remove, se valor vazio) uma chave escalar.
//
// Generaliza o que antes era exclusivo do php. A edição continua sendo pelo
// yaml.Node, preservando comentários e demais chaves.
func SetChave(dir, chave, valor string) error {
	if valor == "" {
		return removerChaveDoArquivo(dir, chave)
	}

	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("lendo %s: %w", caminho, err)
		}
		// Arquivo novo: geramos o modelo inteiro, com esta chave ativa e as
		// demais documentadas em comentário.
		var novo Config
		switch chave {
		case "php":
			novo.PHP = valor
		case "node":
			novo.Node = valor
		default:
			return escreverAtomico(caminho, []byte(fmt.Sprintf("%s: %q\n", chave, valor)))
		}
		return escreverAtomico(caminho, []byte(Modelo(novo)))
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(dados, &doc); err != nil {
		return fmt.Errorf("%s inválido: %w", caminho, err)
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}
	definirEscalar(mapa, chave, valor)

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}
	return escreverAtomico(caminho, preservarEspacamento(dados, saida))
}

// SetLista grava uma chave com valores em sequência.
//
// Usada para services, que é uma lista. Compartilha a disciplina do
// SetChave: edita pelo yaml.Node, preservando comentários e demais chaves.
func SetLista(dir, chave string, valores []string) error {
	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lendo %s: %w", caminho, err)
	}

	if len(dados) == 0 && chave == "services" {
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			return fmt.Errorf("criando %s: %w", filepath.Dir(caminho), err)
		}
		return escreverAtomico(caminho, []byte(Modelo(Config{Services: valores})))
	}

	var doc yaml.Node
	if len(dados) > 0 {
		if err := yaml.Unmarshal(dados, &doc); err != nil {
			return fmt.Errorf("%s inválido: %w", caminho, err)
		}
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}
	definirSequencia(mapa, chave, valores)

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(caminho), err)
	}
	return escreverAtomico(caminho, preservarEspacamento(dados, saida))
}

// definirSequencia cria ou substitui uma chave de lista.
func definirSequencia(mapa *yaml.Node, chave string, valores []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range valores {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}

	for i := 0; i+1 < len(mapa.Content); i += 2 {
		if mapa.Content[i].Value == chave {
			mapa.Content[i+1] = seq
			return
		}
	}

	mapa.Content = append(mapa.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: chave},
		seq,
	)
}

// removerChaveDoArquivo tira uma chave, apagando o arquivo se ele ficar vazio.
func removerChaveDoArquivo(dir, chave string) error {
	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lendo %s: %w", caminho, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(dados, &doc); err != nil {
		return fmt.Errorf("%s inválido: %w", caminho, err)
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}
	if !removerChave(mapa, chave) {
		return nil
	}

	if len(mapa.Content) == 0 {
		if err := os.Remove(caminho); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removendo %s: %w", caminho, err)
		}
		return nil
	}

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}
	return escreverAtomico(caminho, preservarEspacamento(dados, saida))
}

// SetPHP fixa a versão de PHP do projeto, preservando o resto do arquivo.
//
// Se o arquivo não existir, é criado a partir do modelo comentado. Se existir,
// apenas o valor da chave "php" muda — comentários, ordem e formatação do
// resto permanecem, pela mesma razão que fizemos isso com o settings.json:
// o arquivo pertence ao desenvolvedor.
func SetPHP(dir, versao string) error {
	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("lendo %s: %w", caminho, err)
		}
		return escreverAtomico(caminho, []byte(Modelo(Config{PHP: versao})))
	}

	// yaml.Node é a representação em árvore do documento, e o yaml.v3 guarda
	// os comentários nela. Decodificar para Node (em vez de para a struct
	// Config) e recodificar preserva o que a struct descartaria.
	var doc yaml.Node
	if err := yaml.Unmarshal(dados, &doc); err != nil {
		return fmt.Errorf("%s inválido: %w", caminho, err)
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}

	definirEscalar(mapa, "php", versao)

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}
	return escreverAtomico(caminho, preservarEspacamento(dados, saida))
}

// ClearPHP remove a fixação, voltando a decisão para o composer.json.
func ClearPHP(dir string) error {
	caminho := Path(dir)

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // já não havia nada fixado
		}
		return fmt.Errorf("lendo %s: %w", caminho, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(dados, &doc); err != nil {
		return fmt.Errorf("%s inválido: %w", caminho, err)
	}

	mapa, err := mapaRaiz(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", caminho, err)
	}
	if !removerChave(mapa, "php") {
		return nil
	}

	// Se não sobrou nenhuma chave, o arquivo perdeu o propósito. Manter um
	// devmanager.yaml contendo só "{}" e comentários órfãos seria pior que
	// não ter arquivo: ele apareceria no git e confundiria quem lesse depois.
	if len(mapa.Content) == 0 {
		if err := os.Remove(caminho); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removendo %s: %w", caminho, err)
		}
		return nil
	}

	saida, err := serializar(&doc)
	if err != nil {
		return err
	}
	return escreverAtomico(caminho, preservarEspacamento(dados, saida))
}

// mapaRaiz devolve o nó de mapeamento na raiz do documento.
//
// A estrutura do yaml.v3 é: DocumentNode -> Content[0] -> MappingNode.
// Um arquivo vazio não tem Content, então criamos o mapeamento na hora.
func mapaRaiz(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 || len(doc.Content) == 0 {
		mapa := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{mapa}
		return mapa, nil
	}

	raiz := doc.Content[0]
	if raiz.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("a raiz do arquivo não é um mapeamento de chaves")
	}
	return raiz, nil
}

// definirEscalar cria ou atualiza uma chave de nível raiz.
//
// Num MappingNode, Content é uma lista plana e alternada:
// [chave1, valor1, chave2, valor2, ...]. Por isso os saltos de 2 em 2.
func definirEscalar(mapa *yaml.Node, chave, valor string) {
	for i := 0; i+1 < len(mapa.Content); i += 2 {
		if mapa.Content[i].Value != chave {
			continue
		}
		v := mapa.Content[i+1]
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = valor
		// Style com aspas duplas evita que "8.3" seja lido como número
		// na próxima leitura — um clássico do YAML.
		v.Style = yaml.DoubleQuotedStyle
		return
	}

	mapa.Content = append(mapa.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: chave},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: valor, Style: yaml.DoubleQuotedStyle},
	)
}

func removerChave(mapa *yaml.Node, chave string) bool {
	for i := 0; i+1 < len(mapa.Content); i += 2 {
		if mapa.Content[i].Value == chave {
			// Remove o par chave+valor de uma vez. O ... expande o slice
			// da direita como argumentos individuais do append.
			mapa.Content = append(mapa.Content[:i], mapa.Content[i+2:]...)
			return true
		}
	}
	return false
}

// preservarEspacamento devolve o texto novo com as linhas em branco que o
// original tinha.
//
// O yaml.v3 guarda comentários no Node, mas não linhas vazias: reserializar
// um arquivo espaçado por seções devolve um bloco denso. Como toda edição
// nossa passa por aqui, `devm service drop redis` reformatava o arquivo
// inteiro do desenvolvedor — um diff com uma linha de conteúdo e várias de
// ruído.
//
// A reconstrução é por CONTEÚDO da linha, não por posição: as chaves e os
// comentários continuam os mesmos entre as duas versões, e o que mudou de
// lugar simplesmente não casa e fica sem o espaço.
func preservarEspacamento(original, novo []byte) []byte {
	if len(original) == 0 {
		return novo
	}

	// Linhas que, no original, vinham logo depois de uma linha em branco.
	espacadas := make(map[string]bool)
	anteriorVazia := false
	for _, linha := range strings.Split(string(original), "\n") {
		if strings.TrimSpace(linha) == "" {
			anteriorVazia = true
			continue
		}
		if anteriorVazia {
			espacadas[linha] = true
		}
		anteriorVazia = false
	}

	if len(espacadas) == 0 {
		return novo
	}

	linhas := strings.Split(string(novo), "\n")
	saida := make([]string, 0, len(linhas)+len(espacadas))
	for i, linha := range linhas {
		// Nunca no começo do arquivo, e nunca duplicando uma vazia.
		if i > 0 && espacadas[linha] && strings.TrimSpace(saida[len(saida)-1]) != "" {
			saida = append(saida, "")
		}
		saida = append(saida, linha)
	}
	return []byte(strings.Join(saida, "\n"))
}

func serializar(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) // o padrão do yaml.v3 é 4; 2 é a convenção do ecossistema
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("gerando YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("finalizando YAML: %w", err)
	}
	return buf.Bytes(), nil
}

// escreverAtomico grava num temporário e renomeia por cima, para que uma
// falha no meio nunca deixe o arquivo truncado.
func escreverAtomico(destino string, dados []byte) error {
	dir := filepath.Dir(destino)

	tmp, err := os.CreateTemp(dir, ".devmanager-*.yaml")
	if err != nil {
		return fmt.Errorf("criando arquivo temporário: %w", err)
	}
	nome := tmp.Name()
	defer os.Remove(nome)

	if _, err := tmp.Write(dados); err != nil {
		tmp.Close()
		return fmt.Errorf("escrevendo %s: %w", destino, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", nome, err)
	}
	if err := os.Chmod(nome, 0o644); err != nil {
		return fmt.Errorf("ajustando permissões: %w", err)
	}
	if err := os.Rename(nome, destino); err != nil {
		return fmt.Errorf("gravando %s: %w", destino, err)
	}
	return nil
}
