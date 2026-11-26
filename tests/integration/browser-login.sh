#!/bin/bash
# Ephemeral OS users and trust store inside the network-isolated browser image.
set -euo pipefail
[[ -f /.dockerenv && -f /fixture/nginx-dxberry.conf && ! -e /dev/snd ]]
[[ $(ls /sys/class/net) == lo && ! -e /etc/dxberry/tls/station.key ]]
umask 077
tls=/etc/dxberry/tls
mkdir -p "$tls" /var/lib/dxberry/station
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=Fixture-CA \
  -addext basicConstraints=critical,CA:TRUE -keyout /tmp/test-ca.key -out /tmp/test-ca.crt 2>/dev/null
openssl req -new -newkey rsa:2048 -nodes -subj /CN=dxberry.local \
  -keyout "$tls/station.key" -out /tmp/station.csr 2>/dev/null
printf '%s\n' 'subjectAltName=DNS:dxberry.local' 'extendedKeyUsage=serverAuth' > /tmp/station.ext
openssl x509 -req -in /tmp/station.csr -CA /tmp/test-ca.crt -CAkey /tmp/test-ca.key -CAcreateserial \
  -days 1 -extfile /tmp/station.ext -out "$tls/station.crt" 2>/dev/null
chmod 644 /tmp/test-ca.crt "$tls/station.crt"
runuser -u fixturebrowser -- mkdir -p /home/fixturebrowser/.pki/nssdb
db=sql:/home/fixturebrowser/.pki/nssdb
runuser -u fixturebrowser -- certutil -N -d "$db" --empty-password
runuser -u fixturebrowser -- certutil -A -d "$db" -n fixture-ca -t 'C,,' -i /tmp/test-ca.crt
sed -e "s|@CERT@|$tls/station.crt|g" -e "s|@KEY@|$tls/station.key|g" \
  /fixture/nginx-dxberry.conf > /etc/nginx/sites-available/default
/opt/dxberry/bin/dxberryd > /tmp/station.log 2>&1 &
station=$!
trap 'kill "$station" 2>/dev/null || true' EXIT
nginx -t
nginx
ready=0
for ((i=0; i<50; i++)); do
  if curl --noproxy '*' --cacert /tmp/test-ca.crt --max-time 1 -fsS https://dxberry.local/api/version >/dev/null 2>&1; then ready=1; break; fi
  kill -0 "$station"
  sleep .2
done
(( ready ))
password=$(openssl rand -hex 24)
printf 'fixtureadmin:%s\n' "$password" | chpasswd
printf '%s' "$password" | runuser -u fixturebrowser -- node tests/integration/browser-login.js
