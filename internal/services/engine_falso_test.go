package services

import (
	"os"
	"path/filepath"
	"testing"
)

// engineFalso é um script que se comporta como podman/docker para os
// comandos que usamos, guardando estado em arquivos.
//
// Escrever um engine falso dá bastante trabalho, mas é a única forma de
// testar a CONSTRUÇÃO dos comandos — que é onde moram os erros de verdade
// deste pacote. Um mock em Go testaria só a nossa própria abstração; o script
// exercita o caminho real, incluindo o parsing da saída.
const scriptEngineFalso = `#!/bin/sh
set -e
ESTADO="${FAKE_ESTADO:?FAKE_ESTADO não definido}"
mkdir -p "$ESTADO/containers" "$ESTADO/rodando" "$ESTADO/volumes" "$ESTADO/imagens"

# Registra cada invocação, para os testes conferirem os argumentos montados.
echo "$@" >> "$ESTADO/chamadas.log"

case "$1" in
  version)
    echo "9.9.9-falso" ;;

  info)
    echo "true" ;;

  ps)
    todos=0
    for a in "$@"; do [ "$a" = "--all" ] && todos=1; done
    if [ "$todos" = "1" ]; then
      ls "$ESTADO/containers" 2>/dev/null || true
    else
      ls "$ESTADO/rodando" 2>/dev/null || true
    fi ;;

  run)
    nome=""
    prox=0
    for a in "$@"; do
      [ "$prox" = "1" ] && { nome="$a"; prox=0; }
      [ "$a" = "--name" ] && prox=1
    done
    [ -n "$nome" ] || { echo "run sem --name" >&2; exit 1; }
    touch "$ESTADO/containers/$nome" "$ESTADO/rodando/$nome"
    # Guarda os mapeamentos de porta para responder ao comando "port".
    mkdir -p "$ESTADO/portas"
    : > "$ESTADO/portas/$nome"
    prox=0
    for a in "$@"; do
      if [ "$prox" = "1" ]; then
        host=$(echo "$a" | cut -d: -f2)
        cont=$(echo "$a" | cut -d: -f3)
        echo "$cont/tcp -> 127.0.0.1:$host" >> "$ESTADO/portas/$nome"
        prox=0
      fi
      [ "$a" = "--publish" ] && prox=1
    done
    echo "$nome" ;;

  start)
    touch "$ESTADO/rodando/$2" ;;

  stop)
    rm -f "$ESTADO/rodando/$2" ;;

  rm)
    alvo="$2"; [ "$2" = "--force" ] && alvo="$3"
    rm -f "$ESTADO/containers/$alvo" "$ESTADO/rodando/$alvo" ;;

  logs)
    echo "log do contêiner $2" ;;

  port)
    if [ -f "$ESTADO/portas/$2" ]; then cat "$ESTADO/portas/$2"; fi ;;

  pull)
    touch "$ESTADO/imagens/$(echo "$2" | tr '/:' '__')" ;;

  image)
    # image inspect <img> — falha se a imagem não foi baixada
    marca="$ESTADO/imagens/$(echo "$3" | tr '/:' '__')"
    [ -f "$marca" ] || { echo "imagem ausente: $3" >&2; exit 1; } ;;

  volume)
    # volume rm <vol>
    rm -f "$ESTADO/volumes/$3" ;;

  *)
    echo "comando não suportado pelo engine falso: $1" >&2
    exit 1 ;;
esac
`

// prepararEngineFalso cria o script e devolve o Manager que o usa.
func prepararEngineFalso(t *testing.T) (*Manager, string) {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "engine-falso")

	if err := os.WriteFile(bin, []byte(scriptEngineFalso), 0o755); err != nil {
		t.Fatal(err)
	}

	estado := filepath.Join(dir, "estado")
	t.Setenv("FAKE_ESTADO", estado)

	return &Manager{
		Engine: &Engine{Bin: bin, Versao: "9.9.9-falso"},
		// Nos testes nunca tocamos nas portas reais da máquina.
		PortaLivre: func(int) error { return nil },
	}, estado
}

// chamadas devolve os comandos que o engine recebeu.
func chamadas(t *testing.T, estado string) []string {
	t.Helper()

	dados, err := os.ReadFile(filepath.Join(estado, "chamadas.log"))
	if err != nil {
		return nil
	}

	var linhas []string
	inicio := 0
	for i, c := range dados {
		if c == '\n' {
			linhas = append(linhas, string(dados[inicio:i]))
			inicio = i + 1
		}
	}
	return linhas
}
