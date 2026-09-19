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

# sha256sum é do GNU coreutils e não vem no macOS; shasum vem nos dois. A
# saída é idêntica — "hash  arquivo" —, então o SHA256SUMS confere igual com
# qualquer um.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

.PHONY: build install test race cover vet fmt cross clean static install-host release

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
	@# Capacidades do setcap são atributos do ARQUIVO: substituir o binário
	@# as apaga. Detectamos antes para poder avisar depois — descobrir isso
	@# só quando o proxy cair na porta alternativa custa caro.
	@TINHA=$$(getcap $(HOST_BIN) 2>/dev/null | grep -c cap_net_bind_service || true); 	mkdir -p $(dir $(HOST_BIN)); 	install -m 0755 bin/devm $(HOST_BIN); 	echo; 	echo "instalado em $(HOST_BIN)"; 	if [ "$$TINHA" != "0" ]; then 		echo; 		echo "ATENÇÃO: o binário tinha cap_net_bind_service e a substituição apagou."; 		echo "para o proxy voltar às portas 80/443:"; 		echo "  sudo setcap 'cap_net_bind_service=+ep' $(HOST_BIN)"; 		echo "  devm daemon stop && devm daemon start"; 	fi

## release: compila os binários de distribuição em ./dist
##
## Um runner Linux produz os quatro: sem cgo, o Go compila para macOS sem
## toolchain externo, então não há motivo para gastar minutos de runner
## macOS — que custam dez vezes mais.
##
## O -trimpath entra aqui e não no `static`: ele apaga os caminhos absolutos
## de compilação embutidos no binário. Num binário local isso é indiferente;
## num binário publicado, o rastro de pilha de um panic exporia a estrutura
## de diretórios da máquina que compilou.
release:
	@rm -rf dist && mkdir -p dist
	@for alvo in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		so=$${alvo%/*}; arch=$${alvo#*/}; \
		printf '  %s/%s\n' "$$so" "$$arch"; \
		CGO_ENABLED=0 GOOS=$$so GOARCH=$$arch go build -trimpath \
			-ldflags "$(LDFLAGS)" -o dist/devm-$$so-$$arch ./cmd/devm || exit 1; \
	done
	@cd dist && $(SHA256) devm-* > SHA256SUMS
	@echo
	@ls -1sh dist/

## clean: remove artefatos de build
clean:
	rm -rf bin/ dist/
