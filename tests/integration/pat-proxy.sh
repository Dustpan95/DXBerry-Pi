#!/usr/bin/env bash
# Exercise the real nginx templates with a session-check fixture and a disposable Pat backend.
# Run inside Dockerfile.webproxy's container, with --network none and the repository mounted read-only.
set -euo pipefail
cd "$(dirname "$0")/../.."
fixture_dir=$(mktemp -d)
backend_pid='' nginx_pid=''
cleanup() {
  [[ -z $nginx_pid ]] || kill "$nginx_pid" 2>/dev/null || true
  [[ -z $backend_pid ]] || kill "$backend_pid" 2>/dev/null || true
  rm -rf "$fixture_dir"
}
trap cleanup EXIT
mkdir -p "$fixture_dir/snippets"
cat provision/templates/nginx-dxberry-auth-{gate,endpoints}.conf > "$fixture_dir/snippets/dxberry-auth.conf"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -keyout "$fixture_dir/key" -out "$fixture_dir/cert" > /dev/null 2>&1
sed -e "s|@CERT@|$fixture_dir/cert|g" -e "s|@KEY@|$fixture_dir/key|g" \
  provision/templates/nginx-pat.conf > "$fixture_dir/pat.conf"
cat > "$fixture_dir/nginx.conf" <<EOF
pid $fixture_dir/nginx.pid;
error_log $fixture_dir/error.log;
events {}
http {
  access_log off;
  include $fixture_dir/pat.conf;
}
EOF
python3 -u - "$fixture_dir" > "$fixture_dir/backend.log" 2>&1 <<'PY' &
import http.server
import pathlib
import sys
import threading

directory = pathlib.Path(sys.argv[1])

class CheckSession(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        assert self.path == '/api/auth/check', self.path
        assert not self.headers.get('Authorization'), 'Authorization leaked to session check'
        self.send_response(200 if self.headers.get('Cookie') == 'fixture=valid' else 401)
        self.end_headers()

class Pat(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        with (directory / 'requests').open('a') as log:
            log.write(self.path + '\n')
        status = int(self.path[1:]) if self.path[1:].isdigit() else 200
        self.send_response(status)
        self.end_headers()
        self.wfile.write(b'Pat fixture')

session = http.server.HTTPServer(('127.0.0.1', 8090), CheckSession)
threading.Thread(target=session.serve_forever, daemon=True).start()
pat = http.server.HTTPServer(('127.0.0.1', 8180), Pat)
(directory / 'ready').touch()
pat.serve_forever()
PY
backend_pid=$!
for ((try=0; try<100; try++)); do
  [[ ! -e $fixture_dir/ready ]] || break
  sleep 0.05
done
test -e "$fixture_dir/ready"
nginx -p "$fixture_dir/" -c nginx.conf -t
nginx -p "$fixture_dir/" -c nginx.conf -g 'daemon off;' &
nginx_pid=$!
for ((try=0; try<100; try++)); do
  if curl -ksS --max-time 1 https://127.0.0.1:8081/ -o /dev/null 2>/dev/null; then break; fi
  sleep 0.05
done
request() {
  curl -ksS --max-time 3 -D "$fixture_dir/headers" -o "$fixture_dir/body" "$@"
}
expect() {
  if ! grep -Fq "$1" "$fixture_dir/headers"; then
    cat "$fixture_dir/headers" >&2
    echo "Expected header: $1" >&2
    exit 1
  fi
}
request https://127.0.0.1:8081/
expect 'HTTP/1.1 302'
expect 'Location: https://127.0.0.1/login?next=/'
test ! -e "$fixture_dir/requests"
echo 'ok: logged-out requests are gated before reaching Pat'

request -H 'Cookie: fixture=valid' -H 'Authorization: Basic fixture' https://127.0.0.1:8081/
expect 'HTTP/1.1 200'
grep -Fq 'Pat fixture' "$fixture_dir/body"
echo 'ok: a running Pat is served behind the cookie gate'

for status in 502 503 504; do
  request -H 'Cookie: fixture=valid' "https://127.0.0.1:8081/$status"
  expect 'HTTP/1.1 303'
  expect 'Location: https://127.0.0.1/?pat=unavailable'
done
request -H 'Cookie: fixture=valid' https://127.0.0.1:8081/404
expect 'HTTP/1.1 404'
echo 'ok: gateway errors return to the console; ordinary missing pages keep their status'

# Stop both fixtures, then bring back only the session check: Pat's port is now closed.
kill "$backend_pid"; wait "$backend_pid" 2>/dev/null || true
python3 -u - "$fixture_dir" > "$fixture_dir/session.log" 2>&1 <<'PY' &
import http.server
import pathlib
import sys

class CheckSession(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.headers.get('Cookie') == 'fixture=valid' else 401)
        self.end_headers()

server = http.server.HTTPServer(('127.0.0.1', 8090), CheckSession)
(pathlib.Path(sys.argv[1]) / 'session-ready').touch()
server.serve_forever()
PY
backend_pid=$!
for ((try=0; try<100; try++)); do
  [[ ! -e $fixture_dir/session-ready ]] || break
  sleep 0.05
done
test -e "$fixture_dir/session-ready"
request -H 'Cookie: fixture=valid' https://127.0.0.1:8081/
expect 'HTTP/1.1 303'
expect 'Location: https://127.0.0.1/?pat=unavailable'
request https://127.0.0.1:8081/
expect 'HTTP/1.1 302'
expect 'Location: https://127.0.0.1/login?next=/'
echo 'ok: a stopped Pat gives guidance while logged-out requests still require login'
