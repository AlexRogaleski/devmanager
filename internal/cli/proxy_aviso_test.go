package cli

import (
	"strings"
	"testing"
)

func TestAvisoDeQuedaSemPermissaoNaoRecomendaSetcap(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		aviso := avisoDeQuedaEm(goos, "sem permissão para a porta 80", 8080, 8443)
		if strings.Contains(aviso, "setcap") {
			t.Errorf("%s: setcap foi abandonado — toda atualização do binário o apaga:\n%s", goos, aviso)
		}
		if !strings.Contains(aviso, "devm setup") {
			t.Errorf("%s: deveria apontar o devm setup:\n%s", goos, aviso)
		}
	}
}

func TestAvisoDePortaOcupadaUsaOComandoDoSistema(t *testing.T) {
	linux := avisoDeQuedaEm("linux", "a porta 80 está ocupada", 8080, 443)
	if !strings.Contains(linux, "ss -ltnp") {
		t.Errorf("no Linux o comando é o ss:\n%s", linux)
	}

	mac := avisoDeQuedaEm("darwin", "a porta 80 está ocupada", 8080, 443)
	if strings.Contains(mac, "ss -ltnp") {
		t.Errorf("o ss não existe no macOS:\n%s", mac)
	}
	if !strings.Contains(mac, "lsof") {
		t.Errorf("no macOS o comando é o lsof:\n%s", mac)
	}
}
