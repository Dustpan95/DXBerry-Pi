#!/bin/bash
# shellcheck shell=bash
# Shared helpers for the DXBerry-Pi provisioner.

: "${DXB_STATE_DIR:=/var/lib/dxberry}"
: "${DXB_LOG_FILE:=$DXB_STATE_DIR/provision.log}"
# Debian's "a reboot is needed" marker; the console and the update check read it.
: "${DXB_REBOOT_FLAG:=/run/reboot-required}"
declare -ga DXB_FAILED_STEPS=() DXB_STATUS_LINES=()
# Space-separated secret key names that were actually consumed (applied somewhere) this run.
# provision_scrub only replaces a secret's plaintext once it is in this list - a key nobody
# managed to use must stay readable so a re-run can retry it.
DXB_CONSUMED_SECRETS=''

# Directory of the user-editable boot (FAT) partition.
dxb_boot_dir() {
  if [[ -n ${DXB_BOOT_DIR:-} ]]; then printf '%s' "$DXB_BOOT_DIR"; return; fi
  if findmnt -no FSTYPE /boot/firmware 2> /dev/null | grep -q vfat; then printf '/boot/firmware'; else printf '/boot'; fi
}

dxb_log() {
  local lvl=$1; shift
  local msg
  msg="$(date '+%F %T') [$lvl] $*"
  printf '%s\n' "$msg" >&2
  [[ -d $(dirname "$DXB_LOG_FILE") ]] && printf '%s\n' "$msg" >> "$DXB_LOG_FILE"
  return 0
}
dxb_info() { dxb_log INFO "$@"; }
dxb_warn() { dxb_log WARN "$@"; }
dxb_error() { dxb_log ERROR "$@"; }

# Successful advance invalidates older evidence even if later work fails. Never
# restore the old generation. Only trusted operator/test configuration selects
# the helper; request JSON cannot select a command or state directory.
_dxb_program_generation_call() {
  local reply='' helper=${DXB_PROGRAM_STATE_CMD:-/opt/dxberry/bin/dxberry-program-state}
  if ! reply=$("$helper" --state "$DXB_STATE_DIR/station" "$1" 2> /dev/null) \
    || [[ $reply != '{"ok":true,"protocol":1}' ]]; then
    dxb_error "ownership tracking is unavailable; check program and radio status"
    return 6
  fi
}

dxb_program_generation_advance() { _dxb_program_generation_call advance; }
# Provisioning alone explicitly prepares the parent before advancing. Normal
# writers/activation still refuse a missing parent and never repair state.
dxb_program_generation_prepare_advance() { _dxb_program_generation_call prepare-advance; }

dxb_step_failed() { DXB_FAILED_STEPS+=("$1: $2"); dxb_error "$1: $2"; }
dxb_status_add() { DXB_STATUS_LINES+=("$1"); }

# dxb_secret_consumed KEY: record that KEY's plaintext value was actually used this run.
dxb_secret_consumed() { DXB_CONSUMED_SECRETS+=" $1"; }
# dxb_secret_was_consumed KEY: true if dxb_secret_consumed KEY was called this run.
dxb_secret_was_consumed() { [[ " $DXB_CONSUMED_SECRETS " == *" $1 "* ]]; }

dxb_status_write() {
  local f=$1
  {
    echo "DXBerry-Pi status - $(date '+%F %T %Z')"
    printf '%s\n' "${DXB_STATUS_LINES[@]}"
    echo
    if (( ${#DXB_FAILED_STEPS[@]} )); then
      echo "FAILED STEPS:"
      printf '  - %s\n' "${DXB_FAILED_STEPS[@]}"
      if [[ ${DXB_MODE:-run} == first-boot ]]; then
        echo "Fix the cause, then run: sudo dxberry-provision --first-boot"
      else
        echo "Fix the cause, then run: sudo dxberry-provision"
      fi
    else
      echo "All steps completed."
    fi
  } > "$f"
}

# dxb_require_root: exit 1 unless running as root. DXB_FAKE_EUID stands in for the real EUID (the
# tests run the commands as themselves).
dxb_require_root() { (( ${DXB_FAKE_EUID:-$EUID} == 0 )) || { echo "this command must run as root (use sudo)" >&2; exit 1; }; }

# Single-quote STRING for a bash-array style file such as dietpi-wifi.txt.
dxb_squote() { printf "'%s'" "${1//\'/\'\\\'\'}"; }

# dxb_set_kv FILE KEY VALUE: replace the first "KEY=" line or append one.
dxb_set_kv() {
  local file=$1 key=$2 val=$3 kre
  kre=$(printf '%s' "$key" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
  [[ -f $file ]] || : > "$file"
  if grep -qE "^[[:blank:]]*${kre}=" "$file"; then
    if k="$key" kre="$kre" v="$val" awk '
      BEGIN { done = 0 }
      {
        if (!done && $0 ~ ("^[[:blank:]]*" ENVIRON["kre"] "=")) { print ENVIRON["k"] "=" ENVIRON["v"]; done = 1 }
        else print
      }' "$file" > "$file.dxbtmp"; then
      chmod --reference="$file" "$file.dxbtmp" 2>/dev/null || true
      mv -f "$file.dxbtmp" "$file"
    else
      rm -f "$file.dxbtmp"
      return 1
    fi
  else
    printf '%s=%s\n' "$key" "$val" >> "$file"
  fi
}

# dxb_ensure_line FILE LINE: append LINE unless an identical line exists. 0 appended, 1 present, 2 error.
dxb_ensure_line() {
  local file=$1 line=$2
  [[ -f $file ]] || : > "$file" || return 2
  grep -qxF -- "$line" "$file" && return 1
  printf '%s\n' "$line" >> "$file" || return 2
}

# dxb_cfgtxt_ensure_line FILE LINE: dxb_ensure_line for a Raspberry Pi config.txt. Everything
# after a [section] header applies only to the models that header names, so a line appended to a
# file ending in (say) [cm4] would never be read on a Pi 4. An [all] header is appended first
# when the file's last header is not already [all]; a file with no header at all is still in the
# unconditional section, so nothing is added there. 0 appended, 1 already present, 2 error.
dxb_cfgtxt_ensure_line() {
  local file=$1 line=$2 last
  [[ -f $file ]] || : > "$file" || return 2
  grep -qxF -- "$line" "$file" && return 1
  last=$(grep -oE '^[[:blank:]]*\[[^]]*\]' "$file" | tail -1 | tr -d '[:blank:]')
  if [[ -n $last && $last != '[all]' ]]; then printf '[all]\n' >> "$file" || return 2; fi
  dxb_ensure_line "$file" "$line"
}

# dxb_render TEMPLATE NAME=VALUE...: print TEMPLATE with @NAME@ placeholders replaced.
# Placeholder names: uppercase letters, digits, and underscores (@NAME@, @IP1@, @ETH0_MAC@, etc).
dxb_render() {
  local file=$1 content kv
  shift
  [[ -f $file ]] || { echo "dxb_render: no such template: $file" >&2; return 1; }
  content=$(< "$file")
  for kv in "$@"; do content=${content//"@${kv%%=*}@"/${kv#*=}}; done
  if [[ $content =~ @[A-Z0-9_]+@ ]]; then
    echo "dxb_render: unresolved placeholder ${BASH_REMATCH[0]} in $file" >&2
    return 1
  fi
  printf '%s\n' "$content"
}

# dxb_write_if_changed FILE CONTENT [MODE]: 0 = written, 1 = already identical.
# Atomic write: temp file + rename. Returns 0 if written, 1 if unchanged. The temp name carries
# the writer's pid so two processes writing the same file (an apply and a udev hotplug apply)
# cannot share one temp file; the rename is still what makes the result atomic. A write can fail
# silently here (a full or read-only filesystem), so every caller re-reads FILE and compares.
dxb_write_if_changed() {
  local dest=$1 content=$2 mode=${3:-} existing_mode tmp
  if [[ -f $dest && ! -L $dest && $(< "$dest") == "$content" ]]; then return 1; fi
  if [[ -f $dest && ! -L $dest ]]; then existing_mode=$(stat -c %a "$dest"); fi
  tmp="$dest.dxbtmp.$$"
  printf '%s\n' "$content" > "$tmp"
  if [[ -n $mode ]]; then
    chmod "$mode" "$tmp"
  elif [[ -n ${existing_mode:-} ]]; then
    chmod "$existing_mode" "$tmp"
  else
    chmod 644 "$tmp"
  fi || { dxb_warn "chmod failed for $dest (vfat?), continuing"; }
  mv -f "$tmp" "$dest"
  return 0
}

# Fixed internal callbacks only. A fresh shared lease excludes the public update
# worker. Inherit it into changing children; an inheriting survivor keeps updates
# out after its parent exits. This is not complete manager/descendant authority.
dxb_with_update_read_lock() {
  local dir=${DXB_RUN_DIR:-/run/dxberry} file identity fresh _dxb_update_read_fd='' rc
  file=$dir/update-apply.lock
  (( EUID == 0 )) || { echo 'root is required for update coordination' >&2; return 6; }
  command -v flock >/dev/null 2>&1 || { echo 'flock is required for update coordination' >&2; return 6; }
  mkdir -p "$dir" 2>/dev/null || return 6
  [[ -d $dir && ! -L $dir ]] || return 6
  identity=$(stat -c '%d:%i:%u:%a' "$dir") || return 6
  case ${identity#*:*:} in 0:700|0:750|0:755) ;; *) echo 'unsafe update coordination directory' >&2; return 6 ;; esac
  if [[ ! -e $file && ! -L $file ]]; then (set -o noclobber; umask 077; : > "$file") 2>/dev/null || true; fi
  [[ -f $file && ! -L $file && $(stat -c '%u:%h:%s:%a' "$file") == 0:1:0:600 ]] \
    || { echo 'unsafe update coordination lock' >&2; return 6; }
  exec {_dxb_update_read_fd}< "$file" || return 6
  if ! command flock -s -n "$_dxb_update_read_fd"; then
    exec {_dxb_update_read_fd}>&-
    echo 'an update is active; retry after it finishes' >&2
    return 6
  fi
  fresh=$(stat -c '%d:%i:%u:%a' "$dir")
  if [[ $identity != "$fresh" || -L $dir || -L $file || ! $file -ef /proc/self/fd/$_dxb_update_read_fd \
    || $(stat -Lc '%u:%h:%s:%a' "/proc/self/fd/$_dxb_update_read_fd") != 0:1:0:600 ]]; then
    exec {_dxb_update_read_fd}>&-
    echo 'update coordination changed while acquiring its lease' >&2
    return 6
  fi
  "$@"; rc=$?
  if [[ $identity != "$(stat -c '%d:%i:%u:%a' "$dir")" || -L $dir || -L $file \
    || ! $file -ef /proc/self/fd/$_dxb_update_read_fd \
    || $(stat -Lc '%u:%h:%s:%a' "/proc/self/fd/$_dxb_update_read_fd") != 0:1:0:600 ]]; then
    echo 'update coordination changed during work; effects are retained' >&2
    rc=6
  fi
  exec {_dxb_update_read_fd}>&-
  return "$rc"
}

# Called only inside the publication read lease. This checks a bounded protocol
# response and observed file continuity, not complete installed-bundle authority.
dxb_radio_event_protocol_ready() (
  local helper=${DXB_RADIO_EVENT_PROTOCOL_CMD:-/opt/dxberry/bin/dxberry-radio-event}
  local parent helper_identity parent_identity helper_hash helper_size probe_root probe_run=${DXB_RUN_DIR:-/run/dxberry}
  (( EUID == 0 )) || return 6
  parent=${helper%/*}
  [[ $helper == /* && -f $helper && ! -L $helper && -x $helper && -d $parent && ! -L $parent ]] \
    || { echo 'compatible radio event helper is unavailable' >&2; return 6; }
  parent_identity=$(stat -c '%d:%i:%u:%a' "$parent") || return 6
  case ${parent_identity#*:*:} in 0:700|0:750|0:755) ;; *) echo 'unsafe radio event helper directory' >&2; return 6 ;; esac
  [[ $(stat -c '%u:%h:%a' "$helper") == 0:1:755 ]] \
    || { echo 'unsafe radio event helper' >&2; return 6; }
  helper_size=$(stat -c '%s' "$helper") || return 6
  if [[ ! $helper_size =~ ^[0-9]+$ ]] || (( helper_size <= 0 || helper_size > 65536 )); then
    echo 'radio event helper size is unsupported' >&2; return 6
  fi
  helper_identity=$(stat -c '%d:%i:%u:%h:%s:%a' "$helper") || return 6
  helper_hash=$(command sha256sum "$helper") || return 6
  probe_root=$(umask 077; command mktemp -d "$probe_run/radio-event-probe.XXXXXX") || return 6
  trap 'command rm -rf -- "$probe_root"' EXIT
  # Limit stdout to one KiB and elapsed execution to three seconds plus TERM
  # grace. Use real timeout even when a caller's test shell has a stand-in.
  if ! (
    ulimit -c 0 || exit 6
    ulimit -f 1 || exit 6
    command timeout --signal=TERM --kill-after=1 3 "$helper" protocol > "$probe_root/protocol" 2>/dev/null
  ) 2>/dev/null; then
    echo 'radio event helper protocol query failed or exceeded its bounds' >&2; return 6
  fi
  if ! command cmp -s "$probe_root/protocol" <(printf '2\n'); then
    echo 'radio event helper protocol is incompatible' >&2; return 6
  fi
  [[ ! -L $parent && $parent_identity == "$(stat -c '%d:%i:%u:%a' "$parent")" \
    && -f $helper && ! -L $helper && $helper_identity == "$(stat -c '%d:%i:%u:%h:%s:%a' "$helper")" \
    && $helper_hash == "$(command sha256sum "$helper")" ]] \
    || { echo 'radio event helper changed during its protocol query' >&2; return 6; }
)
