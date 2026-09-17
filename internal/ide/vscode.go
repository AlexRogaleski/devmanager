package ide

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// VSCode configura o .vscode/settings.json do projeto.
type VSCode struct{}

func (v *VSCode) Name() string { return "vscode" }

// Detected procura sinais de que este projeto é aberto no VS Code.
//
// A checagem é deliberadamente generosa por causa de um cenário concreto: em
// sistemas imutáveis o dev roda o VS Code no HOST e o código dentro de um
// container (distrobox/toolbox). Lá dentro não existe binário "code" algum,
// mas o home é compartilhado — então a config do editor no home é o sinal que
// sobra. Exigir o binário deixaria a feature inútil justamente no ambiente
// que o Dev Manager tem como alvo.
func (v *VSCode) Detected(projectPath string) bool {
	if info, err := os.Stat(filepath.Join(projectPath, ".vscode")); err == nil && info.IsDir() {
		return true
	}

	for _, bin := range []string{"code", "codium", "code-insiders"} {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}

	// Configuração do editor no home, mesmo sem o binário visível aqui.
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, marca := range []string{
		".vscode",
		filepath.Join(".config", "Code"),
		filepath.Join(".config", "VSCodium"),
		filepath.Join("Library", "Application Support", "Code"), // macOS
	} {
		if _, err := os.Stat(filepath.Join(home, marca)); err == nil {
			return true
		}
	}
	return false
}

// chaves são as configurações que o Dev Manager gerencia.
//
// Só mexemos nestas. Qualquer outra coisa no settings.json é preservada —
// o arquivo pertence ao desenvolvedor, não à ferramenta.
func (v *VSCode) chaves(cfg Config) map[string]string {
	return map[string]string{
		// Extensão PHP nativa do VS Code: valida sintaxe com este binário.
		"php.validate.executablePath": cfg.PHPBin,

		// Extensão PHP Debug (xdebug): usa este PHP ao lançar scripts.
		"php.debug.executablePath": cfg.PHPBin,

		// Intelephense: sem isso ele assume a versão mais recente que conhece
		// e passa a aceitar sintaxe que o PHP do projeto não entende.
		"intelephense.environment.phpVersion": cfg.PHPVersion.String(),
	}
}

func (v *VSCode) Configure(cfg Config, dryRun bool) (Result, error) {
	dir := filepath.Join(cfg.ProjectPath, ".vscode")
	arquivo := filepath.Join(dir, "settings.json")

	res := Result{File: arquivo, Changes: map[string]string{}}

	bruto, err := os.ReadFile(arquivo)
	if err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("lendo %s: %w", arquivo, err)
	}

	// Arquivo ausente ou vazio: começamos de um objeto mínimo.
	if len(bytes.TrimSpace(bruto)) == 0 {
		bruto = []byte("{\n}\n")
	}

	// Descobrir os valores ATUAIS exige um JSON válido, então parseamos uma
	// cópia sem comentários e sem vírgulas penduradas. O texto original
	// continua intacto — é ele que vai ser editado.
	atual := map[string]any{}
	limpo := removerVirgulasFinais(stripComments(bruto))
	if err := json.Unmarshal(limpo, &atual); err != nil {
		if !cfg.Force {
			return res, fmt.Errorf(
				"%s não pôde ser interpretado como JSON/JSONC (%w)\n"+
					"  Use --force para fazer backup e regravar, ou ajuste o arquivo à mão.",
				arquivo, err)
		}

		backup := arquivo + ".bak"
		if err := copiarArquivo(arquivo, backup); err != nil {
			return res, fmt.Errorf("criando backup: %w", err)
		}
		res.Backup = backup
		bruto = []byte("{\n}\n")
		atual = map[string]any{}
	}

	// Aplica cada chave no TEXTO, preservando comentários, ordem e formatação.
	saida := bruto
	for chave, valor := range v.chaves(cfg) {
		if existente, ok := atual[chave].(string); ok && existente == valor {
			continue // já está correto
		}

		// strconv.Quote produz um literal de string JSON válido, escapando
		// barras e aspas que possam existir no caminho.
		novo, err := definirChave(saida, chave, strconv.Quote(valor))
		if err != nil {
			return res, fmt.Errorf("editando %s: %w", arquivo, err)
		}
		saida = novo
		res.Changes[chave] = valor
	}

	if len(res.Changes) == 0 {
		res.NoChange = true
		return res, nil
	}
	if dryRun {
		return res, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("criando %s: %w", dir, err)
	}
	if err := escreverAtomico(arquivo, saida); err != nil {
		return res, err
	}
	return res, nil
}

// lerSettings continua útil para inspeção rápida em testes.
func lerSettings(arquivo string) (map[string]any, error) {
	dados, err := os.ReadFile(arquivo)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	m := map[string]any{}
	if len(bytes.TrimSpace(dados)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(removerVirgulasFinais(stripComments(dados)), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// escreverAtomico grava num arquivo temporário e renomeia por cima.
//
// rename é atômico dentro do mesmo sistema de arquivos: ou o arquivo antigo
// continua inteiro, ou o novo está completo. Nunca um meio-termo truncado —
// que é o que aconteceria se o processo morresse no meio de um Write direto.
func escreverAtomico(destino string, dados []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(destino), ".settings-*.json")
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

func copiarArquivo(origem, destino string) error {
	dados, err := os.ReadFile(origem)
	if err != nil {
		return err
	}
	return os.WriteFile(destino, dados, 0o644)
}
