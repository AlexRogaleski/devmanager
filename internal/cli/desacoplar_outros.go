//go:build !unix

package cli

import "os/exec"

// Fora de Unix não há sessões de processo no mesmo sentido; o daemon fica
// atrelado ao processo que o iniciou.
func desacoplarDoTerminal(*exec.Cmd) {}
