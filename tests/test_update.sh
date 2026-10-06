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
