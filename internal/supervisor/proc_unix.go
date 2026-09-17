//go:build unix

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

// Este arquivo só é compilado em sistemas Unix. A tag //go:build unix cobre
// Linux, macOS e BSDs — exatamente o alvo do projeto. O arquivo irmão
// proc_outros.go cobre o resto, para o projeto continuar compilando em
// qualquer plataforma mesmo sem suporte a grupos de processo.

// isolarGrupo coloca o processo em seu próprio grupo.
//
// Sem isso, matar o processo deixa os NETOS vivos — e isso não é teoria:
// `npm run dev` inicia o vite como filho, e matar só o npm deixa o vite
// segurando a porta 5173 para sempre. Um grupo próprio permite sinalizar a
// árvore inteira de uma vez.
//
// O grupo próprio tem outro efeito importante: o Ctrl+C do terminal deixa de
// chegar direto nos filhos, porque eles saem do grupo de primeiro plano.
// Quem decide o que fazer com o sinal passa a ser o supervisor, que encerra
// os processos na ordem e no tempo que quiser.
func isolarGrupo(cmd *exec.Cmd) {
	attr := &syscall.SysProcAttr{Setpgid: true}

	// Grupo próprio tem um efeito colateral perigoso: se o supervisor morrer
	// de repente — SIGKILL, SIGPIPE, pane — os filhos NÃO recebem nada e
	// sobrevivem órfãos, segurando portas. No Linux dá para pedir ao kernel
	// que sinalize o filho quando o pai morrer; nos outros Unix isso não
	// existe e a proteção fica só no encerramento ordenado.
	configurarMorteComPai(attr)

	cmd.SysProcAttr = attr
}

// sinalizarGrupo manda um sinal para o processo e todos os descendentes.
//
// O PID negativo é a convenção do kill(2) para "grupo de processos".
func sinalizarGrupo(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}

	s, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}

	if err := syscall.Kill(-cmd.Process.Pid, s); err != nil {
		// O grupo pode já ter morrido entre o Wait e o Kill — condição normal
		// numa corrida, não erro. Tentamos o processo isolado como reserva.
		return cmd.Process.Signal(sig)
	}
	return nil
}
