#!/bin/bash
# shellcheck shell=bash
# One hamlib rigctld per radio, driven by env files under /run (spec section 7.3).

: "${DXB_RIGCTLD_RUN_DIR:=/run/dxberry/rigctld}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TMPFILES_DIR:=/etc/tmpfiles.d}"
: "${DXB_SYSTEMD_TMPFILES:=systemd-tmpfiles}"
: "${DXB_RIGCTL:=rigctl}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

# dxb_rigctld_env NAME RADIO_JSON [OWNER_KIND]: the EnvironmentFile content for rigctld@NAME.
# OWNER_KIND is dxb_radio_owner_kind's answer (graywolf | session | none; absent = none). Graywolf
# keys cm108 and gpio PTT itself, so only a session program's radio has rigctld key them (spec
# 9.1): the program then uses CAT PTT through rigctld like on any other radio. A radio whose
# rig.control is flrig runs Hamlib's flrig model (4) against flrig's XML-RPC port; the CAT serial
# port is flrig's then, so rigctld gets neither it nor an RTS/DTR line on it.
dxb_rigctld_env() {
  local name=$1 r=$2 kind=${3:-none} model port baud ptt cat='' pttdev='' rig_args='' ptt_args=''
  local use_cat=false use_ptt=false use_hid=false pins binding
  model=$(jq -r '.rig.model' <<< "$r"); port=$(jq -r '.rigctld_port' <<< "$r")
  baud=$(jq -r '.rig.baud' <<< "$r"); ptt=$(jq -r '.rig.ptt_type' <<< "$r")
  [[ $(jq -r '.cat' <<< "$r") == null ]] || cat="/dev/dxberry/$name-cat"
  [[ $(jq -r '.ptt_serial' <<< "$r") == null ]] || pttdev="/dev/dxberry/$name-ptt"
  [[ -z $cat ]] || rig_args="-r $cat -s $baud"
  if [[ $(jq -r '.rig.control // "hamlib"' <<< "$r") == flrig ]]; then
    # flrig owns the CAT serial port: rigctld talks to flrig (Hamlib model 4) on the radio's own
    # XML-RPC port (12345 + index, index = (rigctld port - 4532) / 2) and never opens the port
    model=4; rig_args="-r 127.0.0.1:$(( 12345 + (port - 4532) / 2 ))"
    [[ -n $pttdev ]] || cat=''
  fi
  case $ptt in
    RIG) ptt_args='-P RIG' ;;
    RTS|DTR) [[ -n ${pttdev:-$cat} ]] && ptt_args="-P $ptt -p ${pttdev:-$cat}" ;;
  esac
  # rigctld keys only a pin it was given: a HID that is pinned (/dev/dxberry/NAME-hid exists only
  # then), a GPIO line that is set; otherwise PTT stays off and says why
  if [[ $kind == session ]]; then
    case $(jq -r '.ptt.method // empty' <<< "$r") in
      cm108)
        if [[ $(jq -r '.hid // null | type' <<< "$r") == object ]]; then ptt_args="-P CM108 -p /dev/dxberry/$name-hid"
        else ptt_args=''; dxb_warn "radio $name: its PTT is the sound card's CM108 pin, but no HID is pinned for it; PTT is left off"; fi ;;
      gpio)
        if [[ $(jq -r '.ptt.gpio_line // null | type' <<< "$r") == number ]]; then ptt_args="-P GPIO -p $(jq -r '.ptt.gpio_line' <<< "$r")"
        else ptt_args=''; dxb_warn "radio $name: its PTT is a Pi GPIO pin, but no GPIO line is set; PTT is left off"; fi ;;
    esac
  fi
  # Stable /dev names do not change when their pinned USB identity changes.
  # Include only pins this process actually opens, not unrelated audio/labels
  # or flrig's CAT pin. The extra environment value is metadata, not an argument.
  [[ " $rig_args $ptt_args " == *" /dev/dxberry/$name-cat "* ]] && use_cat=true
  [[ " $ptt_args " == *" /dev/dxberry/$name-ptt "* ]] && use_ptt=true
  [[ " $ptt_args " == *" /dev/dxberry/$name-hid "* ]] && use_hid=true
  pins=$(jq -cS --argjson c "$use_cat" --argjson p "$use_ptt" --argjson h "$use_hid" '
    def pin: {path, vidpid, serial};
    {version:1, cat:(if $c then (.cat | pin) else null end),
     ptt_serial:(if $p then (.ptt_serial | pin) else null end),
     hid:(if $h then (.hid | pin) else null end)}' <<< "$r") || return 6
  binding=$(sha256sum <<< "$pins") || return 6
  binding=${binding%% *}
  [[ $binding =~ ^[0-9a-f]{64}$ ]] || return 6
  printf 'MODEL=%s\nPORT=%s\nRIG_ARGS=%s\nPTT_ARGS=%s\nRIG_PINS_SHA256=%s\n' "$model" "$port" "$rig_args" "$ptt_args" "$binding"
}

# Check the expected intent before clearing it; caller serialization is still
# required. Accepted --no-block work is not proof the process has restarted.
_dxb_rigctld_pending_done() {
  local file=$1 digest=$2
  [[ -e $file || -L $file ]] || return 0
  if [[ -f $file && ! -L $file && $(< "$file") == "$digest" ]]; then
    rm -f -- "$file" && [[ ! -e $file && ! -L $file ]] && return 0
  fi
  dxb_error "rigctld restart intent changed or could not be cleared; try apply again"
  return 6
}

dxb_rigctld_state() {
  local s
  s=$(systemctl is-active "rigctld@$1.service" 2> /dev/null) && { echo "$s"; return 0; }
  case $s in inactive|failed|activating|deactivating) echo "$s" ;; *) echo unknown ;; esac
}

# dxb_rigctld_sync NAME RADIO_JSON PRESENT(0|1) [OWNER_KIND]: env file + start/stop/restart
# (restart only when the env file changed). Starts and restarts are queued (--no-block): the unit is
# ordered after dxberry-radio-hotplug.service, and an apply running inside that service (or holding
# the apply lock while a udev-triggered hotplug waits for it) would wait on itself otherwise; callers
# that need rigctld ready poll for it. 0 ok, 6 error.
dxb_rigctld_sync() {
  local name=$1 r=$2 present=$3 kind=${4:-none} env f pending digest changed=0 retry=0 unit="rigctld@$1.service"
  f="$DXB_RIGCTLD_RUN_DIR/$name.env"
  pending="$DXB_RIGCTLD_RUN_DIR/$name.restart-pending"
  mkdir -p "$DXB_RIGCTLD_RUN_DIR" 2> /dev/null || { dxb_error "cannot create $DXB_RIGCTLD_RUN_DIR"; return 6; }
  env=$(dxb_rigctld_env "$name" "$r" "$kind") || { dxb_error "could not derive $unit configuration"; return 6; }
  if [[ -e $f || -L $f ]] && [[ ! -f $f || -L $f ]]; then dxb_error "refusing a non-regular rigctld environment"; return 6; fi
  if [[ -e $pending || -L $pending ]]; then
    [[ -f $pending && ! -L $pending ]] || { dxb_error "refusing a non-regular rigctld restart intent"; return 6; }
    retry=1
  fi
  [[ -f $f && $(< "$f") == "$env" ]] || changed=1
  digest=$(sha256sum <<< "$env") || return 6
  digest=${digest%% *}
  [[ $digest =~ ^[0-9a-f]{64}$ ]] || return 6
  if (( changed || retry )); then
    # Intent precedes the environment publication. If publication or the
    # restart request fails, a later apply still owes the restart.
    dxb_write_if_changed "$pending" "$digest" 600 || : # unchanged is also success; read back below
    [[ -f $pending && ! -L $pending && $(< "$pending") == "$digest" ]] || { dxb_error "could not retain $unit restart intent"; return 6; }
  fi
  if (( changed )); then
    dxb_write_if_changed "$f" "$env" 644 || :
    [[ -f $f && ! -L $f && $(< "$f") == "$env" ]] || { dxb_error "could not write $f"; return 6; }
  fi
  if (( present )); then
    if systemctl is-active --quiet "$unit"; then
      if (( changed || retry )); then
        systemctl --no-block restart "$unit" || { dxb_error "restart $unit failed"; return 6; }
        _dxb_rigctld_pending_done "$pending" "$digest" || return 6
        dxb_info "$unit restart requested (configuration changed or pending)"
      fi
    else
      systemctl --no-block start "$unit" || { dxb_error "start $unit failed"; return 6; }
      _dxb_rigctld_pending_done "$pending" "$digest" || return 6
      dxb_info "$unit start requested on port $(jq -r '.rigctld_port' <<< "$r")"
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
    rm -f "$DXB_RIGCTLD_RUN_DIR/$n.restart-pending"
    dxb_info "rigctld@$n stopped and removed (radio no longer in the record)"
  done
  # Publication can fail after creating an intent but before its env exists.
  for f in "$DXB_RIGCTLD_RUN_DIR"/*.restart-pending; do
    [[ -f $f || -L $f ]] || continue
    n=${f##*/}; n=${n%.restart-pending}
    [[ $keep == *" $n "* ]] || rm -f -- "$f"
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
  if [[ ${DXB_RADIO_EVENT_QUEUE:-off} == on ]]; then
    declare -F dxb_with_update_read_lock dxb_radio_event_protocol_ready >/dev/null \
      || { echo 'compatible radio event coordination is unavailable' >&2; return 6; }
    dxb_with_update_read_lock _dxb_rigctld_install_units
  else
    _dxb_rigctld_install_units
  fi
}

_dxb_rigctld_install_units() {
  if [[ ${DXB_RADIO_EVENT_QUEUE:-off} == on ]]; then dxb_radio_event_protocol_ready || return 6; fi
  local rc=1 t dest content
  local units=(rigctld@.service dxberry-radio-hotplug.service dxberry-radio-wire.service)
  if [[ ${DXB_RADIO_EVENT_QUEUE:-off} == on ]]; then
    units+=(dxberry-radio-reconcile.service dxberry-radio-reconcile.path dxberry-radio-reconcile.timer)
  fi
  mkdir -p "$DXB_SYSTEMD_DIR" "$DXB_TMPFILES_DIR" 2> /dev/null
  for t in "${units[@]}"; do
    content=$(< "$DXB_TEMPLATES/$t") || return 6
    dest="$DXB_SYSTEMD_DIR/$t"
    if dxb_write_if_changed "$dest" "$content" 644; then
      [[ $(< "$dest") == "$content" ]] || { dxb_error "could not write $dest"; return 6; }
      rc=0
    fi
  done
  content=$(< "$DXB_TEMPLATES/dxberry-radio.tmpfiles") || return 6
  dest="$DXB_TMPFILES_DIR/dxberry-radio.conf"
  if dxb_write_if_changed "$dest" "$content" 644; then
    [[ -f $dest && $(< "$dest") == "$content" ]] || { dxb_error "could not write $dest"; return 6; }
    rc=0
  fi
  if (( rc == 0 )); then
    systemctl daemon-reload || { dxb_error "systemctl daemon-reload failed"; return 6; }
    "$DXB_SYSTEMD_TMPFILES" --create "$DXB_TMPFILES_DIR/dxberry-radio.conf" 2> /dev/null || mkdir -p "$DXB_RIGCTLD_RUN_DIR"
    dxb_info "rigctld@, dxberry-radio-hotplug and dxberry-radio-wire units installed"
  fi
  systemctl enable dxberry-radio-hotplug.service > /dev/null 2>&1 || { dxb_error "systemctl enable dxberry-radio-hotplug.service failed"; return 6; }
  # the wire unit runs the same apply again after graywolf.service is up: the hotplug unit is
  # ordered before graywolf (rigctld and stable names first), so a re-wire that is due at boot
  # (record changed while the owner was stopped) has no owner to talk to until then
  systemctl enable dxberry-radio-wire.service > /dev/null 2>&1 || { dxb_error "systemctl enable dxberry-radio-wire.service failed"; return 6; }
  # A queued event survives active/failed worker jobs. The path rechecks on
  # completion; the timer recovers delivery after manager start limiting.
  if [[ ${DXB_RADIO_EVENT_QUEUE:-off} == on ]]; then
    for t in dxberry-radio-reconcile.path dxberry-radio-reconcile.timer; do
      systemctl enable --now "$t" > /dev/null 2>&1 || { dxb_error "systemctl enable --now $t failed"; return 6; }
    done
  fi
  return $rc
}

# dxb_rigctld_models: Hamlib's rig models (rigctl -l) as [{model, mfg, name, status}], in rigctl's
# order. The listing is fixed-width and a name can be blank (FLRig), so every field is cut at its
# header's column rather than split on spaces. 0 ok, 6 when rigctl fails or lists nothing readable.
dxb_rigctld_models() {
  local out json
  out=$("$DXB_RIGCTL" -l 2> /dev/null) || { dxb_error "could not list Hamlib's radio models (rigctl -l)"; return 6; }
  json=$(awk '
    function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
    !m { m = index($0, "Mfg"); n = index($0, "Model"); v = index($0, "Version"); s = index($0, "Status"); x = index($0, "Macro")
         if (!(m && n && v && s && x)) m = 0
         next }
    { id = trim(substr($0, 1, m - 1)) }
    id ~ /^[0-9]+$/ { print id "\t" trim(substr($0, m, n - m)) "\t" trim(substr($0, n, v - n)) "\t" trim(substr($0, s, x - s)) }' <<< "$out" \
    | jq -Rnc '[inputs | split("\t") | {model: (.[0] | tonumber), mfg: .[1], name: .[2], status: .[3]}]')
  jq -e 'length > 0' <<< "$json" > /dev/null 2>&1 || { dxb_error "rigctl -l listed no radio models this command could read"; return 6; }
  printf '%s\n' "$json"
}
