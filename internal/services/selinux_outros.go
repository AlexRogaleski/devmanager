//go:build !linux

package services

// Fora do Linux não há SELinux; volumes não precisam de rótulo.
func selinuxAtivo() bool { return false }
