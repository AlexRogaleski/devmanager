package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Nomes dos arquivos da autoridade certificadora local.
const (
	arquivoCertCA  = "ca.crt"
	arquivoChaveCA = "ca.key"
)

// validadeFolha é quanto dura um certificado de domínio.
//
// 397 dias é o teto que os navegadores aceitam desde 2020 — certificados mais
// longos são recusados de saída. Ficamos em 365 para ter margem.
const validadeFolha = 365 * 24 * time.Hour

// validadeCA é quanto dura a autoridade.
//
// Dez anos porque trocá-la exige reinstalar a confiança em navegador e
// sistema; é a operação mais chata do ciclo e não deveria ser rotina.
const validadeCA = 10 * 365 * 24 * time.Hour

// CA é a autoridade certificadora local do Dev Manager.
//
// Ela emite certificados para os domínios .test sob demanda. Depois de
// instalada como confiável uma única vez, todo projeto novo ganha HTTPS sem
// nenhum passo adicional — que é a experiência que o Herd oferece e o
// mkcert automatiza manualmente.
type CA struct {
	Dir string

	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

// CarregarOuCriar abre a CA do disco, criando-a na primeira vez.
func CarregarOuCriar(dir string) (*CA, error) {
	c := &CA{Dir: dir, cache: make(map[string]*tls.Certificate)}

	if err := c.carregar(); err == nil {
		return c, nil
	}
	if err := c.criar(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *CA) caminho(nome string) string { return filepath.Join(c.Dir, nome) }

// CaminhoDoCertificado devolve o arquivo a instalar como confiável.
func (c *CA) CaminhoDoCertificado() string { return c.caminho(arquivoCertCA) }

func (c *CA) carregar() error {
	certPEM, err := os.ReadFile(c.caminho(arquivoCertCA))
	if err != nil {
		return err
	}
	chavePEM, err := os.ReadFile(c.caminho(arquivoChaveCA))
	if err != nil {
		return err
	}

	blocoCert, _ := pem.Decode(certPEM)
	blocoChave, _ := pem.Decode(chavePEM)
	if blocoCert == nil || blocoChave == nil {
		return fmt.Errorf("arquivos da CA corrompidos em %s", c.Dir)
	}

	cert, err := x509.ParseCertificate(blocoCert.Bytes)
	if err != nil {
		return err
	}
	chave, err := x509.ParseECPrivateKey(blocoChave.Bytes)
	if err != nil {
		return err
	}

	// CA vencida é tão inútil quanto ausente, e o sintoma seria um erro de
	// TLS confuso no navegador. Tratamos como "não carregou" para que uma
	// nova seja gerada.
	if time.Now().After(cert.NotAfter) {
		return fmt.Errorf("a CA local venceu em %s", cert.NotAfter.Format("2006-01-02"))
	}

	c.cert, c.der, c.key = cert, blocoCert.Bytes, chave
	return nil
}

func (c *CA) criar() error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("criando %s: %w", c.Dir, err)
	}

	// ECDSA P-256 em vez de RSA: chaves menores, geração instantânea e
	// aceitação universal. RSA de 4096 bits levaria segundos e não traria
	// nada para um certificado que só vale no loopback.
	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("gerando a chave da CA: %w", err)
	}

	serie, err := serieAleatoria()
	if err != nil {
		return err
	}

	modelo := &x509.Certificate{
		SerialNumber: serie,
		Subject: pkix.Name{
			CommonName:   "Dev Manager CA",
			Organization: []string{"Dev Manager"},
		},
		NotBefore: time.Now().Add(-time.Hour), // folga para relógios desalinhados
		NotAfter:  time.Now().Add(validadeCA),

		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,

		// MaxPathLenZero impede que esta CA assine outras CAs. Se o arquivo
		// vazar, ele emite certificados para domínios — mas não pode criar
		// uma autoridade intermediária capaz de emitir indefinidamente.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, modelo, modelo, &chave.PublicKey, chave)
	if err != nil {
		return fmt.Errorf("criando o certificado da CA: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}

	if err := gravarPEM(c.caminho(arquivoCertCA), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}

	chaveDER, err := x509.MarshalECPrivateKey(chave)
	if err != nil {
		return fmt.Errorf("serializando a chave da CA: %w", err)
	}
	// 0600 na chave privada: quem a ler consegue emitir certificado para
	// QUALQUER domínio e ser aceito por este navegador. O certificado
	// público, ao lado, é 0644 de propósito — ele precisa ser lido para
	// ser instalado.
	if err := gravarPEM(c.caminho(arquivoChaveCA), "EC PRIVATE KEY", chaveDER, 0o600); err != nil {
		return err
	}

	c.cert, c.der, c.key = cert, der, chave
	return nil
}

// CertificadoPara emite (ou reaproveita) o certificado de um domínio.
//
// A emissão é sob demanda, no handshake TLS: um projeto novo ganha
// certificado no primeiro acesso, sem passo de configuração. É o que torna
// `https://projeto-novo.test` funcionar imediatamente.
func (c *CA) CertificadoPara(dominio string) (*tls.Certificate, error) {
	dominio = normalizar(dominio)

	c.mu.Lock()
	defer c.mu.Unlock()

	if cert, ok := c.cache[dominio]; ok {
		return cert, nil
	}

	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("gerando chave para %s: %w", dominio, err)
	}

	serie, err := serieAleatoria()
	if err != nil {
		return nil, err
	}

	modelo := &x509.Certificate{
		SerialNumber: serie,
		Subject:      pkix.Name{CommonName: dominio},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validadeFolha),

		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		// Os navegadores ignoram o CommonName desde 2017 e olham apenas o
		// SAN. Incluir o domínio aqui não é redundância: é o único campo
		// que realmente vale.
		DNSNames: []string{dominio, "*." + dominio},
	}

	der, err := x509.CreateCertificate(rand.Reader, modelo, c.cert, &chave.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("emitindo certificado para %s: %w", dominio, err)
	}

	cert := &tls.Certificate{
		// A cadeia inclui o certificado da CA para que um cliente que confia
		// na raiz consiga validar sem tê-la em mãos localmente.
		Certificate: [][]byte{der, c.der},
		PrivateKey:  chave,
	}

	c.cache[dominio] = cert
	return cert, nil
}

func serieAleatoria() (*big.Int, error) {
	limite := new(big.Int).Lsh(big.NewInt(1), 128)

	serie, err := rand.Int(rand.Reader, limite)
	if err != nil {
		return nil, fmt.Errorf("sorteando número de série: %w", err)
	}
	return serie, nil
}

func gravarPEM(caminho, tipo string, der []byte, modo os.FileMode) error {
	dados := pem.EncodeToMemory(&pem.Block{Type: tipo, Bytes: der})

	if err := os.WriteFile(caminho, dados, modo); err != nil {
		return fmt.Errorf("gravando %s: %w", caminho, err)
	}
	return nil
}
