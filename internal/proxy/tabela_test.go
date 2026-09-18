package proxy

import "testing"

// normalizar precisa reconhecer o mesmo domínio nas várias formas em que ele
// chega: de navegador, de curl, de DNS.
func TestNormalizar(t *testing.T) {
	casos := map[string]string{
		"fapcen.test":      "fapcen.test",
		"Fapcen.TEST":      "fapcen.test", // DNS não diferencia caixa
		"fapcen.test:8080": "fapcen.test", // navegador manda a porta fora da 80
		"fapcen.test.":     "fapcen.test", // forma absoluta de DNS
		"  fapcen.test  ":  "fapcen.test",
		"FAPCEN.test.:443": "fapcen.test",
		"[::1]":            "[::1]", // IPv6 literal não pode perder o ":"
		"[::1]:8080":       "[::1]",
	}

	for entrada, esperado := range casos {
		if got := normalizar(entrada); got != esperado {
			t.Errorf("normalizar(%q) = %q, esperava %q", entrada, got, esperado)
		}
	}
}

func TestTabelaDefinirEBuscar(t *testing.T) {
	tab := NovaTabela()
	tab.Definir(Rota{Dominio: "fapcen.test", Porta: 8000, Projeto: "fapcen"})

	// A busca precisa funcionar com as mesmas variações que normalizar cobre.
	for _, host := range []string{"fapcen.test", "FAPCEN.TEST", "fapcen.test:8080", "fapcen.test."} {
		r, ok := tab.Buscar(host)
		if !ok {
			t.Errorf("Buscar(%q) não encontrou", host)
			continue
		}
		if r.Porta != 8000 {
			t.Errorf("Buscar(%q).Porta = %d", host, r.Porta)
		}
	}

	if _, ok := tab.Buscar("outro.test"); ok {
		t.Error("encontrou um domínio que não foi registrado")
	}
}

func TestTabelaRemover(t *testing.T) {
	tab := NovaTabela()
	tab.Definir(Rota{Dominio: "api.test", Porta: 3000})

	tab.Remover("API.test")
	if _, ok := tab.Buscar("api.test"); ok {
		t.Error("a rota continua na tabela depois do Remover")
	}

	// Remover o que não existe não pode falhar: é o caminho de um ambiente
	// que caiu sem nunca ter tido domínio.
	tab.Remover("nunca-existiu.test")
}

func TestTabelaDefinirSobrescreve(t *testing.T) {
	tab := NovaTabela()
	tab.Definir(Rota{Dominio: "app.test", Porta: 1000})
	tab.Definir(Rota{Dominio: "app.test", Porta: 2000})

	r, _ := tab.Buscar("app.test")
	if r.Porta != 2000 {
		t.Errorf("Porta = %d, esperava a mais recente (2000)", r.Porta)
	}
	if len(tab.Listar()) != 1 {
		t.Errorf("a tabela ficou com %d rotas, esperava 1", len(tab.Listar()))
	}
}

func TestTabelaListarEhOrdenada(t *testing.T) {
	tab := NovaTabela()
	for _, d := range []string{"zeta.test", "alfa.test", "meio.test"} {
		tab.Definir(Rota{Dominio: d, Porta: 1})
	}

	esperado := []string{"alfa.test", "meio.test", "zeta.test"}
	dominios := tab.Dominios()
	for i, e := range esperado {
		if dominios[i] != e {
			t.Errorf("posição %d = %q, esperava %q", i, dominios[i], e)
		}
	}
}
