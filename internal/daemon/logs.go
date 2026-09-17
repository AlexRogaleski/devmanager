package daemon

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// LinhasPadrao é quantas linhas o anel guarda por ambiente.
//
// Mil linhas cobrem o rastro de pilha mais longo do Laravel com folga, e
// custam alguns quilobytes. Guardar tudo em memória faria um `queue:work`
// falando muito consumir o dia inteiro; guardar pouco tornaria os logs
// inúteis justamente quando algo dá errado.
const LinhasPadrao = 1000

// Anel guarda as últimas linhas de log e permite acompanhar as novas.
//
// É um buffer circular: ao encher, a linha mais antiga sai. O nome "anel"
// vem daí.
type Anel struct {
	mu         sync.Mutex
	linhas     []Linha
	capacidade int

	// inscritos são os clientes acompanhando com ?follow=1.
	inscritos map[int]chan Linha
	proximoID int
}

func NovoAnel(capacidade int) *Anel {
	if capacidade <= 0 {
		capacidade = LinhasPadrao
	}
	return &Anel{
		capacidade: capacidade,
		linhas:     make([]Linha, 0, capacidade),
		inscritos:  make(map[int]chan Linha),
	}
}

// Escrever acrescenta uma linha e notifica quem está acompanhando.
func (a *Anel) Escrever(processo, texto string, em time.Time) {
	linha := Linha{Processo: processo, Texto: texto, Em: em}

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.linhas) == a.capacidade {
		// Descarta a mais antiga deslocando o slice. Para mil linhas isso é
		// irrelevante; um anel com índice seria mais eficiente e menos legível.
		copy(a.linhas, a.linhas[1:])
		a.linhas[len(a.linhas)-1] = linha
	} else {
		a.linhas = append(a.linhas, linha)
	}

	for _, ch := range a.inscritos {
		// Envio NÃO bloqueante: um cliente lento acompanhando logs jamais
		// pode travar o processo que está escrevendo. Se o canal do inscrito
		// está cheio, a linha se perde para ELE — o histórico continua
		// completo para quem pedir depois.
		select {
		case ch <- linha:
		default:
		}
	}
}

// Historico devolve uma cópia das linhas guardadas.
//
// Cópia, e não o slice interno: devolver o interno permitiria a quem chamou
// ler enquanto escrevemos, o que é exatamente uma corrida de dados.
func (a *Anel) Historico() []Linha {
	a.mu.Lock()
	defer a.mu.Unlock()

	saida := make([]Linha, len(a.linhas))
	copy(saida, a.linhas)
	return saida
}

// Inscrever devolve um canal com as linhas novas e a função de cancelamento.
//
// Quem se inscreve DEVE chamar o cancelamento, senão o anel guarda o canal
// para sempre e vaza memória a cada cliente que desconecta.
func (a *Anel) Inscrever(buffer int) (<-chan Linha, func()) {
	if buffer <= 0 {
		buffer = 256
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	id := a.proximoID
	a.proximoID++

	ch := make(chan Linha, buffer)
	a.inscritos[id] = ch

	return ch, func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		if c, existe := a.inscritos[id]; existe {
			delete(a.inscritos, id)
			close(c)
		}
	}
}

// Fechar encerra todos os inscritos.
func (a *Anel) Fechar() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, ch := range a.inscritos {
		delete(a.inscritos, id)
		close(ch)
	}
}

// Escritor adapta o anel para io.Writer, quebrando a entrada em linhas.
//
// O supervisor entrega os bytes como o processo os produziu — e um processo
// escreve "Server ru" numa chamada e "nning\n" na outra. Guardar cada Write
// como uma linha produziria logs picados, então acumulamos até a quebra.
func (a *Anel) Escritor(processo string) io.Writer {
	return &escritorDeAnel{anel: a, processo: processo}
}

type escritorDeAnel struct {
	anel     *Anel
	processo string

	mu  sync.Mutex
	buf bytes.Buffer
}

func (e *escritorDeAnel) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.buf.Write(p)
	agora := time.Now()

	for {
		linha, err := e.buf.ReadBytes('\n')
		if err != nil {
			// Sem quebra ainda: devolve ao buffer e espera o resto.
			e.buf.Write(linha)
			break
		}
		e.anel.Escrever(e.processo, string(bytes.TrimRight(linha, "\r\n")), agora)
	}

	// Sempre reportamos tudo consumido: o que ficou no buffer é linha
	// incompleta, não perda. Devolver menos faria o io.Copy do os/exec
	// abortar com ErrShortWrite.
	return len(p), nil
}
