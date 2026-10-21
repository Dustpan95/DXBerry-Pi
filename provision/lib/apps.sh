#!/bin/bash
# shellcheck shell=bash
# Programs (applications spec, docs/design/2026-10-19-applications.md): the catalog (apps.tsv),
# where each program comes from, whether and at which version it is installed, the install and
# remove job bodies, the dxbapps user, first-fill marks and the per-radio wiring facts the
# program modules (provision/lib/apps/NAME.sh) read.
# Expects common.sh, gps.sh, radio.sh and update.sh to be sourced.

: "${DXB_SHARE:=/opt/dxberry/share}"
: "${DXB_APPS_TSV:=$DXB_SHARE/apps.tsv}"
# where upstream programs that are not Debian packages are unpacked: $DXB_APPS_OPT/<name>/
: "${DXB_APPS_OPT:=/opt/dxberry-apps}"
# the home of the user every program runs as; it holds the programs' settings and data
: "${DXB_APPS_HOME:=/var/lib/dxberry-apps}"
# first-fill marks and the modules' own bookkeeping
: "${DXB_APPS_STATE:=$DXB_STATE_DIR/apps}"
# downloads land here (0700 root; each download 0600) and are removed after every install
: "${DXB_APPS_WORK:=$DXB_STATE_DIR/apps-work}"
: "${DXB_GH_API:=https://api.github.com/repos}"
: "${DXB_APPS_CURL:=curl}"
: "${DXB_APT_GET:=apt-get}"
: "${DXB_APT_CACHE:=apt-cache}"
: "${DXB_DPKG_QUERY:=dpkg-query}"
: "${DXB_USERADD:=useradd}"
: "${DXB_GROUPADD:=groupadd}"
: "${DXB_USERMOD:=usermod}"
: "${DXB_CHOWN:=chown}"
: "${DXB_GETENT:=getent}"
: "${DXB_SYSTEMCTL:=systemctl}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TMPFILES_DIR:=/etc/tmpfiles.d}"
: "${DXB_NGINX_DIR:=/etc/nginx}"
# runs a command as dxbapps: runuser -u dxbapps -- CMD... (a downloaded AppImage unpacks itself
# this way, never as root)
: "${DXB_RUNUSER:=runuser}"
# where an AppImage unpacks itself as dxbapps, in a fresh mktemp directory: outside the state
# directory, which is 0700 root on the Pi and so out of dxbapps' reach
: "${DXB_APPS_EXTRACT_BASE:=/var/tmp}"

# what every program with a screen session needs (not a service, not Graywolf)
DXB_APPS_BASE_PACKAGES='tigervnc-standalone-server openbox socat xauth'
DXB_APPS_USER=dxbapps
DXB_APPS_SCREEN_GROUP=dxbscreen

# dxb_console_user: the login user of the console (Cockpit, SSH).
dxb_console_user() { printf '%s\n' "${DXB_CONSOLE_USER:-dietpi}"; }

# ---- the catalog ---------------------------------------------------------------------------
# dxb_apps_rows: [{name, label, kind, source, packages: [...], description}] in catalog order.
dxb_apps_rows() {
  [[ -r $DXB_APPS_TSV ]] || { dxb_error "cannot read $DXB_APPS_TSV"; return 1; }
  jq -Rnc '[inputs | select(test("^\\s*(#|$)") | not) | split("\t") | select(length == 6)
    | {name: .[0], label: .[1], kind: .[2], source: .[3],
       packages: (if .[4] == "-" then [] else [.[4] | splits(" +") | select(. != "")] end),
       description: .[5]}]' < "$DXB_APPS_TSV"
}

# dxb_apps_row NAME: one row. 3 when the catalog has no NAME.
dxb_apps_row() {
  local rows
  rows=$(dxb_apps_rows) || return 1
  jq -ce --arg n "$1" '.[] | select(.name == $n)' <<< "$rows" 2> /dev/null || { dxb_error "no such program: $1"; return 3; }
}

# _dxb_apps_parse_source SOURCE: {type, repo, package, patterns: [{type, re}]} for builtin,
# apt:PACKAGE, tag:OWNER/REPO or github:OWNER/REPO:TYPE=REGEX[,TYPE=REGEX...]. 2 when malformed.
_dxb_apps_parse_source() {
  jq -cen --arg s "$1" '
    def repo_re: "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$";
    ($s | capture("^(?<t>[a-z]+)(:(?<rest>.*))?$")) as $m
    | ($m.rest // "") as $r
    | if $m.t == "builtin" and $m.rest == null then {type: "builtin", repo: null, package: null, patterns: []}
      elif $m.t == "apt" and ($r | test("^[a-z0-9][a-z0-9+.-]+$")) then {type: "apt", repo: null, package: $r, patterns: []}
      elif $m.t == "tag" and ($r | test(repo_re)) then {type: "tag", repo: $r, package: null, patterns: []}
      elif $m.t == "github" then
        ($r | capture("^(?<repo>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+):(?<p>.+)$")) as $g
        | [$g.p | splits(",(?=(deb|appimage|bin)=)")] as $parts
        | [$parts[] | capture("^(?<type>deb|appimage|bin)=(?<re>.+)$")] as $pats
        | if ($pats | length) == ($parts | length) then {type: "github", repo: $g.repo, package: null, patterns: $pats} else empty end
      else empty end' 2> /dev/null || return 2
}

# _dxb_apps_source NAME: _dxb_apps_parse_source of NAME's catalog source. 3 unknown, 2 malformed.
_dxb_apps_source() {
  local row
  row=$(dxb_apps_row "$1") || return $?
  _dxb_apps_parse_source "$(jq -r .source <<< "$row")" || { dxb_error "$1: the catalog's source is malformed"; return 2; }
}

# ---- modules -------------------------------------------------------------------------------
# _dxb_apps_has NAME HOOK: 0 when NAME has a module (loaded now) that defines app_NAME_HOOK. A
# program without a module (not written yet) simply has no hooks.
_dxb_apps_has() {
  [[ -f $DXB_APPS_DIR/$1.sh ]] || return 1
  dxb_app_load "$1" 2> /dev/null || return 1
  declare -F "app_$1_$2" > /dev/null
}

# ---- the newest release --------------------------------------------------------------------
# _dxb_apps_curl ARGS... URL: curl; an https URL may only ever be fetched (and redirected) over https
_dxb_apps_curl() {
  if [[ ${*: -1} == https://* ]]; then "$DXB_APPS_CURL" --proto =https --proto-redir =https "$@"; else "$DXB_APPS_CURL" "$@"; fi
}
_dxb_apps_api() { _dxb_apps_curl -fsSL --connect-timeout 10 --max-time 30 -H 'Accept: application/vnd.github+json' "$1"; }
_dxb_apps_get() { _dxb_apps_curl -fsSL --connect-timeout 10 --max-time 60 "$1"; }
# _dxb_apps_url_ok URL: a URL taken from a release answer is fetched only when it is https:// (or
# uses the configured API's own scheme, so a stand-in API works) - never a file:// URL, never
# something curl would read as an option, never one with spaces or control characters.
_dxb_apps_url_ok() {
  local scheme=${DXB_GH_API%%://*}
  [[ $1 != *[[:space:][:cntrl:]]* ]] || return 1
  [[ $1 == https://?* ]] && return 0
  [[ $scheme != "$DXB_GH_API" && $1 == "$scheme://"?* ]]
}

# dxb_apps_source_latest SOURCE: what the newest release of SOURCE is, as
# {"version","asset","type":"deb"|"appimage"|"bin"|"tarball"|"apt","url","sha256":hex|null}.
# GitHub sources read releases/latest (never a sort by number: ruling 1); the first pattern that
# matches an asset wins, in the order the source names them. The checksum comes from a release
# asset SHA256SUMS, checksums.txt or <asset>.sha256 that lists the asset. Every URL the answer
# gives must pass _dxb_apps_url_ok. 1 when unreadable or refused.
dxb_apps_source_latest() {
  local src type repo pkg rel tag ver pick asset sname surl sums sha=''
  src=$(_dxb_apps_parse_source "$1") || { dxb_error "cannot read the program source $1"; return 1; }
  type=$(jq -r .type <<< "$src")
  case $type in
    builtin) return 1 ;;
    apt)
      pkg=$(jq -r .package <<< "$src")
      ver=$("$DXB_APT_CACHE" policy "$pkg" 2> /dev/null | sed -n 's/^[[:blank:]]*Candidate:[[:blank:]]*//p' | head -1)
      [[ -n $ver && $ver != '(none)' ]] || return 1
      jq -cn --arg v "$ver" --arg p "$pkg" '{version: $v, asset: $p, type: "apt", url: null, sha256: null}'
      return 0 ;;
  esac
  repo=$(jq -r .repo <<< "$src")
  rel=$(_dxb_apps_api "$DXB_GH_API/$repo/releases/latest" 2> /dev/null) || return 1
  tag=$(jq -r '.tag_name // empty' <<< "$rel" 2> /dev/null)
  # the tag goes into a URL and a file name
  [[ $tag =~ ^[A-Za-z0-9._+~-]+$ ]] || return 1
  ver=${tag#v}
  if [[ $type == tag ]]; then
    jq -cn --arg v "$ver" --arg a "${repo#*/}-$ver.tar.gz" --arg u "https://github.com/$repo/archive/refs/tags/$tag.tar.gz" \
      '{version: $v, asset: $a, type: "tarball", url: $u, sha256: null}'
    return 0
  fi
  pick=$(jq -c --argjson pats "$(jq -c .patterns <<< "$src")" '
    [(.assets // [])[] | {name, url: .browser_download_url}] as $a
    | first($pats[] as $p | $a[] | select(.name | test($p.re)) | . + {type: $p.type}) // empty' <<< "$rel" 2> /dev/null)
  [[ -n $pick ]] || return 1
  asset=$(jq -r .name <<< "$pick")
  [[ $asset =~ ^[A-Za-z0-9._+~-]+$ ]] || return 1
  _dxb_apps_url_ok "$(jq -r '.url // ""' <<< "$pick")" || { dxb_error "$asset: refusing its download URL"; return 1; }
  for sname in SHA256SUMS checksums.txt "$asset.sha256"; do
    surl=$(jq -r --arg s "$sname" '[(.assets // [])[] | select(.name == $s)] | .[0].browser_download_url // empty' <<< "$rel")
    [[ -n $surl ]] || continue
    _dxb_apps_url_ok "$surl" || { dxb_error "$sname: refusing its download URL"; return 1; }
    # published but unreadable is not "no checksum": an install must not go ahead unchecked
    sums=$(_dxb_apps_get "$surl" 2> /dev/null) || return 1
    if [[ $sname == "$asset.sha256" ]]; then
      sha=$(awk 'NF { print $1; exit }' <<< "$sums")
      [[ ${sha,,} =~ ^[0-9a-f]{64}$ ]] || return 1
    else
      sha=$(a=$asset awk '{ n = $2; sub(/^\*/, "", n) } n == ENVIRON["a"] { print $1; exit }' <<< "$sums")
    fi
    sha=${sha,,}
    [[ $sha =~ ^[0-9a-f]{64}$ ]] && break
    sha=''
  done
  jq -cn --arg v "$ver" --argjson p "$pick" --arg s "$sha" \
    '{version: $v, asset: $p.name, type: $p.type, url: $p.url, sha256: (if $s == "" then null else $s end)}'
}

# dxb_apps_latest NAME: dxb_apps_source_latest of NAME's catalog source. 1 unreadable, 3 unknown.
dxb_apps_latest() {
  local row
  row=$(dxb_apps_row "$1") || return $?
  dxb_apps_source_latest "$(jq -r .source <<< "$row")"
}

# ---- what is installed ---------------------------------------------------------------------
# _dxb_apps_dpkg_version PACKAGE: its version when dpkg has it installed, else nothing.
_dxb_apps_dpkg_version() {
  local st='' v=''
  # shellcheck disable=SC2016  # dpkg-query's own ${field} format, not a shell expansion
  read -r st v < <("$DXB_DPKG_QUERY" -W -f '${db:Status-Status} ${Version}\n' "$1" 2> /dev/null)
  [[ $st == installed ]] && printf '%s\n' "$v"
  return 0
}
_dxb_apps_opt_version() { local v=''; [[ -f $DXB_APPS_OPT/$1/VERSION ]] && read -r v < "$DXB_APPS_OPT/$1/VERSION"; [[ -z $v ]] || printf '%s\n' "$v"; }

# _dxb_apps_package NAME SOURCE_JSON: the Debian package that is the program itself - the apt
# package, or NAME for a GitHub source that publishes .deb files - or nothing.
_dxb_apps_package() {
  jq -r --arg n "$1" 'if .type == "apt" then .package elif (.type == "github" and any(.patterns[]; .type == "deb")) then $n else empty end' <<< "$2"
}

# dxb_apps_version NAME: the installed version, empty when not installed. A .deb installed from
# upstream is asked of dpkg first, an unpacked program reads $DXB_APPS_OPT/NAME/VERSION; Graywolf
# (builtin) answers through its module's app_graywolf_version. 3 unknown.
dxb_apps_version() {
  local src pkg v=''
  src=$(_dxb_apps_source "$1") || return $?
  case $(jq -r .type <<< "$src") in
    builtin) if _dxb_apps_has "$1" version; then "app_$1_version" 2> /dev/null; fi; return 0 ;;
    apt) _dxb_apps_dpkg_version "$(jq -r .package <<< "$src")"; return 0 ;;
  esac
  pkg=$(_dxb_apps_package "$1" "$src")
  [[ -z $pkg ]] || v=$(_dxb_apps_dpkg_version "$pkg")
  [[ -n $v ]] || v=$(_dxb_apps_opt_version "$1")
  [[ -z $v ]] || printf '%s\n' "$v"
  return 0
}

# dxb_apps_installed NAME: 0 installed, 1 not, 3 unknown.
dxb_apps_installed() {
  local v
  v=$(dxb_apps_version "$1") || return $?
  [[ -n $v ]]
}

# ---- the program user ----------------------------------------------------------------------
# dxb_apps_user_ensure: the system user dxbapps (spec 7.3) in audio, dialout and dxbscreen, its
# home 0750 (the console user, in the dxbapps group, can read saved pictures and logs), the
# console user in dxbscreen (the screen sockets) and dxbapps, and the desktop PulseAudio user
# units masked for every login (ruling 2: one would take Graywolf's card). Idempotent. 0 ok, 6 failed.
dxb_apps_user_ensure() {
  local u=$DXB_APPS_USER g=$DXB_APPS_SCREEN_GROUP home=$DXB_APPS_HOME console rc=0
  if ! "$DXB_GETENT" group "$g" > /dev/null 2>&1; then
    "$DXB_GROUPADD" --system "$g" || { echo "could not create the group $g" >&2; return 6; }
  fi
  if "$DXB_GETENT" passwd "$u" > /dev/null 2>&1; then
    "$DXB_USERMOD" -aG "audio,dialout,$g" "$u" || { echo "could not add $u to audio, dialout and $g" >&2; rc=6; }
  else
    "$DXB_USERADD" --system --home-dir "$home" --create-home --shell /usr/sbin/nologin --groups "audio,dialout,$g" "$u" \
      || { echo "could not create the user $u" >&2; return 6; }
  fi
  if ! { mkdir -p "$home" && chmod 0750 "$home" && "$DXB_CHOWN" "$u:$u" "$home"; }; then
    echo "could not prepare $home" >&2; rc=6
  fi
  console=$(dxb_console_user)
  if "$DXB_GETENT" passwd "$console" > /dev/null 2>&1; then
    # not fatal: the programs run without it; the program tab says so when its socket is refused
    "$DXB_USERMOD" -aG "$g,$u" "$console" || echo "could not add $console to $g and $u; the program tab cannot open screens for it" >&2
  else
    echo "the console user $console does not exist; it was not added to $g and $u" >&2
  fi
  "$DXB_SYSTEMCTL" --global mask pulseaudio.socket pulseaudio.service > /dev/null 2>&1 \
    || { echo "could not mask the desktop sound server (pulseaudio.socket, pulseaudio.service)" >&2; rc=6; }
  return $rc
}

# ---- first fill ----------------------------------------------------------------------------
# A program's settings for a radio are filled once (callsign, grid, ...); the mark says it was
# done, and it outlives a removal so a reinstall continues where the operator left off.
_dxb_apps_mark() {
  [[ $1 =~ ^[a-z][a-z0-9_]{0,15}$ && $2 =~ ^[a-z][a-z0-9]{0,11}$ ]] || { dxb_error "bad program or radio name: $1 $2"; return 2; }
  printf '%s/firstfill/%s-%s\n' "$DXB_APPS_STATE" "$1" "$2"
}
dxb_apps_firstfill_done() { local f; f=$(_dxb_apps_mark "$1" "$2") || return 2; [[ -e $f ]]; }
dxb_apps_firstfill_mark() { local f; f=$(_dxb_apps_mark "$1" "$2") || return 2; mkdir -p "${f%/*}" && : > "$f"; }

# ---- what a program is told ----------------------------------------------------------------
# dxb_apps_station: {"call","grid"} - CALLSIGN without its -SSID; the grid from LATITUDE and
# LONGITUDE, else from the GPS fix, else "". DXB_CFG must be loaded.
dxb_apps_station() {
  local call=${DXB_CFG[CALLSIGN]:-} lat=${DXB_CFG[LATITUDE]:-} lon=${DXB_CFG[LONGITUDE]:-} grid='' fix
  call=${call%%-*}
  if [[ -n $lat && -n $lon ]]; then
    grid=$(dxb_maidenhead "$lat" "$lon")
  else
    fix=$(dxb_gps_fix 2> /dev/null)
    grid=$(jq -r 'if (.fix // 0) >= 2 then .grid // "" else "" end' <<< "$fix" 2> /dev/null)
  fi
  jq -cn --arg c "$call" --arg g "$grid" '{call: $c, grid: $g}'
}

# dxb_apps_wiring RADIO: what a program needs to use RADIO - its rigctld, sound card, PTT and
# rig control, the per-radio ports (ruling 6: index i = (rigctld port - 4532) / 2) and the wiring
# hash. ptt: cat when rigctld keys the radio (rigctld, cm108 and gpio: ruling 8), vox when the
# audio does (vox, digirig_tone), none. 3 when there is no such radio.
dxb_apps_wiring() {
  local r
  [[ -n ${DXB_RADIOS:-} ]] || dxb_radio_load || return 6
  r=$(dxb_radio_get "$1" 2> /dev/null) || { dxb_error "no such radio: $1"; return 3; }
  jq -c --arg n "$1" --arg h "$(dxb_radio_wire_hash "$r")" '
    ((.rigctld_port - 4532) / 2 | floor) as $i | ($n | ascii_upcase) as $card
    | {radio: $n, label: (if (.label // "") == "" then $n else .label end), index: $i,
       rigctld: ("127.0.0.1:" + (.rigctld_port | tostring)), card: $card, device: ("plughw:CARD=" + $card + ",DEV=0"),
       ptt: ({rigctld: "cat", cm108: "cat", gpio: "cat", vox: "vox", digirig_tone: "vox"}[.ptt.method] // "none"),
       control: (.rig.control // "hamlib"), flrig_port: (12345 + $i), hash: $h}' <<< "$r"
}

# ---- getting and placing files -------------------------------------------------------------
# _dxb_apps_apt ARGS...: apt-get, never asking, waiting out a busy dpkg lock, keeping changed
# configuration files.
_dxb_apps_apt() {
  DEBIAN_FRONTEND=noninteractive "$DXB_APT_GET" "$@" -o DPkg::Lock::Timeout=120 \
    -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold
}

# dxb_apps_download LATEST: LATEST's file (dxb_apps_source_latest) into DXB_APPS_WORK, checked
# against its published checksum ("no checksum published for ASSET" when there is none). Progress
# on stdout, errors on stderr. 0 ok, 6 not downloaded or not matching (nothing left behind).
dxb_apps_download() {
  local asset url sha file got
  asset=$(jq -r .asset <<< "$1"); url=$(jq -r '.url // ""' <<< "$1"); sha=$(jq -r '.sha256 // empty' <<< "$1")
  file=$DXB_APPS_WORK/$asset
  _dxb_apps_url_ok "$url" || { echo "refusing to download $asset from $url; nothing changed" >&2; return 6; }
  if ! { ( umask 077; mkdir -p "$DXB_APPS_WORK" ) && chmod 700 "$DXB_APPS_WORK"; }; then
    echo "could not create $DXB_APPS_WORK; nothing changed" >&2; return 6
  fi
  rm -f "$file"
  echo "downloading $asset"
  if ! ( umask 077; _dxb_apps_curl -fsSL --connect-timeout 15 --max-time 1800 -o "$file" "$url" ); then
    rm -f "$file"; echo "the download of $asset failed; nothing changed" >&2; return 6
  fi
  if [[ -z $sha ]]; then
    echo "no checksum published for $asset"
    return 0
  fi
  got=$(sha256sum "$file" | cut -d' ' -f1)
  if [[ $got != "$sha" ]]; then
    rm -f "$file"; echo "$asset does not match its published checksum; nothing changed" >&2; return 6
  fi
  echo "checksum ok for $asset"
}

# _dxb_apps_keep NAME: the paths (relative to $DXB_APPS_OPT/NAME, one per line) an unpacked
# program keeps from its earlier install - the module's optional app_NAME_keep.
_dxb_apps_keep() { if _dxb_apps_has "$1" keep; then "app_$1_keep"; fi; }

# _dxb_apps_unpack_appimage FILE DEST: FILE (an AppImage) unpacks itself - as dxbapps, never as
# root - in a fresh directory under DXB_APPS_EXTRACT_BASE that only dxbapps can enter, with that
# directory as HOME (the AppImage never sees /root). Root then moves the tree to DEST, out of
# dxbapps' reach first (nothing it runs can add to the tree after that), owns it and takes off
# every setuid/setgid bit and every write bit but its own. The directory is deleted on every way
# out. 0 ok, 1 failed (an error on stderr).
_dxb_apps_unpack_appimage() {
  local file=$1 dest=$2 asset=${1##*/} x rc=1
  x=$(mktemp -d "$DXB_APPS_EXTRACT_BASE/dxberry-appimage.XXXXXXXX") || { echo "could not create a directory in $DXB_APPS_EXTRACT_BASE to unpack $asset" >&2; return 1; }
  if ! { cp "$file" "$x/$asset" && chmod 700 "$x" "$x/$asset" && "$DXB_CHOWN" -R "$DXB_APPS_USER:$DXB_APPS_USER" "$x"; }; then
    echo "could not prepare $x to unpack $asset" >&2
  elif ! ( cd "$x" && "$DXB_RUNUSER" -u "$DXB_APPS_USER" -- env HOME="$x" "$x/$asset" --appimage-extract ) > /dev/null 2>&1 \
      || [[ -L $x/squashfs-root || ! -d $x/squashfs-root ]]; then
    echo "$asset could not be unpacked" >&2
  elif ! { mv -T "$x/squashfs-root" "$dest" && "$DXB_CHOWN" -R root:root "$dest" && chmod -R u-s,g-s,go-w,a+rX "$dest"; }; then
    echo "$asset could not be taken over from $DXB_APPS_USER" >&2
  else
    rc=0
  fi
  rm -rf "$x"
  return $rc
}

# dxb_apps_place NAME LATEST [FILE_NAME]: put LATEST's downloaded file in place. deb: apt-get
# installs it (its dependencies resolve) and an unpacked copy of NAME is removed. appimage:
# _dxb_apps_unpack_appimage, into $DXB_APPS_OPT/NAME/squashfs-root read-only to dxbapps. bin: $DXB_APPS_OPT/NAME/FILE_NAME (default NAME),
# mode 755. tarball: unpacked into $DXB_APPS_OPT/NAME, keeping what _dxb_apps_keep names. Each
# unpacked kind gets $DXB_APPS_OPT/NAME/VERSION and replaces the old directory only once the new
# one is complete. 0 ok, 6 the program was left as it was.
dxb_apps_place() {
  local name=$1 bin=${3:-$1} type asset ver file dir stage old k
  type=$(jq -r .type <<< "$2"); asset=$(jq -r .asset <<< "$2"); ver=$(jq -r .version <<< "$2")
  file=$DXB_APPS_WORK/$asset; dir=$DXB_APPS_OPT/$name
  [[ -f $file ]] || { echo "$asset was not downloaded; $name was left as it was" >&2; return 6; }
  if [[ $type == deb ]]; then
    echo "installing $asset"
    _dxb_apps_apt install -y --no-install-recommends "$file" || { echo "apt-get could not install $asset; $name was left as it was" >&2; return 6; }
    # a copy unpacked from an earlier AppImage release must not be what runs
    rm -rf "$dir"
    return 0
  fi
  stage=$DXB_APPS_OPT/.$name.new; old=$DXB_APPS_OPT/.$name.old
  rm -rf "$stage" "$old"
  if ! { mkdir -p "$stage" && chmod 755 "$DXB_APPS_OPT" "$stage"; }; then
    echo "could not create $stage; $name was left as it was" >&2; rm -rf "$stage"; return 6
  fi
  echo "unpacking $asset"
  case $type in
    appimage) _dxb_apps_unpack_appimage "$file" "$stage/squashfs-root" || { rm -rf "$stage"; return 6; } ;;
    bin)
      if ! { cp "$file" "$stage/$bin" && chmod 755 "$stage/$bin"; }; then
        echo "$asset could not be copied; $name was left as it was" >&2; rm -rf "$stage"; return 6
      fi ;;
    tarball)
      if ! tar -xzf "$file" -C "$stage" --strip-components=1 --no-same-owner; then
        echo "$asset could not be unpacked; $name was left as it was" >&2; rm -rf "$stage"; return 6
      fi
      while IFS= read -r k; do
        [[ -n $k && $k != /* && $k != *..* && -e $dir/$k ]] || continue
        rm -rf "${stage:?}/$k"
        if ! { mkdir -p "$(dirname "$stage/$k")" && cp -a "$dir/$k" "$stage/$k"; }; then
          echo "could not keep $k from the installed $name; $name was left as it was" >&2; rm -rf "$stage"; return 6
        fi
      done <<< "$(_dxb_apps_keep "$name")" ;;
    *) echo "$asset: cannot install a file of type $type" >&2; rm -rf "$stage"; return 6 ;;
  esac
  printf '%s\n' "$ver" > "$stage/VERSION" || { echo "could not write $stage/VERSION; $name was left as it was" >&2; rm -rf "$stage"; return 6; }
  if [[ -e $dir ]] && ! mv -T "$dir" "$old"; then
    echo "could not move the installed $name aside; $name was left as it was" >&2; rm -rf "$stage"; return 6
  fi
  if ! mv -T "$stage" "$dir"; then
    [[ -e $old ]] && mv -T "$old" "$dir"
    echo "could not put the new $name in place; $name was left as it was" >&2; rm -rf "$stage"; return 6
  fi
  rm -rf "$old"
  return 0
}

# dxb_apps_fetch_install NAME SOURCE [FILE_NAME]: the newest release of SOURCE (any catalog-style
# source string, e.g. a module's helper program) downloaded, checked and placed as NAME. 0 ok, 6
# nothing changed.
dxb_apps_fetch_install() {
  local l rc=0
  l=$(dxb_apps_source_latest "$2") || { echo "the newest $1 could not be found; nothing changed" >&2; return 6; }
  dxb_apps_download "$l" || return 6
  dxb_apps_place "$1" "$l" "${3:-}" || rc=6
  rm -f "$DXB_APPS_WORK/$(jq -r .asset <<< "$l")"
  (( rc )) || echo "$1 $(jq -r .version <<< "$l") installed"
  return $rc
}

# ---- install and remove (job bodies) -------------------------------------------------------
_dxb_apps_builtin_refusal() { echo "$1 is part of DXBerry and is updated under Updates" >&2; return 6; }

# dxb_apps_install NAME [update]: install (or, with update, bring up to date) one program. The
# user and groups first; for an upstream program the newest file is downloaded and checked before
# anything is installed; then the packages it needs (apt-get install --no-upgrade: the session
# packages for anything that is not a service, the catalog's packages and, on install, a Debian
# program itself); then the file; then the module's optional app_NAME_install_extra. In update
# mode a Debian program is upgraded with --only-upgrade (Ruling A); an upstream one installs the
# newer file exactly as a first install does. Only packages that are not installed yet are asked
# for. Progress lines on stdout, errors on stderr. 0 ok, 2 usage, 3 unknown, 6 nothing changed,
# 8 something changed (packages went in, a file was placed) but a later step failed.
dxb_apps_install() {
  local name=$1 mode=${2:-install} row src stype label kind pkg='' latest='' type='' ver='' rc=0 fail=6 p
  local -a pkgs=() missing=()
  [[ $mode == install || $mode == update ]] || { echo "usage: dxb_apps_install NAME [update]" >&2; return 2; }
  row=$(dxb_apps_row "$name") || return 3
  src=$(_dxb_apps_source "$name") || return 6
  stype=$(jq -r .type <<< "$src"); label=$(jq -r .label <<< "$row"); kind=$(jq -r .kind <<< "$row")
  [[ $stype != builtin ]] || { _dxb_apps_builtin_refusal "$label"; return 6; }
  dxb_apps_user_ensure || { echo "the program user could not be prepared; $label was not installed" >&2; return 6; }
  if [[ $stype == apt ]]; then
    pkg=$(jq -r .package <<< "$src")
    if [[ $mode == update && -z $(_dxb_apps_dpkg_version "$pkg") ]]; then
      echo "$label is not installed; nothing to update" >&2; return 6
    fi
  else
    echo "looking up the newest $label"
    latest=$(dxb_apps_source_latest "$(jq -r .source <<< "$row")") \
      || { echo "the newest $label could not be found (is the Pi online?); nothing changed" >&2; return 6; }
    type=$(jq -r .type <<< "$latest"); ver=$(jq -r .version <<< "$latest")
    echo "$label $ver ($(jq -r .asset <<< "$latest"))"
    dxb_apps_download "$latest" || { rm -rf "$DXB_APPS_WORK"; return 6; }
  fi
  _dxb_apps_apt update || echo "apt-get update failed; using the package lists already on the Pi"
  [[ $kind == service ]] || read -ra pkgs <<< "$DXB_APPS_BASE_PACKAGES"
  mapfile -t -O "${#pkgs[@]}" pkgs < <(jq -r '.packages[]' <<< "$row")
  [[ $stype == apt && $mode == install ]] && pkgs+=("$pkg")
  for p in "${pkgs[@]}"; do [[ -n $(_dxb_apps_dpkg_version "$p") ]] || missing+=("$p"); done
  if (( ${#missing[@]} )); then
    echo "installing ${missing[*]}"
    if ! _dxb_apps_apt install -y --no-install-recommends --no-upgrade "${missing[@]}"; then
      echo "apt-get could not install ${missing[*]}; $label was not installed" >&2; rm -rf "$DXB_APPS_WORK"; return 6
    fi
    # from here on a failure leaves the Pi changed: the packages stay
    fail=8
    # the logging bridge (logbook.sh) waits for socat: it starts now that socat is here
    if [[ " ${missing[*]} " == *" socat "* ]] && "$DXB_SYSTEMCTL" is-enabled --quiet dxberry-logbridge.service 2> /dev/null; then
      "$DXB_SYSTEMCTL" --no-block start dxberry-logbridge.service > /dev/null 2>&1 || echo "the logging bridge did not start; it starts at the next restart" >&2
    fi
  fi
  if [[ $stype == apt && $mode == update ]]; then
    echo "upgrading $pkg"
    if ! _dxb_apps_apt install -y --no-install-recommends --only-upgrade "$pkg"; then
      echo "apt-get could not upgrade $pkg" >&2; return $fail
    fi
  fi
  if [[ -n $latest ]]; then
    if ! dxb_apps_place "$name" "$latest"; then
      (( fail == 8 )) && echo "the packages $label needs were installed, but $label itself was not" >&2
      rm -rf "$DXB_APPS_WORK"; return $fail
    fi
    # an unpacked release replaces a .deb an earlier release installed, or that one would still run
    pkg=$(_dxb_apps_package "$name" "$src")
    if [[ $type != deb && -n $pkg && -n $(_dxb_apps_dpkg_version "$pkg") ]]; then
      echo "removing the earlier $pkg package"
      _dxb_apps_apt remove -y "$pkg" || { echo "apt-get could not remove the earlier $pkg package" >&2; rc=8; }
    fi
  fi
  if _dxb_apps_has "$name" install_extra; then
    echo "setting up $label"
    "app_${name}_install_extra" || { echo "$label is installed, but setting it up failed" >&2; rc=8; }
  fi
  rm -rf "$DXB_APPS_WORK"
  [[ -n $ver ]] || ver=$(dxb_apps_version "$name")
  (( rc )) || echo "$label${ver:+ $ver} installed"
  return $rc
}

# _dxb_apps_delete_unpacked NAME: delete $DXB_APPS_OPT/NAME except what _dxb_apps_keep names
# (the operator's own files inside an unpacked program, e.g. Wavelog's configuration and uploads);
# without its VERSION the program counts as not installed, and a reinstall keeps those files.
_dxb_apps_delete_unpacked() {
  local dir=$DXB_APPS_OPT/$1 kept=$DXB_APPS_OPT/.$1.kept k any=0
  rm -rf "$kept"
  while IFS= read -r k; do
    [[ -n $k && $k != /* && $k != *..* && -e $dir/$k ]] || continue
    mkdir -p "$(dirname "$kept/$k")" && mv -T "$dir/$k" "$kept/$k" || return 1
    any=1
  done <<< "$(_dxb_apps_keep "$1")"
  rm -rf "$dir" || return 1
  if (( any )); then mv -T "$kept" "$dir" || return 1; else rm -rf "$kept"; fi
}

# _dxb_apps_lock_record: dxberry-radio's record lock (record.lock, fd 8), so no radio is handed to
# a program while it is being removed. 0 ok (also without flock), 5 busy.
_dxb_apps_lock_record() {
  local f=$DXB_RUN_DIR/record.lock
  mkdir -p "$DXB_RUN_DIR" 2> /dev/null
  exec 8> "$f" || return 0
  command -v flock > /dev/null 2>&1 || return 0
  flock -w "${DXB_RADIO_LOCK_WAIT:-180}" 8 || { echo "a radio change is still running; try again when it has finished" >&2; return 5; }
}

# dxb_apps_remove NAME: _dxb_apps_remove under the record lock (released when it returns).
dxb_apps_remove() {
  local rc
  _dxb_apps_lock_record || return 5
  _dxb_apps_remove "$@"; rc=$?
  exec 8>&-
  return $rc
}

# _dxb_apps_remove NAME: refused (5) while a radio is NAME's - the record is read again here,
# under the lock, whatever the command that started the job saw. Then: stop and disable every
# session of NAME, apt-get remove (never purge) the program's own package - not the shared
# packages - delete $DXB_APPS_OPT/NAME (all but what the module's app_NAME_keep names), then the
# module's optional app_NAME_remove_extra. Settings under $DXB_APPS_HOME and the first-fill marks
# stay, so a reinstall continues where the operator left off. 0 ok, 3 unknown, 5 a radio runs it,
# 6 nothing changed (also: the radio record could not be read), 8 something changed (a running or enabled session was stopped, the package
# went) but a later step failed.
_dxb_apps_remove() {
  local name=$1 row src label pkg u rc=0 fail=6 owners
  row=$(dxb_apps_row "$name") || return 3
  src=$(_dxb_apps_source "$name") || return 6
  label=$(jq -r .label <<< "$row")
  [[ $(jq -r .type <<< "$src") != builtin ]] || { _dxb_apps_builtin_refusal "$label"; return 6; }
  # a record that cannot be read could hide an owner: nothing is removed then
  if [[ -f $DXB_RADIOS_FILE ]] && ! dxb_radio_load > /dev/null 2>&1; then
    echo "the radio record could not be read; nothing removed" >&2; return 6
  fi
  [[ -n ${DXB_RADIOS:-} ]] || DXB_RADIOS=$(dxb_radio_empty_record)
  owners=$(jq -r --arg a "$name" '[.radios | to_entries[] | select(.value.owner == $a) | .key] | join(", ")' <<< "$DXB_RADIOS")
  [[ -z $owners ]] || { echo "$name runs on $owners; switch $owners to another program first" >&2; return 5; }
  while IFS= read -r u; do
    [[ -n $u ]] || continue
    if "$DXB_SYSTEMCTL" is-active --quiet "$u" 2> /dev/null || [[ $("$DXB_SYSTEMCTL" is-enabled "$u" 2> /dev/null) == enabled ]]; then
      fail=8
    fi
    "$DXB_SYSTEMCTL" stop "$u" > /dev/null 2>&1
    "$DXB_SYSTEMCTL" disable "$u" > /dev/null 2>&1
    if "$DXB_SYSTEMCTL" is-active --quiet "$u" 2> /dev/null; then echo "$u could not be stopped" >&2; rc=8; fi
  done < <({
    for u in $(jq -r '.radios | keys[]' <<< "$DXB_RADIOS" 2> /dev/null); do dxb_app_session_unit "$name" "$u"; done
    "$DXB_SYSTEMCTL" list-units --all --plain --no-legend "dxberry-screen@$name-*" 2> /dev/null | awk '{ print $1 }'
  } | sort -u)
  rm -f "$(_dxb_sessions_dir)/$name"-*.waiting 2> /dev/null
  pkg=$(_dxb_apps_package "$name" "$src")
  if [[ -n $pkg && -n $(_dxb_apps_dpkg_version "$pkg") ]]; then
    echo "removing $pkg"
    _dxb_apps_apt remove -y "$pkg" || { echo "apt-get could not remove $pkg; $label was not removed" >&2; return $fail; }
  fi
  if [[ -e $DXB_APPS_OPT/$name ]]; then
    echo "deleting $DXB_APPS_OPT/$name"
    _dxb_apps_delete_unpacked "$name" || { echo "could not delete $DXB_APPS_OPT/$name" >&2; rc=8; }
  fi
  if _dxb_apps_has "$name" remove_extra; then
    "app_${name}_remove_extra" || { echo "$label is removed, but cleaning up after it failed" >&2; rc=8; }
  fi
  (( rc )) || echo "$label removed (its settings are kept)"
  return $rc
}

# ---- provisioning --------------------------------------------------------------------------
# provision_apps: the programs' plumbing, every run and idempotent (spec section 15). The
# dxbapps user and its groups; the session, ARDOP, logging-bridge and flush units and the
# tmpfiles file installed (daemon-reload and tmpfiles only when one changed); the logbook step
# (provision_logbook: timer and bridge enabled, its status line); the nginx site of an installed
# program that has one re-rendered, so a renewed Cockpit certificate is picked up (a warning when
# that fails; nginx is never installed here). It never installs or upgrades a program and never
# rewrites a program's settings. A failure is a failed step named apps.
provision_apps() {
  local t content dest changed=0 rows n=0 name site msg
  if ! msg=$(dxb_apps_user_ensure 2>&1); then
    dxb_step_failed apps "${msg:-the program user could not be set up}"
    return 1
  fi
  mkdir -p "$DXB_SYSTEMD_DIR" "$DXB_TMPFILES_DIR" 2> /dev/null
  for t in dxberry-screen@.service dxberry-ardop@.service dxberry-logbridge.service dxberry-logbook-flush.service dxberry-logbook-flush.timer; do
    content=$(< "$DXB_TEMPLATES/$t") || { dxb_step_failed apps "the $t template is missing"; return 1; }
    dest=$DXB_SYSTEMD_DIR/$t
    if dxb_write_if_changed "$dest" "$content" 644; then
      [[ $(< "$dest") == "$content" ]] || { dxb_step_failed apps "could not write $dest"; return 1; }
      changed=1
    fi
  done
  content=$(< "$DXB_TEMPLATES/dxberry-apps.tmpfiles") || { dxb_step_failed apps "the dxberry-apps.tmpfiles template is missing"; return 1; }
  dest=$DXB_TMPFILES_DIR/dxberry-apps.conf
  if dxb_write_if_changed "$dest" "$content" 644; then
    [[ -f $dest && $(< "$dest") == "$content" ]] || { dxb_step_failed apps "could not write $dest"; return 1; }
    changed=1
  fi
  if (( changed )); then
    "$DXB_SYSTEMCTL" daemon-reload || { dxb_step_failed apps "systemctl daemon-reload failed"; return 1; }
    systemd-tmpfiles --create "$dest" 2> /dev/null || dxb_warn "systemd-tmpfiles could not create the program directories from $dest"
  fi
  provision_logbook || return 1
  if rows=$(dxb_apps_rows 2> /dev/null); then
    while IFS= read -r name; do
      dxb_apps_installed "$name" && n=$((n + 1))
    done < <(jq -r '.[].name' <<< "$rows")
  else
    dxb_warn "apps: the program catalog could not be read"
  fi
  if [[ -n $(_dxb_apps_dpkg_version nginx) ]]; then
    for name in pat wavelog; do
      site=$DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf
      [[ -e $site ]] || continue
      dxb_apps_installed "$name" || continue
      if [[ $name == wavelog ]]; then
        dxb_webproxy_site wavelog "ROOT=$DXB_APPS_OPT/wavelog" || dxb_warn "apps: the $name nginx site could not be re-rendered"
      else
        dxb_webproxy_site "$name" || dxb_warn "apps: the $name nginx site could not be re-rendered"
      fi
    done
  fi
  dxb_status_add "apps: $n programs installed (manage them on the DXBerry page)"
  return 0
}
