# Dev Manager

[![CI](https://github.com/AlexRogaleski/devmanager/actions/workflows/ci.yml/badge.svg)](https://github.com/AlexRogaleski/devmanager/actions/workflows/ci.yml)
[![Licença MIT](https://img.shields.io/badge/licença-MIT-blue.svg)](LICENSE)

Ambiente de desenvolvimento local para projetos Laravel/PHP, sem um contêiner
por projeto.

```
$ devm start -d
suma rodando em segundo plano
  PHP        8.5.8
  serve      php artisan serve --host=127.0.0.1 --port=41859
  vite       npm run dev

  https://suma.test
```

O PHP roda **nativo**, como binário estático isolado do sistema. Só a
infraestrutura — banco, Redis, Mailpit — vai para contêiner, e é
**compartilhada** entre os projetos: cada um recebe seu próprio banco dentro
do mesmo servidor.

Medido com dois projetos reais rodando ao mesmo tempo:

| | contêineres | memória em contêiner |
|---|---|---|
| Laravel Sail | 7 | ~5 GB |
| Dev Manager | 3 | 38 MB |

Portas são alocadas automaticamente, então dois projetos sobem juntos sem
configuração — que é o atrito que motivou o projeto.

## Instalação

Precisa de Go 1.26+ para compilar e de Docker **ou** Podman para os serviços.

```sh
git clone https://github.com/AlexRogaleski/devmanager
cd devmanager
make install-host
```

`make install-host` compila um binário **estático** (`CGO_ENABLED=0`) em
`~/.local/bin/devm`. Estático porque ele precisa rodar em qualquer Linux
independente da versão da glibc — inclusive quando compilado dentro de um
container de desenvolvimento e executado no host.

### Configuração da máquina

Três ajustes de sistema, **uma vez por máquina**:

```sh
devm setup           # mostra o que falta e os comandos
devm setup --apply   # executa, pedindo a senha do sudo
```

| ajuste | para quê |
|---|---|
| `systemd-resolved` | fazer `.test` resolver para o loopback |
| `net.ipv4.ip_unprivileged_port_start=80` | abrir as portas 80 e 443 sem root |
| certificado da CA local | HTTPS sem aviso de inseguro |

O Firefox mantém armazenamento de certificados próprio e ignora o do sistema.
Para ele: **Configurações → Privacidade → Certificados → Ver certificados →
Autoridades → Importar**, marcando "confiar para identificar sites".

> **Por que `sysctl` e não `setcap`?** Capacidades são atributos do arquivo, e
> toda atualização do binário as apaga em silêncio — o sintoma é o proxy voltar
> para as portas alternativas depois de um update, sem ninguém entender por
> quê. O `sysctl` é do sistema e sobrevive a qualquer troca de binário.

## Uso

### Criar um projeto novo

```sh
devm new minha-app                          # instalador do Laravel, interativo
devm new minha-app --php 8.4 --database mysql
devm new minha-app -- --react -n            # tudo após -- vai para o instalador
devm new minha-app --plain                  # composer create-project
```

Resolve o problema do ovo e da galinha: os demais comandos encontram o projeto
pela pasta atual, e numa pasta vazia não há projeto. O `devm new` monta um
ambiente temporário com PHP e composer, chama o instalador do Laravel — que
roda no PHP escolhido, não no do sistema — e no fim escreve o
`devmanager.yaml` e registra o projeto.

`--database` faz duas coisas: passa a escolha ao instalador e declara o
serviço correspondente no `devmanager.yaml`, para que o `devm up` seguinte
suba o banco e o crie.

### Configurar um projeto

Crie um `devmanager.yaml` na raiz e versione junto com o código:

```yaml
php: "8.4"
node: "22"

services:
  - mysql:8.4
  - redis
  - mailpit
```

Sem o arquivo, o Dev Manager lê o `require.php` do `composer.json` e usa a
maior versão compatível instalada. O `devmanager.yaml` **vence** a detecção
automática: é como você fixa a versão que roda em produção.

```sh
devm php use 8.4     # grava a versão no devmanager.yaml
devm php use --clear # volta a seguir o composer.json
```

### Preparar e rodar

```sh
devm up          # dependências, .env, chave, serviços, banco do projeto
devm start -d    # sobe servidor e frontend em segundo plano
devm ps          # o que está rodando
devm logs suma -f
devm stop suma
```

`devm up` é idempotente: rodar de novo não reescreve nada que já esteja certo.

Na primeira vez que ele **altera** o `.env`, o original é guardado em
`.env.antes-do-devmanager`. A cópia é feita uma vez só: nas execuções
seguintes ela é preservada, porque o que interessa é o arquivo original — uma
cópia refeita a cada gravação valeria "o estado antes da última edição", e
depois do segundo `devm up` o que você escreveu à mão estaria perdido.

A edição em si é por linha e preserva comentários, ordem e as chaves que o
Dev Manager não conhece. O `.env` novo fica com permissão `0600`, e a cópia
também — mesmas credenciais, mesma restrição. Como o arquivo não está no
`.gitignore` padrão do Laravel, o `devm up` avisa e sugere a linha; ele não
edita o seu `.gitignore`.

### Executar comandos

```sh
devm artisan migrate
devm composer require pacote/nome
devm run npm run build
```

Sempre com o PHP do projeto, mesmo de dentro de uma subpasta. O `composer` é
o phar oficial, baixado e verificado por checksum — não o do sistema, que em
várias distribuições carrega bibliotecas próprias e exige extensões que um PHP
estático enxuto não tem.

### Serviços

```sh
devm service catalog              # o que dá para subir
devm service list                 # o que está rodando
devm service start postgres:17    # sobe avulso
devm service remove redis --data  # remove; --data apaga o volume
devm service engine docker        # fixa o runtime de contêiner
```

Os serviços são compartilhados por versão: `devm-mysql-8-4` atende todos os
projetos que declaram `mysql:8.4`. O isolamento vem do banco — o projeto
`appmake-erp` recebe o banco `appmake_erp`, criado automaticamente, e as
credenciais vão para o `.env`.

Se a porta oficial estiver ocupada, outra livre é escolhida e **anunciada**.

### Runtimes

```sh
devm php list       # instalados
devm php available  # instaláveis
devm php install 8.3
devm php remove 8.3.32
devm php which "^8.2"
```

Os binários vêm do [static-php-cli](https://github.com/crazywhalecc/static-php-cli)
e ficam em `~/.local/share/devmanager/runtimes/`. Nenhum toca no PHP do sistema.

O build padrão é o `bulk`: drivers de MySQL, PostgreSQL e SQLite, mais `intl`,
`readline`, `opcache`, `sodium` e `imagick`. Totalmente estático, 31 MB.
`DEVMANAGER_PHP_VARIANT` troca o conjunto (`common` é menor e sem `intl`;
`gnu-bulk` tem o mesmo do `bulk` mas ligado à glibc).

> **Cuidado ao inspecionar extensões:** `php -m` **não** lista os drivers
> compilados dentro da extensão PDO. No `bulk` eles entram pelos
> `swoole-hook-pgsql` e `swoole-hook-sqlite`, e a saída daquele comando sugere
> falsamente que não existem. `PDO::getAvailableDrivers()` é a fonte correta —
> esse engano custou uma escolha errada de variante padrão neste projeto.

O `devm php list` mostra quanto cada instalação ocupa, e o `devm php remove`
apaga as que não servem mais. Só as baixadas pelo Dev Manager: o PHP da distro
aparece na listagem mas não é candidato a remoção — quem o instalou foi o
gerenciador de pacotes, e é lá que ele deve sair.

A remoção é **recusada** quando algum projeto registrado ficaria sem nenhuma
versão que atenda sua exigência:

```
$ devm php remove 8.4
php 8.4.23 é a única versão que atende estes projetos:
  appmake-erp              exige 8.4 (de devmanager.yaml)
  fapcen                   exige 8.4 (de devmanager.yaml)
  instale outra antes, ou use --force para apagar assim mesmo
```

### Node

```sh
devm node list       # instalados, incluindo os do seu nvm
devm node available  # instaláveis
devm node install 22
devm node remove 22.23.2
devm node use 22     # fixa no devmanager.yaml
devm node use --clear
```

Encontra o que você já tem — **nvm**, fnm, volta e o node do PATH — antes de
oferecer download. Quem já usa nvm não precisa baixar nada.

Sem versão declarada, o Dev Manager **não** gerencia o Node: o `npm` do
sistema continua valendo. Isso é deliberado — impor uma versão a quem não
pediu criaria um shim sequestrando o `npm` sem motivo. A detecção lê, nesta
ordem: `devmanager.yaml`, `.nvmrc`, `engines.node` do `package.json`.

Quando gerenciado, o shim expõe `node`, `npm` e `npx` juntos: um `npm run dev`
que caísse no npm do sistema rodaria com a versão errada de Node por baixo.

`devm node remove` só apaga o que o Dev Manager baixou. Uma versão do nvm
aparece na listagem, mas quem a instalou foi o nvm — remova por lá.

### Assistentes de IA

```sh
devm agents              # mostra o que seria escrito
devm agents apply        # grava DEVMANAGER.md
devm agents apply --link # e acrescenta a referência aos seus arquivos
```

Gera um `DEVMANAGER.md` com as instruções específicas deste projeto — versão
de PHP, serviços, portas reais, comandos — para que assistentes usem `devm
artisan` em vez de `sail artisan`.

É um arquivo **próprio**: nada escrito à mão corre risco, e remover é `rm`. Em
troca, os assistentes não o leem sozinhos, e uma linha de referência precisa
existir num arquivo que eles já leiam (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`,
`copilot-instructions.md`). O comando mostra a linha; `--link` a acrescenta.

> Num projeto migrado do Sail, vale revisar o que os seus arquivos já dizem.
> Uma instrução antiga do tipo "rode tudo com `vendor/bin/sail`" passa a
> contradizer o Dev Manager, e o assistente vai seguir a que ler primeiro.

### Editores

```sh
devm ide         # mostra o que seria configurado
devm ide apply   # grava
```

Aponta o VS Code (`.vscode/settings.json`) e o PhpStorm (`.idea/php.xml`) para
o PHP do projeto. A edição é cirúrgica: comentários, ordem e demais chaves são
preservados.

### Daemon

Os projetos rodam sob um daemon, então continuam de pé com o terminal fechado.

```sh
devm daemon status
devm daemon start
devm daemon stop       # derruba TODOS os ambientes junto
devm daemon logs
devm daemon install    # grava o unit do systemd para subir no login
```

## Como funciona

```
                        devm (CLI)
                            │  socket unix, API JSON
                     ┌──────┴──────┐
                     │   daemon    │
                     └──────┬──────┘
            ┌───────────────┼───────────────┐
        supervisor        proxy            dns
       (processos)     (*.test, TLS)    (:5354)
            │               │
      PHP estático     Caddy? não:
      + node nativo    httputil.ReverseProxy
            │
        ┌───┴────────────────┐
        │  serviços em       │
        │  contêiner         │
        │  (docker/podman)   │
        └────────────────────┘
```

O proxy roteia por nome de host para a porta que o daemon alocou, e emite
certificados no handshake a partir de uma CA local — projeto novo tem HTTPS no
primeiro acesso, sem passo de configuração.

### Dependências

Quatro, no total: `gopkg.in/yaml.v3`, `github.com/miekg/dns` e as duas
indiretas dele (`golang.org/x/net` e `x/sys`). Binário de 14 MB.

Caddy foi avaliado e recusado: 142 dependências e 64 MB de binário para o que
`httputil.ReverseProxy` e `crypto/x509` fazem em ~300 linhas. Já o servidor
DNS **não** existe na stdlib, e escrever um à mão significa acertar compressão
de nomes e EDNS — daí a dependência.

As engines de contêiner são acessadas pelo **binário de CLI**, não pelas
bibliotecas Go de podman e docker: as CLIs aceitam os mesmos argumentos, então
uma implementação atende as duas.

## Desenvolvimento

```sh
make test    # suíte
make race    # com detector de corrida (precisa de gcc)
make cover   # cobertura por pacote
make vet fmt
make cross   # confirma linux e macOS, amd64 e arm64
```

O código é comentado em português, explicando **por que** cada decisão foi
tomada — não o que a linha faz.

O CI (`.github/workflows/ci.yml`) roda exatamente esses alvos do Makefile, e
mais o `make static`, publicando o binário como artefato. Rodar comandos
próprios no CI criaria uma segunda definição de "está certo", e as duas
divergem. Os testes só rodam em Linux por enquanto; a compilação para macOS
é verificada pelo `make cross`, mas a suíte não.

## Limitações conhecidas

- **Linux apenas, por enquanto.** O código evita dependências específicas de
  plataforma e compila para macOS, mas a configuração automática de DNS assume
  `systemd-resolved`.
- **Porta 80 disputada.** Se outro servidor já a ocupa, o proxy cai para 8080
  e avisa. HTTP e HTTPS caem de forma independente.
- **Reiniciar o daemon derruba todos os ambientes.** Eles não voltam sozinhos;
  `devm start -d <projeto>` religa.
- **Sem Xdebug — não há depuração por breakpoint.** O PHP do
  [static-php-cli](https://github.com/crazywhalecc/static-php-cli) é linkado
  estaticamente e **não carrega extensão compartilhada**: não é o caso de
  "falta instalar o Xdebug", é que um `.so` não seria carregado de forma
  alguma. Para o dia a dia sobram `dd()`, `dump()`, `Log` e o
  `php artisan pail`; para uma sessão de breakpoint, um ambiente com PHP
  dinâmico (Sail, ou o PHP da distro) continua necessário.
- **PHP 7.x não está disponível.** O static-php-cli publica a partir do 8.0
  (`devm php available` mostra 8.0 a 8.5). Projetos legados em 7.x ficam fora
  do alcance do Dev Manager.

## Licença

[MIT](LICENSE).
