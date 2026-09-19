#!/bin/sh
# Verificação de ponta a ponta do Dev Manager no macOS.
#
# Roda no job de macOS do CI, num runner com sudo sem senha: configura o DNS
# e a CA DE VERDADE, pelo `devm setup --apply`, e confere o resultado pelos
# mesmos caminhos que um navegador usaria. É o que a suíte de testes não
# alcança — nenhum teste de unidade prova que o /etc/resolver funciona, que o
# PHP estático de macOS tem os drivers certos ou que o plist é aceito.
#
# Numa máquina de verdade, leia antes de rodar: ele altera /etc/resolver e o
# Keychain do sistema, exatamente como o `devm setup --apply` faria.
set -eu

DEVM=${DEVM:-./bin/devm}

falhar() { echo "::error::$*"; exit 1; }
grupo() { echo "::group::$*"; }
fim() { echo "::endgroup::"; }

"$DEVM" version

# Antes do daemon, para que o proxy não ocupe a porta e confunda a sonda.
grupo "portas abaixo de 1024 para um usuário comum"
python3 - <<'PY'
import socket
for host in ("0.0.0.0", "127.0.0.1"):
    s = socket.socket()
    try:
        s.bind((host, 80))
        print(f"{host}:80  permitido")
    except OSError as e:
        print(f"{host}:80  {e.strerror}")
    finally:
        s.close()
PY
fim

grupo "daemon"
"$DEVM" daemon start
trap '"$DEVM" daemon stop >/dev/null 2>&1 || true' EXIT
"$DEVM" proxy status
PORTA=$("$DEVM" proxy status --json | sed -n 's/.*"http_port": *\([0-9]*\).*/\1/p')
[ -n "$PORTA" ] || falhar "não consegui ler a porta HTTP do proxy"
fim

grupo "setup --apply"
"$DEVM" setup
"$DEVM" setup --apply
fim

grupo "DNS"
"$DEVM" dns status
"$DEVM" dns status --json | grep -q '"resolving": true' ||
	falhar "o .test não resolve depois do setup --apply"
dscacheutil -q host -a name qualquer-coisa.test
dscacheutil -q host -a name qualquer-coisa.test | grep -q 'ip_address: 127.0.0.1' ||
	falhar "o sistema não resolve .test para o loopback"
fim

grupo "proxy pelo nome, como um navegador"
# Nenhum projeto rodando: o proxy responde 404 para domínio desconhecido.
# Receber essa resposta prova a cadeia inteira — resolvedor do sistema,
# nosso DNS, 127.0.0.1, proxy.
CODIGO=$(curl -s -o /dev/null -w '%{http_code}' "http://qualquer-coisa.test:$PORTA/")
echo "HTTP $CODIGO"
[ "$CODIGO" = "404" ] || falhar "esperava 404 do proxy, veio $CODIGO"
fim

grupo "proxy não atende pela rede"
# Se o kernel negou a porta 80 no 127.0.0.1, o proxy escuta em 0.0.0.0 e
# fecha no Accept o que vem de fora. O proxy status diz qual caso ocorreu;
# em qualquer um, uma requisição pelo IP de rede não pode receber resposta.
IP=$(ipconfig getifaddr en0 || true)
if [ -n "$IP" ]; then
	if curl -s -m 3 -o /dev/null -H "Host: qualquer-coisa.test" "http://$IP:$PORTA/"; then
		falhar "o proxy respondeu pelo IP de rede $IP — está exposto"
	fi
	echo "sem resposta pelo IP de rede $IP, como devia"
else
	echo "sem IP em en0; verificação pulada"
fi
fim

grupo "CA confiável"
"$DEVM" proxy ca
"$DEVM" setup --json | python3 -c '
import json, sys
passo = next(p for p in json.load(sys.stdin)["steps"] if "certificado" in p["name"])
print(passo)
sys.exit(0 if passo["done"] else 1)
' || falhar "o sistema não confia na CA depois do setup --apply"
fim

grupo "LaunchAgent"
"$DEVM" daemon install
plutil -lint "$HOME/Library/LaunchAgents/com.github.alexrogaleski.devmanager.plist"
fim

grupo "PHP estático de macOS"
"$DEVM" php install 8.4
PHP=$("$DEVM" php which 8.4)
"$PHP" -v
# PDO::getAvailableDrivers, e não `php -m`: o -m não lista os drivers
# compilados dentro da extensão PDO.
DRIVERS=$("$PHP" -r 'echo implode(",", PDO::getAvailableDrivers());')
echo "drivers PDO: $DRIVERS"
for d in mysql pgsql sqlite; do
	echo "$DRIVERS" | grep -qw "$d" || falhar "o PHP de macOS não tem o driver pdo_$d"
done
"$PHP" -r 'exit(extension_loaded("intl") ? 0 : 1);' || falhar "o PHP de macOS não tem intl"
fim

grupo "Node oficial de macOS"
"$DEVM" node install 22
"$DEVM" node which 22
"$("$DEVM" node which 22)" --version
fim

echo "verificação de macOS concluída"
