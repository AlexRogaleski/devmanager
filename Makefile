# Dev Manager — alvos de desenvolvimento.
#
# A versão é injetada no binário em tempo de compilação, sem gerar arquivo:
# o -X sobrescreve a variável Version do pacote cli.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/AlexRogaleski/devmanager/internal/cli.Version=$(VERSION)

.PHONY: build install test race cover vet fmt cross clean

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

## clean: remove artefatos de build
clean:
	rm -rf bin/
