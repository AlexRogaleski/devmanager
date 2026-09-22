// Package upgrade troca o binário do Dev Manager pela versão publicada.
//
// A alternativa é o que fazíamos até aqui: clonar o repositório, compilar e
// instalar à mão. Isso serve a quem desenvolve a ferramenta, não a quem só a
// usa — e mesmo quem desenvolve esquece de atualizar a cópia instalada.
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repositorio é de onde as versões vêm.
const Repositorio = "AlexRogaleski/devmanager"

// Progresso recebe o andamento do download.
type Progresso func(baixado, total int64)

// Versao é uma release publicada.
type Versao struct {
	Tag      string
	Arquivos map[string]string // nome do arquivo → URL de download
}

// Atualizador consulta e instala versões.
//
// Os campos existem para o teste: URLBase aponta para um servidor local, SO e
// Arch fixam a plataforma, e Destino evita que a suíte substitua o binário da
// máquina de quem roda os testes.
type Atualizador struct {
	URLBase string
	Cliente *http.Client
	SO      string
	Arch    string
	Destino string
}

func (a *Atualizador) base() string {
	if a.URLBase != "" {
		return a.URLBase
	}
	return "https://api.github.com"
}

func (a *Atualizador) cliente() *http.Client {
	if a.Cliente != nil {
		return a.Cliente
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// NomeDoArquivo é como o binário desta plataforma se chama na Release.
func (a *Atualizador) NomeDoArquivo() string {
	so, arch := a.SO, a.Arch
	if so == "" {
		so = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	return fmt.Sprintf("devm-%s-%s", so, arch)
}

// Ultima devolve a versão publicada mais recente.
func (a *Atualizador) Ultima(ctx context.Context) (Versao, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", a.base(), Repositorio)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Versao{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.cliente().Do(req)
	if err != nil {
		return Versao{}, fmt.Errorf("consultando as versões publicadas: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Versao{}, fmt.Errorf("consultando as versões publicadas: HTTP %s", resp.Status)
	}

	var corpo struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Nome string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		return Versao{}, fmt.Errorf("lendo a resposta: %w", err)
	}

	v := Versao{Tag: corpo.Tag, Arquivos: make(map[string]string, len(corpo.Assets))}
	for _, a := range corpo.Assets {
		v.Arquivos[a.Nome] = a.URL
	}
	return v, nil
}

// Instalar baixa o binário da versão e o põe no lugar do atual.
//
// A conferência do checksum não é opcional: um binário é executado com as
// permissões de quem o roda, e "baixou e instalou" sem verificar é como a
// maior parte dos incidentes de cadeia de suprimentos começa.
func (a *Atualizador) Instalar(ctx context.Context, v Versao, prog Progresso) (string, error) {
	nome := a.NomeDoArquivo()

	url, ok := v.Arquivos[nome]
	if !ok {
		return "", fmt.Errorf("a versão %s não publica um binário para %s", v.Tag, nome)
	}
	urlSomas, ok := v.Arquivos["SHA256SUMS"]
	if !ok {
		return "", fmt.Errorf("a versão %s não publica o SHA256SUMS", v.Tag)
	}

	esperado, err := a.somaPublicada(ctx, urlSomas, nome)
	if err != nil {
		return "", err
	}

	destino, err := a.caminhoDoBinario()
	if err != nil {
		return "", err
	}

	// O temporário fica NO MESMO diretório do destino: rename entre sistemas
	// de arquivos diferentes falha, e /tmp costuma ser outro.
	tmp, err := os.CreateTemp(filepath.Dir(destino), ".devm-novo-*")
	if err != nil {
		return "", fmt.Errorf("criando arquivo em %s: %w", filepath.Dir(destino), err)
	}
	defer os.Remove(tmp.Name())

	obtido, err := a.baixar(ctx, url, tmp, prog)
	tmp.Close()
	if err != nil {
		return "", err
	}

	if obtido != esperado {
		return "", fmt.Errorf("o binário baixado não confere com o checksum publicado\n  esperado: %s\n  obtido:   %s", esperado, obtido)
	}

	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", err
	}

	// Rename por cima do binário EM EXECUÇÃO funciona: o processo atual
	// continua ligado ao inode antigo, que só desaparece quando ele termina.
	if err := os.Rename(tmp.Name(), destino); err != nil {
		return "", fmt.Errorf("substituindo %s: %w", destino, err)
	}
	return destino, nil
}

// caminhoDoBinario devolve onde gravar, resolvendo links simbólicos.
func (a *Atualizador) caminhoDoBinario() (string, error) {
	if a.Destino != "" {
		return a.Destino, nil
	}

	eu, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("descobrindo o próprio caminho: %w", err)
	}

	return resolverLink(eu), nil
}

// resolverLink segue links simbólicos até o arquivo de verdade.
//
// Sem isso, o rename trocaria o LINK por um arquivo comum: quem instalou com
// `ln -s` para o binário compilado ficaria com uma cópia congelada no lugar
// do link, e a próxima compilação não apareceria mais.
func resolverLink(caminho string) string {
	if resolvido, err := filepath.EvalSymlinks(caminho); err == nil {
		return resolvido
	}
	return caminho
}

// somaPublicada procura, no SHA256SUMS, a linha do arquivo desta plataforma.
func (a *Atualizador) somaPublicada(ctx context.Context, url, arquivo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.cliente().Do(req)
	if err != nil {
		return "", fmt.Errorf("baixando o SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("baixando o SHA256SUMS: HTTP %s", resp.Status)
	}

	dados, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	return SomaDe(string(dados), arquivo)
}

// SomaDe extrai do conteúdo de um SHA256SUMS a soma de um arquivo.
func SomaDe(conteudo, arquivo string) (string, error) {
	for _, linha := range strings.Split(conteudo, "\n") {
		campos := strings.Fields(linha)
		if len(campos) != 2 {
			continue
		}
		// O formato do sha256sum marca o modo binário com um "*" antes do
		// nome; sem tirá-lo, a comparação nunca casa.
		if strings.TrimPrefix(campos[1], "*") == arquivo {
			return campos[0], nil
		}
	}
	return "", fmt.Errorf("o SHA256SUMS publicado não tem a linha de %s", arquivo)
}

// baixar grava o corpo da resposta e devolve o sha256 do que foi gravado.
func (a *Atualizador) baixar(ctx context.Context, url string, destino io.Writer, prog Progresso) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.cliente().Do(req)
	if err != nil {
		return "", fmt.Errorf("baixando %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("baixando %s: HTTP %s", url, resp.Status)
	}

	h := sha256.New()

	var origem io.Reader = resp.Body
	if prog != nil {
		origem = &leitorMedido{r: resp.Body, total: resp.ContentLength, notificar: prog}
	}

	// MultiWriter manda os mesmos bytes para o arquivo e para o cálculo do
	// hash: o conteúdo é lido uma vez só.
	if _, err := io.Copy(io.MultiWriter(destino, h), origem); err != nil {
		return "", fmt.Errorf("gravando o binário: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type leitorMedido struct {
	r         io.Reader
	total     int64
	lido      int64
	notificar Progresso
}

func (l *leitorMedido) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.lido += int64(n)
	if n > 0 {
		l.notificar(l.lido, l.total)
	}
	return n, err
}
