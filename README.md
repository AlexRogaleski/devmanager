# Dev Manager

[![CI](https://github.com/AlexRogaleski/devmanager/actions/workflows/ci.yml/badge.svg)](https://github.com/AlexRogaleski/devmanager/actions/workflows/ci.yml)
[![Licença MIT](https://img.shields.io/badge/licença-MIT-blue.svg)](LICENSE)

Ambiente de desenvolvimento local para projetos Laravel/PHP, sem um contêiner
por projeto.

```
$ devm start
minha-app rodando em segundo plano
  PHP        8.4.23
  serve      php artisan serve --host=127.0.0.1 --port=41859
  vite       npm run dev

  https://minha-app.test
```

O PHP roda nativo, como binário estático isolado do sistema. Só a
infraestrutura — banco, Redis, Mailpit — vai para contêiner, e é compartilhada
entre os projetos: cada um recebe seu próprio banco dentro do mesmo servidor.

Com dois projetos Laravel rodando ao mesmo tempo:

| | contêineres | memória em contêiner |
|---|---|---|
| Laravel Sail | 7 | ~5 GB |
| Dev Manager | 3 | 38 MB |

As portas são alocadas automaticamente, então vários projetos sobem juntos sem
ajuste de configuração.

## Requisitos

- Linux com `systemd-resolved`, ou macOS
- Docker **ou** Podman (para os serviços) — no macOS, Docker Desktop ou Podman
  machine

## Instalação

O binário é estático e não depende de glibc, Go nem nada instalado no sistema.
Baixe o da última versão.

No Linux:

```sh
curl -LO https://github.com/AlexRogaleski/devmanager/releases/latest/download/devm-linux-amd64
curl -LO https://github.com/AlexRogaleski/devmanager/releases/latest/download/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
install -m 755 devm-linux-amd64 ~/.local/bin/devm
```

No macOS Apple Silicon:

```sh
curl -LO https://github.com/AlexRogaleski/devmanager/releases/latest/download/devm-darwin-arm64
curl -LO https://github.com/AlexRogaleski/devmanager/releases/latest/download/SHA256SUMS
grep devm-darwin-arm64 SHA256SUMS | shasum -a 256 -c
mkdir -p ~/.local/bin && install -m 755 devm-darwin-arm64 ~/.local/bin/devm
```

O macOS não põe `~/.local/bin` no `PATH`; acrescente
`export PATH="$HOME/.local/bin:$PATH"` ao `~/.zshrc`. Baixe pelo `curl`, e não
pelo navegador: o navegador marca o arquivo como vindo da internet, e o
Gatekeeper bloqueia um binário sem assinatura da Apple — se acontecer,
`xattr -d com.apple.quarantine ~/.local/bin/devm` libera.

Há binários para Linux e macOS, amd64 e arm64. O de Linux x86-64 e o de macOS
Apple Silicon passam pela suíte a cada versão, e o de macOS também por uma
verificação de ponta a ponta num Mac. Os de Linux ARM64 e de macOS Intel são
compilados, mas não testados.

### Compilando do código

Precisa de Go 1.26+:

```sh
git clone https://github.com/AlexRogaleski/devmanager
cd devmanager
make install-host
```

É o caminho de quem vai mexer no código: o build incremental leva menos de um
segundo, e `make install-host` já coloca o resultado em `~/.local/bin/devm`.

### Atualizando

```sh
devm upgrade          # baixa e instala a última versão publicada
devm upgrade --check  # só informa se há versão nova
```

O binário é verificado contra o `SHA256SUMS` da Release antes de substituir o
que está instalado: checksum que não bate significa não instalar. Um binário
compilado do código local (`v0.5.0-3-gabc1234`) não é trocado sem `--force` —
ele costuma ser mais novo que a versão publicada.

### Configuração da máquina

Três ajustes de sistema, uma vez por máquina:

```sh
devm setup           # mostra o que falta e os comandos
devm setup --apply   # executa, pedindo a senha do sudo
```

| ajuste | Linux | macOS |
|---|---|---|
| resolver `.test` para o loopback | drop-in do `systemd-resolved` | arquivo em `/etc/resolver` |
| abrir as portas 80 e 443 sem root | `net.ipv4.ip_unprivileged_port_start=80` | não precisa |
| HTTPS sem aviso de inseguro | CA nas âncoras do sistema | CA no Keychain |

O Firefox mantém um armazenamento de certificados próprio e ignora o do
sistema. Para ele: **Configurações → Privacidade → Certificados → Ver
certificados → Autoridades → Importar**, marcando "confiar para identificar
sites".

## Uso

### Criar um projeto

```sh
devm new minha-app                            # instalador do Laravel, interativo
devm new minha-app --php 8.4 --database mysql
devm new minha-app -- --react -n              # tudo após -- vai para o instalador
devm new minha-app --plain                    # composer create-project
```

Monta um ambiente temporário com PHP e composer, chama o instalador do Laravel
— que roda no PHP escolhido, não no do sistema — e no fim escreve o
`devmanager.yaml` e registra o projeto. O `--database` passa a escolha ao
instalador e declara o serviço correspondente, para que o `devm up` seguinte
suba o banco e o crie.

### Configurar um projeto existente

```sh
devm init          # cria o devmanager.yaml a partir do que o projeto já usa
devm init --print  # só mostra o resultado, para quem já tem o arquivo
```

O `devm init` lê o que o projeto já tem e declara o que encontrar:

- `compose.yaml` / `docker-compose.yml` — as imagens viram serviços, com a
  versão da tag; se o projeto vinha do Laravel Sail, a versão do PHP sai do
  runtime que o Sail constrói (`vendor/laravel/sail/runtimes/8.4`);
- `.env` (ou `.env.example`) — `DB_CONNECTION` diz o banco, `QUEUE_CONNECTION`
  e afins denunciam o Redis, `MAIL_PORT=1025` denuncia o Mailpit;
- `composer.json` — a versão de PHP, quando não há pista melhor.

Cada linha do resumo diz de onde veio, para conferir antes de versionar:

```
devmanager.yaml criado

  php 8.4          compose.yaml (runtime do Sail)
  mailpit          compose.yaml
  mysql:8.0        compose.yaml
                   ↳ a máquina já tem mysql:8.4; a mesma versão evita um segundo contêiner
  redis:8          .env (QUEUE_CONNECTION=redis)
```

Um arquivo que já existe nunca é sobrescrito: o comando lista o que o projeto
usa e não declara, e mostra o `devm service add` que resolve.

O arquivo gerado traz cada opção explicada e exemplificada em comentário.
Versione junto com o código:

```yaml
php: "8.4"
node: "22"

services:
  - mysql:8.4
  - redis
  - mailpit
```

Sem o arquivo, o Dev Manager lê o `require.php` do `composer.json` e usa a
maior versão compatível instalada. O `devmanager.yaml` vence a detecção
automática — é como se fixa a versão que roda em produção.

#### Processos

Sem declarar nada, `devm start` sobe `php artisan serve` mais o script de
desenvolvimento do `package.json`. Para mudar isso — acrescentar um worker de
fila, um servidor de SSR — declare `processes`, que substitui o padrão inteiro:

```yaml
processes:
  serve: php artisan serve --host=127.0.0.1 --port={{port}}
  queue: php artisan queue:listen --tries=1
  vite: npm run dev -- --port {{port:vite}}
```

`{{port}}` recebe a porta que o ambiente ganhou, e é nela que o proxy publica
`<projeto>.test`. Fixar um número no lugar do marcador funciona, mas passa a
ser sua responsabilidade não colidir com outro projeto.

`{{port:nome}}` pede uma porta extra para um segundo processo que também
escute. Ela é sorteada uma vez e vale para o ambiente todo, então dois
processos podem se referir a ela pelo mesmo nome.

Os processos recebem as portas no ambiente — `DEVM_PORT` para a principal e
`DEVM_PORT_VITE` para `{{port:vite}}` —, que é como o `vite.config.js`
descobre onde o backend subiu sem ninguém escrever número nenhum.

Um processo que termina **com sucesso** é reiniciado; um que termina **com
erro** derruba o ambiente. É essa diferença que faz um worker de fila
funcionar aqui: `queue:work --max-time=3600` sai de propósito para reciclar a
memória, e `queue:restart` faz o mesmo depois de um deploy — nos dois casos o
processo cumpriu o ciclo. Já um `serve` que morre com a porta ocupada é falha
de verdade, e manter o bundler rodando sozinho só fingiria que o ambiente
está de pé. Três saídas limpas em dez segundos param o ciclo, para que um
`--stop-when-empty` com a fila vazia não vire laço apertado.

```sh
devm add .           # registra o projeto
devm scan ~/Projetos # registra todos os projetos de um diretório
devm list            # projetos registrados e o estado de cada um
```

### Preparar e rodar

```sh
devm up          # dependências, .env, chave, serviços, banco do projeto
devm start       # sobe servidor e frontend em segundo plano (--attach fica no terminal)
devm ps          # o que está rodando
devm restart     # derruba e sobe de novo, com as mesmas opções
devm open        # abre o projeto no navegador
devm logs minha-app -f
devm stop minha-app
```

Os ambientes voltam sozinhos quando o daemon reinicia — por atualização, por
reboot. O que o daemon guarda é a INTENÇÃO (quais projetos você pediu para
subir, com quais opções), não o estado: um daemon que caiu não tem processo
nenhum de pé, e um arquivo dizendo o contrário seria mentira. Parar um
ambiente com `devm stop` o tira dessa lista — derrubar de propósito é
definitivo.

`devm up` é idempotente: rodar de novo não reescreve nada que já esteja certo.
Ele instala dependências, cria o `.env` e a chave, sobe os serviços declarados,
cria o banco da aplicação — e também o banco que o `phpunit.xml` pedir, sem o
qual `artisan test` falha na conexão — e o link de `storage`.

A edição do `.env` é feita por linha, preservando comentários, ordem e as
chaves desconhecidas. Na primeira alteração, o original é guardado em
`.env.antes-do-devmanager` — arquivo com credenciais, que o `devm up` sugere
acrescentar ao `.gitignore`.

### Executar comandos

```sh
devm artisan migrate
devm composer require pacote/nome
devm run npm run build
```

Sempre com o PHP do projeto, mesmo de dentro de uma subpasta. O `composer`
usado é o phar oficial, baixado e verificado por checksum.

Para uma sessão inteira de trabalho, `devm shell` abre um shell com o
ambiente do projeto — e aí `php`, `composer`, `node` e `npm` funcionam sem
prefixo nenhum:

```sh
devm shell
```

Ele abre o shell que você está **usando**, e não o de login: numa máquina
cujo login é bash mas cujo terminal abre zsh, o `$SHELL` diria bash o tempo
todo. Use `--shell` para escolher outro.

O histórico é por projeto: a seta para cima traz o que você digitou ali, e
não o comando de outro projeto com um caminho que aqui não existe. Ele fica
em `~/.local/share/devmanager/historicos/`, fora da pasta do projeto —
histórico de terminal é do desenvolvedor, não do repositório. Sair com `exit`
devolve o terminal como estava, e nada é acrescentado ao seu `.bashrc` ou
`.zshrc`.

### Banco de dados

```sh
devm db shell                 # psql, mysql ou sqlite3, já conectado
devm db dump                  # <projeto>-2026-09-22-1530.sql
devm db dump copia.sql        # ou o nome que você quiser
devm db restore copia.sql     # recria o banco e aplica o arquivo
devm db restore copia.sql --keep   # aplica por cima, sem recriar
```

O serviço e o banco saem do projeto: o `devmanager.yaml` diz qual serviço, e
o `DB_DATABASE` do `.env` diz qual banco — nada de decorar nome de contêiner,
cliente do dialeto e senha. Funciona com PostgreSQL, MySQL, MariaDB e SQLite.

Em SQLite o arquivo é o backup, e a cópia é feita com `VACUUM INTO`, não com
um `cp`: quando o banco está em modo WAL, as escritas recentes vivem no
arquivo `-wal` ao lado, e copiar só o `.sqlite` produziria um backup sem elas.

O dump é sempre do mesmo dialeto — um arquivo do SQLite não se aplica num
MySQL. Para trocar de banco, o caminho é rodar as migrações no destino e
transferir os dados pela aplicação.

O `restore` **recria o banco** antes de aplicar, e por isso pergunta antes.
Aplicar um dump sobre um banco que já tem as tabelas produz uma cascata de
"already exists" na qual um erro de verdade passa despercebido; começar do
zero torna o resultado previsível. O `--keep` serve para dumps parciais, que
devem mesmo entrar por cima.

### Serviços

No projeto — mexe no `devmanager.yaml`, versionado junto com o código:

```sh
devm service add postgres:17 redis  # declara; use `devm up` para subir
devm service drop redis             # o projeto deixa de exigir
```

Na máquina — mexe nos contêineres:

```sh
devm service catalog              # o que dá para subir
devm service list                 # o que está rodando
devm service start postgres:17    # sobe avulso
devm service stop --unused        # para os que nenhum projeto ativo usa
devm service remove redis --data  # remove; --data apaga o volume
devm service engine docker        # fixa o runtime de contêiner
```

Disponíveis: PostgreSQL, MySQL, MariaDB, Redis e Mailpit.

Os serviços são compartilhados por versão: um contêiner `devm-mysql-8-4`
atende todos os projetos que declaram `mysql:8.4`. O isolamento vem do banco —
um projeto chamado `minha-app` recebe o banco `minha_app`, criado
automaticamente, e as credenciais vão para o `.env`.

Se a porta oficial estiver ocupada, outra livre é escolhida e anunciada.

Serviço ligado significa "algum projeto ativo precisa dele": ao derrubar um
ambiente, os serviços que o Dev Manager subiu para ele e que nenhum outro
ambiente usa são parados junto, e os dados ficam no volume. Um serviço que
você subiu à mão com `devm service start` é seu e não é desligado.

Quem usa o banco fora do Dev Manager o dia inteiro pode desligar esse
comportamento no `~/.config/devmanager/config.yaml`:

```yaml
keep_services: true
```

### Versões de PHP

```sh
devm php list       # instalados, com o espaço que ocupam
devm php available  # instaláveis
devm php install 8.3
devm php remove 8.3.32
devm php which "^8.2"
```

Os binários vêm do [static-php-cli](https://github.com/crazywhalecc/static-php-cli)
e ficam em `~/.local/share/devmanager/runtimes/`. Nenhum toca no PHP do
sistema.

O build padrão é o `bulk`: 31 MB, com drivers de MySQL, PostgreSQL e SQLite,
mais `intl`, `readline`, `opcache`, `sodium` e `imagick`.
`DEVMANAGER_PHP_VARIANT` troca o conjunto.

### Configuração do PHP

O PHP estático não carrega `php.ini` nenhum, e os padrões compilados são
apertados demais para desenvolver: 128M de memória e 2M de upload. O PHPStan
morre no meio da análise, e o primeiro teste de upload falha por um motivo
que não parece ter relação.

O Dev Manager gera um `php.ini` junto ao shim de cada projeto, com o que a
imagem do Laravel Sail já entregava:

| | Sail | PHP estático sem ini | Dev Manager |
|---|---|---|---|
| `memory_limit` | `-1` | `128M` | `-1` |
| `upload_max_filesize` | `100M` | `2M` | `100M` |
| `post_max_size` | `100M` | `8M` | `100M` |

Para mudar num projeto, declare no `devmanager.yaml`:

```yaml
php_ini:
  memory_limit: 512M
  max_execution_time: 120
```

Vale para tudo: o terminal, o editor e qualquer processo filho. O `php` do
shim é um script que aponta o `PHPRC` antes de executar o interpretador, e um
`PHPRC` já definido é respeitado.

O `devm php remove` apaga apenas as versões baixadas pelo Dev Manager, e
recusa quando algum projeto registrado ficaria sem nenhuma versão que atenda
sua exigência:

```
$ devm php remove 8.4
php 8.4.23 é a única versão que atende estes projetos:
  loja                     exige 8.4 (de devmanager.yaml)
  blog                     exige ^8.4 (de composer.json)
  instale outra antes, ou use --force para apagar assim mesmo
```

### Versões de Node

```sh
devm node list       # instalados, inclusive os do nvm
devm node available  # instaláveis
devm node install 22
devm node remove 22.23.2
devm node use 22     # fixa no devmanager.yaml
devm node use --clear
```

Versões já presentes na máquina — nvm, fnm, volta e o node do PATH — são
encontradas antes de oferecer download, e `devm node remove` não mexe nelas.

Sem versão declarada, o Node não é gerenciado e o `npm` do sistema continua
valendo. A detecção lê, nesta ordem: `devmanager.yaml`, `.nvmrc`,
`engines.node` do `package.json`.

### Assistentes de IA

```sh
devm agents              # mostra o que seria escrito
devm agents apply        # grava DEVMANAGER.md
devm agents apply --link # e acrescenta a referência aos arquivos existentes
```

Gera um `DEVMANAGER.md` com as instruções específicas do projeto — versão de
PHP, serviços, portas reais, comandos — para que assistentes usem
`devm artisan` em vez de `sail artisan`.

É um arquivo próprio, então nada escrito à mão corre risco. Em troca, os
assistentes não o leem sozinhos: uma linha de referência precisa existir num
arquivo que eles já leiam (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`,
`copilot-instructions.md`). O comando mostra a linha; `--link` a acrescenta.

Em projetos migrados do Sail, convém revisar instruções antigas do tipo "rode
tudo com `vendor/bin/sail`", que passam a contradizer o Dev Manager.

### Editores

```sh
devm ide         # mostra o que seria configurado
devm ide apply   # grava
```

Aponta o VS Code (`.vscode/settings.json`) e o PhpStorm (`.idea/php.xml`) para
o PHP do projeto, preservando comentários, ordem e as demais chaves.

### Daemon

Os projetos rodam sob um daemon, então continuam de pé com o terminal fechado.

```sh
devm daemon status
devm daemon start
devm daemon stop       # derruba TODOS os ambientes junto
devm daemon logs
devm daemon install    # sobe no login: systemd no Linux, launchd no macOS
```

## Como funciona

A CLI conversa com o daemon por um socket Unix, com uma API JSON. O daemon
mantém três partes: um supervisor, que cuida dos processos de cada projeto; um
proxy, que roteia os domínios `.test`; e um servidor DNS local, que faz esses
domínios resolverem.

Cada projeto recebe uma porta alta livre. O proxy escuta em 80 e 443, roteia
por nome de host para a porta correspondente e emite o certificado durante o
handshake TLS, a partir de uma CA local — um projeto novo tem HTTPS no
primeiro acesso, sem passo de configuração.

Nada fica exposto na rede. No Linux, o proxy escuta só em `127.0.0.1`. O
macOS só libera a porta 80 sem root em `0.0.0.0`; lá o proxy escuta em todas
as interfaces e fecha, no momento da conexão, tudo o que não vem do
loopback. O servidor DNS responde apenas `127.0.0.1`, sem IPv6.

Os contêineres são acessados pelo binário de CLI do Docker ou do Podman, não
pelas bibliotecas Go de cada um: as duas CLIs aceitam os mesmos argumentos,
então uma implementação atende as duas.

São quatro dependências no total, e o binário tem 14 MB. O proxy e a CA usam
`httputil.ReverseProxy` e `crypto/x509` da biblioteca padrão; só o servidor
DNS, ausente da stdlib, justificou uma dependência externa.

## Desenvolvimento

```sh
make test    # suíte
make race    # com detector de corrida (precisa de gcc)
make cover   # cobertura por pacote
make vet fmt
make cross    # confirma linux e macOS, amd64 e arm64
make release # os quatro binários de distribuição em ./dist
```

O código é comentado em português, explicando por que cada decisão foi tomada
— não o que a linha faz. O CI roda esses mesmos alvos no Linux e no macOS; no
macOS, também uma verificação de ponta a ponta
(`.github/scripts/verificar-macos.sh`) que configura DNS e CA de verdade num
runner. Uma tag `v*` dispara o `make release` e cria a Release no GitHub — só
depois de os dois sistemas passarem.

## Limitações conhecidas

- **No Linux, só com `systemd-resolved`.** É ele que a configuração
  automática de DNS usa; sem ele, o `.test` precisa ser configurado à mão.
- **Sem Xdebug — não há depuração por breakpoint.** O PHP do static-php-cli é
  linkado estaticamente e não carrega extensão compartilhada, então não é o
  caso de "falta instalar o Xdebug": um `.so` não seria carregado de forma
  alguma. Restam `dd()`, `dump()`, `Log` e `php artisan pail`; para uma sessão
  de breakpoint é preciso um ambiente com PHP dinâmico.
- **PHP 7.x não está disponível.** O static-php-cli publica a partir do 8.0.
  Projetos legados em 7.x ficam fora do alcance.
- **Porta 80 disputada.** Se outro servidor já a ocupa, o proxy cai para 8080
  e avisa. HTTP e HTTPS caem de forma independente.
  `devm start <projeto>` religa.
- **No macOS, processos podem ficar órfãos se o daemon morrer de repente.** O
  Linux mata os processos dos projetos junto com o daemon; o macOS não tem
  equivalente. Parar pelo `devm daemon stop` ou pelo Ctrl+C encerra tudo
  normalmente — só um `kill -9` ou uma queda do daemon deixa um `artisan
  serve` para trás.
- **No macOS com o firewall ligado, ele pergunta se o `devm` pode aceitar
  conexões.** É o efeito de escutar em `0.0.0.0`. Com o firewall desligado,
  que é o padrão, nada aparece.
- **Os serviços em contêiner não são testados no macOS pelo CI**, porque os
  runners de macOS do GitHub não têm Docker. Todo o resto roda lá a cada
  versão.

## Licença

[MIT](LICENSE).
