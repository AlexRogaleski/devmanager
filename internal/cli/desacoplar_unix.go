//go:build unix

package cli

import (
	"os/exec"
	"syscall"
)

// desacoplarDoTerminal põe o daemon numa sessão própria.
//
// Setsid é o que faz o daemon sobreviver ao fechamento do terminal: sem ele,
// o processo continua no grupo de primeiro plano do shell e recebe o SIGHUP
// que o terminal manda ao fechar — morrendo junto, o que derrota o propósito
// inteiro de existir um daemon.
func desacoplarDoTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
