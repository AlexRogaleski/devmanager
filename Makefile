# Dev Manager — alvos de desenvolvimento.
#
# A versão é injetada no binário em tempo de compilação, sem gerar arquivo:
# o -X sobrescreve a variável Version do pacote cli.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/AlexRogaleski/devmanager/internal/cli.Version=$(VERSION)

# Binário para rodar NO HOST.
#
# CGO_ENABLED=0 é o ponto central: com cgo ligado, o Go usa a glibc do sistema
# para os pacotes net e os/user, e o binário sai dinamicamente ligado à glibc
# do Ubuntu deste container. Rodá-lo no host dependeria das versões casarem —
# hoje casam (2.43 nos dois), mas isso quebra na primeira atualização de um
# dos lados.
#
# Sem cgo, o Go usa implementações em Go puro e o binário fica estático: roda
# em qualquer Linux x86-64, independente de distribuição ou versão de glibc.
# É a mesma propriedade que escolhemos para o PHP.
HOST_BIN ?= $(HOME)/.local/bin/devm

.PHONY: build install test race cover vet fmt cross clean static install-host

## build: compila o binário em ./bin/devm
build:
	go build -ldflags "$(LDFLAGS)" -o bin/devm ./cmd/devm

## install: instala o devm em $(go env GOPATH)/bin
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/devm

## test: roda a suíte
test:
	go test ./...

## race: roda a suíte com o detector de corrida (precisa de cgo/gcc)
race:
	go test ./... -race

## cover: mostra a cobertura por pacote
cover:
	go test ./... -cover

## vet: análise estática da stdlib
vet:
	go vet ./...

## fmt: formata tudo e falha se algo estava fora do padrão
fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "arquivos acima precisam de gofmt -w ." && exit 1)

## cross: confirma que o projeto compila nas plataformas alvo
cross:
	GOOS=linux   GOARCH=amd64 go build ./...
	GOOS=linux   GOARCH=arm64 go build ./...
	GOOS=darwin  GOARCH=amd64 go build ./...
	GOOS=darwin  GOARCH=arm64 go build ./...
	@echo "compila para linux e macOS, amd64 e arm64"

## static: compila um binário estático, sem dependência de glibc
static:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/devm ./cmd/devm
	@echo
	@echo "binário estático em bin/devm"
	@ldd bin/devm 2>&1 | head -1 || true

## install-host: instala o binário estático em ~/.local/bin, para rodar no host
##
## O home é compartilhado entre o container de desenvolvimento e o host, então
## o binário compilado aqui fica imediatamente disponível lá.
install-host: static
	@mkdir -p $(dir $(HOST_BIN))
	install -m 0755 bin/devm $(HOST_BIN)
	@echo
	@echo "instalado em $(HOST_BIN)"
	@echo "no host, rode:  devm version"

## clean: remove artefatos de build
clean:
	rm -rf bin/
