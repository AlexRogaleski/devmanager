package runtimes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// providerFalso existe só neste arquivo de teste e mesmo assim É um Provider.
//
// Não há "implements", não há registro, não há arquivo de configuração: ele
// tem os métodos Name e List com as assinaturas certas, logo satisfaz a
// interface. Essa é a diferença prática das interfaces implícitas do Go — o
// teste não precisa de PHP instalado, e o Manager não sabe que está sendo
// enganado.
type providerFalso struct {
	nome     string
	runtimes []Runtime
	falhaCom error
}

func (p *providerFalso) Name() string { return p.nome }

func (p *providerFalso) List(ctx context.Context) ([]Runtime, error) {
	if p.falhaCom != nil {
		return nil, p.falhaCom
	}
	return p.runtimes, nil
}

func php(versao, origem string) Runtime {
	return Runtime{
		Language: "php",
		Version:  semver.MustParse(versao),
		Bin:      "/falso/" + origem + "/php" + versao,
		Source:   origem,
	}
}

func TestManagerListOrdenaDoMaiorParaOMenor(t *testing.T) {
	m := NewManager(
		&providerFalso{nome: "a", runtimes: []Runtime{php("8.2.20", "a"), php("8.4.3", "a")}},
		&providerFalso{nome: "b", runtimes: []Runtime{php("8.3.15", "b")}},
	)

	lista, err := m.List(context.Background(), "php")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	esperado := []string{"8.4.3", "8.3.15", "8.2.20"}
	if len(lista) != len(esperado) {
		t.Fatalf("vieram %d runtimes, esperava %d", len(lista), len(esperado))
	}
	for i, v := range esperado {
		if lista[i].Version.String() != v {
			t.Errorf("posição %d = %s, esperava %s", i, lista[i].Version, v)
		}
	}
}

func TestManagerListFiltraPorLinguagem(t *testing.T) {
	node := Runtime{Language: "node", Version: semver.MustParse("24.15.0"), Source: "a"}
	m := NewManager(&providerFalso{nome: "a", runtimes: []Runtime{php("8.3.0", "a"), node}})

	lista, err := m.List(context.Background(), "php")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(lista) != 1 || lista[0].Language != "php" {
		t.Errorf("esperava só o php, veio %v", lista)
	}
}

// Um Provider quebrado não pode apagar os que funcionam: se o download do PHP
// estático falhar, o PHP do sistema ainda precisa aparecer.
func TestManagerListToleraProviderQuebrado(t *testing.T) {
	m := NewManager(
		&providerFalso{nome: "quebrado", falhaCom: errors.New("sem rede")},
		&providerFalso{nome: "bom", runtimes: []Runtime{php("8.3.0", "bom")}},
	)

	lista, err := m.List(context.Background(), "php")
	if err != nil {
		t.Fatalf("não deveria falhar com um provider bom disponível: %v", err)
	}
	if len(lista) != 1 {
		t.Errorf("esperava 1 runtime, veio %d", len(lista))
	}
}

// Mas se TODOS falharem e nada for encontrado, aí é erro de verdade.
func TestManagerListFalhaQuandoNinguemResponde(t *testing.T) {
	m := NewManager(&providerFalso{nome: "quebrado", falhaCom: errors.New("sem rede")})

	if _, err := m.List(context.Background(), "php"); err == nil {
		t.Fatal("esperava erro quando todos os providers falham")
	}
}

func TestManagerResolve(t *testing.T) {
	m := NewManager(&providerFalso{nome: "a", runtimes: []Runtime{
		php("8.1.29", "a"), php("8.2.20", "a"), php("8.3.15", "a"), php("8.4.3", "a"),
	}})

	casos := map[string]string{
		"^8.2":       "8.4.3",
		"8.2.*":      "8.2.20",
		">=8.1 <8.3": "8.2.20",
	}
	for constraint, esperado := range casos {
		c, err := semver.ParseConstraint(constraint)
		if err != nil {
			t.Fatal(err)
		}
		r, err := m.Resolve(context.Background(), "php", c)
		if err != nil {
			t.Errorf("Resolve(%q) falhou: %v", constraint, err)
			continue
		}
		if r.Version.String() != esperado {
			t.Errorf("Resolve(%q) = %s, esperava %s", constraint, r.Version, esperado)
		}
	}
}

// O erro carrega dados estruturados, não só texto — é isso que vai permitir
// ao daemon reagir instalando a versão faltante em vez de só reclamar.
func TestResolveDevolveNotFoundErrorComDados(t *testing.T) {
	m := NewManager(&providerFalso{nome: "a", runtimes: []Runtime{php("8.3.15", "a")}})

	c, _ := semver.ParseConstraint("^7.4")
	_, err := m.Resolve(context.Background(), "php", c)
	if err == nil {
		t.Fatal("esperava erro")
	}

	// errors.As procura na cadeia de erros embrulhados um que seja do tipo
	// alvo e, se achar, preenche a variável. É o equivalente tipado do
	// catch (NotFoundException $e) do PHP.
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("erro = %T, esperava *NotFoundError", err)
	}
	if len(nf.Disponiveis) != 1 || nf.Disponiveis[0].String() != "8.3.15" {
		t.Errorf("Disponiveis = %v, esperava [8.3.15]", nf.Disponiveis)
	}
	if nf.Constraint.String() != "^7.4" {
		t.Errorf("Constraint = %s, esperava ^7.4", nf.Constraint)
	}
}

// SystemProvider é testado contra binários falsos num diretório temporário:
// nada de depender do PHP que por acaso esteja instalado na máquina do CI.
func TestSystemProviderDescobreBinarios(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts de shell não se aplicam no Windows")
	}

	dir := t.TempDir()
	criarPHPFalso(t, dir, "php8.2", "8.2.20")
	criarPHPFalso(t, dir, "php8.4", "8.4.3")
	criarPHPFalso(t, dir, "naoephp", "9.9.9") // nome fora do padrão: deve ser ignorado

	p := &SystemProvider{ExtraDirs: []string{dir}}
	encontrados, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	achadas := map[string]bool{}
	for _, r := range encontrados {
		achadas[r.Version.String()] = true
		if r.Source != SourceSystem {
			t.Errorf("Source = %q, esperava %q", r.Source, SourceSystem)
		}
	}
	for _, v := range []string{"8.2.20", "8.4.3"} {
		if !achadas[v] {
			t.Errorf("não encontrou o PHP %s (achou: %v)", v, achadas)
		}
	}
	if achadas["9.9.9"] {
		t.Error("binário com nome fora do padrão php* não deveria ser considerado")
	}
}

func criarPHPFalso(t *testing.T, dir, nome, versao string) {
	t.Helper()

	// Um script que ignora os argumentos e imprime a versão é suficiente:
	// o SystemProvider só precisa que o binário responda a
	// `php -n -r 'echo PHP_VERSION;'` com um número de versão.
	script := "#!/bin/sh\nprintf '" + versao + "'\n"
	caminho := filepath.Join(dir, nome)
	if err := os.WriteFile(caminho, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
