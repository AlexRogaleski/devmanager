package registry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func registroDeTeste(t *testing.T) *Registro {
	t.Helper()
	r, err := Carregar(Path(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCarregarArquivoAusente(t *testing.T) {
	r := registroDeTeste(t)
	if len(r.Projetos) != 0 {
		t.Errorf("esperava registro vazio, veio %v", r.Projetos)
	}
}

func TestAdicionarESalvarERecarregar(t *testing.T) {
	dir := t.TempDir()
	caminho := Path(dir)

	r, err := Carregar(caminho)
	if err != nil {
		t.Fatal(err)
	}

	projetoDir := filepath.Join(dir, "meu-app")
	if err := r.Adicionar(Projeto{Caminho: projetoDir}); err != nil {
		t.Fatalf("Adicionar falhou: %v", err)
	}
	if err := r.Salvar(); err != nil {
		t.Fatalf("Salvar falhou: %v", err)
	}

	// Recarregar de um processo novo tem que devolver o mesmo conteúdo:
	// é o que um segundo comando devm vai fazer.
	r2, err := Carregar(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Projetos) != 1 {
		t.Fatalf("esperava 1 projeto, veio %d", len(r2.Projetos))
	}
	if r2.Projetos[0].Nome != "meu-app" {
		t.Errorf("Nome = %q, esperava derivar da pasta (meu-app)", r2.Projetos[0].Nome)
	}
	if r2.Projetos[0].AdicionadoEm.IsZero() {
		t.Error("AdicionadoEm não foi preenchido")
	}
}

func TestCaminhoVirarAbsoluto(t *testing.T) {
	r := registroDeTeste(t)

	if err := r.Adicionar(Projeto{Caminho: "."}); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(r.Projetos[0].Caminho) {
		t.Errorf("Caminho = %q, esperava absoluto", r.Projetos[0].Caminho)
	}
}

// Os dois erros de duplicidade exigem ações diferentes do usuário, então
// precisam ser tipos diferentes — o scan usa isso para contar separadamente.
func TestDuplicidades(t *testing.T) {
	r := registroDeTeste(t)
	dir := t.TempDir()

	if err := r.Adicionar(Projeto{Caminho: filepath.Join(dir, "api")}); err != nil {
		t.Fatal(err)
	}

	t.Run("mesmo caminho", func(t *testing.T) {
		err := r.Adicionar(Projeto{Caminho: filepath.Join(dir, "api")})

		var jaReg *JaRegistradoError
		if !errors.As(err, &jaReg) {
			t.Fatalf("erro = %T (%v), esperava *JaRegistradoError", err, err)
		}
	})

	t.Run("mesmo nome, outro caminho", func(t *testing.T) {
		err := r.Adicionar(Projeto{Caminho: filepath.Join(t.TempDir(), "api")})

		var emUso *NomeEmUsoError
		if !errors.As(err, &emUso) {
			t.Fatalf("erro = %T (%v), esperava *NomeEmUsoError", err, err)
		}
		if !strings.Contains(emUso.Error(), "--name") {
			t.Errorf("a mensagem deveria sugerir a saída: %v", emUso)
		}
	})

	t.Run("nome explícito resolve", func(t *testing.T) {
		err := r.Adicionar(Projeto{Nome: "api-2", Caminho: filepath.Join(t.TempDir(), "api")})
		if err != nil {
			t.Fatalf("com --name deveria funcionar: %v", err)
		}
	})
}

func TestNomeIgnoraMaiusculas(t *testing.T) {
	r := registroDeTeste(t)
	dir := t.TempDir()

	if err := r.Adicionar(Projeto{Nome: "Loja", Caminho: filepath.Join(dir, "a")}); err != nil {
		t.Fatal(err)
	}

	// DNS não diferencia maiúsculas: "Loja.test" e "loja.test" são o mesmo
	// domínio, então não podem ser dois projetos.
	if err := r.Adicionar(Projeto{Nome: "loja", Caminho: filepath.Join(dir, "b")}); err == nil {
		t.Error("nomes que diferem só em caixa deveriam colidir")
	}

	if _, ok := r.Buscar("LOJA"); !ok {
		t.Error("Buscar deveria ignorar a caixa")
	}
}

func TestRemover(t *testing.T) {
	r := registroDeTeste(t)
	if err := r.Adicionar(Projeto{Caminho: filepath.Join(t.TempDir(), "app")}); err != nil {
		t.Fatal(err)
	}

	if !r.Remover("app") {
		t.Error("Remover devolveu false para projeto existente")
	}
	if len(r.Projetos) != 0 {
		t.Errorf("projeto não foi removido: %v", r.Projetos)
	}
	if r.Remover("nao-existe") {
		t.Error("Remover devolveu true para projeto inexistente")
	}
}

func TestOrdenacaoEstavel(t *testing.T) {
	r := registroDeTeste(t)
	dir := t.TempDir()

	for _, nome := range []string{"zeta", "alfa", "meio"} {
		if err := r.Adicionar(Projeto{Nome: nome, Caminho: filepath.Join(dir, nome)}); err != nil {
			t.Fatal(err)
		}
	}

	esperado := []string{"alfa", "meio", "zeta"}
	for i, e := range esperado {
		if r.Projetos[i].Nome != e {
			t.Errorf("posição %d = %q, esperava %q", i, r.Projetos[i].Nome, e)
		}
	}
}

// O nome vira um domínio local, então precisa ser válido para DNS.
func TestValidarNome(t *testing.T) {
	validos := []string{"api", "meu-app", "app_2", "a", "Loja123"}
	for _, n := range validos {
		if err := ValidarNome(n); err != nil {
			t.Errorf("ValidarNome(%q) = %v, esperava aceitar", n, err)
		}
	}

	invalidos := map[string]string{
		"":                      "vazio",
		"meu app":               "espaço",
		"app/sub":               "barra",
		"app.test":              "ponto",
		"-app":                  "começa com hífen",
		"app-":                  "termina com hífen",
		strings.Repeat("a", 64): "longo demais",
	}
	for n, motivo := range invalidos {
		if err := ValidarNome(n); err == nil {
			t.Errorf("ValidarNome(%q) deveria recusar (%s)", n, motivo)
		}
	}
}

func TestArquivoCorrompido(t *testing.T) {
	dir := t.TempDir()
	caminho := Path(dir)

	if err := os.WriteFile(caminho, []byte("{isto não é json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Falhar alto é o certo aqui: silenciar e começar do zero apagaria a
	// lista de projetos do usuário sem aviso.
	if _, err := Carregar(caminho); err == nil {
		t.Fatal("esperava erro para registro corrompido")
	}
}

func TestFormatoDoArquivo(t *testing.T) {
	dir := t.TempDir()
	r, err := Carregar(Path(dir))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Adicionar(Projeto{
		Nome:         "api",
		Caminho:      filepath.Join(dir, "api"),
		AdicionadoEm: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Salvar(); err != nil {
		t.Fatal(err)
	}

	dados, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}

	var bruto map[string]any
	if err := json.Unmarshal(dados, &bruto); err != nil {
		t.Fatalf("arquivo gravado não é JSON válido: %v", err)
	}
	if _, ok := bruto["projects"]; !ok {
		t.Errorf("esperava a chave \"projects\":\n%s", dados)
	}
	if strings.Contains(string(dados), "caminho") {
		t.Errorf("as chaves deveriam estar em inglês, como as tags declaram:\n%s", dados)
	}
}

func TestBuscarPorCaminho(t *testing.T) {
	r := registroDeTeste(t)
	dir := t.TempDir()
	projeto := filepath.Join(dir, "api")

	if err := r.Adicionar(Projeto{Caminho: projeto}); err != nil {
		t.Fatal(err)
	}

	if _, ok := r.BuscarPorCaminho(projeto); !ok {
		t.Error("não encontrou pelo caminho absoluto")
	}
	if _, ok := r.BuscarPorCaminho(filepath.Join(dir, "outro")); ok {
		t.Error("encontrou um caminho que não foi registrado")
	}
}
