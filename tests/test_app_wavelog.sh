#!/usr/bin/env bash
# Wavelog on the Pi (provision/lib/apps/wavelog.sh, nginx-wavelog.conf). (Prefix wl_.)
# shellcheck disable=SC1091,SC2016,SC2034,SC2317,SC2329
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/webproxy.sh"

# wl_env: the module loaded (here, not when the file is read: in the one shared test shell its
# install_extra would otherwise run in test_apps.sh's installs of a stand-in wavelog), the
# program environment, an nginx tree and Cockpit's certificate under $TEST_TMP, nginx
# and mariadb stand-ins (mariadb keeps its stdin in $TEST_TMP/mariadb, its arguments in
# $TEST_TMP/mariadb-argv), and Wavelog 3.2.4 released and installed the way the app-install job
# does it (dxb_apps_install)
wl_env() {
  fx_apps_env
  source "$DXB_LIB/apps/wavelog.sh"
  export DXB_NGINX_DIR=$TEST_TMP/etc/nginx DXB_POLICY_RC=$TEST_TMP/usr/sbin/policy-rc.d DXB_NGINX=wl_nginx \
    DXB_COCKPIT_CERTS=$TEST_TMP/etc/cockpit/ws-certs.d DXB_MARIADB=wl_mariadb
  mkdir -p "$DXB_NGINX_DIR/sites-enabled" "$DXB_NGINX_DIR/sites-available" "${DXB_POLICY_RC%/*}" "$DXB_COCKPIT_CERTS"
  : > "$DXB_COCKPIT_CERTS/0-self-signed.cert"; : > "$DXB_COCKPIT_CERTS/0-self-signed.key"
  : > "$TEST_TMP/mariadb"; : > "$TEST_TMP/mariadb-argv"
  wl_new_release 3.2.4
  dxb_apps_install wavelog > /dev/null || _fail "the first install failed"
}
wl_nginx() { echo "nginx $*" >> "$TEST_TMP/calls"; }
wl_mariadb() { printf '%s\n' "$*" >> "$TEST_TMP/mariadb-argv"; cat >> "$TEST_TMP/mariadb"; [[ ! -e $TEST_TMP/mariadb-fail ]]; }
# wl_new_release VERSION: Wavelog VERSION is GitHub's latest release, its tag tarball holding
# index.php ("VERSION"), a stock application/config/config.php and the empty data directories
wl_new_release() {
  local s=$TEST_TMP/src/wavelog-$1
  rm -rf "$s"; mkdir -p "$s/application/config" "$s/userdata" "$s/uploads" "$s/backup" "$s/install" "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags"
  echo "$1" > "$s/index.php"; echo stock > "$s/application/config/config.php.sample"; echo '<?php' > "$s/install/index.php"
  : > "$s/userdata/.keep"; : > "$s/backup/notes.xml"
  tar -czf "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags/$1.tar.gz" -C "$TEST_TMP/src" "wavelog-$1"
  ap_release wavelog/wavelog "$1"
}
wl_site() { printf '%s\n' "$DXB_NGINX_DIR/sites-available/dxberry-wavelog.conf"; }

# ---- the brief's tests -------------------------------------------------------------------

test_wavelog_install_uses_socket_auth_and_gates_the_installer() {
  wl_env; assert_ok app_wavelog_install_extra > /dev/null
  assert_contains "$(cat "$TEST_TMP/mariadb")" "IDENTIFIED VIA unix_socket"
  assert_not_contains "$(cat "$TEST_TMP/mariadb")" "PASSWORD"
  assert_file_contains "$DXB_NGINX_DIR/sites-available/dxberry-wavelog.conf" "location ^~ /install"
  assert_file_contains "$DXB_NGINX_DIR/sites-available/dxberry-wavelog.conf" "listen 127.0.0.1:8083"
}
# The update goes through the app-install job's own path (dxb_apps_install NAME update), which
# places the new tarball and keeps what app_wavelog_keep names, then runs install_extra.
test_wavelog_update_keeps_config_and_userdata() {
  wl_env; app_wavelog_install_extra > /dev/null
  echo mine > "$DXB_APPS_OPT/wavelog/application/config/config.php"; echo pic > "$DXB_APPS_OPT/wavelog/userdata/x"
  wl_new_release 3.2.5; dxb_apps_install wavelog update > /dev/null
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/config.php")" "mine"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/userdata/x")" "pic"
  # and it is the new release
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/index.php")" "3.2.5"
  assert_eq "$(dxb_apps_version wavelog)" "3.2.5"
}

# ---- beyond the brief --------------------------------------------------------------------

test_wavelog_database_statements_are_the_briefs_on_stdin_only() {
  wl_env
  assert_eq "$(cat "$TEST_TMP/mariadb")" "CREATE DATABASE IF NOT EXISTS wavelog CHARACTER SET utf8mb4;
CREATE USER IF NOT EXISTS 'www-data'@'localhost' IDENTIFIED VIA unix_socket;
GRANT ALL PRIVILEGES ON wavelog.* TO 'www-data'@'localhost';"
  assert_eq "$(cat "$TEST_TMP/mariadb-argv")" ""                                  # no statement in an argument
  assert_file_contains "$TEST_TMP/calls" "systemctl enable --now mariadb"
}
test_wavelog_tree_belongs_to_www_data() {
  wl_env
  assert_file_contains "$TEST_TMP/calls" "chown -R www-data:www-data $DXB_APPS_OPT/wavelog"
}
test_wavelog_keeps_the_operators_files_across_updates_and_removal() {
  wl_env
  assert_eq "$(app_wavelog_keep | tr '\n' ' ')" "application/config/config.php application/config/database.php userdata uploads backup "
  mkdir -p "$DXB_APPS_OPT/wavelog/uploads"
  echo db > "$DXB_APPS_OPT/wavelog/application/config/database.php"; echo adi > "$DXB_APPS_OPT/wavelog/uploads/u.adi"
  echo b > "$DXB_APPS_OPT/wavelog/backup/notes_1.xml"
  assert_ok dxb_apps_remove wavelog > /dev/null
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/database.php")" "db"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/uploads/u.adi")" "adi"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/backup/notes_1.xml")" "b"
  assert_fails dxb_apps_installed wavelog
  # the site went, the database stayed
  [[ -e $(wl_site) || -L $DXB_NGINX_DIR/sites-enabled/dxberry-wavelog.conf ]] && _fail "the site is still there"
  assert_eq "$(grep -c 'CREATE DATABASE' "$TEST_TMP/mariadb")" "1"
  assert_file_not_contains "$TEST_TMP/mariadb" "DROP"
  # a reinstall continues with them
  assert_ok dxb_apps_install wavelog > /dev/null
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/database.php")" "db"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/index.php")" "3.2.4"
}
test_wavelog_site_listens_on_8082_tls_and_loopback_8083() {
  wl_env
  local f; f=$(wl_site)
  assert_file_contains "$f" "listen 8082 ssl;"
  assert_file_contains "$f" "listen 127.0.0.1:8083;"
  assert_file_contains "$f" "ssl_certificate \"$DXB_COCKPIT_CERTS/0-self-signed.cert\";"
  assert_file_contains "$f" "ssl_certificate_key \"$DXB_COCKPIT_CERTS/0-self-signed.key\";"
  assert_file_contains "$f" "root \"$DXB_APPS_OPT/wavelog\";"
  assert_file_contains "$f" "fastcgi_pass unix:/run/php/php-fpm.sock;"
  assert_file_not_contains "$f" "@"
  assert_eq "$(readlink "$DXB_NGINX_DIR/sites-enabled/dxberry-wavelog.conf")" "$f"
  assert_file_contains "$TEST_TMP/calls" "systemctl reload nginx"
}
test_wavelog_site_gates_the_installer_and_backups_and_hides_the_code() {
  wl_env
  local f; f=$(wl_site)
  # the console login guards /install (and the backups Wavelog writes), not the whole site:
  # Wavelog has its own login, and its API must answer without a Cockpit cookie
  assert_file_not_contains "$f" "include snippets/dxberry-auth.conf;"
  assert_file_contains "$f" "include snippets/dxberry-auth-endpoints.conf;"
  assert_eq "$(grep -c 'include snippets/dxberry-auth-gate.conf;' "$f")" "2"
  assert_eq "$(awk '/location \^~ \/install/ { getline; print; exit }' "$f" | tr -d ' ')" "includesnippets/dxberry-auth-gate.conf;"
  assert_eq "$(awk '/location \^~ \/backup\// { getline; print; exit }' "$f" | tr -d ' ')" "includesnippets/dxberry-auth-gate.conf;"
  assert_file_contains "$f" 'location ~ ^/(application|system|src|docker|updates|uploads)(/|$) {'
  assert_file_contains "$f" 'location ~ /\. {'
  # every deny comes before the PHP handler (nginx takes the first regex that matches)
  local deny php
  deny=$(grep -n 'location ~ ^/(application' "$f" | cut -d: -f1); php=$(grep -n '^    location ~ \\.php(/|\$) {' "$f" | cut -d: -f1)
  if [[ -z $deny || -z $php ]] || (( deny > php )); then _fail "the denies must come before the PHP location ($deny, $php)"; fi
  # the braces balance
  assert_eq "$(tr -cd '{' < "$f" | wc -c)" "$(tr -cd '}' < "$f" | wc -c)"
  # and the header says what it was built from
  assert_file_contains "$DXB_ROOT/provision/templates/nginx-wavelog.conf" "wavelog_docs"
}
# The auth snippet is split so a single location can be gated: the gate (auth_request and
# error_page, valid inside a location) and the endpoints (the two locations, server level). Pat's
# server-level snippet is the two together, as before.
test_wavelog_auth_snippets_split_and_pat_is_unchanged() {
  wl_env
  local s=$DXB_NGINX_DIR/snippets
  assert_file_contains "$s/dxberry-auth-gate.conf" "auth_request /.dxberry-auth;"
  assert_file_contains "$s/dxberry-auth-gate.conf" "error_page 401 403 = @dxberry-login;"
  assert_file_not_contains "$s/dxberry-auth-gate.conf" "location"
  assert_file_contains "$s/dxberry-auth-endpoints.conf" "location = /.dxberry-auth {"
  assert_file_contains "$s/dxberry-auth-endpoints.conf" 'location @dxberry-login { return 302 https://$host/; }'
  assert_file_not_contains "$s/dxberry-auth-endpoints.conf" "auth_request /"
  assert_eq "$(grep -v '^#' "$s/dxberry-auth.conf")" "$(cat "$s/dxberry-auth-gate.conf" "$s/dxberry-auth-endpoints.conf" | grep -v '^#')"
  # Pat's site still includes the whole snippet at server level
  assert_ok dxb_webproxy_site pat
  assert_file_contains "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf" "include snippets/dxberry-auth.conf;"
}
test_wavelog_nginx_comes_only_through_the_web_proxy() {
  wl_env
  assert_eq "$(dxb_apps_row wavelog | jq -r '.packages | join(" ")')" "php-fpm php-mysql php-curl php-mbstring php-xml php-zip php-gd mariadb-server"
  # nginx was installed by dxb_webproxy_install (inside its policy-rc.d guard), never with the packages
  assert_file_contains "$TEST_TMP/calls" "apt-get install -y --no-install-recommends nginx -o"
  assert_file_not_contains "$TEST_TMP/calls" "--no-upgrade php-fpm php-mysql php-curl php-mbstring php-xml php-zip php-gd mariadb-server nginx"
  assert_eq "$(grep -c -- '--no-upgrade.* nginx' "$TEST_TMP/calls")" "0"
}
test_wavelog_remove_extra_removes_only_the_site() {
  wl_env; AP_INSTALLED=nginx
  : > "$TEST_TMP/mariadb"
  assert_ok app_wavelog_remove_extra
  [[ -e $(wl_site) ]] && _fail "the site is still there"
  [[ -f $DXB_APPS_OPT/wavelog/index.php ]] || _fail "remove_extra must leave Wavelog's files"
  assert_eq "$(cat "$TEST_TMP/mariadb")" ""
}
test_wavelog_module_facts() {
  wl_env
  assert_eq "$(app_wavelog_kind)" "service"
  assert_eq "$(app_wavelog_label)" "Wavelog"
  assert_ok app_wavelog_installed
  assert_eq "$(app_wavelog_open)" '{"kind":"url","port":8082,"path":"/"}'
  assert_contains "$(app_wavelog_setup_note)" "on the database step enter localhost, wavelog, www-data and no password"
}
test_wavelog_a_failing_database_step_fails_the_setup() {
  wl_env
  : > "$TEST_TMP/mariadb-fail"
  assert_eq "$(app_wavelog_install_extra > /dev/null 2>&1; echo $?)" "6"
}
