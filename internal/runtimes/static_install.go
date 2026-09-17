package runtimes

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// tamanhoMaximo limita o que aceitamos extrair.
//
// Sem esse teto, um servidor comprometido (ou só quebrado) poderia devolver
// um gzip que se expande indefinidamente e enche o disco — a "zip bomb".
// Os builds reais ficam entre 12 e 30 MB; 512 MB é folga suficiente.
const tamanhoMaximo = 512 << 20

// Progresso recebe atualizações durante o download. Pode ser nil.
//
// Um callback em vez de escrever direto no terminal mantém o pacote runtimes
// sem saber o que é uma CLI — quem decide como mostrar é a camada de cima.
type Progresso func(baixado, total int64)

// Install baixa e instala uma versão exata de PHP.
//
// A instalação é atômica: tudo acontece num diretório temporário e só no fim
// ele é renomeado para o lugar definitivo. Uma queda de rede no meio não
// deixa um PHP pela metade que depois seria listado como se funcionasse.
func (p *StaticProvider) Install(ctx context.Context, v semver.Version, prog Progresso) (Runtime, error) {
	destino := p.DirDaVersao(v)
	binFinal := filepath.Join(destino, "bin", "php")

	// Já instalado: operação idempotente, devolve o que existe.
	if _, err := os.Stat(binFinal); err == nil {
		return Runtime{Language: "php", Version: v, Bin: binFinal, Source: SourceStatic}, nil
	}

	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("criando %s: %w", p.Dir, err)
	}

	// MkdirTemp no MESMO diretório de destino: rename só é atômico dentro do
	// mesmo sistema de arquivos, e /tmp costuma ser outro (tmpfs).
	tmpDir, err := os.MkdirTemp(p.Dir, ".instalando-"+v.String()+"-*")
	if err != nil {
		return Runtime{}, fmt.Errorf("criando diretório temporário: %w", err)
	}
	defer os.RemoveAll(tmpDir) // limpa em qualquer caminho de saída

	binTmp := filepath.Join(tmpDir, "bin", "php")
	if err := os.MkdirAll(filepath.Dir(binTmp), 0o755); err != nil {
		return Runtime{}, fmt.Errorf("criando estrutura temporária: %w", err)
	}

	if err := p.baixarEExtrair(ctx, v, binTmp, prog); err != nil {
		return Runtime{}, err
	}

	// Verificar ANTES de mover: se o binário não roda ou é de outra versão,
	// nada chega ao diretório definitivo. Melhor falhar aqui do que instalar
	// algo quebrado que só vai dar erro na primeira vez que for usado.
	instalada, err := versaoDoBinario(ctx, binTmp, 15*time.Second)
	if err != nil {
		return Runtime{}, fmt.Errorf("o binário baixado não executou: %w", err)
	}
	if instalada != v {
		return Runtime{}, fmt.Errorf("baixamos %s mas o binário se identifica como %s", v, instalada)
	}

	if err := os.Rename(tmpDir, destino); err != nil {
		// Corrida entre dois `devm php install` simultâneos: se o destino
		// apareceu no meio do caminho, o trabalho do outro serve.
		if _, errStat := os.Stat(binFinal); errStat == nil {
			return Runtime{Language: "php", Version: v, Bin: binFinal, Source: SourceStatic}, nil
		}
		return Runtime{}, fmt.Errorf("movendo instalação para %s: %w", destino, err)
	}

	return Runtime{Language: "php", Version: v, Bin: binFinal, Source: SourceStatic}, nil
}

// baixarEExtrair puxa o tarball e grava o binário php no caminho indicado.
//
// O tarball é processado em STREAMING: os bytes vão da rede para o gzip, do
// gzip para o tar e do tar para o arquivo, sem nunca existirem inteiros na
// memória nem num arquivo intermediário. É a composição de io.Reader que
// torna isso natural em Go.
func (p *StaticProvider) baixarEExtrair(ctx context.Context, v semver.Version, destino string, prog Progresso) error {
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
		return fmt.Errorf("versão %s não existe para esta plataforma (%s)", v, url)
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

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("o tarball de %s não contém o binário php", v)
		}
		if err != nil {
			return fmt.Errorf("lendo o tarball: %w", err)
		}

		// Comparamos apenas o NOME BASE e escrevemos num caminho que nós
		// escolhemos. Assim uma entrada maliciosa como "../../.bashrc" não
		// tem para onde escapar — o clássico "zip slip" fica impossível
		// por construção, sem precisar validar caminhos.
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "php" {
			continue
		}
		if hdr.Size > tamanhoMaximo {
			return fmt.Errorf("binário grande demais: %d bytes", hdr.Size)
		}

		return escreverBinario(destino, tr)
	}
}

// escreverBinario grava o executável com permissão de execução.
func escreverBinario(destino string, origem io.Reader) error {
	f, err := os.OpenFile(destino, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("criando %s: %w", destino, err)
	}

	// io.CopyN com o teto+1 detecta excesso: se copiar mais que o limite,
	// o tar declarou um tamanho e entregou outro.
	n, err := io.CopyN(f, origem, tamanhoMaximo+1)
	if err != nil && !errors.Is(err, io.EOF) {
		f.Close()
		return fmt.Errorf("gravando %s: %w", destino, err)
	}
	if n > tamanhoMaximo {
		f.Close()
		return fmt.Errorf("binário excede o limite de %d bytes", tamanhoMaximo)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", destino, err)
	}
	return nil
}

// leitorComProgresso embrulha um io.Reader contando o que passa por ele.
//
// É o padrão de decoração da stdlib: como Read tem uma assinatura só, dá para
// interpor comportamento sem que nem a origem nem o destino saibam. O gzip
// acha que está lendo da rede; a rede não sabe que está sendo medida.
type leitorComProgresso struct {
	r         io.Reader
	total     int64
	lido      int64
	ultimo    time.Time
	notificar Progresso
}

func (l *leitorComProgresso) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.lido += int64(n)

	// Limita a frequência das notificações: sem isso seriam milhares por
	// segundo, e redesenhar o terminal custaria mais que o download.
	agora := time.Now()
	if agora.Sub(l.ultimo) > 100*time.Millisecond || errors.Is(err, io.EOF) {
		l.ultimo = agora
		l.notificar(l.lido, l.total)
	}
	return n, err
}
