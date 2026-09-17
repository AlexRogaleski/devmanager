//go:build linux

package supervisor

import "syscall"

// configurarMorteComPai pede ao kernel que mande SIGTERM ao filho assim que o
// processo pai morrer, qualquer que seja o motivo.
//
// É prctl(PR_SET_PDEATHSIG) por baixo, e existe só no Linux. Sem isso, um
// `kill -9 devm` deixaria o vite e o artisan serve rodando para sempre — foi
// exatamente o que aconteceu no primeiro teste, quando o devm morreu de
// SIGPIPE e os dois processos sobreviveram segurando as portas.
func configurarMorteComPai(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGTERM
}
