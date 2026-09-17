package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/AlexRogaleski/devmanager/internal/semver"
)

// SourceSystem marca runtimes que vieram do gerenciador de pacotes do SO.
const SourceSystem = "system"

// nomePHP casa "php", "php8", "php8.3" e "php83" — as convenções de nome
// usadas por Debian/Ubuntu, Fedora, Homebrew e Remi, respectivamente.
var nomePHP = regexp.MustCompile(`^php(\d+(\.\d+)?)?$`)

// SystemProvider encontra os PHPs já instalados na máquina.
//
// Ele é o Provider de menor prioridade e serve de rede de segurança: funciona
// sem baixar nada, no primeiro minuto de uso. O objetivo do projeto continua
// sendo o PHP estático isolado, que virá como StaticProvider — mas a interface
// já garante que trocar um pelo outro não toca em nenhum outro pacote.
type SystemProvider struct {
	// ExtraDirs permite apontar pastas fora do PATH. Nos testes é por aqui
	// que injetamos um diretório temporário com binários falsos.
	ExtraDirs []string

	// Timeout limita quanto esperamos por cada binário interrogado.
	// Valor zero usa o padrão — assim SystemProvider{} já é utilizável,
	// o que em Go se chama "zero value útil".
	Timeout time.Duration
}

func (p *SystemProvider) Name() string { return SourceSystem }

// List varre os diretórios candidatos e interroga cada binário encontrado.
func (p *SystemProvider) List(ctx context.Context) ([]Runtime, error) {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	// Chaveado pelo caminho REAL: em Debian /usr/bin/php é um symlink para
	// /usr/bin/php8.3, e sem resolver o link o mesmo PHP apareceria duas vezes.
	vistos := make(map[string]Runtime)

	for _, dir := range p.diretorios() {
		entradas, err := os.ReadDir(dir)
		if err != nil {
			continue // diretório do PATH que não existe é normal, não é falha
		}

		for _, e := range entradas {
			if e.IsDir() || !nomePHP.MatchString(e.Name()) {
				continue
			}

			caminho := filepath.Join(dir, e.Name())
			real, err := filepath.EvalSymlinks(caminho)
			if err != nil {
				real = caminho
			}
			if _, jaVi := vistos[real]; jaVi {
				continue
			}

			v, err := versaoDoBinario(ctx, real, timeout)
			if err != nil {
				continue // não é um PHP funcional; ignora em silêncio
			}

			vistos[real] = Runtime{
				Language: "php",
				Version:  v,
				Bin:      real,
				Source:   SourceSystem,
			}
		}
	}

	// make com capacidade conhecida evita realocações durante o append.
	encontrados := make([]Runtime, 0, len(vistos))
	for _, r := range vistos {
		encontrados = append(encontrados, r)
	}
	return encontrados, nil
}

// diretorios devolve onde procurar, na ordem.
func (p *SystemProvider) diretorios() []string {
	dirs := append([]string{}, p.ExtraDirs...)

	// filepath.SplitList usa o separador certo por sistema — ":" no Linux e
	// macOS, ";" no Windows. Detalhe pequeno que já deixa o código portátil.
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)

	// Caminhos comuns que costumam ficar fora do PATH de sessões não
	// interativas, como a de um daemon iniciado pelo systemd.
	dirs = append(dirs,
		"/usr/bin", "/usr/local/bin", "/bin",
		"/opt/homebrew/bin",            // Homebrew no Apple Silicon
		"/usr/local/opt/php/bin",       // Homebrew no Intel
		"/opt/remi/php83/root/usr/bin", // Remi no Fedora/RHEL
	)
	return dirs
}

// versaoDoBinario pergunta a versão ao próprio interpretador.
//
// Executar o binário é a única fonte confiável: o nome do arquivo mente
// (um "php" pode ser 8.1 ou 8.5) e adivinhar pelo caminho é frágil.
func versaoDoBinario(ctx context.Context, bin string, timeout time.Duration) (semver.Version, error) {
	// context.WithTimeout deriva um contexto que se cancela sozinho. O defer
	// cancel() libera os recursos do timer mesmo quando o comando termina antes.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// CommandContext mata o processo se o contexto for cancelado — sem isso,
	// um PHP travado numa extensão quebrada penduraria a CLI para sempre.
	//
	// -n ignora o php.ini: um ini com extensão faltando emitiria warnings
	// no stdout e contaminaria a saída que vamos parsear.
	cmd := exec.CommandContext(ctx, bin, "-n", "-r", "echo PHP_VERSION;")
	saida, err := cmd.Output()
	if err != nil {
		return semver.Version{}, err
	}

	return semver.Parse(strings.TrimSpace(string(saida)))
}
