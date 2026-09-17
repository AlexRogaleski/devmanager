package runtimes

import (
	"errors"
	"strings"
)

// Pequenos utilitários mantidos separados para não poluir a leitura do
// arquivo principal, que é onde mora a arquitetura.
func joinErros(errs []error) error { return errors.Join(errs...) }

func joinStrings(s []string, sep string) string { return strings.Join(s, sep) }
