package daemon

import (
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/services"
)

func spec(t *testing.T, texto string) services.Spec {
	t.Helper()
	s, err := services.ParseSpec(texto)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func containers(specs []services.Spec) []string {
	nomes := make([]string, 0, len(specs))
	for _, s := range specs {
		nomes = append(nomes, s.Container())
	}
	slices.Sort(nomes)
	return nomes
}

// O caso que motivou tudo: subir um projeto PostgreSQL não pode deixar o
// MySQL de outro projeto ligado, mas também não pode derrubar o Redis que um
// projeto ainda rodando usa.
func TestOciososPoupamOQueOutroAmbienteUsa(t *testing.T) {
	s := &Servidor{
		ambientes: map[string]*ambiente{
			"suma": {nome: "suma", servicos: []services.Spec{spec(t, "redis:8")}},
		},
		subidos: map[string]services.Spec{
			spec(t, "postgres:17").Container(): spec(t, "postgres:17"),
			spec(t, "redis:8").Container():     spec(t, "redis:8"),
			spec(t, "mysql:8.4").Container():   spec(t, "mysql:8.4"),
		},
	}

	obtido := containers(s.selecionarOciosos())
	esperado := []string{"devm-mysql-8-4", "devm-postgres-17"}

	if !slices.Equal(obtido, esperado) {
		t.Errorf("ociosos = %v, esperava %v", obtido, esperado)
	}

	// Segunda chamada não repete o que já foi decidido.
	if resto := s.selecionarOciosos(); len(resto) != 0 {
		t.Errorf("a segunda chamada devolveu %v, esperava nada", containers(resto))
	}

	// E o que está em uso continua registrado, para ser desligado quando o
	// último projeto que o usa cair.
	if _, ainda := s.subidos["devm-redis-8"]; !ainda {
		t.Error("o redis em uso não podia sair da lista de subidos")
	}
}

// Um serviço que já estava rodando quando o daemon chegou não é nosso: ele
// nunca entra em `subidos`, e portanto nunca é desligado por nós.
func TestServicoDeTerceiroNaoEDesligado(t *testing.T) {
	s := &Servidor{
		ambientes: map[string]*ambiente{},
		subidos:   map[string]services.Spec{},
	}

	if ociosos := s.selecionarOciosos(); len(ociosos) != 0 {
		t.Errorf("selecionou %v sem ter subido nada", containers(ociosos))
	}
}

func TestSemAmbientesTudoQueSubimosEOcioso(t *testing.T) {
	s := &Servidor{
		ambientes: map[string]*ambiente{},
		subidos: map[string]services.Spec{
			spec(t, "mailpit").Container(): spec(t, "mailpit"),
		},
	}

	if obtido := containers(s.selecionarOciosos()); !slices.Equal(obtido, []string{"devm-mailpit-latest"}) {
		t.Errorf("ociosos = %v", obtido)
	}
}

// Encerrar tem de esquecer os ambientes ANTES de decidir o que está ocioso.
// Sem isso, `devm daemon stop` deixava todos os serviços ligados: a
// contabilidade ainda enxergava os ambientes que acabara de derrubar.
func TestEncerrarEsqueceOsAmbientes(t *testing.T) {
	encerrado := make(chan struct{})
	close(encerrado)

	s := NovoServidor(filepath.Join(t.TempDir(), "devm.sock"), "teste", io.Discard)
	s.ambientes["web"] = &ambiente{
		nome:      "web",
		anel:      NovoAnel(4),
		cancelar:  func() {},
		encerrado: encerrado,
		servicos:  []services.Spec{spec(t, "redis:8")},
	}

	// subidos vazio de propósito: o teste é sobre a contabilidade, e assim
	// nada toca no engine de contêineres.
	if err := s.Encerrar(); err != nil {
		t.Fatal(err)
	}

	if len(s.ambientes) != 0 {
		t.Errorf("sobraram %d ambiente(s) depois de encerrar", len(s.ambientes))
	}
}
