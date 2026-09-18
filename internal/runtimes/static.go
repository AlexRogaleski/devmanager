package runtimes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// SourceStatic marca runtimes baixados como binário estático.
const SourceStatic = "static"

// URLPadrao é o repositório de builds do static-php-cli.
const URLPadrao = "https://dl.static-php.dev/static-php-cli"

// VariantePadrao é o conjunto de extensões usado quando nada é especificado.
//
// Medindo os builds reais com PDO::getAvailableDrivers():
//
//	common   (12 MB)  drivers mysql, pgsql e sqlite; SEM intl, readline
//	                  nem opcache
//	bulk     (31 MB)  os mesmos drivers, MAIS intl, readline, opcache,
//	                  sodium, imagick e swoole — e totalmente estático
//	gnu-bulk (114 MB) idêntico ao bulk em extensões, mas ligado à glibc
//
// O bulk é estritamente melhor que o common: mesmos drivers de banco e mais
// 16 extensões, por 19 MB a mais. O gnu-bulk não traz nada além e custa 83 MB
// a mais, além de deixar de ser estático — o que descarta a portabilidade que
// motivou a escolha por binário estático.
//
// Nota sobre como medir: `php -m` NÃO lista os drivers compilados dentro da
// extensão PDO. Uma leitura apressada dessa saída sugere que o bulk não tem
// pdo_pgsql nem pdo_sqlite, e a conclusão é falsa —
// PDO::getAvailableDrivers() mostra os três. Esse engano custou a escolha
// errada de padrão durante um tempo.
const VariantePadrao = "bulk"

// StaticProvider instala PHPs estáticos isolados do sistema.
//
// É a razão de ser do projeto: binários autocontidos, sem depender do
// gerenciador de pacotes da distro. Funciona igual em Ubuntu, Fedora
// Silverblue, Bazzite ou macOS, que é o que torna a ferramenta viável em
// sistemas imutáveis.
type StaticProvider struct {
	// Dir é a raiz das instalações: <Dir>/<versão>/bin/php.
	Dir string

	// Variant escolhe o conjunto de extensões. Vazio usa VariantePadrao.
	Variant string

	// BaseURL permite apontar para um espelho — e, nos testes, para um
	// servidor HTTP local, sem tocar na rede de verdade.
	BaseURL string

	// Client permite injetar timeouts e transportes próprios.
	Client *http.Client
}

func (p *StaticProvider) Name() string { return SourceStatic }

func (p *StaticProvider) variante() string {
	if p.Variant == "" {
		return VariantePadrao
	}
	return p.Variant
}

func (p *StaticProvider) baseURL() string {
	if p.BaseURL == "" {
		return URLPadrao
	}
	return p.BaseURL
}

func (p *StaticProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	// Sem timeout global de propósito: o download de 30 MB pode ser lento.
	// O controle de tempo fica com o context de quem chama, que é cancelável.
	return http.DefaultClient
}

// List devolve os PHPs já instalados por este Provider.
//
// A varredura é do disco, não de um índice nosso: se o usuário apagar uma
// pasta à mão, o Dev Manager simplesmente deixa de ver aquela versão. Estado
// derivado do disco nunca fica dessincronizado.
func (p *StaticProvider) List(ctx context.Context) ([]Runtime, error) {
	entradas, err := os.ReadDir(p.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // nada instalado ainda: normal, não é erro
		}
		return nil, fmt.Errorf("lendo %s: %w", p.Dir, err)
	}

	var encontrados []Runtime
	for _, e := range entradas {
		if !e.IsDir() {
			continue
		}

		bin := filepath.Join(p.Dir, e.Name(), "bin", "php")
		if _, err := os.Stat(bin); err != nil {
			continue
		}

		// A versão vem do binário, não do nome da pasta: se alguém renomear
		// a pasta, continuamos reportando a verdade.
		v, err := versaoDoBinario(ctx, bin, 5*time.Second)
		if err != nil {
			continue
		}

		encontrados = append(encontrados, Runtime{
			Language: "php",
			Version:  v,
			Bin:      bin,
			Source:   SourceStatic,
		})
	}
	return encontrados, nil
}

// nomeDoBuild casa os arquivos publicados, no formato
// php-8.3.32-cli-linux-x86_64.tar.gz
var nomeDoBuild = regexp.MustCompile(`^php-(\d+\.\d+\.\d+)-cli-([a-z]+)-([a-z0-9_]+)\.tar\.gz$`)

// Installable lista as versões que dá para baixar para esta plataforma.
func (p *StaticProvider) Installable(ctx context.Context) ([]semver.Version, error) {
	entradas, err := p.indice(ctx)
	if err != nil {
		return nil, err
	}

	so, arch := plataforma()

	var versoes []semver.Version
	for _, e := range entradas {
		m := nomeDoBuild.FindStringSubmatch(e.Name)
		if m == nil || m[2] != so || m[3] != arch {
			continue
		}
		v, err := semver.Parse(m[1])
		if err != nil {
			continue
		}
		versoes = append(versoes, v)
	}

	slices.SortFunc(versoes, func(a, b semver.Version) int { return b.Compare(a) })
	return versoes, nil
}

// entradaIndice é uma linha do índice publicado pelo servidor.
type entradaIndice struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  string `json:"size"`
}

func (p *StaticProvider) indice(ctx context.Context) ([]entradaIndice, error) {
	url := fmt.Sprintf("%s/%s/?format=json", p.baseURL(), p.variante())

	// NewRequestWithContext amarra a requisição ao context: cancelar o
	// context aborta a conexão em andamento, não só ignora o resultado.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("consultando %s: %w", url, err)
	}
	// Fechar o corpo é obrigatório, senão a conexão vaza do pool.
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consultando %s: HTTP %s", url, resp.Status)
	}

	var entradas []entradaIndice
	if err := json.NewDecoder(resp.Body).Decode(&entradas); err != nil {
		return nil, fmt.Errorf("lendo índice de %s: %w", url, err)
	}
	return entradas, nil
}

// plataforma traduz os nomes do Go para os nomes usados nos arquivos.
func plataforma() (so, arch string) {
	so = runtime.GOOS
	if so == "darwin" {
		so = "macos"
	}

	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		arch = runtime.GOARCH
	}
	return so, arch
}

// URLDoBuild devolve a URL do tarball de uma versão exata.
func (p *StaticProvider) URLDoBuild(v semver.Version) string {
	so, arch := plataforma()
	return fmt.Sprintf("%s/%s/php-%s-cli-%s-%s.tar.gz", p.baseURL(), p.variante(), v, so, arch)
}

// DirDaVersao devolve onde uma versão fica instalada.
func (p *StaticProvider) DirDaVersao(v semver.Version) string {
	return filepath.Join(p.Dir, v.String())
}

// Remove apaga uma versão baixada.
func (p *StaticProvider) Remove(v semver.Version) error {
	return removerDir(p.Dir, p.DirDaVersao(v))
}
