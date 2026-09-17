// Package registry guarda a lista de projetos que o Dev Manager conhece.
//
// Até aqui a ferramenta só enxergava a pasta atual. Um registro persistido é
// o que permite listar projetos, subir e derrubar ambientes sem estar dentro
// deles, e alimentar uma GUI — e é a estrutura que o daemon vai servir pela
// API quando existir.
//
// O arquivo é JSON, não YAML: diferente do devmanager.yaml, este arquivo é
// escrito pela máquina e não deveria ser editado à mão, então preservar
// comentários não faz sentido aqui.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// FileName é o nome do arquivo dentro do diretório de dados.
const FileName = "projects.json"

// Projeto é uma entrada do registro.
//
// Guardamos o MÍNIMO: nome e caminho. Tudo o mais — versão de PHP, se está
// pronto, se é Laravel — é derivado do disco na hora de mostrar. Estado
// derivado que é persistido fica velho; estado derivado na hora nunca mente.
type Projeto struct {
	// Nome identifica o projeto e vira o domínio local (nome.test),
	// por isso precisa ser único no registro.
	Nome string `json:"name"`

	Caminho      string    `json:"path"`
	AdicionadoEm time.Time `json:"added_at"`
}

// Registro é o conjunto de projetos conhecidos.
type Registro struct {
	Projetos []Projeto `json:"projects"`

	// caminho é minúsculo: não é dado do registro, é de onde ele veio.
	// Campos não exportados são ignorados pelo encoding/json.
	caminho string
}

// Path devolve onde o registro fica, dado o diretório de dados.
func Path(dataDir string) string {
	return filepath.Join(dataDir, FileName)
}

// Carregar lê o registro. Arquivo ausente devolve um registro vazio.
func Carregar(caminho string) (*Registro, error) {
	r := &Registro{caminho: caminho}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}
	if len(dados) == 0 {
		return r, nil
	}

	if err := json.Unmarshal(dados, r); err != nil {
		return nil, fmt.Errorf("%s corrompido: %w", caminho, err)
	}
	r.caminho = caminho
	r.ordenar()
	return r, nil
}

// Adicionar registra um projeto.
//
// Recusa duas situações diferentes, com mensagens diferentes, porque exigem
// ações diferentes do usuário: caminho já registrado (não há o que fazer) e
// nome em uso por outro caminho (precisa escolher outro nome).
func (r *Registro) Adicionar(p Projeto) error {
	abs, err := filepath.Abs(p.Caminho)
	if err != nil {
		return fmt.Errorf("resolvendo caminho %q: %w", p.Caminho, err)
	}
	p.Caminho = abs

	if p.Nome == "" {
		p.Nome = filepath.Base(abs)
	}
	if err := ValidarNome(p.Nome); err != nil {
		return err
	}

	for _, existente := range r.Projetos {
		if existente.Caminho == abs {
			return &JaRegistradoError{Nome: existente.Nome, Caminho: abs}
		}
		if strings.EqualFold(existente.Nome, p.Nome) {
			return &NomeEmUsoError{Nome: p.Nome, Caminho: existente.Caminho}
		}
	}

	if p.AdicionadoEm.IsZero() {
		p.AdicionadoEm = time.Now()
	}

	r.Projetos = append(r.Projetos, p)
	r.ordenar()
	return nil
}

// Remover tira um projeto do registro pelo nome. Não apaga nada do disco.
func (r *Registro) Remover(nome string) bool {
	for i, p := range r.Projetos {
		if strings.EqualFold(p.Nome, nome) {
			r.Projetos = append(r.Projetos[:i], r.Projetos[i+1:]...)
			return true
		}
	}
	return false
}

// Buscar encontra um projeto pelo nome.
func (r *Registro) Buscar(nome string) (Projeto, bool) {
	for _, p := range r.Projetos {
		if strings.EqualFold(p.Nome, nome) {
			return p, true
		}
	}
	return Projeto{}, false
}

// BuscarPorCaminho encontra um projeto pelo caminho no disco.
func (r *Registro) BuscarPorCaminho(caminho string) (Projeto, bool) {
	abs, err := filepath.Abs(caminho)
	if err != nil {
		return Projeto{}, false
	}
	for _, p := range r.Projetos {
		if p.Caminho == abs {
			return p, true
		}
	}
	return Projeto{}, false
}

// Salvar grava o registro no disco, de forma atômica.
func (r *Registro) Salvar() error {
	if r.caminho == "" {
		return fmt.Errorf("registro sem caminho de gravação")
	}

	if err := os.MkdirAll(filepath.Dir(r.caminho), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(r.caminho), err)
	}

	dados, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("serializando registro: %w", err)
	}

	return escreverAtomico(r.caminho, append(dados, '\n'))
}

// ordenar mantém a lista estável por nome.
//
// Sem isso a ordem seria a de inserção, e cada gravação produziria um diff
// diferente — irrelevante hoje, mas importante quando alguém olhar o arquivo
// ou quando a GUI exibir a lista.
func (r *Registro) ordenar() {
	slices.SortFunc(r.Projetos, func(a, b Projeto) int {
		return strings.Compare(strings.ToLower(a.Nome), strings.ToLower(b.Nome))
	})
}

// ValidarNome garante que o nome pode virar um domínio local.
//
// A validação é aqui, e não só na CLI, porque o nome vai virar "nome.test":
// aceitar um nome com espaço ou barra criaria um projeto que nunca poderia
// ser servido, e o erro apareceria muito depois da causa.
func ValidarNome(nome string) error {
	if nome == "" {
		return fmt.Errorf("o nome do projeto não pode ser vazio")
	}
	if len(nome) > 63 {
		return fmt.Errorf("o nome %q é longo demais (máximo 63 caracteres, por causa do DNS)", nome)
	}

	for _, r := range nome {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("o nome %q tem caractere inválido %q (use letras, números, - e _)", nome, r)
		}
	}

	if strings.HasPrefix(nome, "-") || strings.HasSuffix(nome, "-") {
		return fmt.Errorf("o nome %q não pode começar nem terminar com hífen", nome)
	}
	return nil
}

// JaRegistradoError sai quando o caminho já está no registro.
type JaRegistradoError struct {
	Nome    string
	Caminho string
}

func (e *JaRegistradoError) Error() string {
	return fmt.Sprintf("%s já está registrado como %q", e.Caminho, e.Nome)
}

// NomeEmUsoError sai quando outro projeto já usa o nome.
type NomeEmUsoError struct {
	Nome    string
	Caminho string
}

func (e *NomeEmUsoError) Error() string {
	return fmt.Sprintf("o nome %q já é usado por %s (escolha outro com --name)", e.Nome, e.Caminho)
}

func escreverAtomico(destino string, dados []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(destino), ".projects-*.json")
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
