#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/settings.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/webproxy.sh"
source "$DXB_LIB/station.sh"

# sn_env: the station step over a throwaway tree. Everything it writes is under $TEST_TMP/etc (and
# the stamp under state/), the app's binary is a stand-in and has already made its certificate.
sn_env() {
  fx_apps_env
  export DXB_TEMPLATES=$DXB_ROOT/provision/templates DXB_NGINX_DIR=$TEST_TMP/etc/nginx DXB_NGINX=sn_nginx \
    DXB_STATION_TLS=$TEST_TMP/etc/dxberry/tls DXB_PAM_DIR=$TEST_TMP/etc/pam.d DXB_SYSTEMD_DIR=$TEST_TMP/etc/systemd/system \
    DXB_STATION_BIN=$TEST_TMP/opt/bin/dxberryd DXB_POLICY_RC=$TEST_TMP/usr/sbin/policy-rc.d DXB_STATION_WAIT_S=0 \
    DXB_COCKPIT_LINK=$TEST_TMP/usr-share-cockpit/dxberry DXB_COCKPIT_DROPIN_DIR=$TEST_TMP/etc/systemd/system/cockpit.socket.d \
    DXB_NETSAFE_UNIT_FILE=$TEST_TMP/etc/systemd/system/dxberry-config-boot.service DXB_SYSTEMCTL=sn_systemctl \
    DXB_STATION_CURL=sn_curl DXB_SS=sn_ss DXB_STATION_API_WAIT_S=0 DXB_STATION_LISTEN_WAIT_S=0
  mkdir -p "$DXB_NGINX_DIR/sites-enabled" "$DXB_NGINX_DIR/sites-available" "$DXB_SYSTEMD_DIR" "$DXB_PAM_DIR" \
    "$DXB_STATION_TLS" "${DXB_STATION_BIN%/*}" "${DXB_POLICY_RC%/*}"
  : > "$DXB_NGINX_DIR/sites-enabled/default"
  printf '#!/bin/sh\necho 0.5.0-rc1\n' > "$DXB_STATION_BIN"; chmod 755 "$DXB_STATION_BIN"
  echo cert > "$DXB_STATION_TLS/station.crt"; echo key > "$DXB_STATION_TLS/station.key"
  AP_INSTALLED='nginx libpam-runtime libopus0 pulseaudio pulseaudio-utils'
  # the network undo's boot unit is in place already (its own install reloads systemd when it writes it)
  cp "$DXB_TEMPLATES/dxberry-config-boot.service" "$DXB_NETSAFE_UNIT_FILE"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_CFG=([_IP]=10.0.0.90)
}
# enable --now starts the unit, which the fixture's is-active then reports
# $TEST_TMP/restart-fails and $TEST_TMP/cockpit-stays make those two calls fail
sn_systemctl() {
  local rc
  fx_systemctl "$@"; rc=$?
  if [[ $1 == enable && $2 == --now ]]; then echo "$3" >> "$TEST_TMP/active"; fi
  if [[ $1 == restart && -e $TEST_TMP/restart-fails ]]; then return 1; fi
  if [[ $1 == reload && -e $TEST_TMP/reload-fails ]]; then return 1; fi
  if [[ $* == "disable --now cockpit.socket" && -e $TEST_TMP/cockpit-stays ]]; then return 1; fi
  return "$rc"
}
# the app's version answer ($TEST_TMP/api-down: it does not) and the listening sockets
# ($TEST_TMP/no-listen: nothing on 443)
sn_curl() { echo "curl $*" >> "$TEST_TMP/calls"; [[ ! -e $TEST_TMP/api-down ]]; }
sn_ss() { echo "ss $*" >> "$TEST_TMP/calls"; [[ -e $TEST_TMP/no-listen ]] || echo "LISTEN 0 511 0.0.0.0:443 0.0.0.0:*"; }
sn_nginx() { echo "nginx $*" >> "$TEST_TMP/calls"; [[ ! -e $TEST_TMP/nginx-bad ]]; }
# sn_run: provision_station in a subshell with systemctl a recorder; its failed steps and status
# lines come back through $TEST_TMP/failed and $TEST_TMP/status.
sn_run() {
  (
    systemctl() { sn_systemctl "$@"; }
    provision_station
    printf '%s\n' "${DXB_FAILED_STEPS[@]}" > "$TEST_TMP/failed"
    printf '%s\n' "${DXB_STATUS_LINES[@]}" > "$TEST_TMP/status"
  ) 2> "$TEST_TMP/stderr"
}
sn_calls() { cat "$TEST_TMP/calls"; }
# sn_tree: every file the step may write, with its time and size
sn_tree() { find "$TEST_TMP/etc" "$TEST_TMP/state/dxberryd.sha256" -printf '%p %T@ %s %m\n' 2> /dev/null | sort; }

test_station_installs_unit_and_site() {
  sn_env
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_eq "$(cat "$TEST_TMP/status")" "station: https://10.0.0.90/ (log in as dietpi)"
  local unit=$DXB_SYSTEMD_DIR/dxberryd.service site=$DXB_NGINX_DIR/sites-available/dxberry-dxberry.conf
  assert_file_contains "$unit" "ExecStart=/opt/dxberry/bin/dxberryd"
  assert_file_contains "$unit" "Environment=GOMAXPROCS=2"
  assert_file_contains "$unit" "MemoryMax=400M"
  assert_file_contains "$unit" "After=network.target nginx.service"
  assert_eq "$(< "$DXB_PAM_DIR/dxberry")" "$(< "$DXB_ROOT/provision/templates/pam-dxberry")"
  assert_file_contains "$site" "listen 443 ssl default_server;"
  assert_file_contains "$site" "listen 80 default_server;"
  assert_file_contains "$site" "ssl_certificate \"$DXB_STATION_TLS/station.crt\";"
  assert_file_contains "$site" "ssl_certificate_key \"$DXB_STATION_TLS/station.key\";"
  assert_file_contains "$site" "proxy_pass http://127.0.0.1:8090;"
  assert_eq "$(readlink "$DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf")" "$site"
  assert_eq "$(stat -c %a "$DXB_STATION_TLS")" "700"
  assert_contains "$(sn_calls)" "systemctl enable --now dxberryd.service"
  assert_eq "$(grep -c 'systemctl daemon-reload' "$TEST_TMP/calls")" "1"
  assert_contains "$(sn_calls)" "nginx -t"
  assert_contains "$(sn_calls)" "systemctl reload nginx"
  # nothing to restart on a first start (enable --now started it)
  assert_not_contains "$(sn_calls)" "systemctl restart dxberryd"
}

test_station_idempotent() {
  sn_env
  sn_run
  local before after
  before=$(sn_tree)
  : > "$TEST_TMP/calls"
  sn_run
  after=$(sn_tree)
  assert_eq "$after" "$before"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_not_contains "$(sn_calls)" "restart"
  assert_not_contains "$(sn_calls)" "daemon-reload"
  assert_not_contains "$(sn_calls)" "reload nginx"
  assert_not_contains "$(sn_calls)" "apt-get"
  assert_not_contains "$(sn_calls)" "purge"
}

test_station_restarts_the_app_only_when_its_binary_or_unit_changed() {
  sn_env
  sn_run
  printf '#!/bin/sh\necho 0.5.0-rc2\n' > "$DXB_STATION_BIN"
  : > "$TEST_TMP/calls"
  sn_run
  assert_contains "$(sn_calls)" "systemctl restart dxberryd.service"
  : > "$TEST_TMP/calls"
  sn_run
  assert_not_contains "$(sn_calls)" "restart"
  echo "# stale" >> "$DXB_SYSTEMD_DIR/dxberryd.service"
  : > "$TEST_TMP/calls"
  sn_run
  assert_contains "$(sn_calls)" "systemctl daemon-reload"
  assert_contains "$(sn_calls)" "systemctl restart dxberryd.service"
}

test_station_removes_cockpit() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws cockpit-bridge cockpit-system'
  mkdir -p "$DXB_COCKPIT_DROPIN_DIR" "${DXB_COCKPIT_LINK%/*}"
  : > "$DXB_COCKPIT_DROPIN_DIR/dxberry-listen.conf"; ln -s /opt/dxberry/cockpit/dxberry "$DXB_COCKPIT_LINK"
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_contains "$(sn_calls)" "systemctl disable --now cockpit.socket"
  assert_contains "$(sn_calls)" "apt-get purge -y cockpit-ws cockpit-bridge cockpit-system"
  [[ ! -e $DXB_COCKPIT_DROPIN_DIR && ! -L $DXB_COCKPIT_LINK ]] || _fail "Cockpit's drop-in or the page link is still there"
  # Cockpit held 443 and 80: it must be gone before nginx loads the site that binds them
  local off site
  off=$(grep -n 'disable --now cockpit.socket' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  site=$(grep -n 'systemctl reload nginx' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  (( off < site )) || _fail "nginx reloaded (line $site) before Cockpit stopped (line $off)"
  assert_eq "$(grep -c 'systemctl daemon-reload' "$TEST_TMP/calls")" "1"
}

test_station_keeps_cockpit_until_the_app_is_ready() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws'
  rm -f "$DXB_STATION_BIN"
  sn_run
  assert_not_contains "$(sn_calls)" "cockpit"
  assert_not_contains "$(sn_calls)" "dxberryd.service"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_eq "$(cat "$TEST_TMP/status")" "station: unavailable (dxberryd is not installed)"
  [[ -f $DXB_PAM_DIR/dxberry ]] || _fail "the PAM service file is written even on a tree without the binary"
  # the binary is there but made no certificate
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws'
  rm -f "$DXB_STATION_TLS"/station.*
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "no certificate"
  assert_not_contains "$(sn_calls)" "cockpit"
}

test_station_a_failed_purge_is_a_failed_step_but_the_site_still_goes_in() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws'
  echo cockpit-ws > "$TEST_TMP/apt-fail-on"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "could not remove Cockpit"
  [[ -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the station site must still be enabled"
}

test_station_installs_nginx_and_pam_only_when_missing() {
  sn_env
  AP_INSTALLED=''
  sn_run
  assert_contains "$(sn_calls)" "apt-get install -y --no-install-recommends nginx"
  assert_contains "$(sn_calls)" "apt-get install -y --no-install-recommends libpam-runtime"
  [[ ! -e $DXB_POLICY_RC ]] || _fail "policy-rc.d left behind"
  sn_env
  sn_run
  assert_not_contains "$(sn_calls)" "apt-get"
}

test_station_installs_the_audio_runtime_only_when_missing() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime pulseaudio'
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_contains "$(sn_calls)" "apt-get install -y --no-install-recommends libopus0 pulseaudio-utils"
  assert_not_contains "$(sn_calls)" "recommends libopus0 pulseaudio "
  sn_env
  sn_run
  assert_not_contains "$(sn_calls)" "apt-get"
  # a failed install is a failed step named station, and the run goes on
  sn_env
  AP_INSTALLED='nginx libpam-runtime'
  echo libopus0 > "$TEST_TMP/apt-fail-on"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "libopus0 pulseaudio pulseaudio-utils"
  assert_contains "$(cat "$TEST_TMP/status")" "station: https://"
}

test_boot_overrides_install_the_audio_runtime() {
  local line
  line=$(grep '^AUTO_SETUP_APT_INSTALLS=' "$DXB_ROOT/boot/dietpi.overrides.txt")
  assert_contains "$line" " libopus0 "
  assert_contains "$line" " pulseaudio "
  assert_contains "$line" " pulseaudio-utils"
}

test_station_unmasks_logind_and_always_starts_it() {
  sn_env
  echo masked > "$TEST_TMP/enabled-systemd-logind"
  sn_run
  assert_contains "$(sn_calls)" "systemctl unmask systemd-logind"
  assert_contains "$(sn_calls)" "systemctl start systemd-logind"
  rm -f "$TEST_TMP/enabled-systemd-logind"; : > "$TEST_TMP/calls"
  sn_run
  assert_not_contains "$(sn_calls)" "unmask"
  assert_contains "$(sn_calls)" "systemctl start systemd-logind"
}

test_station_keeps_the_network_undo_boot_unit() {
  sn_env
  sn_run
  assert_file_contains "$DXB_NETSAFE_UNIT_FILE" "ExecStart=/opt/dxberry/bin/dxberry-config revert --boot"
  assert_contains "$(sn_calls)" "systemctl enable dxberry-config-boot.service"
}

test_station_rerenders_the_pat_site_onto_the_station_certificate() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime pat'
  mkdir -p "$TEST_TMP/old"; echo c > "$TEST_TMP/old/a.crt"; echo k > "$TEST_TMP/old/a.key"
  # a Pat site on an old certificate path
  local avail=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf
  dxb_render "$DXB_TEMPLATES/nginx-pat.conf" "CERT=$TEST_TMP/old/a.crt" "KEY=$TEST_TMP/old/a.key" > "$avail"
  ln -s "$avail" "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf"
  sn_run
  assert_file_contains "$avail" "ssl_certificate \"$DXB_STATION_TLS/station.crt\";"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
}

test_station_auth_endpoint_points_at_app() {
  sn_env
  sn_run
  local f=$DXB_NGINX_DIR/snippets/dxberry-auth-endpoints.conf
  assert_file_contains "$f" "proxy_pass http://127.0.0.1:8090/api/auth/check;"
  # shellcheck disable=SC2016  # nginx variables, matched literally
  assert_file_contains "$f" 'return 302 https://$host/login?next=$request_uri;'
  assert_file_not_contains "$f" "443/cockpit"
  assert_file_not_contains "$f" "proxy_ssl_verify"
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" "auth_request /.dxberry-auth;"
}

test_station_site_template_shape() {
  local t=$DXB_ROOT/provision/templates/nginx-dxberry.conf
  # the map sits before the server block (http context), and every proxy header is the specified one
  assert_eq "$(grep -n '^map \|^server ' "$t" | cut -d: -f2 | cut -d' ' -f1 | tr '\n' ' ')" "map server "
  # shellcheck disable=SC2016  # nginx variables, matched literally
  assert_file_contains "$t" 'proxy_set_header X-Forwarded-For $remote_addr;'
  assert_file_not_contains "$t" "proxy_add_x_forwarded_for"
  assert_file_contains "$t" "proxy_read_timeout 1h;"
  assert_file_contains "$t" "client_max_body_size 1m;"
  # the tab token rides the events URL's query string: that location must not be access-logged,
  # and must proxy the WebSocket exactly as the catch-all does
  local ev; ev=$(sed -n '/location = \/api\/events {/,/^    }/p' "$t")
  assert_contains "$ev" "access_log off;"
  assert_contains "$ev" "proxy_pass http://127.0.0.1:8090;"
  assert_contains "$ev" "proxy_http_version 1.1;"
  assert_contains "$ev" "proxy_read_timeout 1h;"
  # shellcheck disable=SC2016
  assert_contains "$ev" 'proxy_set_header Upgrade $http_upgrade;'
  # shellcheck disable=SC2016
  assert_contains "$ev" 'proxy_set_header Connection $connection_upgrade;'
  # shellcheck disable=SC2016
  assert_file_contains "$t" 'proxy_set_header Connection $connection_upgrade;'
  # the port the browser used stays in Host, so a WebSocket behind a forwarded port keeps its Origin match
  # shellcheck disable=SC2016
  assert_file_contains "$t" 'proxy_set_header Host $http_host;'
  # shellcheck disable=SC2016
  assert_file_not_contains "$t" 'proxy_set_header Host $host;'
}

# The terminal is a real login and the CLIs write all over the system: no filesystem sandbox.
test_station_unit_sandbox_leaves_what_the_app_needs() {
  local u=$DXB_ROOT/provision/templates/dxberryd.service d
  for d in PrivateDevices DevicePolicy RestrictNamespaces PrivateTmp ProtectSystem ProtectHome ReadWritePaths; do
    if grep -q "^$d=" "$u"; then _fail "$d is set in dxberryd.service"; fi
  done
  # udevadm trigger, modprobe and kernel package upgrades need these writable and reachable
  for d in ProtectKernelTunables ProtectKernelModules ProtectControlGroups RestrictRealtime; do
    if grep -q "^$d=" "$u"; then _fail "$d is set in dxberryd.service"; fi
  done
  for d in NoNewPrivileges=no RestrictSUIDSGID=no TasksMax=256 MemoryMax=400M Restart=always Environment=GOMAXPROCS=2; do
    assert_file_contains "$u" "$d"
  done
}

test_station_site_listens_on_ipv4_only() {
  assert_file_not_contains "$DXB_ROOT/provision/templates/nginx-dxberry.conf" "[::]"
}

# nginx -t that, like the real one, rejects an enabled site whose certificate file is gone
sn_nginx_strict() {
  local f c
  echo "nginx $*" >> "$TEST_TMP/calls"
  for f in "$DXB_NGINX_DIR"/sites-enabled/*; do
    [[ -f $f ]] || continue
    while read -r c; do [[ -f $c ]] || { echo "emerg: cannot load certificate $c"; return 1; }; done \
      < <(sed -n 's/^ *ssl_certificate "\(.*\)";/\1/p' "$f")
  done
  return 0
}
# apt whose purge of Cockpit removes its certificate directory, as the package does
sn_apt_purge() {
  if [[ $1 == purge ]]; then echo "purged $TEST_TMP/etc/cockpit/ws-certs.d" >> "$TEST_TMP/calls"; rm -rf "$TEST_TMP/etc/cockpit"; fi
  ap_apt "$@"
}

test_station_moves_pat_off_cockpits_certificate_before_the_purge() (
  sn_env
  # shellcheck disable=SC2030,SC2031  # a subshell test: the change is meant to stay inside
  export DXB_NGINX=sn_nginx_strict DXB_APT_GET=sn_apt_purge
  AP_INSTALLED='nginx libpam-runtime pat cockpit-ws'
  local old=$TEST_TMP/etc/cockpit/ws-certs.d avail=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf
  mkdir -p "$old"; echo c > "$old/0-self-signed.cert"; echo k > "$old/0-self-signed.key"
  dxb_render "$DXB_TEMPLATES/nginx-pat.conf" "CERT=$old/0-self-signed.cert" "KEY=$old/0-self-signed.key" > "$avail"
  ln -s "$avail" "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf"
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_file_contains "$avail" "ssl_certificate \"$DXB_STATION_TLS/station.crt\";"
  local reload purge
  reload=$(grep -n 'systemctl reload nginx' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  purge=$(grep -n '^purged ' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  if [[ -z $reload || -z $purge ]] || (( reload > purge )); then _fail "Pat was not re-rendered and reloaded before the purge (reload '$reload', purge '$purge')"; fi
  [[ -L $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the station site is not linked"
  assert_eq "$(cat "$TEST_TMP/status")" "station: https://10.0.0.90/ (log in as dietpi)"
)

# nginx whose -t rejects the re-rendered Pat site (the previous one is put back by the refresh)
sn_nginx_pat_bad() {
  echo "nginx $*" >> "$TEST_TMP/calls"
  ! grep -q 'station.crt' "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf"
}

test_station_keeps_cockpit_when_a_site_still_uses_its_certificate() (
  sn_env
  # shellcheck disable=SC2030,SC2031
  export DXB_NGINX=sn_nginx_pat_bad
  AP_INSTALLED='nginx libpam-runtime pat cockpit-ws'
  local old=$TEST_TMP/etc/cockpit/ws-certs.d avail=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf
  mkdir -p "$old"; echo c > "$old/0-self-signed.cert"; echo k > "$old/0-self-signed.key"
  dxb_render "$DXB_TEMPLATES/nginx-pat.conf" "CERT=$old/0-self-signed.cert" "KEY=$old/0-self-signed.key" > "$avail"
  ln -s "$avail" "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "site pat still uses Cockpit's certificate; not removing Cockpit"
  assert_not_contains "$(sn_calls)" "purge"
  assert_not_contains "$(sn_calls)" "disable --now cockpit.socket"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the station site was loaded"
  assert_eq "$(cat "$TEST_TMP/status")" "station: unavailable (see FAILED STEPS)"
  [[ -f $old/0-self-signed.cert ]] || _fail "Cockpit's certificate was touched"
)

test_station_does_not_load_the_site_when_cockpit_will_not_stop() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws'
  : > "$TEST_TMP/cockpit-stays"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "could not stop cockpit.socket"
  assert_not_contains "$(sn_calls)" "purge"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the station site was loaded over a running Cockpit"
}

# a certificate renewed in place makes the site stale; the site counts as current only once nginx
# has reloaded, so a failed reload leaves it stale and the next run tries again
test_station_site_stamp_only_after_reload() {
  sn_env
  sn_run
  local avail=$DXB_NGINX_DIR/sites-available/dxberry-dxberry.conf
  touch -d '-1 hour' "$avail"
  : > "$TEST_TMP/reload-fails"
  : > "$TEST_TMP/calls"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "nginx could not reload"
  [[ $DXB_STATION_TLS/station.crt -nt $avail ]] || _fail "the site was stamped as current although nginx did not reload"
  rm -f "$TEST_TMP/reload-fails"
  : > "$TEST_TMP/calls"
  sn_run
  assert_contains "$(sn_calls)" "systemctl reload nginx"
  [[ ! $DXB_STATION_TLS/station.crt -nt $avail ]] || _fail "the site is still stale after a successful reload"
}

test_station_stamps_the_binary_only_after_a_successful_restart() {
  sn_env
  sn_run
  local stamp=$DXB_STATE_DIR/dxberryd.sha256 old
  old=$(< "$stamp")
  printf '#!/bin/sh\necho 0.5.0-rc2\n' > "$DXB_STATION_BIN"
  : > "$TEST_TMP/restart-fails"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "could not restart"
  assert_eq "$(< "$stamp")" "$old"
  # the next run tries again
  rm -f "$TEST_TMP/restart-fails"
  : > "$TEST_TMP/calls"
  sn_run
  assert_contains "$(sn_calls)" "systemctl restart dxberryd.service"
  assert_eq "$(< "$stamp")" "$(sha256sum "$DXB_STATION_BIN" | cut -d' ' -f1)"
}

test_station_is_not_run_without_the_template_files() {
  sn_env
  DXB_TEMPLATES=$TEST_TMP/none sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "station"
}

# line number of the first recorded call matching PATTERN
sn_line() { grep -n -- "$1" "$TEST_TMP/calls" | head -1 | cut -d: -f1; }

test_station_checks_the_app_and_the_site_before_it_touches_cockpit() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws cockpit-bridge cockpit-system'
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  local v t off rl ss
  v=$(sn_line 'curl .*127.0.0.1:8090/api/version'); t=$(sn_line '^nginx -t'); off=$(sn_line 'disable --now cockpit.socket')
  rl=$(sn_line 'systemctl reload nginx'); ss=$(sn_line '^ss ')
  if [[ -z $v || -z $t || -z $off || -z $rl || -z $ss ]] || ! (( v < t && t < off && off < rl && rl < ss )); then
    _fail "wrong order: version $v, nginx -t $t, cockpit stop $off, reload $rl, ss $ss"
  fi
  assert_contains "$(sn_calls)" "ss -Hltn sport = :443"
  assert_eq "$(cat "$TEST_TMP/status")" "station: https://10.0.0.90/ (log in as dietpi)"
}

# nginx whose -t rejects the station site once it is enabled
sn_nginx_site_bad() { echo "nginx $*" >> "$TEST_TMP/calls"; [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]]; }

test_station_keeps_cockpit_when_nginx_rejects_the_station_site() {
  sn_env
  # shellcheck disable=SC2031
  export DXB_NGINX=sn_nginx_site_bad
  AP_INSTALLED='nginx libpam-runtime cockpit-ws cockpit-bridge cockpit-system'
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "nginx rejected the dxberry site"
  assert_not_contains "$(sn_calls)" "disable --now cockpit.socket"
  assert_not_contains "$(sn_calls)" "purge"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf && ! -L $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the rejected site is still enabled"
  assert_eq "$(cat "$TEST_TMP/status")" "station: unavailable (see FAILED STEPS)"
}

test_station_keeps_cockpit_when_the_app_does_not_answer() {
  sn_env
  AP_INSTALLED='nginx libpam-runtime cockpit-ws'
  : > "$TEST_TMP/api-down"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "did not answer"
  assert_not_contains "$(sn_calls)" "cockpit.socket"
  assert_not_contains "$(sn_calls)" "purge"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || _fail "the station site was enabled"
  assert_eq "$(cat "$TEST_TMP/status")" "station: unavailable (see FAILED STEPS)"
}

test_station_fails_when_nothing_listens_on_443_afterwards() {
  sn_env
  : > "$TEST_TMP/no-listen"
  sn_run
  assert_contains "$(cat "$TEST_TMP/failed")" "nothing is listening on port 443"
  assert_eq "$(cat "$TEST_TMP/status")" "station: unavailable (see FAILED STEPS)"
}

# station/build.sh stamps the version it is given (a release build passes the tag with its rc
# suffix) and otherwise provision/VERSION; go itself is a stand-in that records its arguments
test_station_build_stamps_the_given_version() (
  mkdir -p "$TEST_TMP/gobin"
  printf '#!/bin/bash\necho "$@" > "%s/go-args"\n' "$TEST_TMP" > "$TEST_TMP/gobin/go"; chmod 755 "$TEST_TMP/gobin/go"
  PATH=$TEST_TMP/gobin:$PATH bash "$DXB_ROOT/station/build.sh" "$TEST_TMP/out" 0.5.0-rc1
  assert_contains "$(cat "$TEST_TMP/go-args")" "-X main.version=0.5.0-rc1"
  PATH=$TEST_TMP/gobin:$PATH bash "$DXB_ROOT/station/build.sh" "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/go-args")" "-X main.version=$(< "$DXB_ROOT/provision/VERSION")"
)

# hraban/opus builds its Ogg stream code against libopusfile unless the nolibopusfile tag is set,
# and the Pi installs libopus0 only: the station build always sets the tag
test_station_build_leaves_out_libopusfile() (
  mkdir -p "$TEST_TMP/gobin"
  printf '#!/bin/bash\necho "$@" > "%s/go-args"\n' "$TEST_TMP" > "$TEST_TMP/gobin/go"; chmod 755 "$TEST_TMP/gobin/go"
  PATH=$TEST_TMP/gobin:$PATH bash "$DXB_ROOT/station/build.sh" "$TEST_TMP/out" 0.5.0-rc3
  assert_contains "$(cat "$TEST_TMP/go-args")" "build -tags nolibopusfile "
)

# the radios' sound server unit goes in beside the app's, and the app is not restarted for it
test_station_installs_the_audio_unit() {
  sn_env
  sn_run
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  assert_eq "$(< "$DXB_SYSTEMD_DIR/dxberry-audio@.service")" "$(< "$DXB_ROOT/provision/templates/dxberry-audio@.service")"
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-apps.tmpfiles" "d /run/dxberry/audio 0750 root dxbscreen -"
}
