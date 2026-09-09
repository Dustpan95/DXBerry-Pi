#!/bin/bash
# shellcheck shell=bash
# One hamlib rigctld per radio, driven by env files under /run (spec section 7.3).

: "${DXB_RIGCTLD_RUN_DIR:=/run/dxberry/rigctld}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TMPFILES_DIR:=/etc/tmpfiles.d}"
: "${DXB_RIGCTL:=rigctl}"

# dxb_rigctld_env NAME RADIO_JSON: the EnvironmentFile content for rigctld@NAME.
dxb_rigctld_env() {
  local name=$1 r=$2 model port baud ptt cat='' pttdev='' rig_args='' ptt_args=''
  model=$(jq -r '.rig.model' <<< "$r"); port=$(jq -r '.rigctld_port' <<< "$r")
  baud=$(jq -r '.rig.baud' <<< "$r"); ptt=$(jq -r '.rig.ptt_type' <<< "$r")
  [[ $(jq -r '.cat' <<< "$r") == null ]] || cat="/dev/dxberry/$name-cat"
  [[ $(jq -r '.ptt_serial' <<< "$r") == null ]] || pttdev="/dev/dxberry/$name-ptt"
  [[ -z $cat ]] || rig_args="-r $cat -s $baud"
  case $ptt in
    RIG) ptt_args='-P RIG' ;;
    RTS|DTR) [[ -n ${pttdev:-$cat} ]] && ptt_args="-P $ptt -p ${pttdev:-$cat}" ;;
  esac
  printf 'MODEL=%s\nPORT=%s\nRIG_ARGS=%s\nPTT_ARGS=%s\n' "$model" "$port" "$rig_args" "$ptt_args"
}

dxb_rigctld_state() {
  local s
  s=$(systemctl is-active "rigctld@$1.service" 2> /dev/null) && { echo "$s"; return 0; }
  case $s in inactive|failed|activating|deactivating) echo "$s" ;; *) echo unknown ;; esac
}

# dxb_rigctld_sync NAME RADIO_JSON PRESENT(0|1): env file + start/stop/restart. 0 ok, 6 error.
dxb_rigctld_sync() {
  local name=$1 r=$2 present=$3 env f changed=0 unit="rigctld@$1.service"
  f="$DXB_RIGCTLD_RUN_DIR/$name.env"
  mkdir -p "$DXB_RIGCTLD_RUN_DIR" 2> /dev/null || { dxb_error "cannot create $DXB_RIGCTLD_RUN_DIR"; return 6; }
  env=$(dxb_rigctld_env "$name" "$r")
  if dxb_write_if_changed "$f" "$env" 644; then
    [[ $(< "$f") == "$env" ]] || { dxb_error "could not write $f"; return 6; }
    changed=1
  fi
  if (( present )); then
    if systemctl is-active --quiet "$unit"; then
      (( changed )) && { systemctl restart "$unit" || { dxb_error "restart $unit failed"; return 6; }; dxb_info "$unit restarted (configuration changed)"; }
    else
      systemctl start "$unit" || { dxb_error "start $unit failed"; return 6; }
      dxb_info "$unit started on port $(jq -r '.rigctld_port' <<< "$r")"
    fi
  elif systemctl is-active --quiet "$unit"; then
    systemctl stop "$unit" || { dxb_error "stop $unit failed"; return 6; }
    dxb_info "$unit stopped (radio absent)"
  fi
  return 0
}

# dxb_rigctld_stop_all_except NAME...: stop and forget instances whose radio left the record.
dxb_rigctld_stop_all_except() {
  local f n keep=" $* "
  for f in "$DXB_RIGCTLD_RUN_DIR"/*.env; do
    [[ -f $f ]] || continue
    n=${f##*/}; n=${n%.env}
    [[ $keep == *" $n "* ]] && continue
    systemctl stop "rigctld@$n.service" 2> /dev/null
    rm -f "$f"
    dxb_info "rigctld@$n stopped and removed (radio no longer in the record)"
  done
  return 0
}

# dxb_rigctld_query PORT: "FREQ MODE" from the running instance, or "? ?".
dxb_rigctld_query() {
  local out
  if out=$(timeout 1 "$DXB_RIGCTL" -m 2 -r "127.0.0.1:$1" f m 2> /dev/null) && [[ -n $out ]]; then
    awk 'NR == 1 { f = $1 } NR == 2 { m = $1 } END { print f, m }' <<< "$out"
  else
    echo '? ?'
  fi
}

# 0 installed/changed, 1 already in place, 6 failure.
dxb_rigctld_install_units() {
  local rc=1 t dest content
  mkdir -p "$DXB_SYSTEMD_DIR" "$DXB_TMPFILES_DIR" 2> /dev/null
  for t in rigctld@.service dxberry-radio-hotplug.service; do
    content=$(< "$DXB_TEMPLATES/$t") || return 6
    dest="$DXB_SYSTEMD_DIR/$t"
    if dxb_write_if_changed "$dest" "$content" 644; then
      [[ $(< "$dest") == "$content" ]] || { dxb_error "could not write $dest"; return 6; }
      rc=0
    fi
  done
  content=$(< "$DXB_TEMPLATES/dxberry-radio.tmpfiles") || return 6
  if dxb_write_if_changed "$DXB_TMPFILES_DIR/dxberry-radio.conf" "$content" 644; then rc=0; fi
  if (( rc == 0 )); then
    systemctl daemon-reload || { dxb_error "systemctl daemon-reload failed"; return 6; }
    systemd-tmpfiles --create "$DXB_TMPFILES_DIR/dxberry-radio.conf" 2> /dev/null || mkdir -p "$DXB_RIGCTLD_RUN_DIR"
    dxb_info "rigctld@ and dxberry-radio-hotplug units installed"
  fi
  systemctl enable dxberry-radio-hotplug.service > /dev/null 2>&1 || { dxb_error "systemctl enable dxberry-radio-hotplug.service failed"; return 6; }
  return $rc
}
