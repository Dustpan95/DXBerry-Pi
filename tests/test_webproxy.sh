#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/webproxy.sh"

# wp_env: the program environment plus an nginx tree, a policy-rc.d path and a Cockpit certificate
# directory under $TEST_TMP, and nginx itself a stand-in (it records; wp-fail makes -t fail)
wp_env() {
  fx_apps_env
  export DXB_NGINX_DIR=$TEST_TMP/etc/nginx DXB_POLICY_RC=$TEST_TMP/usr/sbin/policy-rc.d \
    DXB_NGINX=wp_nginx DXB_COCKPIT_CERTS=$TEST_TMP/etc/cockpit/ws-certs.d
  mkdir -p "$DXB_NGINX_DIR/sites-enabled" "$DXB_NGINX_DIR/sites-available" "${DXB_POLICY_RC%/*}" "$DXB_COCKPIT_CERTS"
  : > "$DXB_NGINX_DIR/sites-enabled/default"
  : > "$DXB_COCKPIT_CERTS/0-self-signed.cert"; : > "$DXB_COCKPIT_CERTS/0-self-signed.key"
}
wp_nginx() {
  echo "nginx $*" >> "$TEST_TMP/calls"
  [[ ! -e $TEST_TMP/wp-fail ]]
}
# an apt that notes whether policy-rc.d exists at the moment it runs
wp_apt() {
  if [[ -e $DXB_POLICY_RC ]]; then echo "policy present: $(< "$DXB_POLICY_RC")" >> "$TEST_TMP/policy-seen"; else echo "policy absent" >> "$TEST_TMP/policy-seen"; fi
  ap_apt "$@"
}

test_webproxy_install_holds_the_policy_file_only_while_apt_runs() {
  wp_env; export DXB_APT_GET=wp_apt
  assert_ok dxb_webproxy_install
  assert_file_contains "$TEST_TMP/policy-seen" "policy present: "
  assert_file_contains "$TEST_TMP/policy-seen" "exit 101"
  [[ ! -e $DXB_POLICY_RC ]] || _fail "policy-rc.d left behind"
  assert_file_contains "$TEST_TMP/calls" "apt-get install -y --no-install-recommends nginx"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/default ]] || _fail "the default site is still enabled"
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" "auth_request /.dxberry-auth;"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable --now nginx"
}
test_webproxy_install_removes_the_policy_file_when_apt_fails() {
  wp_env; export DXB_APT_GET=wp_apt; echo 100 > "$TEST_TMP/apt-rc"
  assert_eq "$(dxb_webproxy_install > /dev/null 2>&1; echo $?)" "6"
  [[ ! -e $DXB_POLICY_RC ]] || _fail "policy-rc.d left behind after a failed apt"
  assert_file_not_contains "$TEST_TMP/calls" "systemctl enable"
}
test_webproxy_install_leaves_an_existing_policy_file_alone() {
  wp_env; export DXB_APT_GET=wp_apt
  printf '#!/bin/sh\nexit 101 # image build\n' > "$DXB_POLICY_RC"; cp "$DXB_POLICY_RC" "$TEST_TMP/policy-before"
  assert_ok dxb_webproxy_install
  assert_eq "$(cmp "$TEST_TMP/policy-before" "$DXB_POLICY_RC" && echo same)" "same"
}
test_webproxy_install_skips_apt_when_nginx_is_installed_and_is_idempotent() {
  wp_env; AP_INSTALLED=nginx
  assert_ok dxb_webproxy_install
  assert_ok dxb_webproxy_install
  assert_file_not_contains "$TEST_TMP/calls" "apt-get"
  [[ ! -e $DXB_POLICY_RC ]] || _fail "policy-rc.d created without an install"
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" "location @dxberry-login { return 302 https://\$host/; }"
}
test_webproxy_install_removes_a_stale_policy_file_of_its_own() {
  wp_env; AP_INSTALLED=nginx
  printf '#!/bin/sh\n%s\nexit 101\n' "$DXB_POLICY_MARK" > "$DXB_POLICY_RC"
  assert_ok dxb_webproxy_install
  [[ ! -e $DXB_POLICY_RC ]] || _fail "a stale policy-rc.d of ours was kept"
}
test_webproxy_policy_file_carries_the_marker() {
  wp_env; export DXB_APT_GET=wp_apt
  assert_ok dxb_webproxy_install
  assert_file_contains "$TEST_TMP/policy-seen" "# DXBerry: temporary, removed after the nginx install"
}
test_webproxy_auth_snippet_sends_only_the_cookie() {
  wp_env; AP_INSTALLED=nginx
  dxb_webproxy_install
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" 'proxy_set_header Authorization "";'
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" 'proxy_set_header Upgrade "";'
  # shellcheck disable=SC2016  # nginx variables, matched literally
  assert_file_contains "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" 'proxy_set_header Host $host;'
}
test_webproxy_nginx_test_output_goes_to_stderr() {
  wp_env
  wp_nginx() { echo "nginx $*" >> "$TEST_TMP/calls"; echo "emerg: bad directive"; [[ ! -e $TEST_TMP/wp-fail ]]; }
  : > "$TEST_TMP/wp-fail"
  dxb_webproxy_site pat 2> "$TEST_TMP/err" > /dev/null
  assert_file_contains "$TEST_TMP/err" "emerg: bad directive"
}
test_webproxy_cert_picks_the_newest_complete_pair() {
  wp_env
  : > "$DXB_COCKPIT_CERTS/5-newer.cert"; : > "$DXB_COCKPIT_CERTS/5-newer.key"
  : > "$DXB_COCKPIT_CERTS/9-nokey.cert"
  touch -d '2026-01-01' "$DXB_COCKPIT_CERTS/0-self-signed.cert"
  touch -d '2026-06-01' "$DXB_COCKPIT_CERTS/5-newer.cert"
  touch -d '2026-09-01' "$DXB_COCKPIT_CERTS/9-nokey.cert"
  assert_eq "$(dxb_webproxy_cert | tr '\n' ' ')" "$DXB_COCKPIT_CERTS/5-newer.cert $DXB_COCKPIT_CERTS/5-newer.key "
}
test_webproxy_cert_without_a_pair_is_6() {
  wp_env; rm -f "$DXB_COCKPIT_CERTS"/*
  : > "$DXB_COCKPIT_CERTS/lonely.cert"
  assert_eq "$(dxb_webproxy_cert 2> "$TEST_TMP/err"; echo $?)" "6"
  assert_contains "$(< "$TEST_TMP/err")" "certificate"
}
test_webproxy_pat_site_is_rendered_enabled_and_reloaded() {
  wp_env
  assert_ok dxb_webproxy_site pat
  local f=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf
  assert_file_contains "$f" "listen 8081 ssl;"
  assert_file_contains "$f" "include snippets/dxberry-auth.conf;"
  assert_file_contains "$f" "ssl_certificate \"$DXB_COCKPIT_CERTS/0-self-signed.cert\";"
  assert_file_contains "$f" "ssl_certificate_key \"$DXB_COCKPIT_CERTS/0-self-signed.key\";"
  # shellcheck disable=SC2016  # nginx variables, matched literally
  assert_file_contains "$f" 'proxy_set_header Host $http_host;'
  assert_file_contains "$f" "proxy_pass http://127.0.0.1:8180;"
  assert_file_not_contains "$f" "@CERT@"
  assert_eq "$(readlink "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf")" "$f"
  assert_eq "$(grep 'nginx -t\|systemctl reload nginx' "$TEST_TMP/calls" | tr '\n' '|')" "nginx -t|systemctl reload nginx|"
}
test_webproxy_site_with_a_failing_nginx_test_is_unlinked_and_6() {
  wp_env; : > "$TEST_TMP/wp-fail"
  assert_eq "$(dxb_webproxy_site pat 2> /dev/null; echo $?)" "6"
  [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf && ! -L $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "a failing site is still enabled"
  assert_file_not_contains "$TEST_TMP/calls" "systemctl reload nginx"
}
test_webproxy_site_rejects_a_bad_name_and_a_missing_template() {
  wp_env
  assert_eq "$(dxb_webproxy_site '../x' 2> /dev/null; echo $?)" "6"
  assert_eq "$(dxb_webproxy_site nosuch 2> /dev/null; echo $?)" "6"
}
test_webproxy_site_remove_unlinks_and_reloads() {
  wp_env; AP_INSTALLED=nginx
  dxb_webproxy_site pat
  : > "$TEST_TMP/calls"
  assert_ok dxb_webproxy_site_remove pat
  [[ ! -e $DXB_NGINX_DIR/sites-available/dxberry-pat.conf && ! -L $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "site files left"
  assert_file_contains "$TEST_TMP/calls" "systemctl reload nginx"
  assert_ok dxb_webproxy_site_remove pat
}
# a refresh whose reload fails leaves the site looking stale, so the next run tries again (a
# renewed certificate is never taken as picked up before nginx has loaded it)
test_webproxy_refresh_marks_the_site_current_only_after_a_reload() (
  wp_env
  wp_reload_fails() { echo "systemctl $*" >> "$TEST_TMP/calls"; [[ $1 != reload ]]; }
  local avail=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf
  assert_ok dxb_webproxy_site pat
  touch -d '-2 hours' "$avail"; touch -d '-1 hour' "$DXB_COCKPIT_CERTS/0-self-signed.cert"
  assert_ok dxb_webproxy_site_stale pat
  DXB_SYSTEMCTL=wp_reload_fails
  dxb_webproxy_site_refresh pat 2> /dev/null; assert_eq "$?" "6"
  assert_ok dxb_webproxy_site_stale pat
  DXB_SYSTEMCTL=fx_systemctl
  assert_ok dxb_webproxy_site_refresh pat
  dxb_webproxy_site_stale pat; assert_eq "$?" "1"
)
