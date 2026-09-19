package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// Origens de Node.
const (
	SourceNVM      = "nvm"
	SourceNodeSist = "system"
	SourceNodeOfic = "nodejs.org"
)

// comandosDoNode são os executáveis que uma instalação de Node oferece.
//
// Expor os três é obrigatório, não conveniência: um `npm run dev` que caísse
// no npm do sistema rodaria com a versão errada de Node por baixo — que é
// exatamente o problema que gerenciar runtime existe para resolver.
var comandosDoNode = []string{"node", "npm", "npx"}

// NodeSystemProvider encontra as instalações de Node já presentes na máquina.
//
// Diferente do PHP, aqui há uma fonte que quase todo desenvolvedor já tem: o
// nvm. Aproveitá-la significa que o gerenciamento de versão funciona no
// primeiro comando, sem baixar nada — e respeita as versões que a pessoa já
// escolheu instalar.
type NodeSystemProvider struct {
	// ExtraDirs aponta diretórios adicionais contendo bin/node.
	ExtraDirs []string

	Timeout time.Duration
}

func (p *NodeSystemProvider) Name() string { return SourceNodeSist }

func (p *NodeSystemProvider) List(ctx context.Context) ([]Runtime, error) {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	vistos := make(map[string]Runtime)

	for _, origem := range p.candidatos() {
		real, err := filepath.EvalSymlinks(origem.bin)
		if err != nil {
			continue
		}
		if _, jaVi := vistos[real]; jaVi {
			continue
		}

		v, err := versaoDoNode(ctx, real, timeout)
		if err != nil {
			continue
		}

		vistos[real] = Runtime{
			Language: "node",
			Version:  v,
			Bin:      real,
			Source:   origem.fonte,
			Comandos: comandosVizinhos(real),
		}
	}

	encontrados := make([]Runtime, 0, len(vistos))
	for _, r := range vistos {
		encontrados = append(encontrados, r)
	}
	return encontrados, nil
}

type candidatoNode struct {
	bin   string
	fonte string
}

// candidatos lista onde procurar Node, por ordem de especificidade.
func (p *NodeSystemProvider) candidatos() []candidatoNode {
	var saida []candidatoNode

	for _, dir := range p.ExtraDirs {
		saida = append(saida, candidatoNode{filepath.Join(dir, "bin", "node"), SourceNodeSist})
	}

	home, err := os.UserHomeDir()
	if err == nil {
		// nvm respeita NVM_DIR, e a instalação padrão varia: ~/.nvm é o
		// clássico, mas quem segue o XDG acaba em ~/.config/nvm.
		nvmDirs := []string{os.Getenv("NVM_DIR")}
		nvmDirs = append(nvmDirs,
			filepath.Join(home, ".nvm"),
			filepath.Join(home, ".config", "nvm"),
		)
		for _, nvm := range nvmDirs {
			if nvm == "" {
				continue
			}
			saida = append(saida, versoesEm(filepath.Join(nvm, "versions", "node"), "bin/node", SourceNVM)...)
		}

		// fnm e volta, os sucessores mais comuns do nvm.
		for _, fnm := range diretoriosDoFnm(home) {
			saida = append(saida, versoesEm(filepath.Join(fnm, "node-versions"), "installation/bin/node", "fnm")...)
		}
		saida = append(saida, versoesEm(filepath.Join(home, ".volta", "tools", "image", "node"), "bin/node", "volta")...)
	}

	// PATH por último: é a versão "global", a menos específica de todas.
	if caminho, err := exec.LookPath("node"); err == nil {
		saida = append(saida, candidatoNode{caminho, SourceNodeSist})
	}
	return saida
}

// diretoriosDoFnm lista onde o fnm pode guardar as versões.
//
// O fnm usa o diretório de dados da plataforma, e ele muda de sistema para
// sistema: ~/.local/share/fnm no Linux, ~/Library/Application Support/fnm
// no macOS. O ~/.fnm é das versões antigas, e o FNM_DIR vence todos. A lista
// tinha só o ~/.fnm, e quem instalou o fnm nos últimos anos não era
// encontrado — em nenhum dos dois sistemas.
//
// Listar todos em qualquer sistema é seguro: pasta inexistente é pulada, e
// duplicatas caem na deduplicação pelo caminho real, mais adiante.
func diretoriosDoFnm(home string) []string {
	var dirs []string
	if d := os.Getenv("FNM_DIR"); d != "" {
		dirs = append(dirs, d)
	}

	dados := filepath.Join(home, ".local", "share")
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		dados = d
	}

	return append(dirs,
		filepath.Join(dados, "fnm"),
		filepath.Join(home, "Library", "Application Support", "fnm"),
		filepath.Join(home, ".fnm"),
	)
}

// versoesEm lista subdiretórios de versão de um gerenciador.
func versoesEm(raiz, relativo, fonte string) []candidatoNode {
	entradas, err := os.ReadDir(raiz)
	if err != nil {
		return nil
	}

	var saida []candidatoNode
	for _, e := range entradas {
		if !e.IsDir() {
			continue
		}
		saida = append(saida, candidatoNode{filepath.Join(raiz, e.Name(), relativo), fonte})
	}
	return saida
}

// comandosVizinhos monta o mapa de executáveis a partir do caminho do node.
//
// npm e npx ficam ao lado do node na mesma pasta bin, em todas as formas de
// instalação. Conferimos a existência em vez de assumir: uma instalação
// mínima pode não ter o npx, e um link quebrado no shim é pior que a ausência.
func comandosVizinhos(binNode string) map[string]string {
	dir := filepath.Dir(binNode)

	cmds := map[string]string{"node": binNode}
	for _, nome := range comandosDoNode[1:] {
		caminho := filepath.Join(dir, nome)
		if _, err := os.Stat(caminho); err == nil {
			cmds[nome] = caminho
		}
	}
	return cmds
}

// versaoDoNode pergunta a versão ao próprio binário.
func versaoDoNode(ctx context.Context, bin string, timeout time.Duration) (semver.Version, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	saida, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return semver.Version{}, err
	}
	// A saída vem como "v24.15.0"; o Parse já descarta o "v".
	return semver.Parse(strings.TrimSpace(string(saida)))
}
