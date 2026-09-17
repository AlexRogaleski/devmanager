//go:build unix && !linux

package supervisor

import "syscall"

// Fora do Linux não há equivalente a PR_SET_PDEATHSIG. No macOS, a proteção
// contra órfãos depende exclusivamente do encerramento ordenado — que cobre
// Ctrl+C, mas não uma morte abrupta do supervisor.
func configurarMorteComPai(*syscall.SysProcAttr) {}
