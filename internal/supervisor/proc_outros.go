//go:build !unix

package supervisor

import (
	"os"
	"os/exec"
)

// Fora de Unix não há grupos de processo no mesmo sentido, então sinalizamos
// só o processo iniciado. Descendentes podem sobreviver — limitação conhecida,
// documentada aqui em vez de escondida.

func isolarGrupo(cmd *exec.Cmd) {}

func sinalizarGrupo(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}
