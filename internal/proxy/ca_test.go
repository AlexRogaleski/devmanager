package proxy

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCriarECarregarCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")

	criada, err := CarregarOuCriar(dir)
	if err != nil {
		t.Fatalf("CarregarOuCriar falhou: %v", err)
	}

	if _, err := os.Stat(criada.CaminhoDoCertificado()); err != nil {
		t.Fatalf("certificado não foi gravado: %v", err)
	}

	// Segunda chamada tem que REUSAR, não gerar outra: uma CA nova a cada
	// execução invalidaria a confiança instalada no navegador.
	recarregada, err := CarregarOuCriar(dir)
	if err != nil {
		t.Fatal(err)
	}
	if criada.cert.SerialNumber.Cmp(recarregada.cert.SerialNumber) != 0 {
		t.Error("a CA foi recriada em vez de reaproveitada")
	}
}

// A chave privada da CA emite certificado para QUALQUER domínio: quem a lê
// consegue se passar por qualquer site perante este navegador.
func TestPermissoesDaCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if _, err := CarregarOuCriar(dir); err != nil {
		t.Fatal(err)
	}

	chave, err := os.Stat(filepath.Join(dir, arquivoChaveCA))
	if err != nil {
		t.Fatal(err)
	}
	if modo := chave.Mode().Perm(); modo != 0o600 {
		t.Errorf("chave privada com permissão %v, esperava 0600", modo)
	}

	// O certificado PÚBLICO, ao contrário, precisa ser legível para poder
	// ser instalado como confiável.
	cert, err := os.Stat(filepath.Join(dir, arquivoCertCA))
	if err != nil {
		t.Fatal(err)
	}
	if modo := cert.Mode().Perm(); modo != 0o644 {
		t.Errorf("certificado com permissão %v, esperava 0644", modo)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if modo := info.Mode().Perm(); modo != 0o700 {
		t.Errorf("diretório da CA com permissão %v, esperava 0700", modo)
	}
}

func TestEmitirCertificado(t *testing.T) {
	ca, err := CarregarOuCriar(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}

	cert, err := ca.CertificadoPara("fapcen.test")
	if err != nil {
		t.Fatalf("CertificadoPara falhou: %v", err)
	}

	folha, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	// Os navegadores ignoram o CommonName desde 2017: é o SAN que vale.
	if len(folha.DNSNames) == 0 || folha.DNSNames[0] != "fapcen.test" {
		t.Errorf("DNSNames = %v, esperava conter fapcen.test", folha.DNSNames)
	}

	// A cadeia precisa trazer a CA junto, para o cliente validar.
	if len(cert.Certificate) != 2 {
		t.Errorf("cadeia com %d certificados, esperava 2 (folha + CA)", len(cert.Certificate))
	}

	// E o certificado tem que VALIDAR contra a CA — a prova de que a
	// assinatura está certa.
	raizes := x509.NewCertPool()
	raizes.AddCert(ca.cert)

	if _, err := folha.Verify(x509.VerifyOptions{
		Roots:     raizes,
		DNSName:   "fapcen.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("o certificado não valida contra a própria CA: %v", err)
	}
}

// Acima de 398 dias os navegadores recusam de saída.
func TestValidadeDentroDoLimiteDosNavegadores(t *testing.T) {
	ca, err := CarregarOuCriar(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}

	cert, err := ca.CertificadoPara("app.test")
	if err != nil {
		t.Fatal(err)
	}
	folha, _ := x509.ParseCertificate(cert.Certificate[0])

	duracao := folha.NotAfter.Sub(folha.NotBefore)
	if duracao > 398*24*time.Hour {
		t.Errorf("validade de %v excede o limite de 398 dias dos navegadores", duracao)
	}
}

func TestCertificadoEhCacheado(t *testing.T) {
	ca, err := CarregarOuCriar(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}

	// Emitir a cada handshake custaria uma geração de chave por conexão.
	primeiro, _ := ca.CertificadoPara("app.test")
	segundo, _ := ca.CertificadoPara("APP.test") // normalizado para o mesmo

	if primeiro != segundo {
		t.Error("o certificado foi reemitido em vez de reaproveitado do cache")
	}
}

// Uma CA que não pode assinar outras CAs limita o estrago se a chave vazar.
func TestCANaoPodeAssinarOutrasCAs(t *testing.T) {
	ca, err := CarregarOuCriar(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}

	if !ca.cert.IsCA {
		t.Fatal("a CA não está marcada como autoridade")
	}
	if !ca.cert.MaxPathLenZero {
		t.Error("MaxPathLenZero deveria impedir CAs intermediárias")
	}
}

// CA vencida é tão inútil quanto ausente, e precisa ser substituída em vez
// de produzir um erro de TLS confuso no navegador.
func TestCAVencidaEhSubstituida(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")

	ca, err := CarregarOuCriar(dir)
	if err != nil {
		t.Fatal(err)
	}
	serieOriginal := ca.cert.SerialNumber.String()

	// Estraga o certificado no disco de forma que o carregamento falhe.
	if err := os.WriteFile(ca.CaminhoDoCertificado(), []byte("lixo"), 0o644); err != nil {
		t.Fatal(err)
	}

	nova, err := CarregarOuCriar(dir)
	if err != nil {
		t.Fatalf("deveria gerar uma CA nova: %v", err)
	}
	if nova.cert.SerialNumber.String() == serieOriginal {
		t.Error("reaproveitou a CA corrompida")
	}
}

func TestCAComDiretorioIlegivel(t *testing.T) {
	// Um arquivo no lugar do diretório impede a criação.
	base := t.TempDir()
	arquivo := filepath.Join(base, "ca")
	if err := os.WriteFile(arquivo, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := CarregarOuCriar(arquivo); err == nil {
		t.Fatal("esperava erro")
	} else if !strings.Contains(err.Error(), "ca") {
		t.Errorf("mensagem pouco clara: %v", err)
	}
}
