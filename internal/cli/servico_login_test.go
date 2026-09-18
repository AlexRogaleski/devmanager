package cli

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func dadosDeTeste() dadosDoServico {
	return dadosDoServico{
		Exe:  "/Users/ana/.local/bin/devm",
		Home: "/Users/ana",
		Path: "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		Log:  "/Users/ana/.local/share/devmanager/daemon.log",
	}
}

func TestServicoLinux(t *testing.T) {
	d := dadosDeTeste()
	d.Home = "/home/ana"
	s, ok := servicoDeLoginPara("linux", d)
	if !ok {
		t.Fatal("linux deveria ser suportado")
	}

	if s.arquivo != "/home/ana/.config/systemd/user/devmanager.service" {
		t.Errorf("arquivo = %q", s.arquivo)
	}
	for _, esperado := range []string{
		"ExecStart=" + d.Exe + " daemon run",
		// Sem isto a saída iria para o journal, e o `devm daemon logs`
		// diria "nenhum log ainda" com o daemon rodando.
		"StandardOutput=append:" + d.Log,
		"StandardError=append:" + d.Log,
	} {
		if !strings.Contains(s.conteudo, esperado) {
			t.Errorf("faltou %q:\n%s", esperado, s.conteudo)
		}
	}
}

func TestServicoLinuxRespeitaXDGConfigHome(t *testing.T) {
	d := dadosDeTeste()
	d.ConfigHome = "/outro/config"
	s, _ := servicoDeLoginPara("linux", d)
	if s.arquivo != "/outro/config/systemd/user/devmanager.service" {
		t.Errorf("arquivo = %q", s.arquivo)
	}
}

// valoresDoPlist devolve os textos de todos os <string> do documento — e
// falha se o XML não for bem formado, que é o primeiro requisito do launchd.
func valoresDoPlist(t *testing.T, plist string) []string {
	t.Helper()

	dec := xml.NewDecoder(strings.NewReader(plist))
	// O plist declara um DOCTYPE externo; o decoder não precisa buscá-lo.
	dec.Strict = true

	var valores []string
	dentroDeString := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("plist não é XML bem formado: %v\n%s", err, plist)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			dentroDeString = v.Name.Local == "string"
		case xml.CharData:
			if dentroDeString {
				valores = append(valores, string(v))
			}
		case xml.EndElement:
			dentroDeString = false
		}
	}
	return valores
}

func TestServicoMacOS(t *testing.T) {
	d := dadosDeTeste()
	s, ok := servicoDeLoginPara("darwin", d)
	if !ok {
		t.Fatal("darwin deveria ser suportado")
	}

	if s.arquivo != "/Users/ana/Library/LaunchAgents/"+rotuloLaunchd+".plist" {
		t.Errorf("arquivo = %q", s.arquivo)
	}

	valores := valoresDoPlist(t, s.conteudo)
	for _, esperado := range []string{
		rotuloLaunchd,
		d.Exe, "daemon", "run",
		// O PATH da sessão: sem ele o launchd entrega um PATH mínimo, e o
		// docker em /usr/local/bin ficaria invisível para o daemon.
		d.Path,
		d.Log,
	} {
		if !contemValor(valores, esperado) {
			t.Errorf("faltou o valor %q no plist: %q", esperado, valores)
		}
	}

	// launchctl load é o comando dos tutoriais antigos, obsoleto desde o
	// macOS 10.10.
	if !strings.Contains(strings.Join(s.ligar, "\n"), "launchctl bootstrap") {
		t.Errorf("ligar = %q", s.ligar)
	}
}

// Um & no caminho, sem escape, torna o plist inválido — e o launchd o
// recusa sem dizer por quê.
func TestPlistEscapaCaracteresEspeciais(t *testing.T) {
	d := dadosDeTeste()
	d.Exe = "/Users/ana/P&D <testes>/devm"

	valores := valoresDoPlist(t, plistLaunchd(d))
	if !contemValor(valores, d.Exe) {
		t.Errorf("o caminho não sobreviveu ao escape: %q", valores)
	}
}

func TestServicoSemSuporte(t *testing.T) {
	if _, ok := servicoDeLoginPara("windows", dadosDeTeste()); ok {
		t.Error("windows não tem serviço de login")
	}
}

func contemValor(valores []string, alvo string) bool {
	for _, v := range valores {
		if v == alvo {
			return true
		}
	}
	return false
}
