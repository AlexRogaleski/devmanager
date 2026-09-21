package environment

import (
	"strings"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/config"
	"github.com/AlexRogaleski/devmanager/internal/project"
	"github.com/AlexRogaleski/devmanager/internal/supervisor"
)

// portasFalsas devolve números previsíveis, na ordem em que forem pedidos.
//
// O teste precisa afirmar QUAL porta cada processo recebeu, e uma porta
// sorteada pelo kernel não permite afirmar nada.
func portasFalsas(numeros ...int) func() (int, error) {
	i := 0
	return func() (int, error) {
		n := numeros[i]
		i++
		return n, nil
	}
}

func TestMarcadorRecebeAPortaDoAmbiente(t *testing.T) {
	procs := []supervisor.Processo{
		{Nome: "serve", Linha: "php artisan serve --port=" + MarcadorPorta},
		{Nome: "queue", Linha: "php artisan queue:work"},
	}

	ex, err := resolverPortas(procs, 43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}

	if ex.Processos[0].Linha != "php artisan serve --port=43210" {
		t.Errorf("marcador não foi trocado: %q", ex.Processos[0].Linha)
	}
	if ex.Porta != 43210 || ex.Servidor != "serve" {
		t.Errorf("servidor errado: porta=%d nome=%q", ex.Porta, ex.Servidor)
	}
	if ex.Processos[1].Linha != "php artisan queue:work" {
		t.Errorf("linha sem marcador foi alterada: %q", ex.Processos[1].Linha)
	}
}

// O mesmo nome tem de receber sempre a mesma porta: é o que permite um
// processo apontar para o outro.
func TestPortaNomeadaEEstavelPorNome(t *testing.T) {
	procs := []supervisor.Processo{
		{Nome: "vite", Linha: "npm run dev -- --port {{port:vite}}"},
		{Nome: "ssr", Linha: "node ssr.js --vite http://127.0.0.1:{{port:vite}} --port {{port:ssr}}"},
	}

	ex, err := resolverPortas(procs, 5000, portasFalsas(5173, 13714))
	if err != nil {
		t.Fatal(err)
	}

	if ex.Extras["vite"] != 5173 || ex.Extras["ssr"] != 13714 {
		t.Fatalf("portas extras erradas: %v", ex.Extras)
	}
	esperado := "node ssr.js --vite http://127.0.0.1:5173 --port 13714"
	if ex.Processos[1].Linha != esperado {
		t.Errorf("linha resolvida:\n  %s\nesperava:\n  %s", ex.Processos[1].Linha, esperado)
	}
	// Nenhum deles pediu a porta principal: não há servidor a publicar.
	if ex.Porta != 0 {
		t.Errorf("porta principal deveria ficar livre, veio %d", ex.Porta)
	}
}

// Quem fixou a porta no devmanager.yaml escuta NELA. Anunciar ao proxy a
// porta sorteada pelo daemon daria 502 no domínio do projeto.
func TestPortaFixadaAMaoVenceAPortaSorteada(t *testing.T) {
	procs := []supervisor.Processo{
		{Nome: "serve", Linha: "php artisan serve --host=127.0.0.1 --port=8000"},
	}

	ex, err := resolverPortas(procs, 43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}

	if ex.Porta != 8000 {
		t.Errorf("porta = %d, esperava a declarada (8000)", ex.Porta)
	}
	if ex.Processos[0].Linha != "php artisan serve --host=127.0.0.1 --port=8000" {
		t.Errorf("a linha não podia mudar: %q", ex.Processos[0].Linha)
	}
}

// `php artisan serve` sem --port escuta na 8000, não numa porta livre.
func TestArtisanServeSemPortaEscutaNa8000(t *testing.T) {
	ex, err := resolverPortas(
		[]supervisor.Processo{{Nome: "serve", Linha: "php artisan serve"}},
		43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}
	if ex.Porta != portaPadraoDoArtisan {
		t.Errorf("porta = %d, esperava %d", ex.Porta, portaPadraoDoArtisan)
	}
}

func TestPortaComEspacoTambemELida(t *testing.T) {
	ex, err := resolverPortas(
		[]supervisor.Processo{{Nome: "serve", Linha: "php artisan serve --port 9001"}},
		43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}
	if ex.Porta != 9001 {
		t.Errorf("porta = %d, esperava 9001", ex.Porta)
	}
}

// Filtrar tirando o servidor tem de zerar a porta: um domínio apontando para
// porta vazia responde 502, pior que domínio nenhum.
func TestFiltrarSemOServidorZeraAPorta(t *testing.T) {
	ex, err := resolverPortas([]supervisor.Processo{
		{Nome: "serve", Linha: "php artisan serve --port=" + MarcadorPorta},
		{Nome: "vite", Linha: "npm run dev"},
	}, 43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}

	so, err := ex.Filtrar([]string{"vite"})
	if err != nil {
		t.Fatal(err)
	}
	if so.Porta != 0 || so.Servidor != "" {
		t.Errorf("sobrou servidor fantasma: porta=%d nome=%q", so.Porta, so.Servidor)
	}

	// E o caminho oposto: com o servidor na lista, a porta continua.
	com, err := ex.Filtrar([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	if com.Porta != 43210 {
		t.Errorf("porta = %d, esperava 43210", com.Porta)
	}
}

func TestSemFrontendPreservaOServidor(t *testing.T) {
	ex, err := resolverPortas([]supervisor.Processo{
		{Nome: "serve", Linha: "php artisan serve --port=" + MarcadorPorta},
		{Nome: "vite", Linha: "npm run dev"},
	}, 43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}

	sem := ex.SemFrontend()
	if len(sem.Processos) != 1 || sem.Processos[0].Nome != "serve" {
		t.Fatalf("processos: %+v", sem.Processos)
	}
	if sem.Porta != 43210 {
		t.Errorf("porta = %d, esperava 43210", sem.Porta)
	}
}

func TestErrosDeMarcador(t *testing.T) {
	casos := []struct {
		nome   string
		procs  []supervisor.Processo
		trecho string
	}{
		{
			nome: "dois pedindo a porta principal",
			procs: []supervisor.Processo{
				{Nome: "serve", Linha: "php artisan serve --port=" + MarcadorPorta},
				{Nome: "ssr", Linha: "node ssr.js --port=" + MarcadorPorta},
			},
			trecho: "os dois a porta principal",
		},
		{
			nome:   "marcador sem fechamento",
			procs:  []supervisor.Processo{{Nome: "vite", Linha: "npm run dev -- --port {{port:vite"}},
			trecho: "sem o }} que fecha",
		},
		{
			nome:   "marcador sem nome",
			procs:  []supervisor.Processo{{Nome: "vite", Linha: "npm run dev -- --port {{port:}}"}},
			trecho: "sem nome",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := resolverPortas(c.procs, 43210, portasFalsas(1, 2, 3))
			if err == nil {
				t.Fatal("esperava erro, veio nil")
			}
			if !strings.Contains(err.Error(), c.trecho) {
				t.Errorf("erro %q não menciona %q", err, c.trecho)
			}
		})
	}
}

// O padrão detectado passa pelo MESMO caminho de quem declara processes.
func TestPadraoDetectadoUsaOMarcador(t *testing.T) {
	p := &project.Project{Name: "app", Path: t.TempDir(), Kind: project.KindLaravel}

	procs, err := processosDeclarados(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 || !strings.Contains(procs[0].Linha, MarcadorPorta) {
		t.Fatalf("o padrão deveria conter o marcador: %+v", procs)
	}

	ex, err := Processos(p, 43210)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Porta != 43210 || ex.Servidor != "serve" {
		t.Errorf("servidor não foi reconhecido: %+v", ex)
	}
}

func TestProcessesDoProjetoVenceOPadrao(t *testing.T) {
	p := &project.Project{
		Name: "app",
		Path: t.TempDir(),
		Kind: project.KindLaravel,
		Config: &config.Config{Processes: map[string]string{
			"queue": "php artisan queue:work",
			"serve": "php artisan serve --port=" + MarcadorPorta,
		}},
	}

	ex, err := Processos(p, 43210)
	if err != nil {
		t.Fatal(err)
	}
	// Ordem alfabética: queue antes de serve.
	if len(ex.Processos) != 2 || ex.Processos[0].Nome != "queue" {
		t.Fatalf("processos: %+v", ex.Processos)
	}
	if ex.Porta != 43210 {
		t.Errorf("porta = %d, esperava 43210", ex.Porta)
	}
}

// As portas precisam chegar aos processos: é assim que o frontend descobre
// onde o backend subiu.
func TestAmbienteExportaAsPortas(t *testing.T) {
	ex, err := resolverPortas([]supervisor.Processo{
		{Nome: "serve", Linha: "php artisan serve --port=" + MarcadorPorta},
		{Nome: "vite", Linha: "npm run dev -- --port {{port:vite-ssr}}"},
	}, 43210, portasFalsas(5173))
	if err != nil {
		t.Fatal(err)
	}

	obtido := strings.Join(ex.Ambiente(), " ")
	esperado := "DEVM_PORT=43210 DEVM_PORT_VITE_SSR=5173"
	if obtido != esperado {
		t.Errorf("ambiente = %q, esperava %q", obtido, esperado)
	}
}

// Sem servidor não há DEVM_PORT: melhor a variável faltar que apontar para
// uma porta onde não há ninguém.
func TestAmbienteSemServidorNaoInventaPorta(t *testing.T) {
	ex, err := resolverPortas(
		[]supervisor.Processo{{Nome: "queue", Linha: "php artisan queue:work"}},
		43210, portasFalsas())
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Ambiente()) != 0 {
		t.Errorf("ambiente = %v, esperava vazio", ex.Ambiente())
	}
}
