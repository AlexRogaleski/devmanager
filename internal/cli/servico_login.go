package cli

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
)

// servicoDeLogin descreve como fazer o daemon subir no login num sistema.
//
// No Linux é um serviço de usuário do systemd; no macOS, um LaunchAgent do
// launchd. Os dois são a mesma ideia — um arquivo que o gerenciador de
// serviços da sessão lê — e cabem na mesma forma.
type servicoDeLogin struct {
	arquivo  string
	conteudo string

	// ligar e desligar são impressos, não executados: ativar algo para rodar
	// em todo login é decisão de quem usa, não efeito colateral de um
	// comando de instalação.
	ligar    []string
	desligar []string
}

// dadosDoServico é o que varia de máquina para máquina.
type dadosDoServico struct {
	Exe        string // caminho real do binário
	Home       string
	ConfigHome string // $XDG_CONFIG_HOME, se definido
	Path       string // $PATH no momento da instalação
	Log        string // arquivo que o `devm daemon logs` lê
}

// rotuloLaunchd identifica o agente no launchd. A convenção é DNS reverso de
// quem publica; o do repositório é o que existe.
const rotuloLaunchd = "com.github.alexrogaleski.devmanager"

func servicoDeLoginPara(goos string, d dadosDoServico) (*servicoDeLogin, bool) {
	switch goos {
	case "linux":
		dir := filepath.Join(d.Home, ".config")
		if d.ConfigHome != "" {
			dir = d.ConfigHome
		}
		return &servicoDeLogin{
			arquivo:  filepath.Join(dir, "systemd", "user", "devmanager.service"),
			conteudo: fmt.Sprintf(unitSystemd, d.Exe, d.Log, d.Log),
			ligar: []string{
				"systemctl --user daemon-reload",
				"systemctl --user enable --now devmanager",
			},
			desligar: []string{"systemctl --user disable --now devmanager"},
		}, true

	case "darwin":
		arquivo := filepath.Join(d.Home, "Library", "LaunchAgents", rotuloLaunchd+".plist")
		return &servicoDeLogin{
			arquivo:  arquivo,
			conteudo: plistLaunchd(d),
			// bootstrap é a forma atual; o `launchctl load` que aparece em
			// tanto tutorial está obsoleto desde o macOS 10.10.
			ligar:    []string{"launchctl bootstrap gui/$(id -u) " + arquivo},
			desligar: []string{"launchctl bootout gui/$(id -u)/" + rotuloLaunchd},
		}, true
	}
	return nil, false
}

// unitSystemd é o modelo do serviço de usuário.
//
// Type=simple porque o `daemon run` fica em primeiro plano: um processo que
// se bifurca sozinho exigiria Type=forking e um arquivo de PID, e confundiria
// o systemd sobre qual processo observar.
//
// Serviço de USUÁRIO (systemctl --user), não de sistema: o daemon roda com as
// permissões do dev, enxerga o home dele e o Podman rootless dele. Um serviço
// de sistema precisaria de root e quebraria todo o modelo de isolamento.
//
// A saída vai para o mesmo arquivo que o `devm daemon start` usa. Sem isso
// ela iria para o journal, e o `devm daemon logs` diria "nenhum log ainda"
// para um daemon que está rodando — o log existiria, só que num lugar que o
// próprio devm não consulta.
const unitSystemd = `[Unit]
Description=Dev Manager
Documentation=https://github.com/AlexRogaleski/devmanager
After=default.target

[Service]
Type=simple
ExecStart=%s daemon run
Restart=on-failure
RestartSec=2
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`

// plistLaunchd monta o LaunchAgent do macOS.
//
// O PATH é gravado no arquivo, e esse é o ponto que não se adivinha: o
// launchd entrega aos agentes um PATH mínimo, /usr/bin:/bin:/usr/sbin:/sbin.
// O Docker Desktop instala o docker em /usr/local/bin e o Homebrew em
// /opt/homebrew/bin — nenhum dos dois estaria visível, e o daemon subiria
// sem conseguir iniciar um único serviço. Guardar o PATH da sessão que
// instalou é o que faz o daemon enxergar o mesmo que o terminal enxerga.
//
// KeepAlive com SuccessfulExit=false reinicia só depois de uma falha, como o
// Restart=on-failure do systemd: um `devm daemon stop` encerra com sucesso e
// não deve ser desfeito pelo launchd um segundo depois.
func plistLaunchd(d dadosDoServico) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
		<string>run</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, xmlTexto(rotuloLaunchd), xmlTexto(d.Exe), xmlTexto(d.Path), xmlTexto(d.Log), xmlTexto(d.Log))
}

// xmlTexto escapa um valor para dentro de um elemento XML.
//
// Um & ou < num caminho — improvável, mas legal num nome de pasta — tornaria
// o plist inválido, e o launchd o recusa sem dizer por quê.
func xmlTexto(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
