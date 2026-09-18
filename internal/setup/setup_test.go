package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devmdns "github.com/AlexRogaleski/devmanager/internal/dns"
)

// Configurado, sem resolver e com o daemon parado: o que falta é o servidor
// DNS, que vive dentro do daemon. Oferecer regravar a configuração pediria a
// senha do sudo para não mudar nada.
func TestDNSConfiguradoComDaemonParadoApontaODaemon(t *testing.T) {
	estado := devmdns.Estado{
		Suportado:      true,
		MecanismoAtivo: true,
		Configurado:    true,
		Resolvendo:     false,
		Arquivo:        "/etc/resolver/test",
	}

	p := decidirDNS(estado, Entrada{EnderecoDNS: "127.0.0.1:5354", TLD: "test", DaemonAtivo: false})

	if p.Feito {
		t.Fatal("não resolve, então não está feito")
	}
	if len(p.Comandos) != 0 {
		t.Errorf("não deveria oferecer comandos com o daemon parado: %q", p.Comandos)
	}
	if !strings.Contains(p.Detalhe, "devm daemon start") {
		t.Errorf("o detalhe deveria mandar subir o daemon: %q", p.Detalhe)
	}
}

// Com o daemon de pé e o nome sem resolver, a configuração é que está errada
// ou não foi recarregada — aí sim regravar é o conserto.
func TestDNSConfiguradoComDaemonAtivoOfereceComandos(t *testing.T) {
	estado := devmdns.Estado{Suportado: true, MecanismoAtivo: true, Configurado: true}

	p := decidirDNS(estado, Entrada{EnderecoDNS: "127.0.0.1:5354", TLD: "test", DaemonAtivo: true})

	if len(p.Comandos) == 0 {
		t.Fatalf("deveria oferecer os comandos de instalação: %+v", p)
	}
}

func TestDNSResolvendoEstaFeito(t *testing.T) {
	p := decidirDNS(devmdns.Estado{Resolvendo: true}, Entrada{TLD: "test"})
	if !p.Feito || len(p.Comandos) != 0 {
		t.Errorf("resolvendo = feito, sem comandos: %+v", p)
	}
}

func TestCASemCertificadoMandaSubirODaemon(t *testing.T) {
	p := passoCA(context.Background(), "")
	if p.Feito || !strings.Contains(p.Detalhe, "devm daemon start") {
		t.Errorf("sem CA, o detalhe deveria explicar que o daemon a cria: %+v", p)
	}
}

// TestConfiancaLinuxComparaOConteudo guarda o caso que a checagem por
// existência errava: uma âncora antiga com o mesmo nome, deixada por uma CA
// que foi recriada depois.
func TestConfiancaLinuxComparaOConteudo(t *testing.T) {
	raiz := t.TempDir()
	ancoras := filepath.Join(raiz, "anchors")
	if err := os.MkdirAll(ancoras, 0o755); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(raiz, "ca.crt")
	escrever(t, cert, "CA ATUAL")

	conf, ok := confiancaLinux([]ancora{{ancoras, "sudo update-ca-trust"}}, cert)
	if !ok {
		t.Fatal("o diretório de âncoras existe; deveria haver uma forma de confiar")
	}
	ctx := context.Background()
	destino := filepath.Join(ancoras, "devmanager.crt")

	if conf.instalada(ctx) {
		t.Error("sem arquivo na âncora, não está instalada")
	}

	escrever(t, destino, "CA ANTIGA")
	if conf.instalada(ctx) {
		t.Error("uma âncora com outro conteúdo é de uma CA anterior, não da atual")
	}

	escrever(t, destino, "CA ATUAL")
	if !conf.instalada(ctx) {
		t.Error("mesmo conteúdo: a CA atual está instalada")
	}

	if !strings.Contains(strings.Join(conf.comandos, "\n"), "update-ca-trust") {
		t.Errorf("faltou o comando de atualização da família: %q", conf.comandos)
	}
}

func TestConfiancaLinuxSemAncoraConhecida(t *testing.T) {
	inexistente := filepath.Join(t.TempDir(), "nao-existe")
	if _, ok := confiancaLinux([]ancora{{inexistente, "x"}}, "/ca.crt"); ok {
		t.Error("sem diretório de âncoras não há como saber o comando certo")
	}
}

func TestConfiancaMacOS(t *testing.T) {
	conf, ok := confiancaPara("darwin", "/Users/ana/.local/share/devmanager/ca/ca.crt")
	if !ok {
		t.Fatal("darwin deveria ser suportado")
	}

	comando := strings.Join(conf.comandos, "\n")
	for _, esperado := range []string{
		"security add-trusted-cert",
		"-r trustRoot",
		"/Library/Keychains/System.keychain",
		"'/Users/ana/.local/share/devmanager/ca/ca.crt'",
	} {
		if !strings.Contains(comando, esperado) {
			t.Errorf("faltou %q:\n%s", esperado, comando)
		}
	}
}

func TestConfiancaSemSuporte(t *testing.T) {
	if _, ok := confiancaPara("windows", "/ca.crt"); ok {
		t.Error("windows não tem integração")
	}
}

func escrever(t *testing.T, caminho, conteudo string) {
	t.Helper()
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}
