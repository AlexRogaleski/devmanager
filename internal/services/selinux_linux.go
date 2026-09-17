//go:build linux

package services

import "os"

// selinuxAtivo detecta SELinux em modo de aplicação.
//
// A checagem é o sistema de arquivos do próprio SELinux: /sys/fs/selinux só
// existe quando ele está carregado, e "enforce" contém 1 quando está
// bloqueando de fato. Em modo permissivo o rótulo :Z é desnecessário, mas
// tampouco atrapalha — por isso basta o diretório existir.
func selinuxAtivo() bool {
	info, err := os.Stat("/sys/fs/selinux")
	return err == nil && info.IsDir()
}
