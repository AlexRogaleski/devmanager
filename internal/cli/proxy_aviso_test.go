package cli

import (
	"strings"
	"testing"
)

func TestAvisoDeQuedaSemPermissaoNaoRecomendaSetcap(t *testing.T) {
	aviso := avisoDeQuedaEm("linux", "sem permissão para a porta 80", 8080, 8443)
	if strings.Contains(aviso, "setcap") {
		t.Errorf("setcap foi abandonado — toda atualização do binário o apaga:\n%s", aviso)
	}
	if !strings.Contains(aviso, "devm setup") {
		t.Errorf("no Linux o setup tem o conserto (sysctl):\n%s", aviso)
	}
}

// No macOS não existe ajuste equivalente, e mandar rodar o setup seria mandar
// procurar um conserto que ele não tem.
func TestAvisoDeQuedaSemPermissaoNoMacOS(t *testing.T) {
	aviso := avisoDeQuedaEm("darwin", "sem permissão para a porta 80", 8080, 8443)
	if strings.Contains(aviso, "devm setup") || strings.Contains(aviso, "setcap") {
		t.Errorf("no macOS não há ajuste a sugerir:\n%s", aviso)
	}
	if !strings.Contains(aviso, "porta") || !strings.Contains(aviso, "URL") {
		t.Errorf("deveria dizer que os projetos funcionam com a porta na URL:\n%s", aviso)
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
