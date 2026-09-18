package dns

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIntegracaoLinux(t *testing.T) {
	integ, ok := integracaoPara("linux", "test")
	if !ok {
		t.Fatal("linux deveria ser suportado")
	}

	if integ.arquivo != "/etc/systemd/resolved.conf.d/devmanager.conf" {
		t.Errorf("arquivo = %q", integ.arquivo)
	}

	linhas, err := integ.linhas("127.0.0.1:5354", "test")
	if err != nil {
		t.Fatal(err)
	}
	conteudo := strings.Join(linhas, "\n")

	// O "~" transforma o domínio em ROTEAMENTO. Sem ele, o .test viraria
	// sufixo de BUSCA e o sistema tentaria resolver "google.com.test".
	for _, esperado := range []string{"[Resolve]", "DNS=127.0.0.1:5354", "Domains=~test"} {
		if !strings.Contains(conteudo, esperado) {
			t.Errorf("faltou %q:\n%s", esperado, conteudo)
		}
	}
}

func TestIntegracaoMacOS(t *testing.T) {
	integ, ok := integracaoPara("darwin", "test")
	if !ok {
		t.Fatal("darwin deveria ser suportado")
	}

	// Um arquivo por domínio: o nome do arquivo É o domínio roteado.
	if integ.arquivo != "/etc/resolver/test" {
		t.Errorf("arquivo = %q", integ.arquivo)
	}

	linhas, err := integ.linhas("127.0.0.1:5354", "test")
	if err != nil {
		t.Fatal(err)
	}

	// Host e porta em linhas separadas: o formato do resolver não aceita
	// host:porta, e "nameserver 127.0.0.1:5354" seria ignorado.
	for _, esperado := range []string{"nameserver 127.0.0.1", "port 5354"} {
		if !contem(linhas, esperado) {
			t.Errorf("faltou a linha %q: %q", esperado, linhas)
		}
	}
	for _, l := range linhas {
		if strings.HasPrefix(l, "nameserver") && strings.Contains(l, ":") {
			t.Errorf("nameserver com porta embutida não funciona no macOS: %q", l)
		}
	}
}

// Os dois arquivos precisam dizer como desfazer: quem os encontrar em /etc
// daqui a um ano não vai lembrar de onde vieram.
func TestArquivosExplicamComoDesfazer(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		integ, _ := integracaoPara(goos, "test")
		linhas, _ := integ.linhas("127.0.0.1:5354", "test")
		if !strings.Contains(strings.Join(linhas, "\n"), "devm dns uninstall") {
			t.Errorf("%s: o arquivo não diz como desfazer: %q", goos, linhas)
		}
	}
}

func TestSistemaSemSuporte(t *testing.T) {
	if _, ok := integracaoPara("windows", "test"); ok {
		t.Fatal("windows não tem integração")
	}

	e := verificarEm(context.Background(), "windows", "127.0.0.1:5354", "test")
	if e.Suportado {
		t.Error("Suportado deveria ser false")
	}
	if !strings.Contains(e.Motivo, "windows") {
		t.Errorf("o motivo deveria citar o sistema: %q", e.Motivo)
	}
}

func TestEnderecoSemPortaEhErro(t *testing.T) {
	integ, _ := integracaoPara("darwin", "test")
	if _, err := integ.linhas("127.0.0.1", "test"); err == nil {
		t.Error("endereço sem porta deveria falhar: a porta do resolver seria perdida")
	}
}

// TestComandosTemUmaLinhaCada guarda a invariante de que o `devm setup
// --apply` depende: ele executa cada elemento num `sh -c` separado.
//
// A versão anterior violava isso com um heredoc — a linha do tee rodava sem
// corpo, gravando o arquivo vazio, e "[Resolve]" era executado como comando.
func TestComandosTemUmaLinhaCada(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		integ, _ := integracaoPara(goos, "test")

		instalar, err := integ.comandosDeInstalacao("127.0.0.1:5354", "test")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range append(instalar, integ.comandosDeRemocao()...) {
			if strings.Contains(c, "\n") {
				t.Errorf("%s: comando de várias linhas quebraria o --apply:\n%s", goos, c)
			}
		}
	}
}

// TestGravacaoProduzOConteudoExato executa o comando gerado e compara o
// arquivo resultante, byte a byte, com o que se pretendia gravar.
//
// É a verificação que faltava: os testes anteriores conferiam que o TEXTO
// do comando continha certas palavras, e um texto pode conter todas elas e
// ainda gravar outra coisa ao ser executado. As linhas daqui foram escolhidas
// para quebrar um comando mal protegido: aspa simples, %, barra invertida,
// cifrão e crase — cada uma é especial para o shell ou para o printf.
func TestGravacaoProduzOConteudoExato(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o comando gerado é para sh")
	}

	dir := t.TempDir()

	// Um sudo falso que só executa o que recebe: o que está em teste é o
	// comando que o Dev Manager monta, não o sudo.
	falsos := filepath.Join(dir, "bin")
	if err := os.MkdirAll(falsos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(falsos, "sudo"), []byte("#!/bin/sh\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	linhas := []string{
		"# it's um comentário com aspa",
		"100% sem diretiva de printf",
		`barra \n invertida literal`,
		"cifrão $HOME sem expansão",
		"crase `whoami` sem execução",
		"",
		"nameserver 127.0.0.1",
	}
	destino := filepath.Join(dir, "saida com espaço.conf")

	cmd := exec.Command("sh", "-c", gravarComSudo(destino, linhas))
	cmd.Env = append(os.Environ(), "PATH="+falsos+string(os.PathListSeparator)+os.Getenv("PATH"))
	if saida, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("o comando falhou: %v\n%s", err, saida)
	}

	gravado, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	esperado := strings.Join(linhas, "\n") + "\n"
	if string(gravado) != esperado {
		t.Errorf("conteúdo gravado difere do pretendido\n--- gravado ---\n%s--- esperado ---\n%s", gravado, esperado)
	}
}

func TestEnderecosDoDscacheutil(t *testing.T) {
	saida := `name: app.test
ip_address: 127.0.0.1

name: app.test
ipv6_address: ::1

`
	ips := enderecosDoDscacheutil(saida)
	if len(ips) != 2 || ips[0] != "127.0.0.1" || ips[1] != "::1" {
		t.Fatalf("ips = %q", ips)
	}
	if !algumLoopback(ips) {
		t.Error("127.0.0.1 é loopback")
	}

	// Nome que não resolve: o dscacheutil não imprime nada, e isso tem que
	// virar "não resolve", não um falso positivo.
	if algumLoopback(enderecosDoDscacheutil("")) {
		t.Error("saída vazia não pode contar como resolvido")
	}

	// Resolveu, mas para fora do loopback: o domínio está indo para outro
	// servidor, e não para o nosso.
	if algumLoopback(enderecosDoDscacheutil("name: app.test\nip_address: 93.184.216.34\n")) {
		t.Error("um IP público não é o nosso servidor")
	}
}

func contem(linhas []string, alvo string) bool {
	for _, l := range linhas {
		if l == alvo {
			return true
		}
	}
	return false
}
