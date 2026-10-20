#!/usr/bin/env bash
# Shared environment for the program tests (apps.sh and the program modules). Sourced, never run.
# fx_apps_env: every path under $TEST_TMP, every external command a stand-in, two radios.
#   radio1 - rigctld 4532, VOX PTT, no CAT pin
#   radio2 - rigctld 4534, PTT through rigctld (CAT)
# Stand-ins record into $TEST_TMP/calls ("apt-get ...", "useradd ...", "systemctl ...", ...);
# downloads (ap_curl) are recorded apart, in $TEST_TMP/curl-calls ("curl URL") and with every
# option in $TEST_TMP/curl-args, so a test can say a file never reached apt by looking at calls alone.
# shellcheck disable=SC2034,SC2317

# shellcheck source=tests/fixtures/sysfs.sh
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

fx_apps_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_RUN_DIR=$TEST_TMP/run \
    DXB_RADIOS_FILE=$TEST_TMP/state/radios.json DXB_WIRED_FILE=$TEST_TMP/state/wired.json \
    DXB_SCREENS_DIR=$TEST_TMP/run/screens DXB_APPS_DIR=$DXB_LIB/apps \
    DXB_APPS_TSV=$DXB_ROOT/provision/share/apps.tsv DXB_APPS_OPT=$TEST_TMP/opt/dxberry-apps \
    DXB_APPS_HOME=$TEST_TMP/home/dxbapps DXB_APPS_STATE=$TEST_TMP/state/apps DXB_APPS_WORK=$TEST_TMP/state/apps-work \
    DXB_GH_API=http://gh DXB_APPS_CURL=ap_curl DXB_APT_GET=ap_apt DXB_DPKG_QUERY=ap_dpkgq DXB_APT_CACHE=ap_aptcache \
    DXB_USERADD=ap_useradd DXB_GROUPADD=ap_groupadd DXB_USERMOD=ap_usermod DXB_CHOWN=ap_chown DXB_GETENT=ap_getent \
    DXB_SYSTEMCTL=fx_systemctl DXB_RUNUSER=ap_runuser DXB_GPSPIPE=$TEST_TMP/bin/gpspipe \
    DXB_APPS_EXTRACT_BASE=$TEST_TMP/vartmp
  unset DXB_CONSOLE_USER
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR" "$TEST_TMP/http" "$TEST_TMP/bin" "$DXB_APPS_EXTRACT_BASE"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/curl-calls"; : > "$TEST_TMP/curl-args"; : > "$TEST_TMP/active"
  # gpsd answers with $TEST_TMP/gpspipe.out (nothing: no receiver); timeout needs a real file
  printf '#!/bin/bash\ncat "%s/gpspipe.out" 2> /dev/null\nexit 0\n' "$TEST_TMP" > "$TEST_TMP/bin/gpspipe"
  chmod 755 "$TEST_TMP/bin/gpspipe"
  # the accounts that already exist: the console login user, and the groups Debian ships
  printf 'dietpi:x:1000:1000::/home/dietpi:/bin/bash\n' > "$TEST_TMP/passwd"
  printf 'audio:x:29:\ndialout:x:20:\ndietpi:x:1000:\n' > "$TEST_TMP/group"
  # AP_INSTALLED: Debian packages dpkg-query reports installed, "name" (version 1.0) or "name=version"
  # AP_CANDIDATE: what apt-cache policy offers ("" = no candidate)
  # AP_RUNUSER_RC: when set, ap_runuser fails with it
  AP_INSTALLED=''; AP_CANDIDATE=1.0; AP_RUNUSER_RC=''
  DXB_CFG=()
  DXB_RADIOS='{"version":1,"gps":{"device":"auto","baud":9600,"pps":""},"radios":{
    "radio1":{"label":"FT-818","profile":"generic","audio":{"path":"usb-0:1.3:1.0","vidpid":"0d8c:013c","serial":""},"cat":null,"hid":null,"ptt_serial":null,
      "ptt":{"method":"vox","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4532,"wiring":"full","owner":""},
    "radio2":{"label":"IC-7300","profile":"generic","audio":{"path":"usb-0:1.1:1.0","vidpid":"08bb:2901","serial":""},"cat":{"path":"usb-0:1.1:1.2","vidpid":"10c4:ea60","serial":""},"hid":null,"ptt_serial":null,
      "ptt":{"method":"rigctld","gpio_line":null},"rig":{"model":3073,"baud":115200,"ptt_type":"RIG"},"rigctld_port":4534,"wiring":"full","owner":""}}}'
  DXB_RADIOS=$(jq -c . <<< "$DXB_RADIOS")
}

# ap_release OWNER/REPO TAG ASSET...: GitHub's releases/latest for OWNER/REPO, each asset
# downloadable from http://dl/ASSET (put its bytes in $TEST_TMP/http/ASSET).
ap_release() {
  local repo=$1 tag=$2; shift 2
  mkdir -p "$TEST_TMP/http/$repo/releases"
  jq -n --arg t "$tag" '{tag_name: $t, draft: false, prerelease: false,
    assets: ($ARGS.positional | map({name: ., browser_download_url: ("http://dl/" + .)}))}' \
    --args "$@" > "$TEST_TMP/http/$repo/releases/latest"
}

# ap_curl ARGS...: curl for the tests - the URL without its scheme and host is a file under
# $TEST_TMP/http; -o FILE writes it there; a missing file is curl's 22.
ap_curl() {
  local url='' out='' f
  echo "curl $*" >> "$TEST_TMP/curl-args"
  while (( $# )); do case $1 in -o) out=$2; shift ;; -H) shift ;; http*) url=$1 ;; esac; shift; done
  echo "curl $url" >> "$TEST_TMP/curl-calls"
  f=${url#*://}; f=$TEST_TMP/http/${f#*/}
  [[ -f $f ]] || return 22
  if [[ -n $out ]]; then cp "$f" "$out"; else cat "$f"; fi
}
# ap_apt ARGS...: apt-get - records the call; exits with $TEST_TMP/apt-rc (0), or, when
# $TEST_TMP/apt-fail-on holds one of this call's own words, only THAT call fails (100).
ap_apt() {
  local w fail
  echo "apt-get $*" >> "$TEST_TMP/calls"
  fail=$(cat "$TEST_TMP/apt-fail-on" 2> /dev/null || true)
  if [[ -n $fail ]]; then
    for w in "$@"; do [[ $w == "$fail" ]] && return 100; done
    return 0
  fi
  return "$(cat "$TEST_TMP/apt-rc" 2> /dev/null || echo 0)"
}
# ap_dpkgq -W -f FORMAT PKG: "installed VERSION" for a package in AP_INSTALLED, else dpkg-query's 1.
ap_dpkgq() {
  local p=${*: -1} w
  for w in $AP_INSTALLED; do
    if [[ $w == "$p" ]]; then echo "installed 1.0"; return 0; fi
    if [[ $w == "$p="* ]]; then echo "installed ${w#*=}"; return 0; fi
  done
  echo "dpkg-query: no packages found matching $p" >&2
  return 1
}
# ap_aptcache policy PKG: the policy apt-cache prints, with AP_CANDIDATE as the candidate.
ap_aptcache() {
  local p=${*: -1}
  printf '%s:\n  Installed: (none)\n  Candidate: %s\n  Version table:\n' "$p" "${AP_CANDIDATE:-(none)}"
}
# ap_getent passwd|group NAME: a line from $TEST_TMP/passwd or $TEST_TMP/group (2 when absent).
ap_getent() { grep -m1 "^$2:" "$TEST_TMP/$1" 2> /dev/null || return 2; }
# account stand-ins: recorded, and remembered so a second run finds what the first one made
ap_groupadd() { echo "groupadd $*" >> "$TEST_TMP/calls"; printf '%s:x:900:\n' "${*: -1}" >> "$TEST_TMP/group"; }
ap_useradd() {
  echo "useradd $*" >> "$TEST_TMP/calls"
  printf '%s:x:900:900::%s:/usr/sbin/nologin\n' "${*: -1}" "$DXB_APPS_HOME" >> "$TEST_TMP/passwd"
  printf '%s:x:900:\n' "${*: -1}" >> "$TEST_TMP/group"
  [[ " $* " != *" --create-home "* ]] || mkdir -p "$DXB_APPS_HOME"
}
ap_usermod() { echo "usermod $*" >> "$TEST_TMP/calls"; }
ap_chown() { echo "chown $*" >> "$TEST_TMP/calls"; }
# ap_runuser -u USER -- CMD ARGS...: runuser - records the call. An AppImage's --appimage-extract
# makes ./squashfs-root (an AppRun and a setuid helper, so a test can see the bit taken off);
# any other command runs as it is.
ap_runuser() {
  echo "runuser $*" >> "$TEST_TMP/calls"
  [[ -z ${AP_RUNUSER_RC:-} ]] || return "$AP_RUNUSER_RC"
  [[ $1 == -u && $3 == -- ]] || return 1
  shift 3
  if [[ " $* " == *" --appimage-extract "* ]]; then
    mkdir -p squashfs-root && printf '#!/bin/sh\n' > squashfs-root/AppRun && chmod 755 squashfs-root/AppRun \
      && : > squashfs-root/helper && chmod 4755 squashfs-root/helper
    return
  fi
  "$@"
}
