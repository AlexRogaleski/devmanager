// Package semver representa versões e faixas de versão no estilo do Composer.
//
// Ele não depende de PHP nem do Dev Manager: é só aritmética de versões.
// Quando entrar o gerenciamento de Node, o mesmo pacote atende — ".nvmrc"
// e "engines.node" do package.json usam a mesma gramática de constraints.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version é uma versão concreta, já normalizada para três componentes.
//
// Repare que é um struct de valor, não ponteiro: é pequeno (24 bytes), imutável
// na prática e comparável com == pelo próprio compilador. Structs assim devem
// ser passados por valor — ponteiro aqui só adicionaria trabalho ao coletor.
type Version struct {
	Major int
	Minor int
	Patch int
}

// Parse lê uma versão em texto.
//
// Aceita as formas que aparecem no mundo real do Composer e do PHP:
//
//	8.3        -> 8.3.0
//	8.3.4      -> 8.3.4
//	v13.19.0   -> 13.19.0   (o "v" do composer.lock)
//	8.4.0RC2   -> 8.4.0      (sufixo de pré-release descartado)
//	8.3.4-dev  -> 8.3.4
//
// Descartar o sufixo de pré-release é uma decisão consciente: para escolher
// qual PHP roda um projeto, "8.4.0RC2" e "8.4.0" são a mesma faixa. Se um dia
// precisarmos ordenar RCs entre si, o campo entra aqui sem quebrar quem chama.
func Parse(s string) (Version, error) {
	original := s

	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")

	// Corta o primeiro caractere que não seja dígito ou ponto: isso remove
	// "-dev", "+build", "RC2", "beta1" de uma vez só.
	if i := strings.IndexFunc(s, func(r rune) bool {
		return !(r >= '0' && r <= '9') && r != '.'
	}); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, ".")

	if s == "" {
		return Version{}, fmt.Errorf("versão inválida: %q", original)
	}

	partes := strings.Split(s, ".")
	if len(partes) > 3 {
		return Version{}, fmt.Errorf("versão inválida: %q (componentes demais)", original)
	}

	// O slice nasce com 3 posições zeradas, então componentes ausentes
	// viram 0 sem nenhum if extra: "8.3" preenche [8 3 0].
	nums := [3]int{}
	for i, parte := range partes {
		n, err := strconv.Atoi(parte)
		if err != nil {
			return Version{}, fmt.Errorf("versão inválida: %q", original)
		}
		if n < 0 {
			return Version{}, fmt.Errorf("versão inválida: %q (componente negativo)", original)
		}
		nums[i] = n
	}

	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// MustParse é para versões literais escritas no código, que não podem falhar.
// O prefixo "Must" é convenção do Go para "entra em pânico em vez de devolver
// erro" — use só com valores constantes e em testes, nunca com entrada do usuário.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String faz Version implementar fmt.Stringer, a interface que o pacote fmt
// procura ao formatar um valor com %s ou %v. Basta existir este método para
// que fmt.Printf("%s", v) imprima "8.3.4" em vez do struct cru.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// MajorMinor devolve "8.3" — a granularidade em que as pessoas falam de PHP.
func (v Version) MajorMinor() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Compare devolve -1, 0 ou 1. É o contrato que slices.SortFunc espera.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sinal(v.Major - o.Major)
	case v.Minor != o.Minor:
		return sinal(v.Minor - o.Minor)
	case v.Patch != o.Patch:
		return sinal(v.Patch - o.Patch)
	}
	return 0
}

// Less permite ordenações diretas e leitura fluente nas comparações.
func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

func sinal(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}
