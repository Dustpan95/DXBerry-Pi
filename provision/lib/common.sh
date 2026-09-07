#!/bin/bash
# shellcheck shell=bash
# Shared helpers for the DXBerry-Pi provisioner.

: "${DXB_STATE_DIR:=/var/lib/dxberry}"
: "${DXB_LOG_FILE:=$DXB_STATE_DIR/provision.log}"
declare -ga DXB_FAILED_STEPS=() DXB_STATUS_LINES=()

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
dxb_step_failed() { DXB_FAILED_STEPS+=("$1: $2"); dxb_error "$1: $2"; }
dxb_status_add() { DXB_STATUS_LINES+=("$1"); }

dxb_status_write() {
  local f=$1
  {
    echo "DXBerry-Pi status - $(date '+%F %T %Z')"
    printf '%s\n' "${DXB_STATUS_LINES[@]}"
    echo
    if (( ${#DXB_FAILED_STEPS[@]} )); then
      echo "FAILED STEPS:"
      printf '  - %s\n' "${DXB_FAILED_STEPS[@]}"
      echo "Fix the cause, then run: sudo dxberry-provision"
    else
      echo "All steps completed."
    fi
  } > "$f"
}

dxb_require_root() { (( EUID == 0 )) || { echo "this command must run as root (use sudo)" >&2; exit 1; }; }

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
# Atomic write: temp file + rename. Returns 0 if written, 1 if unchanged.
dxb_write_if_changed() {
  local dest=$1 content=$2 mode=${3:-} existing_mode
  if [[ -f $dest && ! -L $dest && $(< "$dest") == "$content" ]]; then return 1; fi
  if [[ -f $dest && ! -L $dest ]]; then existing_mode=$(stat -c %a "$dest"); fi
  printf '%s\n' "$content" > "$dest.dxbtmp"
  if [[ -n $mode ]]; then
    chmod "$mode" "$dest.dxbtmp"
  elif [[ -n ${existing_mode:-} ]]; then
    chmod "$existing_mode" "$dest.dxbtmp"
  else
    chmod 644 "$dest.dxbtmp"
  fi || { dxb_warn "chmod failed for $dest (vfat?), continuing"; }
  mv -f "$dest.dxbtmp" "$dest"
  return 0
}
