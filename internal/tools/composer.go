// Package tools gerencia as ferramentas PHP que o Dev Manager fornece.
//
// A lição que motivou este pacote: depender do composer do sistema não
// funciona. O /usr/bin/composer do Ubuntu tem shebang com caminho absoluto
// (#!/usr/bin/php), então ignora o PATH e roda sempre no PHP da distro; e ele
// carrega bibliotecas Symfony de /usr/share/php, que exigem extensões que um
// PHP estático enxuto não tem.
//
// O phar oficial do getcomposer.org não tem nenhum desses problemas: é
// autocontido e roda em qualquer PHP. Fornecer a ferramenta, em vez de tomar
// emprestada, é o que torna o ambiente reprodutível.
package tools

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
	"strings"
)

// Progresso recebe atualizações durante um download. Pode ser nil.
type Progresso func(baixado, total int64)

// URLComposer é o site oficial de distribuição.
const URLComposer = "https://getcomposer.org"

// Composer baixa e mantém o composer.phar oficial.
type Composer struct {
	// Dir é a raiz das instalações: <Dir>/<versão>/composer.phar
	Dir string

	// BaseURL permite espelho e, nos testes, um servidor local.
	BaseURL string

	Client *http.Client
}

func (c *Composer) baseURL() string {
	if c.BaseURL == "" {
		return URLComposer
	}
	return c.BaseURL
}

func (c *Composer) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

// versoes espelha o /versions do getcomposer.org.
type versoes struct {
	Stable []struct {
		Path    string `json:"path"`
		Version string `json:"version"`
	} `json:"stable"`
}

// VersaoEstavel consulta qual é o composer estável atual.
func (c *Composer) VersaoEstavel(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/versions", nil)
	if err != nil {
		return "", fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("consultando versões do composer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("consultando versões do composer: HTTP %s", resp.Status)
	}

	var v versoes
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", fmt.Errorf("lendo versões do composer: %w", err)
	}
	if len(v.Stable) == 0 {
		return "", fmt.Errorf("nenhuma versão estável publicada")
	}
	return v.Stable[0].Version, nil
}

// Instalado devolve o caminho do phar de uma versão, se ela já existe.
func (c *Composer) Instalado(versao string) (string, bool) {
	caminho := filepath.Join(c.Dir, versao, "composer.phar")
	if _, err := os.Stat(caminho); err != nil {
		return "", false
	}
	return caminho, true
}

// QualquerInstalado devolve algum composer já baixado, sem consultar a rede.
//
// Serve para o modo offline: se já temos um composer, não há motivo para
// falhar só porque a consulta de versões não respondeu.
func (c *Composer) QualquerInstalado() (string, bool) {
	entradas, err := os.ReadDir(c.Dir)
	if err != nil {
		return "", false
	}

	// Percorre de trás para frente: ReadDir devolve ordenado por nome, e
	// versões maiores tendem a vir por último.
	for i := len(entradas) - 1; i >= 0; i-- {
		if !entradas[i].IsDir() {
			continue
		}
		caminho := filepath.Join(c.Dir, entradas[i].Name(), "composer.phar")
		if _, err := os.Stat(caminho); err == nil {
			return caminho, true
		}
	}
	return "", false
}

// Ensure garante que existe um composer.phar e devolve seu caminho.
func (c *Composer) Ensure(ctx context.Context, prog Progresso) (string, error) {
	versao, err := c.VersaoEstavel(ctx)
	if err != nil {
		// Rede indisponível não é motivo para falhar se já temos a ferramenta.
		if caminho, ok := c.QualquerInstalado(); ok {
			return caminho, nil
		}
		return "", err
	}

	if caminho, ok := c.Instalado(versao); ok {
		return caminho, nil
	}
	return c.baixar(ctx, versao, prog)
}

func (c *Composer) baixar(ctx context.Context, versao string, prog Progresso) (string, error) {
	destino := filepath.Join(c.Dir, versao)
	phar := filepath.Join(destino, "composer.phar")

	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return "", fmt.Errorf("criando %s: %w", c.Dir, err)
	}

	// Temporário no mesmo filesystem, para o rename final ser atômico.
	tmpDir, err := os.MkdirTemp(c.Dir, ".baixando-"+versao+"-*")
	if err != nil {
		return "", fmt.Errorf("criando diretório temporário: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	esperado, err := c.checksum(ctx, versao)
	if err != nil {
		return "", err
	}

	tmpPhar := filepath.Join(tmpDir, "composer.phar")
	obtido, err := c.baixarPhar(ctx, versao, tmpPhar, prog)
	if err != nil {
		return "", err
	}

	// Comparação antes de instalar: um phar corrompido ou adulterado nunca
	// chega ao diretório final. Diferente dos builds de PHP, aqui o checksum
	// é publicado, então não há desculpa para não verificar.
	if obtido != esperado {
		return "", fmt.Errorf("checksum do composer %s não confere\n  esperado: %s\n  obtido:   %s",
			versao, esperado, obtido)
	}

	if err := os.Rename(tmpDir, destino); err != nil {
		if _, errStat := os.Stat(phar); errStat == nil {
			return phar, nil // outro processo chegou primeiro
		}
		return "", fmt.Errorf("instalando composer em %s: %w", destino, err)
	}
	return phar, nil
}

func (c *Composer) checksum(ctx context.Context, versao string) (string, error) {
	url := fmt.Sprintf("%s/download/%s/composer.phar.sha256sum", c.baseURL(), versao)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("baixando checksum: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("baixando checksum de %s: HTTP %s", url, resp.Status)
	}

	// O arquivo tem o formato "<hash>  composer.phar".
	dados, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("lendo checksum: %w", err)
	}

	campos := strings.Fields(string(dados))
	if len(campos) == 0 {
		return "", fmt.Errorf("checksum vazio em %s", url)
	}
	return campos[0], nil
}

// baixarPhar grava o arquivo e devolve o sha256 do que foi gravado.
func (c *Composer) baixarPhar(ctx context.Context, versao, destino string, prog Progresso) (string, error) {
	url := fmt.Sprintf("%s/download/%s/composer.phar", c.baseURL(), versao)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("montando requisição: %w", err)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("baixando %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("baixando %s: HTTP %s", url, resp.Status)
	}

	f, err := os.OpenFile(destino, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("criando %s: %w", destino, err)
	}
	defer f.Close()

	h := sha256.New()

	// io.MultiWriter manda os mesmos bytes para os dois destinos ao mesmo
	// tempo: o arquivo e o cálculo do hash. Assim o conteúdo é lido uma vez
	// só, sem precisar reabrir o arquivo depois para conferir.
	var origem io.Reader = resp.Body
	if prog != nil {
		origem = &leitorMedido{r: resp.Body, total: resp.ContentLength, notificar: prog}
	}

	if _, err := io.Copy(io.MultiWriter(f, h), origem); err != nil {
		return "", fmt.Errorf("gravando %s: %w", destino, err)
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
	l.notificar(l.lido, l.total)
	return n, err
}
