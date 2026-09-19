package runtimes

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// nodeFalso cria uma instalação de Node de mentira num layout de gerenciador.
func nodeFalso(t *testing.T, raiz, versao string, comNpx bool) {
	t.Helper()

	bin := filepath.Join(raiz, "v"+versao, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	nomes := []string{"node", "npm"}
	if comNpx {
		nomes = append(nomes, "npx")
	}
	for _, n := range nomes {
		script := "#!/bin/sh\nprintf 'v" + versao + "'\n"
		if err := os.WriteFile(filepath.Join(bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNodeSystemProviderDescobreVersoes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os binários de teste são scripts de shell")
	}

	raiz := t.TempDir()
	nodeFalso(t, raiz, "22.11.0", true)
	nodeFalso(t, raiz, "20.18.0", true)

	p := &NodeSystemProvider{ExtraDirs: []string{
		filepath.Join(raiz, "v22.11.0"),
		filepath.Join(raiz, "v20.18.0"),
	}}

	encontrados, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	versoes := map[string]Runtime{}
	for _, r := range encontrados {
		versoes[r.Version.String()] = r
		if r.Language != "node" {
			t.Errorf("Language = %q", r.Language)
		}
	}
	for _, v := range []string{"22.11.0", "20.18.0"} {
		if _, ok := versoes[v]; !ok {
			t.Errorf("não encontrou o Node %s (achou: %v)", v, versoes)
		}
	}
}

// O shim precisa expor npm e npx junto: um `npm run dev` que caísse no npm do
// sistema rodaria com a versão errada de Node por baixo.
func TestNodeExpoeNpmENpx(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os binários de teste são scripts de shell")
	}

	raiz := t.TempDir()
	nodeFalso(t, raiz, "22.11.0", true)

	p := &NodeSystemProvider{ExtraDirs: []string{filepath.Join(raiz, "v22.11.0")}}
	encontrados, err := p.List(context.Background())
	if err != nil || len(encontrados) == 0 {
		t.Fatalf("não encontrou o Node: %v", err)
	}

	cmds := encontrados[0].Executaveis()
	for _, n := range []string{"node", "npm", "npx"} {
		if _, ok := cmds[n]; !ok {
			t.Errorf("faltou o comando %q: %v", n, cmds)
		}
	}
}

// Instalação sem npx não pode gerar um link quebrado no shim.
func TestNodeSemNpxNaoInventaComando(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os binários de teste são scripts de shell")
	}

	raiz := t.TempDir()
	nodeFalso(t, raiz, "18.20.0", false)

	cmds := comandosVizinhos(filepath.Join(raiz, "v18.20.0", "bin", "node"))
	if _, ok := cmds["npx"]; ok {
		t.Error("declarou npx que não existe no disco")
	}
	if _, ok := cmds["npm"]; !ok {
		t.Error("não declarou o npm, que existe")
	}
}

func TestSemPrimeiroComponente(t *testing.T) {
	casos := map[string]string{
		"node-v22.11.0-linux-x64/bin/node": "bin/node",
		"node-v22.11.0-linux-x64/":         "",
		"node-v22.11.0-linux-x64":          "",
		"./node-v22/bin/npm":               "bin/npm",
		"a/b/c":                            "b/c",
	}
	for entrada, esperado := range casos {
		if got := semPrimeiroComponente(entrada); got != esperado {
			t.Errorf("semPrimeiroComponente(%q) = %q, esperava %q", entrada, got, esperado)
		}
	}
}

// Diferente do PHP, aqui extraímos uma ÁRVORE — então a validação de caminho
// é obrigatória, e não opcional por construção.
func TestExtrairArvoreBloqueiaEscape(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	conteudo := []byte("malicioso")
	// Precisa de ".." suficientes para sobrar um depois que filepath.Clean
	// resolve e o primeiro componente é removido — com poucos, o caminho
	// ainda cai dentro do destino e não há escape a recusar.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "topo/../../../escapou.txt",
		Mode:     0o644,
		Size:     int64(len(conteudo)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Write(conteudo)
	tw.Close()

	destino := filepath.Join(t.TempDir(), "instalacao")
	err := extrairArvore(tar.NewReader(&buf), destino)
	if err == nil {
		t.Fatal("esperava recusa de caminho que escapa do destino")
	}
	if !strings.Contains(err.Error(), "suspeita") {
		t.Errorf("mensagem inesperada: %v", err)
	}
}

// Um ".." raso é resolvido pelo Clean e o arquivo cai DENTRO do destino —
// seguro, e por isso não é recusado. O teste registra esse comportamento
// para que uma refatoração não o transforme em recusa sem querer.
func TestExtrairArvoreEscapeRasoCaiDentro(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	conteudo := []byte("inofensivo")
	tw.WriteHeader(&tar.Header{
		Name: "topo/../../dentro.txt", Mode: 0o644,
		Size: int64(len(conteudo)), Typeflag: tar.TypeReg,
	})
	tw.Write(conteudo)
	tw.Close()

	destino := filepath.Join(t.TempDir(), "instalacao")
	if err := extrairArvore(tar.NewReader(&buf), destino); err != nil {
		t.Fatalf("não deveria recusar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destino, "dentro.txt")); err != nil {
		t.Errorf("o arquivo deveria ter caído dentro do destino: %v", err)
	}
}

// Link absoluto apontaria para fora da árvore instalada.
func TestExtrairArvoreBloqueiaLinkAbsoluto(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if err := tw.WriteHeader(&tar.Header{
		Name:     "topo/bin/npm",
		Linkname: "/etc/passwd",
		Typeflag: tar.TypeSymlink,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	err := extrairArvore(tar.NewReader(&buf), filepath.Join(t.TempDir(), "i"))
	if err == nil {
		t.Fatal("esperava recusa de link absoluto")
	}
}

func TestExtrairArvoreNormal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links simbólicos exigem privilégio no Windows")
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	conteudo := []byte("#!/bin/sh\necho oi\n")
	tw.WriteHeader(&tar.Header{Name: "topo/bin/", Mode: 0o755, Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{
		Name: "topo/bin/node", Mode: 0o755,
		Size: int64(len(conteudo)), Typeflag: tar.TypeReg,
	})
	tw.Write(conteudo)
	// Link relativo, como o npm real é distribuído.
	tw.WriteHeader(&tar.Header{
		Name: "topo/bin/npm", Linkname: "../lib/node_modules/npm/bin/npm-cli.js",
		Typeflag: tar.TypeSymlink,
	})
	tw.Close()

	destino := filepath.Join(t.TempDir(), "instalacao")
	if err := extrairArvore(tar.NewReader(&buf), destino); err != nil {
		t.Fatalf("extrairArvore falhou: %v", err)
	}

	// O diretório de topo foi removido: bin/node, não topo/bin/node.
	info, err := os.Stat(filepath.Join(destino, "bin", "node"))
	if err != nil {
		t.Fatalf("arquivo não foi extraído no lugar certo: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("perdeu a permissão de execução: %v", info.Mode())
	}

	if _, err := os.Lstat(filepath.Join(destino, "bin", "npm")); err != nil {
		t.Errorf("link relativo não foi criado: %v", err)
	}
}

func TestArquivoDaPlataforma(t *testing.T) {
	arq := arquivoDaPlataforma()

	// O índice do nodejs.org usa "linux-x64" e "osx-arm64-tar", não os nomes
	// do Go ("linux/amd64").
	if strings.Contains(arq, "amd64") {
		t.Errorf("usou o nome do Go em vez do nodejs.org: %q", arq)
	}
	if arq == "" {
		t.Error("plataforma vazia")
	}
}

// fnmFalso cria uma versão no layout do fnm: <dir>/node-versions/vX/installation/bin/node.
func fnmFalso(t *testing.T, dirFnm, versao string) {
	t.Helper()
	nodeFalso(t, filepath.Join(dirFnm, "node-versions"), versao, true)
	// nodeFalso cria <raiz>/vX/bin; o fnm guarda em <raiz>/vX/installation/bin.
	raiz := filepath.Join(dirFnm, "node-versions", "v"+versao)
	if err := os.MkdirAll(filepath.Join(raiz, "installation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(raiz, "bin"), filepath.Join(raiz, "installation", "bin")); err != nil {
		t.Fatal(err)
	}
}

// O fnm guarda as versões no diretório de dados da plataforma — e a lista
// tinha só o ~/.fnm das versões antigas. Quem instalou o fnm nos últimos
// anos não era encontrado, nem no Linux nem no macOS.
func TestNodeEncontraOFnmEmTodosOsLugares(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os binários de teste são scripts de shell")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("NVM_DIR", "")
	personalizado := filepath.Join(t.TempDir(), "meu-fnm")
	t.Setenv("FNM_DIR", personalizado)

	fnmFalso(t, filepath.Join(home, ".local", "share", "fnm"), "22.1.0")                // Linux
	fnmFalso(t, filepath.Join(home, "Library", "Application Support", "fnm"), "20.1.0") // macOS
	fnmFalso(t, filepath.Join(home, ".fnm"), "18.1.0")                                  // versões antigas
	fnmFalso(t, personalizado, "23.1.0")                                                // FNM_DIR

	encontrados, err := (&NodeSystemProvider{}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	achados := map[string]string{}
	for _, r := range encontrados {
		achados[r.Version.String()] = r.Source
	}
	for _, v := range []string{"22.1.0", "20.1.0", "18.1.0", "23.1.0"} {
		if achados[v] != "fnm" {
			t.Errorf("Node %s do fnm não encontrado (achados: %v)", v, achados)
		}
	}
}
