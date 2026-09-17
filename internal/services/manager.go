package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Estado é a situação de um serviço nesta máquina.
type Estado string

const (
	EstadoAusente Estado = "ausente" // contêiner não existe
	EstadoParado  Estado = "parado"  // existe, não está rodando
	EstadoRodando Estado = "rodando"
)

// rotulo marca os contêineres criados por nós.
//
// Filtrar por rótulo, em vez de por prefixo de nome, garante que nunca vamos
// parar ou apagar um contêiner que o usuário criou por conta própria — mesmo
// que ele por acaso se chame "devm-postgres-17".
const rotulo = "devmanager=1"

// Servico é um serviço observado na máquina.
type Servico struct {
	Nome      string `json:"name"`
	Versao    string `json:"version"`
	Container string `json:"container"`
	Imagem    string `json:"image"`
	Estado    Estado `json:"state"`
	Portas    []Porta
}

// Manager executa operações de serviço através do engine detectado.
type Manager struct {
	Engine *Engine

	// Saida recebe o progresso de operações longas, como baixar uma imagem.
	Saida io.Writer

	// PortaLivre confere se uma porta do host está disponível.
	//
	// É um campo, e não uma chamada direta, para poder ser substituído nos
	// testes: a máquina de quem roda a suíte pode ter a 5432 ocupada por um
	// PostgreSQL de verdade — foi exatamente o que aconteceu aqui — e o teste
	// não pode depender disso.
	PortaLivre func(porta int) error
}

// List devolve os serviços que o Dev Manager conhece nesta máquina.
func (m *Manager) List(ctx context.Context) ([]Servico, error) {
	todos, err := m.nomesDeContainer(ctx, true)
	if err != nil {
		return nil, err
	}
	rodando, err := m.nomesDeContainer(ctx, false)
	if err != nil {
		return nil, err
	}

	emExecucao := make(map[string]bool, len(rodando))
	for _, n := range rodando {
		emExecucao[n] = true
	}

	servicos := make([]Servico, 0, len(todos))
	for _, nome := range todos {
		s := servicoDoNome(nome)
		s.Estado = EstadoParado

		if emExecucao[nome] {
			s.Estado = EstadoRodando
			// Só dá para perguntar as portas de um contêiner rodando; para
			// um parado, ficamos com as do catálogo como aproximação.
			if portas := m.portasReais(ctx, nome); len(portas) > 0 {
				s.Portas = portas
			}
		}
		servicos = append(servicos, s)
	}

	sort.Slice(servicos, func(i, j int) bool { return servicos[i].Container < servicos[j].Container })
	return servicos, nil
}

// Estado informa a situação de uma spec específica.
func (m *Manager) Estado(ctx context.Context, spec Spec) (Estado, error) {
	servicos, err := m.List(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range servicos {
		if s.Container == spec.Container() {
			return s.Estado, nil
		}
	}
	return EstadoAusente, nil
}

// Start sobe um serviço, criando o contêiner se ele ainda não existe.
//
// A operação é idempotente: chamar com um serviço já rodando não faz nada.
func (m *Manager) Start(ctx context.Context, spec Spec) (Servico, error) {
	estado, err := m.Estado(ctx, spec)
	if err != nil {
		return Servico{}, err
	}

	switch estado {
	case EstadoRodando:
		return servicoDoNome(spec.Container()), nil

	case EstadoParado:
		// Contêiner já existe com a configuração de antes: só religamos.
		// Recriar perderia dados que não estivessem no volume.
		if err := m.executar(ctx, "start", spec.Container()); err != nil {
			return Servico{}, fmt.Errorf("religando %s: %w", spec.Container(), err)
		}
		s := servicoDoNome(spec.Container())
		s.Estado = EstadoRodando
		return s, nil
	}

	// Ausente: verificar as portas ANTES de criar. Um contêiner que falha
	// ao subir por porta ocupada fica parado no disco e confunde o próximo
	// `devm service list`.
	if err := m.verificarPortas(spec); err != nil {
		return Servico{}, err
	}

	if err := m.baixarImagem(ctx, spec); err != nil {
		return Servico{}, err
	}
	if err := m.criar(ctx, spec); err != nil {
		return Servico{}, err
	}

	s := servicoDoNome(spec.Container())
	s.Estado = EstadoRodando
	s.Portas = spec.Portas
	return s, nil
}

// Stop para um serviço sem apagar nada.
func (m *Manager) Stop(ctx context.Context, spec Spec) error {
	estado, err := m.Estado(ctx, spec)
	if err != nil {
		return err
	}
	if estado != EstadoRodando {
		return nil // já parado ou ausente: nada a fazer
	}
	return m.executar(ctx, "stop", spec.Container())
}

// Remove apaga o contêiner. Com apagarDados, apaga também o volume.
//
// Separar as duas coisas é essencial: recriar um contêiner é rotina, apagar
// o banco de dados é irreversível. Juntar as operações num comando só seria
// um convite a perder dados por engano.
func (m *Manager) Remove(ctx context.Context, spec Spec, apagarDados bool) error {
	estado, err := m.Estado(ctx, spec)
	if err != nil {
		return err
	}

	if estado != EstadoAusente {
		if err := m.executar(ctx, "rm", "--force", spec.Container()); err != nil {
			return fmt.Errorf("removendo %s: %w", spec.Container(), err)
		}
	}

	if apagarDados && spec.VolumeInterno != "" {
		// Ignoramos o erro: o volume pode nunca ter sido criado, e isso
		// não é falha — o resultado pedido (volume inexistente) já vale.
		_ = m.executar(ctx, "volume", "rm", spec.Volume())
	}
	return nil
}

// Logs escreve os logs do serviço no destino informado.
func (m *Manager) Logs(ctx context.Context, spec Spec, destino io.Writer, seguir bool) error {
	args := []string{"logs"}
	if seguir {
		args = append(args, "--follow")
	}
	args = append(args, spec.Container())

	cmd := exec.CommandContext(ctx, m.Engine.Bin, args...)
	cmd.Stdout = destino
	cmd.Stderr = destino

	return cmd.Run()
}

// criar monta e executa o comando de criação do contêiner.
func (m *Manager) criar(ctx context.Context, spec Spec) error {
	args := []string{
		"run", "--detach",
		"--name", spec.Container(),
		"--label", rotulo,
		// Reiniciar sozinho depois de um reboot é o que faz o serviço
		// "simplesmente estar lá" no dia seguinte. "unless-stopped" respeita
		// um stop deliberado, diferente de "always".
		"--restart", "unless-stopped",
	}

	for _, p := range spec.Portas {
		// Ligamos só ao loopback. Sem o 127.0.0.1 explícito, o serviço fica
		// exposto na rede local — com as credenciais fracas de
		// desenvolvimento. Num café com Wi-Fi aberto isso é um problema real.
		args = append(args, "--publish", fmt.Sprintf("127.0.0.1:%d:%d", p.Host, p.Interna))
	}

	chaves := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		chaves = append(chaves, k)
	}
	sort.Strings(chaves) // ordem estável deixa o comando reproduzível
	for _, k := range chaves {
		args = append(args, "--env", k+"="+spec.Env[k])
	}

	if spec.VolumeInterno != "" {
		args = append(args, "--volume", spec.Volume()+":"+spec.VolumeInterno)
	}

	args = append(args, spec.ImagemCompleta())

	return m.executar(ctx, args...)
}

// baixarImagem garante a imagem localmente, reportando o progresso.
func (m *Manager) baixarImagem(ctx context.Context, spec Spec) error {
	imagem := spec.ImagemCompleta()

	// Imagem já presente: nada a baixar. O inspect é barato e evita uma
	// consulta de rede a cada start.
	if err := m.executar(ctx, "image", "inspect", imagem); err == nil {
		return nil
	}

	if m.Saida != nil {
		fmt.Fprintf(m.Saida, "baixando %s (pode demorar na primeira vez)...\n", imagem)
	}

	cmd := exec.CommandContext(ctx, m.Engine.Bin, "pull", imagem)
	if m.Saida != nil {
		cmd.Stdout = m.Saida
		cmd.Stderr = m.Saida
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("baixando %s: %w", imagem, err)
	}
	return nil
}

// verificarPortas confere se as portas do host estão livres.
func (m *Manager) verificarPortas(spec Spec) error {
	checar := m.PortaLivre
	if checar == nil {
		checar = portaLivre
	}

	for _, p := range spec.Portas {
		if err := checar(p.Host); err != nil {
			return &PortaOcupadaError{Servico: spec.Nome, Porta: p.Host}
		}
	}
	return nil
}

func portaLivre(porta int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", porta))
	if err != nil {
		return err
	}
	return l.Close()
}

// portasReais pergunta ao engine quais portas o contêiner realmente publicou.
//
// Reconstruir a partir do catálogo daria a resposta ERRADA para quem subiu o
// serviço numa porta alternativa — situação comum, já que 5432 e 3306 vivem
// ocupadas por instalações nativas. A saída do comando `port` tem o mesmo
// formato nas duas engines:
//
//	5432/tcp -> 127.0.0.1:5433
func (m *Manager) portasReais(ctx context.Context, container string) []Porta {
	saida, err := m.capturar(ctx, "port", container)
	if err != nil {
		return nil
	}

	var portas []Porta
	for _, linha := range strings.Split(saida, "\n") {
		interna, hostParte, ok := strings.Cut(strings.TrimSpace(linha), " -> ")
		if !ok {
			continue
		}

		internaNum := numeroAntesDaBarra(interna)
		hostNum := numeroDepoisDoDoisPontos(hostParte)
		if internaNum == 0 || hostNum == 0 {
			continue
		}
		portas = append(portas, Porta{Host: hostNum, Interna: internaNum})
	}
	return portas
}

func numeroAntesDaBarra(s string) int {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func numeroDepoisDoDoisPontos(s string) int {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// nomesDeContainer lista os contêineres com o nosso rótulo.
func (m *Manager) nomesDeContainer(ctx context.Context, incluirParados bool) ([]string, error) {
	args := []string{"ps", "--filter", "label=" + rotulo, "--format", "{{.Names}}"}
	if incluirParados {
		args = append(args, "--all")
	}

	saida, err := m.capturar(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listando contêineres: %w", err)
	}

	var nomes []string
	for _, linha := range strings.Split(saida, "\n") {
		if nome := limparNome(linha); nome != "" {
			nomes = append(nomes, nome)
		}
	}
	return nomes, nil
}

// limparNome normaliza a saída do template entre as engines.
//
// O campo Names do Podman é uma LISTA de nomes, e o template pode renderizá-la
// como "[devm-postgres-17]" dependendo da versão; no Docker é uma string
// simples. Tirar colchetes e aspas cobre os dois sem precisar detectar a
// versão do engine.
func limparNome(linha string) string {
	n := strings.TrimSpace(linha)
	n = strings.Trim(n, "[]\"' ")

	// Se ainda restou mais de um nome, ficamos com o primeiro: é o nome
	// que demos ao criar o contêiner.
	if i := strings.IndexAny(n, " ,"); i > 0 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

// servicoDoNome reconstrói serviço e versão a partir do nome do contêiner.
func servicoDoNome(container string) Servico {
	s := Servico{Container: container, Estado: EstadoAusente}

	resto := strings.TrimPrefix(container, prefixoContainer)
	// O nome do serviço nunca tem hífen no catálogo, então o primeiro
	// hífen separa nome de versão.
	nome, versao, _ := strings.Cut(resto, "-")

	s.Nome, s.Versao = nome, strings.ReplaceAll(versao, "-", ".")
	if def, ok := catalogo[nome]; ok {
		s.Imagem = def.Imagem + ":" + s.Versao
		s.Portas = def.Portas
	}
	return s
}

func (m *Manager) executar(ctx context.Context, args ...string) error {
	_, err := m.capturar(ctx, args...)
	return err
}

// capturar roda o engine e devolve o stdout, com o stderr no erro.
func (m *Manager) capturar(ctx context.Context, args ...string) (string, error) {
	ctx, cancelar := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelar()

	cmd := exec.CommandContext(ctx, m.Engine.Bin, args...)

	var saida, erros bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &erros

	if err := cmd.Run(); err != nil {
		// A mensagem do engine no stderr é muito mais útil que
		// "exit status 125", então ela vira o erro.
		msg := strings.TrimSpace(erros.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%s: %s", m.Engine.Bin, msg)
	}
	return saida.String(), nil
}

// PortaOcupadaError indica conflito de porta no host.
type PortaOcupadaError struct {
	Servico string
	Porta   int
}

func (e *PortaOcupadaError) Error() string {
	return fmt.Sprintf("a porta %d, necessária para %s, já está em uso\n"+
		"  pare quem está usando, ou rode outro serviço nessa porta", e.Porta, e.Servico)
}
