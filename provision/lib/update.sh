#!/bin/bash
# shellcheck shell=bash
# Updates (console spec section 11): what is installed against what is out, cached for six hours,
# and the jobs that update Graywolf, DXBerry and the system packages (Task 3).

: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_UPDATE_CACHE:=$DXB_STATE_DIR/update-check.json}"
: "${DXB_UPDATE_CONF:=$DXB_STATE_DIR/update.conf}"
: "${DXB_UPDATE_TTL_S:=21600}"
: "${DXB_UPDATE_API:=https://api.github.com/repos/Dustpan95/DXBerry-Pi/releases?per_page=30}"
: "${DXB_UPDATE_CURL:=curl}"
: "${DXB_DPKG:=dpkg}"
: "${DXB_APT_GET:=apt-get}"
: "${DXB_OPT:=/opt/dxberry}"
: "${DXB_RELEASE_FILE:=/etc/dxberry-release}"
: "${DXB_REBOOT_FLAG:=/run/reboot-required}"

# dxb_update_newer A B: 0 when version B is newer than A. dpkg's ordering, with the first "-"
# written as "~" so a release candidate sorts before its release (0.3.0-rc4 < 0.3.0).
dxb_update_newer() {
  local a=${1#v} b=${2#v}
  [[ -n $b ]] || return 1
  [[ -n $a ]] || return 0
  # the backslash matters: an unescaped ~ in the replacement is tilde-expanded to $HOME
  "$DXB_DPKG" --compare-versions "${a/-/\~}" lt "${b/-/\~}" 2> /dev/null
}

dxb_update_prereleases() {
  local v
  v=$(sed -n 's/^PRERELEASES=//p' "$DXB_UPDATE_CONF" 2> /dev/null | head -1)
  [[ $v == on ]] && echo on || echo off
}

# dxb_update_set_prereleases on|off: 0 written, 2 not on/off, 6 not written.
dxb_update_set_prereleases() {
  [[ $1 == on || $1 == off ]] || return 2
  mkdir -p "$(dirname "$DXB_UPDATE_CONF")" 2> /dev/null
  dxb_write_if_changed "$DXB_UPDATE_CONF" "PRERELEASES=$1" 644 > /dev/null 2>&1
  [[ $(dxb_update_prereleases) == "$1" ]] || return 6
}

# _dxb_update_fetch URL: the body, quickly (a check must not hang on a slow network).
_dxb_update_fetch() { "$DXB_UPDATE_CURL" -fsSL --connect-timeout 10 --max-time 30 -H 'Accept: application/vnd.github+json' "$1"; }

# dxb_update_graywolf_info: installed and latest Graywolf, and the dxberry.txt pin (DXB_CFG loaded).
dxb_update_graywolf_info() {
  local installed latest sums line pin=${DXB_CFG[GRAYWOLF_VERSION]:-}
  installed=$(dxb_gw_installed_version)
  sums=$("$DXB_CURL" -fsSL --connect-timeout 10 --max-time 30 "$(dxb_gw_release_base)/checksums.txt" 2> /dev/null) || return 1
  line=$(printf '%s\n' "$sums" | dxb_gw_pick_deb "${DXB_DPKG_ARCH:-$(dpkg --print-architecture)}")
  latest=$(sed -nE 's/^[^ ]+ graywolf_([0-9.]+)_.*/\1/p' <<< "$line")
  [[ -n $latest ]] || return 1
  jq -cn --arg i "$installed" --arg l "$latest" --arg p "$pin" \
    --argjson u "$([[ -z $pin ]] && dxb_update_newer "$installed" "$latest" && echo true || echo false)" \
    '{installed: $i, latest: $l, pinned: (if $p == "" then null else $p end), update: $u}'
}

# dxb_update_dxberry_info: the installed DXBerry and the newest release that carries an update file
# (pre-releases only when switched on). "Newest" is the highest version among the candidates, not
# GitHub's listing order (creation date): a back-port or re-tag could be listed first but be
# numbered lower, and must not hide the real latest release.
dxb_update_dxberry_info() {
  local installed commit pre releases candidates pick='' latest='' c cl
  installed=$(sed -n 's/^DXBERRY_VERSION=//p' "$DXB_RELEASE_FILE" 2> /dev/null | head -1)
  commit=$(sed -n 's/^DXBERRY_COMMIT=//p' "$DXB_RELEASE_FILE" 2> /dev/null | head -1)
  pre=$(dxb_update_prereleases)
  releases=$(_dxb_update_fetch "$DXB_UPDATE_API" 2> /dev/null) || return 1
  jq -e 'type == "array"' <<< "$releases" > /dev/null 2>&1 || return 1
  candidates=$(jq -c --arg pre "$pre" '
    [.[] | select(.draft | not) | select($pre == "on" or (.prerelease | not))
     | . as $r
     | ([$r.assets[] | select(.name | test("^dxberry-pi-.*\\.tar\\.gz$"))] | .[0]) as $t
     | select($t != null)
     | ([$r.assets[] | select(.name == ($t.name + ".sha256"))] | .[0]) as $s
     | select($s != null)
     | {latest: ($r.tag_name | ltrimstr("v")), prerelease: $r.prerelease, url: $t.browser_download_url,
        sha_url: $s.browser_download_url, name: $t.name}]' <<< "$releases")
  while IFS= read -r c; do
    [[ -n $c ]] || continue
    cl=$(jq -r '.latest' <<< "$c")
    if dxb_update_newer "$latest" "$cl"; then pick=$c; latest=$cl; fi
  done < <(jq -c '.[]' <<< "$candidates")
  [[ -n $pick ]] || pick='{"latest": null, "prerelease": false, "url": null, "sha_url": null, "name": null}'
  jq -c --arg i "$installed" --arg c "$commit" --argjson p "$([[ $pre == on ]] && echo true || echo false)" \
    --argjson u "$([[ -n $latest ]] && dxb_update_newer "$installed" "$latest" && echo true || echo false)" \
    '{installed: $i, commit: $c} + . + {update: $u, include_prereleases: $p}' <<< "$pick"
}

# dxb_update_system_info: how many Debian packages an upgrade would change (after apt-get update).
dxb_update_system_info() {
  local sim
  DEBIAN_FRONTEND=noninteractive "$DXB_APT_GET" update -qq > /dev/null 2>&1 || dxb_warn "apt-get update failed; the package list may be old"
  sim=$("$DXB_APT_GET" -s -o Debug::NoLocking=1 upgrade 2> /dev/null) || return 1
  awk '/^Inst /{ print $2 }' <<< "$sim" | jq -Rsc 'split("\n") | map(select(length > 0)) | {count: length, packages: .[:50]}'
}

dxb_update_rollback_info() {
  local v=''
  if [[ -x $DXB_OPT.prev/bin/dxberry-provision ]]; then v=$(head -1 "$DXB_OPT.prev/VERSION" 2> /dev/null); fi
  jq -cn --arg v "$v" --argjson a "$([[ -x $DXB_OPT.prev/bin/dxberry-provision ]] && echo true || echo false)" \
    '{available: $a, version: (if $a and $v != "" then $v else null end)}'
}

# dxb_update_check [refresh]: the cached answer while it is younger than DXB_UPDATE_TTL_S, else a
# fresh one (written to the cache). rollback and reboot_required are always worked out now.
dxb_update_check() {
  local now age='' j gw dx sy
  now=$(date +%s)
  if [[ ${1:-} != refresh && -f $DXB_UPDATE_CACHE ]]; then
    age=$(( now - $(jq -r '.checked_at // 0' "$DXB_UPDATE_CACHE" 2> /dev/null || echo 0) ))
  fi
  if [[ -n $age ]] && (( age >= 0 && age < DXB_UPDATE_TTL_S )); then
    j=$(jq -c '. + {cached: true}' "$DXB_UPDATE_CACHE")
  else
    dxb_config_load "$(dxb_boot_dir)/dxberry.txt" > /dev/null 2>&1; dxb_config_validate > /dev/null 2>&1
    gw=$(dxb_update_graywolf_info) || gw='{"error":"Graywolf'\''s release list could not be read"}'
    dx=$(dxb_update_dxberry_info) || dx='{"error":"the DXBerry release list could not be read"}'
    sy=$(dxb_update_system_info) || sy='{"error":"apt-get could not simulate an upgrade"}'
    j=$(jq -cn --argjson t "$now" --argjson g "$gw" --argjson d "$dx" --argjson s "$sy" '{checked_at: $t, graywolf: $g, dxberry: $d, system: $s}')
    mkdir -p "$(dirname "$DXB_UPDATE_CACHE")" 2> /dev/null
    dxb_write_if_changed "$DXB_UPDATE_CACHE" "$j" 644 > /dev/null 2>&1
    j=$(jq -c '. + {cached: false}' <<< "$j")
  fi
  jq -c --argjson r "$(dxb_update_rollback_info)" --argjson rb "$([[ -e $DXB_REBOOT_FLAG ]] && echo true || echo false)" \
    '. + {rollback: $r, reboot_required: $rb}' <<< "$j"
}
