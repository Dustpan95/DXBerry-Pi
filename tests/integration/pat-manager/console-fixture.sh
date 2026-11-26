#!/bin/bash
# Used only inside the isolated guest by TestPatProductionSession.
set -euo pipefail
umask 077
private=/run/pat-console-test
tls=/etc/dxberry/tls
origin=https://dxberry.local
route=$origin/api/radio/fixture2/pat
curlargs=(--silent --show-error --noproxy '*' --max-time 6
  --resolve dxberry.local:443:127.0.0.1 --cacert "$tls/station.crt"
  -H 'Sec-Fetch-Site: same-origin')

request() {
  local expected=$1 actual
  shift
  actual=$(curl "${curlargs[@]}" -o "$private/response" -w '%{http_code}' "$@")
  if [[ $actual != "$expected" ]]; then
    echo "Pat console fixture expected HTTP $expected, received $actual" >&2
    return 1
  fi
}
unavailable() {
  request 503 -b "$private/cookies" "$route"
  jq -e 'keys == ["code","ok","reason"] and .code == "unavailable" and .ok == false' "$private/response" >/dev/null
}

case "$1" in
  setup)
    mkdir -m 700 "$private"
    mkdir -p /etc/systemd/system/dxberryd.service.d
    cat > /etc/systemd/system/dxberryd.service.d/fixture.conf <<'UNIT'
[Service]
ExecStart=
ExecStart=/opt/dxberry/bin/dxberryd -pat-native-status
PrivateDevices=yes
UNIT
    sed -e "s|@CERT@|$tls/station.crt|g" -e "s|@KEY@|$tls/station.key|g" \
      /fixture/nginx-dxberry.conf > /etc/nginx/sites-available/default
    systemctl daemon-reload
    systemctl start dxberryd.service
    # dxberryd creates the certificate for this fresh guest.
    for ((i=0; i<120; i++)); do
      [[ -s $tls/station.crt && -s $tls/station.key ]] && break
      sleep .1
    done
    nginx -t
    systemctl start nginx.service
    for ((i=0; i<50; i++)); do
      if curl "${curlargs[@]}" --fail "$origin/api/version" >/dev/null 2>&1; then break; fi
      sleep .2
    done
    request 401 "$route"
    password=$(openssl rand -hex 24)
    printf 'fixtureadmin:%s\n' "$password" | chpasswd
    printf '{"user":"fixtureadmin","password":"%s"}' "$password" |
      request 200 -c "$private/cookies" -H 'Content-Type: application/json' --data-binary @- "$origin/api/auth/login"
    unavailable
    ;;
  running)
    request 200 -b "$private/cookies" "$route"
    jq -e 'keys == ["radio","status"] and .radio == "fixture2" and (.status |
      keys == ["active_listeners","connected","dialing","observed_at"] and
      .active_listeners == [] and .connected == false and .dialing == false and
      (.observed_at | type == "string" and length > 0))' "$private/response" >/dev/null
    ;;
  stopped)
    unavailable
    systemctl stop dxberryd.service nginx.service
    ;;
  *) exit 2 ;;
esac
