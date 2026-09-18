package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	casos := map[string]struct {
		nome, versao, container string
	}{
		"postgres:17": {"postgres", "17", "devm-postgres-17"},
		"postgres":    {"postgres", "17", "devm-postgres-17"}, // versão padrão
		"redis":       {"redis", "8", "devm-redis-8"},
		"mysql:8.4":   {"mysql", "8.4", "devm-mysql-8-4"}, // ponto vira hífen no nome
		"MailPit":     {"mailpit", "latest", "devm-mailpit-latest"},
		" redis : 8 ": {"redis", "8", "devm-redis-8"},
	}

	for entrada, esperado := range casos {
		t.Run(entrada, func(t *testing.T) {
			spec, err := ParseSpec(entrada)
			if err != nil {
				t.Fatalf("ParseSpec(%q) falhou: %v", entrada, err)
			}
			if spec.Nome != esperado.nome {
				t.Errorf("Nome = %q, esperava %q", spec.Nome, esperado.nome)
			}
			if spec.Versao != esperado.versao {
				t.Errorf("Versao = %q, esperava %q", spec.Versao, esperado.versao)
			}
			if got := spec.Container(); got != esperado.container {
				t.Errorf("Container() = %q, esperava %q", got, esperado.container)
			}
		})
	}
}

// Recusar um serviço desconhecido tem que ensinar o que existe.
func TestParseSpecDesconhecido(t *testing.T) {
	_, err := ParseSpec("mongodb")

	var desconhecido *ServicoDesconhecidoError
	if !errors.As(err, &desconhecido) {
		t.Fatalf("erro = %T, esperava *ServicoDesconhecidoError", err)
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("a mensagem deveria listar os disponíveis: %v", err)
	}
}

func TestStartCriaContainer(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")
	ctx := context.Background()

	s, err := m.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start falhou: %v", err)
	}
	if s.Estado != EstadoRodando {
		t.Errorf("Estado = %q, esperava %q", s.Estado, EstadoRodando)
	}

	// A imagem tem que ser baixada ANTES de criar o contêiner.
	log := strings.Join(chamadas(t, estado), "\n")
	if !strings.Contains(log, "pull docker.io/library/postgres:17") {
		t.Errorf("não baixou a imagem:\n%s", log)
	}

	var linhaRun string
	for _, c := range chamadas(t, estado) {
		if strings.HasPrefix(c, "run ") {
			linhaRun = c
		}
	}
	if linhaRun == "" {
		t.Fatalf("não houve comando run:\n%s", log)
	}

	// Cada uma destas é uma decisão de segurança ou de comportamento que
	// não pode se perder numa refatoração.
	exigidos := map[string]string{
		"--name devm-postgres-17":          "nome determinístico do contêiner",
		"--label devmanager=1":             "rótulo que evita mexer em contêiner alheio",
		"--restart unless-stopped":         "sobrevive a reboot, respeita stop deliberado",
		"127.0.0.1:5432:5432":              "publicado SÓ no loopback",
		"--volume devm-postgres-17-dados:": "volume nomeado, não bind mount",
		"--env POSTGRES_PASSWORD=secret":   "credenciais de desenvolvimento",
		"docker.io/library/postgres:17":    "imagem qualificada, exigência do podman",
	}
	for trecho, porque := range exigidos {
		if !strings.Contains(linhaRun, trecho) {
			t.Errorf("faltou %q (%s)\n  run: %s", trecho, porque, linhaRun)
		}
	}
}

// Publicar em 0.0.0.0 exporia um banco com senha "secret" na rede local.
func TestStartNuncaExpoeNaRede(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("mysql")

	if _, err := m.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	for _, c := range chamadas(t, estado) {
		if !strings.HasPrefix(c, "run ") {
			continue
		}
		if strings.Contains(c, "0.0.0.0:") || strings.Contains(c, "--publish 3306:") {
			t.Errorf("serviço exposto fora do loopback: %s", c)
		}
	}
}

func TestStartEhIdempotente(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("redis")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatalf("segundo Start falhou: %v", err)
	}

	var runs int
	for _, c := range chamadas(t, estado) {
		if strings.HasPrefix(c, "run ") {
			runs++
		}
	}
	if runs != 1 {
		t.Errorf("criou o contêiner %d vezes, esperava 1", runs)
	}
}

// Religar um contêiner parado usa start, não run: recriar perderia o que não
// estivesse no volume e mudaria a configuração silenciosamente.
func TestStartReligaSemRecriar(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}

	var runs, starts int
	for _, c := range chamadas(t, estado) {
		switch {
		case strings.HasPrefix(c, "run "):
			runs++
		case strings.HasPrefix(c, "start "):
			starts++
		}
	}
	if runs != 1 {
		t.Errorf("runs = %d, esperava 1", runs)
	}
	if starts != 1 {
		t.Errorf("starts = %d, esperava 1", starts)
	}
}

func TestListReportaEstados(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	ctx := context.Background()

	pg, _ := ParseSpec("postgres:17")
	rd, _ := ParseSpec("redis")

	if _, err := m.Start(ctx, pg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, rd); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, rd); err != nil {
		t.Fatal(err)
	}

	lista, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 2 {
		t.Fatalf("esperava 2 serviços, veio %d: %v", len(lista), lista)
	}

	estados := map[string]Estado{}
	for _, s := range lista {
		estados[s.Nome] = s.Estado
	}
	if estados["postgres"] != EstadoRodando {
		t.Errorf("postgres = %q, esperava rodando", estados["postgres"])
	}
	if estados["redis"] != EstadoParado {
		t.Errorf("redis = %q, esperava parado", estados["redis"])
	}
}

// Remover o contêiner NÃO pode apagar os dados sem pedido explícito.
func TestRemovePreservaDadosPorPadrao(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, spec, false); err != nil {
		t.Fatal(err)
	}

	for _, c := range chamadas(t, estado) {
		if strings.HasPrefix(c, "volume rm") {
			t.Errorf("apagou o volume sem --data: %s", c)
		}
	}

	if err := m.Remove(ctx, spec, true); err != nil {
		t.Fatal(err)
	}
	var apagou bool
	for _, c := range chamadas(t, estado) {
		if strings.Contains(c, "volume rm devm-postgres-17-dados") {
			apagou = true
		}
	}
	if !apagou {
		t.Error("com --data o volume deveria ter sido apagado")
	}
}

func TestStopDeServicoAusente(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("redis")

	if err := m.Stop(context.Background(), spec); err != nil {
		t.Errorf("parar serviço inexistente não deveria falhar: %v", err)
	}
}

// Porta ocupada tem que falhar ANTES de criar o contêiner, senão fica um
// contêiner morto no disco confundindo o próximo list.
func TestStartRecusaPortaOcupada(t *testing.T) {
	m, estado := prepararEngineFalso(t)
	spec, _ := ParseSpec("postgres:17")

	// Simula a porta ocupada sem depender do estado real da máquina.
	m.PortaLivre = func(int) error { return errors.New("ocupada") }

	_, err := m.Start(context.Background(), spec)

	var ocupada *PortaOcupadaError
	if !errors.As(err, &ocupada) {
		t.Fatalf("erro = %T (%v), esperava *PortaOcupadaError", err, err)
	}

	for _, c := range chamadas(t, estado) {
		if strings.HasPrefix(c, "run ") {
			t.Errorf("criou o contêiner apesar da porta ocupada: %s", c)
		}
	}
}

// O campo Names do podman pode vir como lista entre colchetes; o do docker
// vem como string. limparNome tem que cobrir os dois.
func TestLimparNome(t *testing.T) {
	casos := map[string]string{
		"devm-postgres-17":             "devm-postgres-17",
		"[devm-postgres-17]":           "devm-postgres-17",
		`"devm-redis-8"`:               "devm-redis-8",
		"  devm-mysql-8-4  ":           "devm-mysql-8-4",
		"[devm-redis-8 outro-apelido]": "devm-redis-8",
		"":                             "",
		"   ":                          "",
	}
	for entrada, esperado := range casos {
		if got := limparNome(entrada); got != esperado {
			t.Errorf("limparNome(%q) = %q, esperava %q", entrada, got, esperado)
		}
	}
}

func TestServicoDoNome(t *testing.T) {
	s := servicoDoNome("devm-mysql-8-4")
	if s.Nome != "mysql" {
		t.Errorf("Nome = %q", s.Nome)
	}
	if s.Versao != "8.4" {
		t.Errorf("Versao = %q, esperava 8.4", s.Versao)
	}
}

func TestLogs(t *testing.T) {
	m, _ := prepararEngineFalso(t)
	spec, _ := ParseSpec("redis")
	ctx := context.Background()

	if _, err := m.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := m.Logs(ctx, spec, &buf, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "devm-redis-8") {
		t.Errorf("logs = %q", buf.String())
	}
}

// Detectar precisa achar o engine no PATH e recusar com clareza quando não há.
func TestDetectar(t *testing.T) {
	dir := t.TempDir()
	falso := filepath.Join(dir, "podman")
	if err := os.WriteFile(falso, []byte(scriptEngineFalso), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_ESTADO", filepath.Join(dir, "estado"))
	// O script falso chama mkdir, ls e cut, que vêm do PATH original —
	// por isso prefixamos em vez de substituir.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	e, err := Detectar(context.Background(), "auto")
	if err != nil {
		t.Fatalf("Detectar falhou: %v", err)
	}
	if e.Bin != "podman" {
		t.Errorf("Bin = %q, esperava podman", e.Bin)
	}
	if !e.QualificaImagem {
		t.Error("podman exige imagem qualificada; a flag deveria estar ligada")
	}
	if e.Versao == "" {
		t.Error("a versão do engine não foi capturada")
	}
}

// Com preferência explícita, só o engine escolhido é tentado — e a mensagem
// de erro não pode sugerir instalar o outro.
func TestDetectarRespeitaPreferencia(t *testing.T) {
	dir := t.TempDir()
	falso := filepath.Join(dir, "podman")
	if err := os.WriteFile(falso, []byte(scriptEngineFalso), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_ESTADO", filepath.Join(dir, "estado"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Preferindo docker, o podman falso presente no PATH é ignorado.
	_, err := Detectar(context.Background(), "docker")
	if err == nil {
		t.Fatal("esperava erro: só o docker deveria ter sido tentado")
	}
	if strings.Contains(err.Error(), "podman:") {
		t.Errorf("o podman não deveria ter sido tentado: %v", err)
	}
	if !strings.Contains(err.Error(), "devm service engine auto") {
		t.Errorf("a mensagem deveria orientar como voltar ao automático: %v", err)
	}

	// Preferindo podman, ele é escolhido.
	e, err := Detectar(context.Background(), "podman")
	if err != nil {
		t.Fatalf("Detectar com preferência podman falhou: %v", err)
	}
	if e.Bin != "podman" {
		t.Errorf("Bin = %q", e.Bin)
	}
}

func TestDetectarSemEngine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := Detectar(context.Background(), "auto")

	var indisponivel *EngineIndisponivelError
	if !errors.As(err, &indisponivel) {
		t.Fatalf("erro = %T, esperava *EngineIndisponivelError", err)
	}
	// A mensagem precisa dizer o que fazer, não só o que faltou.
	if !strings.Contains(err.Error(), "instale") {
		t.Errorf("a mensagem deveria orientar: %v", err)
	}
}
