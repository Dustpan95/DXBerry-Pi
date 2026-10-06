#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"
source "$DXB_LIB/settings.sh"
source "$DXB_LIB/update.sh"

# up_env: a Pi on DXBerry 0.3.0-rc3 with Graywolf 0.14.13; GitHub, Graywolf's releases and apt
# answer from files under $TEST_TMP (nothing reaches the network).
up_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_RUN_DIR=$TEST_TMP/run \
    DXB_UPDATE_CACHE=$TEST_TMP/state/update-check.json DXB_UPDATE_CONF=$TEST_TMP/state/update.conf \
    DXB_UPDATE_JOB_FILE=$TEST_TMP/run/update-job.json DXB_UPDATE_RESULT=$TEST_TMP/run/update-result.json \
    DXB_UPDATE_WORK=$TEST_TMP/state/update-work DXB_OPT=$TEST_TMP/opt/dxberry DXB_SBIN=$TEST_TMP/sbin \
    DXB_RELEASE_FILE=$TEST_TMP/etc/dxberry-release DXB_REBOOT_FLAG=$TEST_TMP/run/reboot-required \
    DXB_UPDATE_CURL=up_curl DXB_CURL=up_curl DXB_APT_GET=up_apt DXB_GW_RELEASES=http://gw-rel DXB_DPKG_ARCH=arm64 \
    DXB_UPDATE_API=http://api/releases DXB_BOOT_DIR=$TEST_TMP/boot DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR" "$TEST_TMP/etc" "$TEST_TMP/http" "$DXB_SBIN" "$DXB_BOOT_DIR"
  printf 'DXBERRY_VERSION=0.3.0-rc3\nDXBERRY_COMMIT=abc1234\nDXBERRY_BUILD_DATE=2026-10-06T00:00:00Z\n' > "$DXB_RELEASE_FILE"
  printf 'PASSWORD=<applied>\n' > "$DXB_BOOT_DIR/dxberry.txt"
  printf 'aaaa  graywolf_0.14.14_arm64.deb\nbbbb  graywolf_0.14.14_amd64.deb\n' > "$TEST_TMP/http/checksums.txt"
  # newest first, as GitHub lists them: an rc with an update file, an rc without, a full release without
  jq -n '[
    {tag_name: "v0.3.0-rc4", prerelease: true, draft: false, assets: [
      {name: "DXBerry-Pi-0.3.0-rc4-rpi234-arm64.img.xz", browser_download_url: "http://files/DXBerry-Pi-0.3.0-rc4-rpi234-arm64.img.xz"},
      {name: "dxberry-pi-0.3.0-rc4.tar.gz", browser_download_url: "http://files/dxberry-pi-0.3.0-rc4.tar.gz"},
      {name: "dxberry-pi-0.3.0-rc4.tar.gz.sha256", browser_download_url: "http://files/dxberry-pi-0.3.0-rc4.tar.gz.sha256"}]},
    {tag_name: "v0.3.0-rc3", prerelease: true, draft: false, assets: [
      {name: "DXBerry-Pi-0.3.0-rc3-rpi234-arm64.img.xz", browser_download_url: "http://files/x.img.xz"}]},
    {tag_name: "v0.2.1", prerelease: false, draft: false, assets: [
      {name: "DXBerry-Pi-0.2.1-rpi234-arm64.img.xz", browser_download_url: "http://files/y.img.xz"}]}
  ]' > "$TEST_TMP/http/releases.json"
  printf 'Inst libfoo1 [1.0] (1.1 Debian:13/stable [arm64])\nInst bar [2] (3 Debian:13/stable [all])\nConf libfoo1 (1.1)\n' > "$TEST_TMP/apt-sim"
  : > "$TEST_TMP/calls"
}
# up_curl ARGS...: curl for the tests - the URL's last path part is a file under $TEST_TMP/http
# (the releases API answers from releases.json); -o FILE writes it there; a missing file is curl's 22.
up_curl() {
  local url='' out='' f
  while (( $# )); do case $1 in -o) out=$2; shift ;; http*) url=$1 ;; esac; shift; done
  echo "curl $url" >> "$TEST_TMP/calls"
  if [[ $url == "$DXB_UPDATE_API" ]]; then f=$TEST_TMP/http/releases.json; else f=$TEST_TMP/http/${url##*/}; fi
  [[ -f $f ]] || return 22
  if [[ -n $out ]]; then cp "$f" "$out"; else cat "$f"; fi
}
# up_apt ARGS...: apt-get for the tests - records the call; -s prints $TEST_TMP/apt-sim; exits with $TEST_TMP/apt-rc (0).
up_apt() {
  echo "apt-get $*" >> "$TEST_TMP/calls"
  if [[ " $* " == *" -s "* ]]; then cat "$TEST_TMP/apt-sim"; fi
  return "$(cat "$TEST_TMP/apt-rc" 2> /dev/null || echo 0)"
}
up_cfg() { dxb_config_load "$DXB_BOOT_DIR/dxberry.txt" > /dev/null 2>&1; dxb_config_validate > /dev/null 2>&1; }

test_update_newer_orders_release_candidates_before_their_release() {
  assert_ok dxb_update_newer 0.3.0-rc3 0.3.0-rc4
  assert_ok dxb_update_newer 0.3.0-rc4 0.3.0
  assert_ok dxb_update_newer v0.2.1 0.3.0-rc1
  assert_fails dxb_update_newer 0.3.0 0.3.0-rc4
  assert_fails dxb_update_newer 0.3.0 0.3.0
  assert_fails dxb_update_newer 0.3.0 ''
  assert_ok dxb_update_newer '' 0.3.0
}

test_update_prereleases_setting() {
  up_env
  assert_eq "$(dxb_update_prereleases)" "off"
  assert_ok dxb_update_set_prereleases on
  assert_eq "$(dxb_update_prereleases)" "on"
  dxb_update_set_prereleases maybe; assert_eq "$?" "2"
  assert_eq "$(dxb_update_prereleases)" "on"
}

test_update_dxberry_info_offers_only_releases_with_an_update_file() {
  local j
  up_env
  j=$(dxb_update_dxberry_info)
  # pre-releases off: the only full release (0.2.1) has no update file
  assert_eq "$(jq -c '{installed, latest, update, include_prereleases}' <<< "$j")" '{"installed":"0.3.0-rc3","latest":null,"update":false,"include_prereleases":false}'
  dxb_update_set_prereleases on
  j=$(dxb_update_dxberry_info)
  assert_eq "$(jq -c '{latest, prerelease, update, name}' <<< "$j")" '{"latest":"0.3.0-rc4","prerelease":true,"update":true,"name":"dxberry-pi-0.3.0-rc4.tar.gz"}'
  assert_eq "$(jq -r '.sha_url' <<< "$j")" "http://files/dxberry-pi-0.3.0-rc4.tar.gz.sha256"
  # the newest is what is installed: nothing to do
  sed -i 's/0.3.0-rc3/0.3.0-rc4/' "$DXB_RELEASE_FILE"
  assert_eq "$(dxb_update_dxberry_info | jq -r '.update')" "false"
  rm "$TEST_TMP/http/releases.json"
  dxb_update_dxberry_info > /dev/null 2>&1; assert_eq "$?" "1"
}

# A release created later but numbered lower (a back-port, a re-tag) must not win just because
# GitHub lists it first: the highest version among the candidates is "latest", not .[0] of the
# listing order (creation date).
test_update_dxberry_info_picks_the_highest_version_not_the_listing_order() {
  local j
  up_env
  jq -n '[
    {tag_name: "v0.3.0-rc2", prerelease: true, draft: false, assets: [
      {name: "dxberry-pi-0.3.0-rc2.tar.gz", browser_download_url: "http://files/dxberry-pi-0.3.0-rc2.tar.gz"},
      {name: "dxberry-pi-0.3.0-rc2.tar.gz.sha256", browser_download_url: "http://files/dxberry-pi-0.3.0-rc2.tar.gz.sha256"}]},
    {tag_name: "v0.3.0-rc4", prerelease: true, draft: false, assets: [
      {name: "dxberry-pi-0.3.0-rc4.tar.gz", browser_download_url: "http://files/dxberry-pi-0.3.0-rc4.tar.gz"},
      {name: "dxberry-pi-0.3.0-rc4.tar.gz.sha256", browser_download_url: "http://files/dxberry-pi-0.3.0-rc4.tar.gz.sha256"}]}
  ]' > "$TEST_TMP/http/releases.json"
  dxb_update_set_prereleases on
  j=$(dxb_update_dxberry_info)
  assert_eq "$(jq -r '.latest' <<< "$j")" "0.3.0-rc4"
  assert_eq "$(jq -r '.name' <<< "$j")" "dxberry-pi-0.3.0-rc4.tar.gz"
}

test_update_graywolf_info_reports_latest_and_the_pin() {
  local j
  up_env
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; up_cfg; dxb_update_graywolf_info )
  assert_eq "$j" '{"installed":"0.14.13","latest":"0.14.14","pinned":null,"update":true}'
  printf 'PASSWORD=<applied>\nGRAYWOLF_VERSION=v0.14.13\n' > "$DXB_BOOT_DIR/dxberry.txt"
  printf 'aaaa  graywolf_0.14.13_arm64.deb\n' > "$TEST_TMP/http/checksums.txt"
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; up_cfg; dxb_update_graywolf_info )
  assert_eq "$(jq -c '{pinned, update}' <<< "$j")" '{"pinned":"v0.14.13","update":false}'
  assert_contains "$(cat "$TEST_TMP/calls")" "curl http://gw-rel/download/v0.14.13/checksums.txt"
}

test_update_system_info_counts_the_upgrades() {
  up_env
  assert_eq "$(dxb_update_system_info)" '{"count":2,"packages":["libfoo1","bar"]}'
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get update"
}

test_update_check_caches_and_keeps_the_live_parts_live() {
  local j
  up_env
  dxb_update_set_prereleases on
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; dxb_update_check )
  assert_eq "$(jq -r '.cached, .graywolf.update, .dxberry.update, .system.count, .reboot_required' <<< "$j" | tr '\n' ' ')" "false true true 2 false "
  assert_eq "$(jq -r 'has("cached") or has("reboot_required") or has("rollback")' "$DXB_UPDATE_CACHE")" "false"
  : > "$TEST_TMP/calls"; touch "$DXB_REBOOT_FLAG"
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; dxb_update_check )
  assert_eq "$(jq -r '.cached, .reboot_required' <<< "$j" | tr '\n' ' ')" "true true "
  assert_eq "$(cat "$TEST_TMP/calls")" ""
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; dxb_update_check refresh )
  assert_eq "$(jq -r '.cached' <<< "$j")" "false"
  # an old cache is refreshed
  jq '.checked_at = 1' "$DXB_UPDATE_CACHE" > "$DXB_UPDATE_CACHE.n" && mv "$DXB_UPDATE_CACHE.n" "$DXB_UPDATE_CACHE"
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; dxb_update_check )
  assert_eq "$(jq -r '.cached' <<< "$j")" "false"
  # a part that cannot be read says so; the rest still answers
  rm "$TEST_TMP/http/checksums.txt"
  j=$( dpkg-query() { printf 'installed 0.14.13\n'; }; dxb_update_check refresh )
  assert_contains "$(jq -r '.graywolf.error' <<< "$j")" "Graywolf"
  assert_eq "$(jq -r '.dxberry.update' <<< "$j")" "true"
}

test_update_rollback_info() {
  up_env
  assert_eq "$(dxb_update_rollback_info)" '{"available":false,"version":null}'
  mkdir -p "$DXB_OPT.prev/bin"; echo 0.3.0-rc2 > "$DXB_OPT.prev/VERSION"; : > "$DXB_OPT.prev/bin/dxberry-provision"; chmod +x "$DXB_OPT.prev/bin/dxberry-provision"
  assert_eq "$(dxb_update_rollback_info)" '{"available":true,"version":"0.3.0-rc2"}'
}

# up_tree DIR VERSION: an installed DXBerry tree (the repo's provision/) at DIR claiming VERSION.
up_tree() { mkdir -p "$1" && cp -r "$DXB_ROOT/provision/." "$1/" && echo "$2" > "$1/VERSION"; }
# up_provision_stub: an EXECUTABLE stand-in for the new tree's dxberry-provision recording how it ran.
up_provision_stub() {
  # shellcheck disable=SC2016  # single-quoted on purpose: expands when the generated stub script runs, not here
  printf '#!/bin/bash\necho "LIB=$DXB_LIB LOG=$DXB_LOG_FILE UPGRADE=$DXB_GW_UPGRADE TTY=$DXB_TTY UMASK=$(umask)" >> %q\nexit "$(cat %q 2> /dev/null || echo 0)"\n' \
    "$TEST_TMP/provision-ran" "$TEST_TMP/provision-rc" > "$TEST_TMP/provision-stub"
  chmod +x "$TEST_TMP/provision-stub"
  export DXB_UPDATE_PROVISION=$TEST_TMP/provision-stub DXB_PROVISION_LOG=$TEST_TMP/state/provision.log
}
# up_release_file VERSION: build the real update file for VERSION and serve it.
up_release_file() { "$DXB_ROOT/build/make-update-tarball.sh" "$1" "$TEST_TMP/http" > /dev/null || _fail "could not build the update file"; }

test_update_apply_dxberry_swaps_the_tree_and_runs_the_new_setup() {
  up_env; up_provision_stub
  up_tree "$DXB_OPT" 0.3.0-rc3
  dxb_update_set_prereleases on
  up_release_file 0.3.0-rc4
  assert_ok dxb_update_apply_dxberry > "$TEST_TMP/out" 2>&1
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc4"
  assert_eq "$(cat "$DXB_OPT.prev/VERSION")" "0.3.0-rc3"
  assert_file_contains "$DXB_RELEASE_FILE" "DXBERRY_VERSION=0.3.0-rc4"
  [[ -L $DXB_SBIN/dxberry-config && -L $DXB_SBIN/dxberry-status ]] || _fail "the commands must be linked"
  [[ -e $DXB_SBIN/dxberry-preboot ]] && _fail "dxberry-preboot is never linked"
  assert_eq "$(cat "$TEST_TMP/provision-ran")" "LIB=$DXB_OPT/lib LOG=$TEST_TMP/state/provision.log UPGRADE=0 TTY=/dev/null UMASK=0022"
  [[ -d $DXB_OPT.new ]] && _fail "the staging tree must be gone"
  # nothing newer: nothing to do
  : > "$TEST_TMP/provision-ran"
  assert_ok dxb_update_apply_dxberry > /dev/null 2>&1
  assert_eq "$(cat "$TEST_TMP/provision-ran")" ""
}

test_update_apply_dxberry_changes_nothing_on_a_bad_download() {
  up_env; up_provision_stub
  up_tree "$DXB_OPT" 0.3.0-rc3
  dxb_update_set_prereleases on
  up_release_file 0.3.0-rc4
  echo "0000000000000000000000000000000000000000000000000000000000000000  dxberry-pi-0.3.0-rc4.tar.gz" > "$TEST_TMP/http/dxberry-pi-0.3.0-rc4.tar.gz.sha256"
  dxb_update_apply_dxberry > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/out")" "checksum"
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc3"
  [[ -e $DXB_OPT.prev || -e $DXB_OPT.new ]] && _fail "a refused update leaves no other tree"
  # an archive without a provisioner is refused too
  mkdir -p "$TEST_TMP/bad/dxberry"; echo 0.3.0-rc4 > "$TEST_TMP/bad/dxberry/VERSION"
  tar -czf "$TEST_TMP/http/dxberry-pi-0.3.0-rc4.tar.gz" -C "$TEST_TMP/bad" dxberry
  ( cd "$TEST_TMP/http" && sha256sum dxberry-pi-0.3.0-rc4.tar.gz > dxberry-pi-0.3.0-rc4.tar.gz.sha256 )
  dxb_update_apply_dxberry > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc3"
  assert_eq "$(cat "$TEST_TMP/provision-ran" 2> /dev/null)" ""
}

test_update_apply_dxberry_reports_a_failed_setup_run() {
  up_env; up_provision_stub
  up_tree "$DXB_OPT" 0.3.0-rc3
  dxb_update_set_prereleases on
  up_release_file 0.3.0-rc4
  echo 1 > "$TEST_TMP/provision-rc"
  dxb_update_apply_dxberry > /dev/null 2>&1; assert_eq "$?" "8"
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc4"
}

test_update_rollback_swaps_back_and_forth() {
  up_env; up_provision_stub
  up_tree "$DXB_OPT" 0.3.0-rc4
  up_tree "$DXB_OPT.prev" 0.3.0-rc3
  assert_ok dxb_update_apply_rollback > /dev/null 2>&1
  assert_eq "$(cat "$DXB_OPT/VERSION") $(cat "$DXB_OPT.prev/VERSION")" "0.3.0-rc3 0.3.0-rc4"
  assert_file_contains "$DXB_RELEASE_FILE" "DXBERRY_VERSION=0.3.0-rc3"
  assert_file_contains "$DXB_RELEASE_FILE" "DXBERRY_COMMIT=unknown"
  assert_contains "$(cat "$TEST_TMP/provision-ran")" "LIB=$DXB_OPT/lib"
  assert_ok dxb_update_apply_rollback > /dev/null 2>&1
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc4"
  rm -rf "$DXB_OPT.prev"
  dxb_update_apply_rollback > /dev/null 2>&1; assert_eq "$?" "6"
  assert_eq "$(cat "$DXB_OPT/VERSION")" "0.3.0-rc4"
}

test_update_apply_graywolf_installs_rebuilds_and_restarts() {
  up_env
  (
    dpkg-query() { if [[ -f $TEST_TMP/gw-new ]]; then printf 'installed 0.14.14\n'; else printf 'installed 0.14.13\n'; fi; }
    dxb_gw_install() { echo "install UPGRADE=${DXB_GW_UPGRADE:-unset}" >> "$TEST_TMP/calls"; touch "$TEST_TMP/gw-new"; }
    dxb_gw_history_in_ram() { echo "history drop-in" >> "$TEST_TMP/calls"; }
    systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
    dxb_update_apply_graywolf > /dev/null 2>&1 || exit 1
  ) || _fail "the Graywolf update failed"
  assert_eq "$(tr '\n' '|' < "$TEST_TMP/calls")" "install UPGRADE=1|history drop-in|systemctl restart graywolf.service|"
  # pinned: nothing installed
  printf 'PASSWORD=<applied>\nGRAYWOLF_VERSION=v0.14.13\n' > "$DXB_BOOT_DIR/dxberry.txt"; : > "$TEST_TMP/calls"
  (
    dxb_gw_install() { echo install >> "$TEST_TMP/calls"; }
    dxb_update_apply_graywolf > "$TEST_TMP/out" 2>&1
  )
  assert_eq "$(cat "$TEST_TMP/calls")" ""
  assert_contains "$(cat "$TEST_TMP/out")" "pinned"
}

test_update_apply_system_upgrades_without_questions() {
  up_env
  assert_ok dxb_update_apply_system > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get update"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get -y --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade"
  echo 100 > "$TEST_TMP/apt-rc"
  dxb_update_apply_system > /dev/null 2>&1; assert_eq "$?" "8"
}
