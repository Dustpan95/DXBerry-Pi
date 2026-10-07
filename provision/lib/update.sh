#!/bin/bash
# shellcheck shell=bash
# Updates (console spec section 11): what is installed against what is out, cached for six hours,
# and the jobs that update Graywolf, DXBerry and the system packages (Task 3).

: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_UPDATE_CACHE:=$DXB_STATE_DIR/update-check.json}"
: "${DXB_UPDATE_CONF:=$DXB_STATE_DIR/update.conf}"
: "${DXB_UPDATE_TTL_S:=21600}"
# an answer in which a part could not be read (offline, GitHub down) is kept only this long
: "${DXB_UPDATE_ERROR_TTL_S:=900}"
: "${DXB_UPDATE_API:=https://api.github.com/repos/Dustpan95/DXBerry-Pi/releases?per_page=30}"
: "${DXB_UPDATE_CURL:=curl}"
: "${DXB_DPKG:=dpkg}"
: "${DXB_APT_GET:=apt-get}"
: "${DXB_DPKG_CMD:=dpkg}"
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
     | ([$r.assets[] | select(.name | test("^dxberry-pi-[A-Za-z0-9.~+-]+\\.tar\\.gz$"))] | .[0]) as $t
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

# dxb_update_rollback_info: .prev is only ever offered when it looks like a complete tree (a
# provisioner, its libraries and a VERSION) - a half-deleted .prev must never be offered or
# swapped in. "updates" says whether .prev still carries dxberry-update, so the page can warn
# that rolling back to a tree without it removes the Updates screen.
dxb_update_rollback_info() {
  local prev=$DXB_OPT.prev v='' a=false upd=false
  if [[ -x $prev/bin/dxberry-provision && -f $prev/lib/common.sh && -f $prev/VERSION ]]; then
    a=true
    v=$(head -1 "$prev/VERSION" 2> /dev/null)
    [[ -f $prev/bin/dxberry-update ]] && upd=true
  fi
  jq -cn --arg v "$v" --argjson a "$a" --argjson u "$upd" \
    '{available: $a, version: (if $a and $v != "" then $v else null end), updates: $u}'
}

# dxb_update_check [refresh]: the cached answer while it is younger than DXB_UPDATE_TTL_S (only
# DXB_UPDATE_ERROR_TTL_S when a part of it could not be read), else a fresh one (written to the
# cache). rollback and reboot_required are always worked out now.
dxb_update_check() {
  local now age='' ttl=$DXB_UPDATE_TTL_S j gw dx sy
  now=$(date +%s)
  if [[ ${1:-} != refresh && -f $DXB_UPDATE_CACHE ]]; then
    age=$(( now - $(jq -r '.checked_at // 0' "$DXB_UPDATE_CACHE" 2> /dev/null || echo 0) ))
    if jq -e '[.graywolf.error, .dxberry.error, .system.error] | any(. != null)' "$DXB_UPDATE_CACHE" > /dev/null 2>&1; then
      ttl=$DXB_UPDATE_ERROR_TTL_S
    fi
  fi
  if [[ -n $age ]] && (( age >= 0 && age < ttl )); then
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

# ---- the jobs' work ------------------------------------------------------------------------
: "${DXB_UPDATE_WORK:=$DXB_STATE_DIR/update-work}"
: "${DXB_SBIN:=/usr/local/sbin}"

# dxb_update_run_provision: the installed tree's provisioner, as a settings change runs it: the
# installed libraries, the real provision log, umask 022, Graywolf kept, never a terminal prompt.
dxb_update_run_provision() {
  local cmd=${DXB_UPDATE_PROVISION:-$DXB_OPT/bin/dxberry-provision}
  ( umask 022; DXB_LIB=$DXB_OPT/lib DXB_LOG_FILE=${DXB_PROVISION_LOG:-$DXB_STATE_DIR/provision.log} \
      DXB_GW_UPGRADE=0 DXB_TTY=/dev/null "$cmd" )
}

# dxb_update_release_from_tree DIR: /etc/dxberry-release's DXBERRY_* lines from DIR's RELEASE
# file, or its VERSION and "unknown" for a tree that has none (one the image installed). Every
# other line already in the release file (DIETPI_IMAGE, DIETPI_IMAGE_SHA256 - see
# build/build-image.sh) is kept as-is: this writes only the DXBERRY_* lines, never erases the rest.
dxb_update_release_from_tree() {
  local t=$1 v c=unknown d other
  v=$(head -1 "$t/VERSION" 2> /dev/null)
  d=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  if [[ -f $t/RELEASE ]]; then
    c=$(sed -n 's/^DXBERRY_COMMIT=//p' "$t/RELEASE" | head -1); c=${c:-unknown}
    d=$(sed -n 's/^DXBERRY_BUILD_DATE=//p' "$t/RELEASE" | head -1)
  fi
  other=$(grep -v '^DXBERRY_' "$DXB_RELEASE_FILE" 2> /dev/null)
  mkdir -p "$(dirname "$DXB_RELEASE_FILE")" 2> /dev/null
  dxb_write_if_changed "$DXB_RELEASE_FILE" \
    "$(printf 'DXBERRY_VERSION=%s\nDXBERRY_COMMIT=%s\nDXBERRY_BUILD_DATE=%s' "$v" "$c" "$d")${other:+$'\n'$other}" \
    644 > /dev/null 2>&1
  return 0
}

# dxb_update_link_commands: /usr/local/sbin links for every dxberry-* command in the tree (a newer
# release can add one); dxberry-preboot runs only from the image's first boot and is never linked.
# Also drops a stale link of ours - one that points into "$DXB_OPT/bin/" but whose target no
# longer exists, e.g. dxberry-update after a rollback to a tree that predates it.
dxb_update_link_commands() {
  local f n target
  mkdir -p "$DXB_SBIN" 2> /dev/null
  for f in "$DXB_OPT"/bin/dxberry-*; do
    n=${f##*/}
    [[ -f $f && $n != dxberry-preboot ]] || continue
    ln -sf "$DXB_OPT/bin/$n" "$DXB_SBIN/$n" || dxb_warn "could not link $DXB_SBIN/$n"
  done
  for f in "$DXB_SBIN"/dxberry-*; do
    [[ -L $f ]] || continue
    target=$(readlink "$f")
    [[ $target == "$DXB_OPT/bin/"* && ! -e $target ]] || continue
    rm -f "$f"
  done
}

# dxb_update_apply_graywolf: 6 when dxberry.txt could not even be read (a pin must never be
# ignored) and when the install failed without changing the installed version (nothing changed);
# 8 when the version did change but a later step (the drop-in, the restart) failed.
dxb_update_apply_graywolf() {
  local before after rc=0 install_rc=0
  if ! dxb_config_load "$(dxb_boot_dir)/dxberry.txt" > /dev/null 2>&1; then
    echo "dxberry.txt could not be read; nothing changed" >&2
    return 6
  fi
  dxb_config_validate > /dev/null 2>&1
  if [[ -n ${DXB_CFG[GRAYWOLF_VERSION]:-} ]]; then
    echo "Graywolf is pinned to ${DXB_CFG[GRAYWOLF_VERSION]} in dxberry.txt; nothing to update"
    return 0
  fi
  before=$(dxb_gw_installed_version)
  DXB_GW_UPGRADE=1 dxb_gw_install || install_rc=1
  after=$(dxb_gw_installed_version)
  if (( install_rc )) && [[ $before == "$after" ]]; then
    echo "graywolf: ${before:-not installed} -> ${after:-not installed}"
    (( ${#DXB_FAILED_STEPS[@]} )) && printf '  %s\n' "${DXB_FAILED_STEPS[@]}"
    return 6
  fi
  (( install_rc )) && rc=8
  # the position-history drop-in is rebuilt from the new package's own unit (spec 11.2)
  dxb_gw_history_in_ram || rc=8
  # try-restart, never restart: an operator who stopped Graywolf on purpose keeps it stopped
  if [[ $before != "$after" ]]; then systemctl try-restart graywolf.service || rc=8; fi
  echo "graywolf: ${before:-not installed} -> ${after:-not installed}"
  (( ${#DXB_FAILED_STEPS[@]} )) && printf '  %s\n' "${DXB_FAILED_STEPS[@]}"
  return $rc
}

dxb_update_apply_dxberry() {
  local info url sha_url name latest dl want new=$DXB_OPT.new prev=$DXB_OPT.prev old=$DXB_OPT.old had_old=0
  info=$(dxb_update_dxberry_info) || { echo "the DXBerry release list could not be read; nothing changed" >&2; return 6; }
  if [[ $(jq -r '.update' <<< "$info") != true ]]; then echo "DXBerry $(jq -r '.installed' <<< "$info") is the newest; nothing to update"; return 0; fi
  url=$(jq -r '.url' <<< "$info"); sha_url=$(jq -r '.sha_url' <<< "$info"); name=$(jq -r '.name' <<< "$info"); latest=$(jq -r '.latest' <<< "$info")
  dl=$DXB_UPDATE_WORK/download
  rm -rf "$dl"; ( umask 077; mkdir -p "$dl" ) || { echo "could not create $dl" >&2; rm -rf "$dl"; return 6; }
  echo "downloading $name"
  # shellcheck disable=SC2015  # intentional: either curl failing falls through to the one error block
  "$DXB_UPDATE_CURL" -fsSL --connect-timeout 15 --max-time 900 -o "$dl/$name" "$url" \
    && "$DXB_UPDATE_CURL" -fsSL --connect-timeout 15 --max-time 60 -o "$dl/$name.sha256" "$sha_url" \
    || { echo "the download failed; nothing changed" >&2; rm -rf "$dl"; return 6; }
  want=$(awk '{ print $1; exit }' "$dl/$name.sha256")
  if [[ ! $want =~ ^[0-9a-f]{64}$ || $(sha256sum "$dl/$name" | cut -d' ' -f1) != "$want" ]]; then
    echo "the update file does not match its checksum; nothing changed" >&2; rm -rf "$dl"; return 6
  fi
  rm -rf "$new"
  if ! mkdir -p "$new"; then echo "could not create $new" >&2; rm -rf "$new" "$dl"; return 6; fi
  if ! tar -xzf "$dl/$name" -C "$new" --strip-components=1 --no-same-owner \
    || [[ ! -x $new/bin/dxberry-provision || ! -f $new/VERSION || ! -f $new/lib/common.sh ]]; then
    echo "the update file is incomplete; nothing changed" >&2; rm -rf "$new" "$dl"; return 6
  fi
  chmod 755 "$new" 2> /dev/null || true
  if [[ $(head -1 "$new/VERSION" 2> /dev/null) != "$latest" ]]; then
    echo "the update file's VERSION does not match release $latest; nothing changed" >&2; rm -rf "$new" "$dl"; return 6
  fi
  rm -rf "$dl"
  chown -R 0:0 "$new" 2> /dev/null || true
  # the previous tree is renamed aside, never deleted in place, so a failed swap can always put
  # everything back - a WiFi-only Pi must never end up without a working $DXB_OPT
  if [[ -e $prev ]]; then
    rm -rf "$old"
    if ! mv -T "$prev" "$old"; then echo "could not move $prev aside; nothing changed" >&2; rm -rf "$new"; return 6; fi
    had_old=1
  fi
  # the tree about to become .prev carries its own real version/commit/date forward, so a later
  # rollback never reports "unknown" for a tree the image itself installed (no RELEASE of its own)
  if [[ ! -f $DXB_OPT/RELEASE ]]; then
    if ! grep '^DXBERRY_' "$DXB_RELEASE_FILE" 2> /dev/null > "$DXB_OPT/RELEASE.dxbtmp"; then rm -f "$DXB_OPT/RELEASE.dxbtmp"; fi
    [[ -s $DXB_OPT/RELEASE.dxbtmp ]] && mv -T "$DXB_OPT/RELEASE.dxbtmp" "$DXB_OPT/RELEASE"
    rm -f "$DXB_OPT/RELEASE.dxbtmp"
  fi
  if ! mv -T "$DXB_OPT" "$prev"; then
    echo "could not move $DXB_OPT aside; nothing changed" >&2
    rm -rf "$new"
    if (( had_old )) && ! mv -T "$old" "$prev"; then echo "could not restore the previous DXBerry at $prev (it is at $old)" >&2; fi
    return 6
  fi
  if ! mv -T "$new" "$DXB_OPT"; then
    echo "could not put the new tree in place; nothing changed" >&2
    rm -rf "$new"
    if ! mv -T "$prev" "$DXB_OPT"; then echo "could not restore $DXB_OPT (the previous tree is at $prev)" >&2; fi
    if (( had_old )) && [[ -e $old ]] && ! mv -T "$old" "$prev"; then echo "could not restore the previous DXBerry at $prev (it is at $old)" >&2; fi
    return 6
  fi
  sync
  rm -rf "$old"
  dxb_update_release_from_tree "$DXB_OPT"
  dxb_update_link_commands
  if [[ $(sed -n 's/^DXBERRY_VERSION=//p' "$DXB_RELEASE_FILE" 2> /dev/null | head -1) != "$(head -1 "$DXB_OPT/VERSION" 2> /dev/null)" ]]; then
    # the swap itself succeeded - returning 6 here would offer the same update again and push
    # the real previous tree out of .prev on the next run
    echo "$DXB_RELEASE_FILE does not show the new version; treating the update as failed" >&2
    return 8
  fi
  echo "DXBerry $(head -1 "$prev/VERSION" 2> /dev/null) -> $(head -1 "$DXB_OPT/VERSION"); running the setup"
  dxb_update_run_provision || { echo "the setup run reported failed steps" >&2; return 8; }
  return 0
}

dxb_update_apply_rollback() {
  local prev=$DXB_OPT.prev next=$DXB_OPT.next
  if [[ ! -x $prev/bin/dxberry-provision || ! -f $prev/lib/common.sh || ! -f $prev/VERSION ]]; then
    echo "there is no earlier DXBerry to go back to" >&2
    return 6
  fi
  rm -rf "$next"
  if ! mv -T "$DXB_OPT" "$next"; then echo "could not move $DXB_OPT aside; nothing changed" >&2; return 6; fi
  if ! mv -T "$prev" "$DXB_OPT"; then
    echo "could not put the earlier tree back; nothing changed" >&2
    if ! mv -T "$next" "$DXB_OPT"; then echo "could not restore $DXB_OPT (the current tree is at $next)" >&2; fi
    return 6
  fi
  if ! mv -T "$next" "$prev"; then dxb_warn "the newer DXBerry stays at $next"; fi
  sync
  dxb_update_release_from_tree "$DXB_OPT"
  dxb_update_link_commands
  if [[ $(sed -n 's/^DXBERRY_VERSION=//p' "$DXB_RELEASE_FILE" 2> /dev/null | head -1) != "$(head -1 "$DXB_OPT/VERSION" 2> /dev/null)" ]]; then
    echo "$DXB_RELEASE_FILE does not show the restored version; treating the rollback as failed" >&2
    return 8
  fi
  echo "DXBerry back to $(head -1 "$DXB_OPT/VERSION"); running the setup"
  dxb_update_run_provision || { echo "the setup run reported failed steps" >&2; return 8; }
  return 0
}

# dxb_update_apply_system: dpkg --configure -a (heals an interrupted install) then apt-get update
# and upgrade, never asking (keeps changed config files) and waiting out a concurrent apt/dpkg
# lock instead of failing at once. 6 when nothing changed yet (configure or update failed), 8 when
# the upgrade itself failed partway through.
dxb_update_apply_system() {
  DEBIAN_FRONTEND=noninteractive "$DXB_DPKG_CMD" --configure -a --force-confdef --force-confold \
    || { echo "dpkg --configure -a failed" >&2; return 6; }
  DEBIAN_FRONTEND=noninteractive "$DXB_APT_GET" update -o DPkg::Lock::Timeout=120 || { echo "apt-get update failed" >&2; return 6; }
  DEBIAN_FRONTEND=noninteractive "$DXB_APT_GET" -y --no-install-recommends \
    -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade -o DPkg::Lock::Timeout=120 \
    || { echo "apt-get upgrade failed" >&2; return 8; }
  return 0
}

# ---- the update job ------------------------------------------------------------------------
: "${DXB_UPDATE_JOB_FILE:=$DXB_RUN_DIR/update-job.json}"
: "${DXB_UPDATE_RESULT:=$DXB_RUN_DIR/update-result.json}"

dxb_update_job_unit() { [[ -f $DXB_UPDATE_JOB_FILE ]] && jq -r '.unit // empty' "$DXB_UPDATE_JOB_FILE" 2> /dev/null; return 0; }
dxb_update_job_running() { local u; u=$(dxb_update_job_unit); [[ -n $u ]] && systemctl is-active --quiet "$u"; }
