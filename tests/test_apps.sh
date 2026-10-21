#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2016,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"

# ap_module NAME BODY: a program module NAME in a directory of its own (DXB_APPS_DIR points there).
# Run the test's body in a subshell: the module's functions must not outlive the test.
ap_module() {
  export DXB_APPS_DIR=$TEST_TMP/mods
  mkdir -p "$DXB_APPS_DIR"
  printf '%s\n' "$2" > "$DXB_APPS_DIR/$1.sh"
}

test_apps_tsv_has_every_program_once() {
  fx_apps_env
  assert_eq "$(dxb_apps_rows | jq -r 'map(.name) | join(" ")')" "graywolf wsjtx js8call fldigi qsstv pat flrig wavelog"
  assert_eq "$(dxb_apps_row js8call | jq -c .packages)" '["libxcb-cursor0","pulseaudio"]'
  dxb_apps_row nope > /dev/null 2>&1; assert_eq "$?" "3"
}

test_apps_rows_carry_every_column() {
  fx_apps_env
  assert_eq "$(dxb_apps_row flrig | jq -c .)" \
    '{"name":"flrig","label":"flrig","kind":"rigcontrol","source":"apt:flrig","packages":[],"description":"Rig control with your radio'\''s full front panel"}'
  # no nginx: it comes only through dxb_webproxy_install, inside its policy-rc.d guard (Task 12)
  assert_eq "$(dxb_apps_row wavelog | jq -r '.packages | join(" ")')" "php-fpm php-mysql php-curl php-mbstring php-xml php-zip php-gd mariadb-server"
  assert_eq "$(dxb_apps_rows | jq -r 'map(.name) | unique | length')" "8"
}

test_apps_source_parses_every_kind() {
  fx_apps_env
  assert_eq "$(_dxb_apps_source js8call | jq -c '{type, repo, patterns: (.patterns | map(.type))}')" \
    '{"type":"github","repo":"JS8Call-improved/JS8Call-improved","patterns":["deb","appimage"]}'
  assert_eq "$(_dxb_apps_source js8call | jq -r '.patterns[0].re')" '^js8call_[0-9.]+_arm64\.deb$'
  assert_eq "$(_dxb_apps_source fldigi | jq -c '{type, package}')" '{"type":"apt","package":"fldigi"}'
  assert_eq "$(_dxb_apps_source wavelog | jq -c '{type, repo}')" '{"type":"tag","repo":"wavelog/wavelog"}'
  assert_eq "$(_dxb_apps_source graywolf | jq -r .type)" "builtin"
}

test_apps_latest_prefers_deb_then_appimage_and_reads_checksums() {
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.3 JS8Call-v3.0.3-aarch64.AppImage
  assert_eq "$(dxb_apps_latest js8call | jq -c '{version, type, asset, sha256}')" \
    '{"version":"3.0.3","type":"appimage","asset":"JS8Call-v3.0.3-aarch64.AppImage","sha256":null}'
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb wsjtx-3.0.2-linux-x86_64.deb SHA256SUMS
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(printf a%.0s {1..64})" > "$TEST_TMP/http/SHA256SUMS"
  assert_eq "$(dxb_apps_latest wsjtx | jq -r '.type + " " + (.sha256 | .[0:4])')" "deb aaaa"
}

test_apps_latest_takes_the_deb_when_both_are_published() {
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.2 JS8Call-v3.0.2-aarch64.AppImage js8call_3.0.2_arm64.deb js8call_3.0.2_arm64.deb.sha256
  printf '%s *js8call_3.0.2_arm64.deb\n' "$(printf B%.0s {1..64})" > "$TEST_TMP/http/js8call_3.0.2_arm64.deb.sha256"
  assert_eq "$(dxb_apps_latest js8call | jq -c '{version, type, asset, url}')" \
    '{"version":"3.0.2","type":"deb","asset":"js8call_3.0.2_arm64.deb","url":"http://dl/js8call_3.0.2_arm64.deb"}'
  # a checksum is compared in lowercase whatever case it was published in
  assert_eq "$(dxb_apps_latest js8call | jq -r '.sha256 | .[0:4]')" "bbbb"
}

test_apps_latest_tag_apt_and_unreadable() {
  fx_apps_env
  ap_release wavelog/wavelog 2.1.2
  assert_eq "$(dxb_apps_latest wavelog | jq -c .)" \
    '{"version":"2.1.2","asset":"wavelog-2.1.2.tar.gz","type":"tarball","url":"https://github.com/wavelog/wavelog/archive/refs/tags/2.1.2.tar.gz","sha256":null}'
  AP_CANDIDATE=1:4.2.06+ds-1
  assert_eq "$(dxb_apps_latest fldigi | jq -c '{version, type, asset}')" '{"version":"1:4.2.06+ds-1","type":"apt","asset":"fldigi"}'
  AP_CANDIDATE=''
  dxb_apps_latest fldigi > /dev/null 2>&1; assert_eq "$?" "1"
  # no release list, or a release with no asset for this Pi
  dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-x86_64.deb
  dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
  # a checksum file that is published but cannot be read is not "no checksum"
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb SHA256SUMS
  dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
  dxb_apps_latest graywolf > /dev/null 2>&1; assert_eq "$?" "1"
  assert_eq "$(tr '\n' ' ' < "$TEST_TMP/curl-calls")" \
    "curl http://gh/wavelog/wavelog/releases/latest curl http://gh/WSJTX/wsjtx/releases/latest curl http://gh/WSJTX/wsjtx/releases/latest curl http://gh/WSJTX/wsjtx/releases/latest curl http://dl/SHA256SUMS "
}

test_apps_install_upstream_deb_verifies_then_installs_with_apt() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  assert_ok dxb_apps_install wsjtx > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/out")" "no checksum published for wsjtx-3.0.2-linux-aarch64.deb"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends --no-upgrade tigervnc-standalone-server openbox socat"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends $DXB_APPS_WORK/wsjtx-3.0.2-linux-aarch64.deb"
}

test_apps_install_checks_the_published_checksum_and_cleans_up() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb SHA256SUMS
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(sha256sum < "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb" | cut -d' ' -f1)" > "$TEST_TMP/http/SHA256SUMS"
  assert_ok dxb_apps_install wsjtx > "$TEST_TMP/out"
  assert_not_contains "$(cat "$TEST_TMP/out")" "no checksum"
  assert_contains "$(cat "$TEST_TMP/out")" "checksum ok"
  # every apt call waits out a busy lock, and the program's packages come before its file
  assert_eq "$(grep -c 'DPkg::Lock::Timeout=120' "$TEST_TMP/calls")" "$(grep -c '^apt-get' "$TEST_TMP/calls")"
  assert_eq "$(grep '^apt-get install' "$TEST_TMP/calls" | tail -1 | grep -c 'aarch64.deb')" "1"
  [[ ! -e $DXB_APPS_WORK/wsjtx-3.0.2-linux-aarch64.deb ]] || _fail "the download was left behind"
}

test_apps_install_refuses_a_file_that_fails_its_checksum() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb SHA256SUMS
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(printf 0%.0s {1..64})" > "$TEST_TMP/http/SHA256SUMS"
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "aarch64.deb"   # never handed to apt
}

test_apps_install_download_or_apt_failure_changes_nothing() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/out")" "download"
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  echo --no-upgrade > "$TEST_TMP/apt-fail-on"
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "aarch64.deb"
  dxb_apps_install nope > /dev/null 2>&1; assert_eq "$?" "3"
}

test_apps_install_debian_program_and_update_mode() {
  fx_apps_env
  assert_ok dxb_apps_install fldigi > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends --no-upgrade tigervnc-standalone-server openbox socat xauth pulseaudio fldigi"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "only-upgrade"
  assert_eq "$(cat "$TEST_TMP/curl-calls")" ""
  # update mode (Ruling A): the program's own package is upgraded, the shared ones only kept
  : > "$TEST_TMP/calls"; AP_INSTALLED=fldigi
  assert_ok dxb_apps_install fldigi update > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends --only-upgrade fldigi"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "xauth pulseaudio fldigi"
  # nothing to upgrade when it is not installed
  : > "$TEST_TMP/calls"; AP_INSTALLED=''
  dxb_apps_install fldigi update > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "only-upgrade"
}

test_apps_install_service_skips_the_session_packages() {
  fx_apps_env
  # a stand-in module: the real one's set-up (database, web site) is test_app_wavelog.sh's
  ap_module wavelog 'app_wavelog_kind() { echo service; }'
  ap_release wavelog/wavelog 2.1.2
  mkdir -p "$TEST_TMP/src/wavelog-2.1.2"; echo '<?php' > "$TEST_TMP/src/wavelog-2.1.2/index.php"
  mkdir -p "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags"
  tar -czf "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags/2.1.2.tar.gz" -C "$TEST_TMP/src" wavelog-2.1.2
  assert_ok dxb_apps_install wavelog > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends --no-upgrade php-fpm"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "tigervnc"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/VERSION")" "2.1.2"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/index.php")" "<?php"
}

test_apps_install_tarball_keeps_what_the_module_names() {
  (
    fx_apps_env
    ap_module wavelog 'app_wavelog_kind() { echo service; }
app_wavelog_keep() { printf "%s\n" application/config/config.php userdata; }'
    mkdir -p "$DXB_APPS_OPT/wavelog/application/config" "$DXB_APPS_OPT/wavelog/userdata"
    echo mine > "$DXB_APPS_OPT/wavelog/application/config/config.php"; echo pic > "$DXB_APPS_OPT/wavelog/userdata/x"
    echo old > "$DXB_APPS_OPT/wavelog/index.php"
    ap_release wavelog/wavelog 2.1.3
    mkdir -p "$TEST_TMP/src/wavelog-2.1.3/application/config" "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags"
    echo new > "$TEST_TMP/src/wavelog-2.1.3/index.php"; echo stock > "$TEST_TMP/src/wavelog-2.1.3/application/config/config.php"
    tar -czf "$TEST_TMP/http/wavelog/wavelog/archive/refs/tags/2.1.3.tar.gz" -C "$TEST_TMP/src" wavelog-2.1.3
    assert_ok dxb_apps_install wavelog update > /dev/null
    assert_eq "$(cat "$DXB_APPS_OPT/wavelog/index.php")" "new"
    assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/config.php")" "mine"
    assert_eq "$(cat "$DXB_APPS_OPT/wavelog/userdata/x")" "pic"
    assert_eq "$(dxb_apps_version wavelog)" "2.1.3"
    # removing it keeps the same files, and counts it as not installed
    assert_ok dxb_apps_remove wavelog > /dev/null
    assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/config.php")" "mine"
    assert_eq "$(cat "$DXB_APPS_OPT/wavelog/userdata/x")" "pic"
    [[ ! -e $DXB_APPS_OPT/wavelog/index.php ]] || _fail "the program's own files were kept"
    assert_fails dxb_apps_installed wavelog
  )
}

test_apps_install_appimage_unpacks_and_replaces_the_old_deb() {
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.3 JS8Call-v3.0.3-aarch64.AppImage
  echo appimage > "$TEST_TMP/http/JS8Call-v3.0.3-aarch64.AppImage"
  AP_INSTALLED=js8call=3.0.2
  assert_ok dxb_apps_install js8call update > "$TEST_TMP/out"
  [[ -f $DXB_APPS_OPT/js8call/squashfs-root/AppRun ]] || _fail "the AppImage was not unpacked into $DXB_APPS_OPT/js8call"
  assert_eq "$(cat "$DXB_APPS_OPT/js8call/VERSION")" "3.0.3"
  # the 3.0.2 package would otherwise still be what runs
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get remove -y js8call"
  AP_INSTALLED=''
  assert_ok dxb_apps_installed js8call
  assert_eq "$(dxb_apps_version js8call)" "3.0.3"
}

# A downloaded AppImage never runs as root: it unpacks itself as dxbapps in a directory of
# dxbapps' own, and root then takes the tree over (no setuid/setgid bits, owned by root).
test_apps_install_appimage_unpacks_as_dxbapps_never_as_root() {
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.3 JS8Call-v3.0.3-aarch64.AppImage
  echo appimage > "$TEST_TMP/http/JS8Call-v3.0.3-aarch64.AppImage"
  assert_ok dxb_apps_install js8call > "$TEST_TMP/out"
  assert_eq "$(grep -c -- '--appimage-extract' "$TEST_TMP/calls")" "$(grep -c '^runuser -u dxbapps -- env HOME=.*--appimage-extract' "$TEST_TMP/calls")"
  assert_contains "$(cat "$TEST_TMP/calls")" "chown -R root:root $DXB_APPS_OPT/.js8call.new/squashfs-root"
  assert_eq "$(stat -c %a "$DXB_APPS_OPT/js8call/squashfs-root/helper")" "755"
  assert_eq "$(stat -c %a "$DXB_APPS_OPT/js8call/squashfs-root/AppRun")" "755"
  [[ ! -e $DXB_APPS_WORK ]] || _fail "the work directory was left behind"
}

# On the Pi the state directory is 0700 root, so dxbapps unpacks in a fresh directory of its own
# outside it (DXB_APPS_EXTRACT_BASE), with that directory as HOME (never /root); the directory is
# gone afterwards, also when the unpacking fails.
test_apps_appimage_unpacks_outside_the_state_dir_with_its_own_home() {
  local line x
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.3 JS8Call-v3.0.3-aarch64.AppImage
  echo appimage > "$TEST_TMP/http/JS8Call-v3.0.3-aarch64.AppImage"
  assert_ok dxb_apps_install js8call > /dev/null
  line=$(grep -- '--appimage-extract' "$TEST_TMP/calls")
  [[ $line =~ ^runuser\ -u\ dxbapps\ --\ env\ HOME=([^ ]+)\ ([^ ]+)\ --appimage-extract$ ]] || { _fail "unexpected extraction call: $line"; return; }
  x=${BASH_REMATCH[1]}
  assert_eq "${BASH_REMATCH[2]}" "$x/JS8Call-v3.0.3-aarch64.AppImage"
  [[ $x == "$DXB_APPS_EXTRACT_BASE"/?* && $x != "$DXB_STATE_DIR"* ]] || _fail "extracted in $x"
  assert_contains "$(cat "$TEST_TMP/calls")" "chown -R dxbapps:dxbapps $x"
  [[ ! -e $x ]] || _fail "$x was left behind"
  [[ -f $DXB_APPS_OPT/js8call/squashfs-root/AppRun ]] || _fail "not unpacked into $DXB_APPS_OPT/js8call"
  # the download directory stays root's alone
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  dxb_apps_download "$(dxb_apps_latest wsjtx)" > /dev/null
  assert_eq "$(stat -c %a "$DXB_APPS_WORK")" "700"
  assert_eq "$(stat -c %a "$DXB_APPS_WORK/wsjtx-3.0.2-linux-aarch64.deb")" "600"
  # a failed unpack leaves no directory behind either (8: the packages it needs went in)
  AP_RUNUSER_RC=1; rm -rf "$DXB_APPS_OPT/js8call"
  dxb_apps_install js8call > "$TEST_TMP/out" 2>&1; assert_eq "$?" "8"
  assert_contains "$(cat "$TEST_TMP/out")" "JS8Call-v3.0.3-aarch64.AppImage could not be unpacked"
  assert_eq "$(ls -A "$DXB_APPS_EXTRACT_BASE")" ""
}

test_apps_https_downloads_never_follow_a_redirect_to_http() {
  fx_apps_env; DXB_GH_API=https://gh
  mkdir -p "$TEST_TMP/http/WSJTX/wsjtx/releases"
  jq -n '{tag_name: "v3.0.2", assets: [{name: "wsjtx-3.0.2-linux-aarch64.deb", browser_download_url: "https://dl/wsjtx-3.0.2-linux-aarch64.deb"},
    {name: "SHA256SUMS", browser_download_url: "https://dl/SHA256SUMS"}]}' > "$TEST_TMP/http/WSJTX/wsjtx/releases/latest"
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(sha256sum < "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb" | cut -d' ' -f1)" > "$TEST_TMP/http/SHA256SUMS"
  assert_ok dxb_apps_install wsjtx > /dev/null
  assert_eq "$(grep -c 'https://' "$TEST_TMP/curl-args")" "3"
  assert_eq "$(grep -c -- '--proto =https --proto-redir =https .*https://' "$TEST_TMP/curl-args")" "3"
  # a plain http:// stand-in is fetched as it is
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  dxb_apps_latest wsjtx > /dev/null
  assert_not_contains "$(cat "$TEST_TMP/curl-args")" "--proto"
}

# ap_forged_release URL [SUMS_URL]: a WSJT-X release whose asset (and SHA256SUMS) download from URL.
ap_forged_release() {
  mkdir -p "$TEST_TMP/http/WSJTX/wsjtx/releases"
  jq -n --arg u "$1" --arg s "${2:-}" '{tag_name: "v3.0.2", assets: ([{name: "wsjtx-3.0.2-linux-aarch64.deb", browser_download_url: $u}]
    + (if $s == "" then [] else [{name: "SHA256SUMS", browser_download_url: $s}] end))}' > "$TEST_TMP/http/WSJTX/wsjtx/releases/latest"
}

test_apps_latest_refuses_urls_that_are_not_https() {
  local u
  fx_apps_env
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  for u in -o/etc/passwd file:///etc/passwd ' http://dl/x'; do
    ap_forged_release "$u"; : > "$TEST_TMP/curl-calls"
    dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
    dxb_apps_install wsjtx > /dev/null 2>&1; assert_eq "$?" "6"
    assert_eq "$(grep -vc '/releases/latest$' "$TEST_TMP/curl-calls")" "0"
  done
  # the checksum file's URL too
  ap_forged_release http://dl/wsjtx-3.0.2-linux-aarch64.deb file:///etc/passwd
  dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
  # the tests' http:// stand-in works only because the configured API is http://; against an
  # https:// API an http:// asset is refused
  ap_forged_release http://dl/wsjtx-3.0.2-linux-aarch64.deb
  assert_ok dxb_apps_latest wsjtx > /dev/null
  DXB_GH_API=https://gh
  dxb_apps_latest wsjtx > /dev/null 2>&1; assert_eq "$?" "1"
  ap_forged_release https://dl/wsjtx-3.0.2-linux-aarch64.deb
  assert_eq "$(dxb_apps_latest wsjtx | jq -r .url)" "https://dl/wsjtx-3.0.2-linux-aarch64.deb"
}

# 8, not 6, once something changed: the shared packages went in, then the program did not
test_apps_install_exit_8_when_packages_went_in_but_the_program_did_not() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  echo "$DXB_APPS_WORK/wsjtx-3.0.2-linux-aarch64.deb" > "$TEST_TMP/apt-fail-on"
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "8"
  # with every shared package already there, nothing changed: 6
  : > "$TEST_TMP/calls"; AP_INSTALLED='tigervnc-standalone-server openbox socat xauth'
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "--no-upgrade"
  # only the missing ones are asked for
  : > "$TEST_TMP/calls"; AP_INSTALLED='openbox'
  dxb_apps_install wsjtx > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "--no-upgrade tigervnc-standalone-server socat xauth -o"
}

test_apps_remove_exit_8_after_stopping_sessions() {
  fx_apps_env
  AP_INSTALLED=fldigi; echo remove > "$TEST_TMP/apt-fail-on"
  echo dxberry-screen@fldigi-radio2.service >> "$TEST_TMP/active"
  dxb_apps_remove fldigi > /dev/null 2>&1; assert_eq "$?" "8"
  : > "$TEST_TMP/active"; echo enabled > "$TEST_TMP/enabled-dxberry-screen@fldigi-radio1.service"
  dxb_apps_remove fldigi > /dev/null 2>&1; assert_eq "$?" "8"
}

test_apps_fetch_install_places_a_bare_binary() {
  fx_apps_env
  ap_release pflarue/ardop v1.0.4.1.3 ardopcf_arm_Linux_64 ardopcf_amd64_Linux_64
  echo elf > "$TEST_TMP/http/ardopcf_arm_Linux_64"
  assert_ok dxb_apps_fetch_install ardop 'github:pflarue/ardop:bin=^ardopcf_arm_Linux_64$' ardopcf > /dev/null
  assert_eq "$(stat -c %a "$DXB_APPS_OPT/ardop/ardopcf")" "755"
  assert_eq "$(cat "$DXB_APPS_OPT/ardop/VERSION")" "1.0.4.1.3"
  [[ ! -e $DXB_APPS_WORK/ardopcf_arm_Linux_64 ]] || _fail "the download was left behind"
}

test_apps_install_and_remove_refuse_graywolf() {
  fx_apps_env
  dxb_apps_install graywolf > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/out")" "Graywolf is part of DXBerry and is updated under Updates"
  dxb_apps_remove graywolf > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/out")" "Graywolf is part of DXBerry and is updated under Updates"
  assert_eq "$(cat "$TEST_TMP/calls")" ""
}

test_apps_install_and_remove_run_the_module_hooks() {
  (
    fx_apps_env
    ap_module fldigi 'app_fldigi_install_extra() { echo "extra install $*" >> "$TEST_TMP/calls"; }
app_fldigi_remove_extra() { echo "extra remove $*" >> "$TEST_TMP/calls"; }'
    assert_ok dxb_apps_install fldigi > /dev/null
    assert_contains "$(cat "$TEST_TMP/calls")" "extra install"
    AP_INSTALLED=fldigi
    assert_ok dxb_apps_remove fldigi > /dev/null
    assert_contains "$(cat "$TEST_TMP/calls")" "extra remove"
    # a failing extra step after the program went in is 8, not 6
    ap_module fldigi 'app_fldigi_install_extra() { return 1; }'
    unset -f app_fldigi_remove_extra
    dxb_apps_install fldigi > /dev/null 2>&1; assert_eq "$?" "8"
    # no module at all: install and remove still work
    rm -f "$DXB_APPS_DIR/fldigi.sh"
    assert_ok dxb_apps_install qsstv > /dev/null
  )
}

test_apps_remove_keeps_settings_and_marks() {
  fx_apps_env; mkdir -p "$DXB_APPS_HOME/.config" "$DXB_APPS_STATE/firstfill"; : > "$DXB_APPS_STATE/firstfill/fldigi-radio2"
  AP_INSTALLED=fldigi
  assert_ok dxb_apps_remove fldigi > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get remove -y fldigi"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "purge"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "remove -y pulseaudio"
  [[ -d $DXB_APPS_HOME/.config && -f $DXB_APPS_STATE/firstfill/fldigi-radio2 ]] || _fail "settings or marks were deleted"
}

test_apps_remove_stops_every_session_and_deletes_the_unpacked_program() {
  fx_apps_env
  mkdir -p "$DXB_APPS_OPT/js8call/squashfs-root"; echo 3.0.3 > "$DXB_APPS_OPT/js8call/VERSION"
  echo dxberry-screen@js8call-radio2.service >> "$TEST_TMP/active"
  assert_ok dxb_apps_remove js8call > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable dxberry-screen@js8call-radio1.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-screen@js8call-radio2.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable dxberry-screen@js8call-radio2.service"
  assert_eq "$(cat "$TEST_TMP/active")" ""
  [[ ! -e $DXB_APPS_OPT/js8call ]] || _fail "$DXB_APPS_OPT/js8call is still there"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "apt-get remove"   # no package was installed
  assert_fails dxb_apps_installed js8call
  assert_eq "$(dxb_apps_version js8call)" ""
  # a package that will not go is 6
  AP_INSTALLED=fldigi; echo remove > "$TEST_TMP/apt-fail-on"
  dxb_apps_remove fldigi > /dev/null 2>&1; assert_eq "$?" "6"
}

test_apps_installed_and_version_follow_the_source() {
  fx_apps_env
  assert_fails dxb_apps_installed fldigi
  assert_eq "$(dxb_apps_version fldigi)" ""
  AP_INSTALLED="fldigi=4.2.06+ds-1 wsjtx=3.0.2"
  assert_ok dxb_apps_installed fldigi
  assert_eq "$(dxb_apps_version fldigi)" "4.2.06+ds-1"
  assert_eq "$(dxb_apps_version wsjtx)" "3.0.2"
  assert_fails dxb_apps_installed wavelog
  mkdir -p "$DXB_APPS_OPT/wavelog"; echo 2.1.2 > "$DXB_APPS_OPT/wavelog/VERSION"
  assert_eq "$(dxb_apps_version wavelog)" "2.1.2"
  dxb_apps_installed nope 2> /dev/null; assert_eq "$?" "3"
  (
    ap_module graywolf 'app_graywolf_version() { echo 0.14.14; }'
    assert_eq "$(dxb_apps_version graywolf)" "0.14.14"
    assert_ok dxb_apps_installed graywolf
  )
}

test_apps_user_ensure_masks_desktop_sound_servers() {
  fx_apps_env
  assert_ok dxb_apps_user_ensure
  assert_contains "$(cat "$TEST_TMP/calls")" "useradd --system --home-dir $DXB_APPS_HOME --create-home --shell /usr/sbin/nologin --groups audio,dialout,dxbscreen dxbapps"
  assert_contains "$(cat "$TEST_TMP/calls")" "usermod -aG dxbscreen,dxbapps dietpi"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --global mask pulseaudio.socket pulseaudio.service"
}

test_apps_user_ensure_is_idempotent() {
  fx_apps_env
  assert_ok dxb_apps_user_ensure
  assert_contains "$(cat "$TEST_TMP/calls")" "groupadd --system dxbscreen"
  assert_eq "$(stat -c %a "$DXB_APPS_HOME")" "750"
  assert_contains "$(cat "$TEST_TMP/calls")" "chown dxbapps:dxbapps $DXB_APPS_HOME"
  : > "$TEST_TMP/calls"
  assert_ok dxb_apps_user_ensure
  assert_not_contains "$(cat "$TEST_TMP/calls")" "useradd"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "groupadd"
  # an existing dxbapps still gets its groups
  assert_contains "$(cat "$TEST_TMP/calls")" "usermod -aG audio,dialout,dxbscreen dxbapps"
  DXB_CONSOLE_USER=pi
  printf 'pi:x:1001:1001::/home/pi:/bin/bash\n' >> "$TEST_TMP/passwd"
  assert_ok dxb_apps_user_ensure
  assert_contains "$(cat "$TEST_TMP/calls")" "usermod -aG dxbscreen,dxbapps pi"
  assert_eq "$(dxb_console_user)" "pi"
  unset DXB_CONSOLE_USER
  assert_eq "$(dxb_console_user)" "dietpi"
}

test_apps_firstfill_marks() {
  fx_apps_env
  assert_fails dxb_apps_firstfill_done wsjtx radio2
  assert_ok dxb_apps_firstfill_mark wsjtx radio2
  assert_ok dxb_apps_firstfill_done wsjtx radio2
  assert_fails dxb_apps_firstfill_done wsjtx radio1
  [[ -f $DXB_APPS_STATE/firstfill/wsjtx-radio2 ]] || _fail "no mark file"
}

test_apps_station_strips_ssid_and_computes_grid() {
  fx_apps_env; DXB_CFG[CALLSIGN]=W0BTE-10; DXB_CFG[LATITUDE]=38.9; DXB_CFG[LONGITUDE]=-94.6
  assert_eq "$(dxb_apps_station)" '{"call":"W0BTE","grid":"EM28qv"}'
}

test_apps_station_grid_from_gps_else_empty() {
  fx_apps_env; DXB_CFG[CALLSIGN]=W0BTE
  assert_eq "$(dxb_apps_station)" '{"call":"W0BTE","grid":""}'
  printf '%s\n' '{"class":"DEVICES","devices":[{"path":"/dev/ttyACM0"}]}' \
    '{"class":"TPV","mode":3,"lat":39.1,"lon":-94.5}' > "$TEST_TMP/gpspipe.out"
  assert_eq "$(dxb_apps_station)" "{\"call\":\"W0BTE\",\"grid\":\"$(dxb_maidenhead 39.1 -94.5)\"}"
  DXB_CFG=()
  assert_eq "$(dxb_apps_station | jq -r .call)" ""
}

test_apps_wiring_maps_ptt_and_ports() {
  fx_apps_env
  assert_eq "$(dxb_apps_wiring radio2 | jq -c '{index, rigctld, card, device, ptt, control, flrig_port}')" \
    '{"index":1,"rigctld":"127.0.0.1:4534","card":"RADIO2","device":"plughw:CARD=RADIO2,DEV=0","ptt":"cat","control":"hamlib","flrig_port":12346}'
}

test_apps_wiring_carries_label_hash_and_every_ptt_method() {
  local m
  fx_apps_env
  assert_eq "$(dxb_apps_wiring radio1 | jq -c '{radio, label, index, ptt, flrig_port}')" \
    '{"radio":"radio1","label":"FT-818","index":0,"ptt":"vox","flrig_port":12345}'
  assert_eq "$(dxb_apps_wiring radio1 | jq -r .hash)" "$(dxb_radio_wire_hash "$(dxb_radio_get radio1)")"
  for m in rigctld:cat cm108:cat gpio:cat vox:vox digirig_tone:vox none:none; do
    DXB_RADIOS=$(jq -c --arg m "${m%%:*}" '.radios.radio1.ptt.method = $m | .radios.radio1.rig.control = "flrig"' <<< "$DXB_RADIOS")
    assert_eq "$(dxb_apps_wiring radio1 | jq -r '.ptt + " " + .control')" "${m#*:} flrig"
  done
  dxb_apps_wiring nope > /dev/null 2>&1; assert_eq "$?" "3"
}

