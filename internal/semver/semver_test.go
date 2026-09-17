package semver

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]Version{
		"8.3":       {8, 3, 0},
		"8.3.4":     {8, 3, 4},
		"v13.19.0":  {13, 19, 0},
		"8":         {8, 0, 0},
		"8.4.0RC2":  {8, 4, 0},
		"8.3.4-dev": {8, 3, 4},
		"1.2.3+b1":  {1, 2, 3},
		"  8.3.4  ": {8, 3, 4},
	}
	for entrada, esperado := range ok {
		v, err := Parse(entrada)
		if err != nil {
			t.Errorf("Parse(%q) falhou: %v", entrada, err)
			continue
		}
		if v != esperado { // structs comparáveis: == funciona direto
			t.Errorf("Parse(%q) = %v, esperava %v", entrada, v, esperado)
		}
	}

	ruins := []string{"", "abc", "8.3.4.5", "-1.0", "."}
	for _, entrada := range ruins {
		if _, err := Parse(entrada); err == nil {
			t.Errorf("Parse(%q) deveria falhar", entrada)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	casos := []struct {
		a, b string
		quer int
	}{
		{"8.3.0", "8.3.0", 0},
		{"8.3.0", "8.4.0", -1},
		{"8.4.0", "8.3.9", 1},
		{"9.0.0", "8.99.99", 1},
		{"8.3.1", "8.3.10", -1},
	}
	for _, c := range casos {
		if got := MustParse(c.a).Compare(MustParse(c.b)); got != c.quer {
			t.Errorf("%s.Compare(%s) = %d, esperava %d", c.a, c.b, got, c.quer)
		}
	}
}

// Este é o teste que mais importa: é a tabela de tradução entre o que o
// desenvolvedor escreve no composer.json e o PHP que vamos escolher.
func TestConstraintAllows(t *testing.T) {
	casos := []struct {
		constraint string
		aceita     []string
		recusa     []string
	}{
		{
			constraint: "^8.3",
			aceita:     []string{"8.3.0", "8.3.15", "8.4.1", "8.99.0"},
			recusa:     []string{"8.2.9", "9.0.0", "7.4.0"},
		},
		{
			constraint: "^8.2",
			aceita:     []string{"8.2.0", "8.3.0", "8.4.0", "8.5.4"},
			recusa:     []string{"8.1.99", "9.0.0"},
		},
		{
			constraint: "~8.3",
			aceita:     []string{"8.3.0", "8.9.0"},
			recusa:     []string{"9.0.0", "8.2.0"},
		},
		{
			constraint: "~8.3.1",
			aceita:     []string{"8.3.1", "8.3.99"},
			recusa:     []string{"8.3.0", "8.4.0"},
		},
		{
			constraint: "8.3.*",
			aceita:     []string{"8.3.0", "8.3.20"},
			recusa:     []string{"8.2.0", "8.4.0"},
		},
		{
			constraint: ">=8.2",
			aceita:     []string{"8.2.0", "9.0.0", "15.0.0"},
			recusa:     []string{"8.1.99"},
		},
		{
			constraint: ">=8.2 <9.0",
			aceita:     []string{"8.2.0", "8.5.4"},
			recusa:     []string{"8.1.0", "9.0.0"},
		},
		{
			constraint: "^8.1 || ^8.3",
			aceita:     []string{"8.1.0", "8.2.0", "8.3.0", "8.4.0"},
			recusa:     []string{"8.0.9", "9.0.0"},
		},
		{
			constraint: "8.3",
			aceita:     []string{"8.3.0", "8.3.7"},
			recusa:     []string{"8.4.0", "8.2.0"},
		},
		{
			constraint: "8.3.4",
			aceita:     []string{"8.3.4"},
			recusa:     []string{"8.3.5", "8.3.3"},
		},
		{
			constraint: "*",
			aceita:     []string{"5.6.0", "8.3.0", "99.0.0"},
		},
		{
			constraint: "", // projeto sem require.php
			aceita:     []string{"8.3.0"},
		},
		{
			// A regra especial do ^ antes do 1.0, usada por pacotes como
			// laravel/wayfinder: "^0.1.14" não pode saltar para 0.2.
			constraint: "^0.1.14",
			aceita:     []string{"0.1.14", "0.1.99"},
			recusa:     []string{"0.2.0", "1.0.0", "0.1.13"},
		},
	}

	for _, c := range casos {
		t.Run(c.constraint, func(t *testing.T) {
			cons, err := ParseConstraint(c.constraint)
			if err != nil {
				t.Fatalf("ParseConstraint(%q) falhou: %v", c.constraint, err)
			}
			for _, s := range c.aceita {
				if !cons.Allows(MustParse(s)) {
					t.Errorf("%q deveria aceitar %s", c.constraint, s)
				}
			}
			for _, s := range c.recusa {
				if cons.Allows(MustParse(s)) {
					t.Errorf("%q NÃO deveria aceitar %s", c.constraint, s)
				}
			}
		})
	}
}

func TestConstraintInvalida(t *testing.T) {
	for _, s := range []string{"^abc", ">=x.y", "!=8.3", "^"} {
		if _, err := ParseConstraint(s); err == nil {
			t.Errorf("ParseConstraint(%q) deveria falhar", s)
		}
	}
}

// Best é a regra de negócio central: dado o que o projeto exige e o que está
// instalado na máquina, qual PHP o Dev Manager vai usar.
func TestConstraintBest(t *testing.T) {
	instalados := []Version{
		MustParse("8.1.29"),
		MustParse("8.2.20"),
		MustParse("8.3.15"),
		MustParse("8.4.3"),
	}

	casos := map[string]string{
		"^8.2":       "8.4.3", // maior compatível, não a menor
		"^8.3":       "8.4.3",
		"8.2.*":      "8.2.20",
		">=8.1 <8.3": "8.2.20",
		"~8.3.0":     "8.3.15",
	}
	for constraint, esperado := range casos {
		c, err := ParseConstraint(constraint)
		if err != nil {
			t.Fatalf("ParseConstraint(%q): %v", constraint, err)
		}
		v, ok := c.Best(instalados)
		if !ok {
			t.Errorf("%q: nenhuma versão serviu, esperava %s", constraint, esperado)
			continue
		}
		if v.String() != esperado {
			t.Errorf("%q escolheu %s, esperava %s", constraint, v, esperado)
		}
	}

	t.Run("nenhuma serve não é erro", func(t *testing.T) {
		c, _ := ParseConstraint("^9.0")
		if _, ok := c.Best(instalados); ok {
			t.Error("esperava ok=false quando nada satisfaz a constraint")
		}
	})

	t.Run("lista vazia", func(t *testing.T) {
		c, _ := ParseConstraint("^8.2")
		if _, ok := c.Best(nil); ok {
			t.Error("esperava ok=false para lista vazia")
		}
	})
}
