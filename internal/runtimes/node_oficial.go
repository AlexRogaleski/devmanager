package runtimes

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// URLNodePadrao é a distribuição oficial.
const URLNodePadrao = "https://nodejs.org/dist"

// NodeOficialProvider baixa Node dos binários oficiais do nodejs.org.
//
// Os tarballs são autocontidos: node, npm e npx com suas bibliotecas, sem
// depender de nada do sistema além da glibc. É o mesmo modelo do PHP
// estático, e o mesmo que o nvm usa por baixo — só que sem exigir que o
// usuário tenha o nvm instalado.
type NodeOficialProvider struct {
	Dir     string
	BaseURL string
	Client  *http.Client
}

func (p *NodeOficialProvider) Name() string { return SourceNodeOfic }

func (p *NodeOficialProvider) baseURL() string {
	if p.BaseURL == "" {
		return URLNodePadrao
	}
	return p.BaseURL
}

func (p *NodeOficialProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// List devolve os Node já baixados por este Provider.
func (p *NodeOficialProvider) List(ctx context.Context) ([]Runtime, error) {
	entradas, err := os.ReadDir(p.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lendo %s: %w", p.Dir, err)
	}

	var encontrados []Runtime
	for _, e := range entradas {
		if !e.IsDir() {
			continue
		}

		bin := filepath.Join(p.Dir, e.Name(), "bin", "node")
		if _, err := os.Stat(bin); err != nil {
			continue
		}

		v, err := versaoDoNode(ctx, bin, 5*time.Second)
		if err != nil {
			continue
		}

		encontrados = append(encontrados, Runtime{
			Language: "node",
			Version:  v,
			Bin:      bin,
			Source:   SourceNodeOfic,
			Comandos: comandosVizinhos(bin),
		})
	}
	return encontrados, nil
}

// versaoPublicada é uma entrada do index.json do nodejs.org.
type versaoPublicada struct {
	Version string          `json:"version"`
	LTS     json.RawMessage `json:"lts"` // false ou o nome da linha ("Jod")
	Files   []string        `json:"files"`
}

// Installable lista as versões baixáveis para esta plataforma.
//
// O índice traz 866 versões, incluindo tudo desde a 0.x. Filtramos pelo
// arquivo correspondente à plataforma, o que descarta as antigas demais para
// terem build compatível.
func (p *NodeOficialProvider) Installable(ctx context.Context) ([]semver.Version, error) {
	lista, err := p.indice(ctx)
	if err != nil {
		return nil, err
	}

	arquivo := arquivoDaPlataforma()

	var versoes []semver.Version
	for _, e := range lista {
		if !slices.Contains(e.Files, arquivo) {
			continue
		}
		v, err := semver.Parse(e.Version)
		if err != nil {
			continue
		}
		versoes = append(versoes, v)
	}

	slices.SortFunc(versoes, func(a, b semver.Version) int { return b.Compare(a) })
	return versoes, nil
}

func (p *NodeOficialProvider) indice(ctx context.Context) ([]versaoPublicada, error) {
	url := p.baseURL() + "/index.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("consultando %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consultando %s: HTTP %s", url, resp.Status)
	}

	var lista []versaoPublicada
	if err := json.NewDecoder(resp.Body).Decode(&lista); err != nil {
		return nil, fmt.Errorf("lendo o índice do Node: %w", err)
	}
	return lista, nil
}

// plataformaNode traduz os nomes do Go para os do nodejs.org.
func plataformaNode() (so, arch string) {
	so = runtime.GOOS
	if so == "darwin" {
		so = "darwin"
	}

	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		arch = runtime.GOARCH
	}
	return so, arch
}

// arquivoDaPlataforma é a chave usada no campo "files" do índice.
func arquivoDaPlataforma() string {
	so, arch := plataformaNode()
	if so == "darwin" {
		return "osx-" + arch + "-tar"
	}
	return so + "-" + arch
}

// URLDoBuild devolve a URL do tarball de uma versão.
//
// Baixamos .tar.gz, e não .tar.xz. O xz é bem menor, mas a stdlib do Go não
// descomprime xz — usá-lo custaria uma dependência para economizar largura de
// banda num download que acontece uma vez por versão.
func (p *NodeOficialProvider) URLDoBuild(v semver.Version) string {
	so, arch := plataformaNode()
	nome := fmt.Sprintf("node-v%s-%s-%s", v, so, arch)
	return fmt.Sprintf("%s/v%s/%s.tar.gz", p.baseURL(), v, nome)
}

// DirDaVersao devolve onde uma versão fica instalada.
func (p *NodeOficialProvider) DirDaVersao(v semver.Version) string {
	return filepath.Join(p.Dir, v.String())
}

// Install baixa e instala uma versão exata de Node.
func (p *NodeOficialProvider) Install(ctx context.Context, v semver.Version, prog Progresso) (Runtime, error) {
	destino := p.DirDaVersao(v)
	binFinal := filepath.Join(destino, "bin", "node")

	if _, err := os.Stat(binFinal); err == nil {
		return Runtime{
			Language: "node", Version: v, Bin: binFinal,
			Source: SourceNodeOfic, Comandos: comandosVizinhos(binFinal),
		}, nil
	}

	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("criando %s: %w", p.Dir, err)
	}

	// Temporário no MESMO diretório: rename só é atômico dentro do mesmo
	// sistema de arquivos, e /tmp costuma ser outro.
	tmpDir, err := os.MkdirTemp(p.Dir, ".instalando-"+v.String()+"-*")
	if err != nil {
		return Runtime{}, fmt.Errorf("criando diretório temporário: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := p.baixarEExtrair(ctx, v, tmpDir, prog); err != nil {
		return Runtime{}, err
	}

	binTmp := filepath.Join(tmpDir, "bin", "node")
	instalada, err := versaoDoNode(ctx, binTmp, 15*time.Second)
	if err != nil {
		return Runtime{}, fmt.Errorf("o binário baixado não executou: %w", err)
	}
	if instalada != v {
		return Runtime{}, fmt.Errorf("baixamos %s mas o binário se identifica como %s", v, instalada)
	}

	if err := os.Rename(tmpDir, destino); err != nil {
		if _, errStat := os.Stat(binFinal); errStat == nil {
			return Runtime{
				Language: "node", Version: v, Bin: binFinal,
				Source: SourceNodeOfic, Comandos: comandosVizinhos(binFinal),
			}, nil
		}
		return Runtime{}, fmt.Errorf("movendo instalação para %s: %w", destino, err)
	}

	return Runtime{
		Language: "node", Version: v, Bin: binFinal,
		Source: SourceNodeOfic, Comandos: comandosVizinhos(binFinal),
	}, nil
}

// baixarEExtrair puxa o tarball e expande a árvore no destino.
//
// Diferente do PHP, que vem como arquivo único, o Node vem como uma árvore
// dentro de um diretório de topo (node-v24.15.0-linux-x64/). Removemos esse
// primeiro componente para que o conteúdo caia direto no destino.
func (p *NodeOficialProvider) baixarEExtrair(ctx context.Context, v semver.Version, destino string, prog Progresso) error {
	url := p.URLDoBuild(v)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("baixando %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("Node %s não existe para esta plataforma (%s)", v, url)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("baixando %s: HTTP %s", url, resp.Status)
	}

	var origem io.Reader = resp.Body
	if prog != nil {
		origem = &leitorComProgresso{r: resp.Body, total: resp.ContentLength, notificar: prog}
	}

	gz, err := gzip.NewReader(origem)
	if err != nil {
		return fmt.Errorf("o arquivo baixado não é um gzip válido: %w", err)
	}
	defer gz.Close()

	return extrairArvore(tar.NewReader(gz), destino)
}

// extrairArvore expande um tar removendo o primeiro componente do caminho.
func extrairArvore(tr *tar.Reader, destino string) error {
	var total int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lendo o tarball: %w", err)
		}

		rel := semPrimeiroComponente(hdr.Name)
		if rel == "" {
			continue
		}

		// Diferente do PHP, aqui extraímos uma ÁRVORE inteira, então não dá
		// para simplesmente escolher um nome de arquivo. A validação vira
		// obrigatória: uma entrada como "../../.bashrc" escaparia do destino
		// e sobrescreveria arquivos do usuário — o "zip slip".
		alvo := filepath.Join(destino, rel)
		if !dentroDe(destino, alvo) {
			return fmt.Errorf("entrada suspeita no tarball: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(alvo, 0o755); err != nil {
				return fmt.Errorf("criando %s: %w", alvo, err)
			}

		case tar.TypeReg:
			total += hdr.Size
			if total > tamanhoMaximo {
				return fmt.Errorf("tarball excede o limite de %d bytes", tamanhoMaximo)
			}
			if err := escreverArquivo(alvo, tr, hdr.FileInfo().Mode()); err != nil {
				return err
			}

		case tar.TypeSymlink:
			// Links dentro do pacote (npm -> ../lib/node_modules/npm/bin/...)
			// são relativos e ficam contidos na árvore. Um link ABSOLUTO,
			// porém, apontaria para fora dela — recusamos.
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("link absoluto no tarball: %q -> %q", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(alvo), 0o755); err != nil {
				return err
			}
			_ = os.Remove(alvo)
			if err := os.Symlink(hdr.Linkname, alvo); err != nil {
				return fmt.Errorf("criando link %s: %w", alvo, err)
			}
		}
	}
}

// semPrimeiroComponente remove o diretório de topo do caminho.
func semPrimeiroComponente(nome string) string {
	nome = filepath.Clean(strings.TrimPrefix(nome, "./"))

	if i := strings.IndexByte(nome, filepath.Separator); i >= 0 {
		return nome[i+1:]
	}
	return "" // é o próprio diretório de topo
}

// dentroDe confere que o alvo não escapou do diretório de destino.
func dentroDe(base, alvo string) bool {
	rel, err := filepath.Rel(base, alvo)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func escreverArquivo(caminho string, origem io.Reader, modo os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", filepath.Dir(caminho), err)
	}

	f, err := os.OpenFile(caminho, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, modo.Perm())
	if err != nil {
		return fmt.Errorf("criando %s: %w", caminho, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, origem); err != nil {
		return fmt.Errorf("gravando %s: %w", caminho, err)
	}
	return nil
}
