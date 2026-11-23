#!/bin/bash
# shellcheck shell=bash
# Graywolf as a radio owner: input + output audio devices, channel and PTT (spec 9.3).
# Expects common.sh, graywolf.sh and radio.sh to be sourced.

# One service for every radio (a channel each), so the unit ignores the radio argument; not a
# session program (no app_graywolf_session), so the hand-over core treats it exactly as before.
app_graywolf_unit() { echo graywolf.service; }
app_graywolf_label() { echo Graywolf; }
app_graywolf_kind() { echo radio; }
app_graywolf_installed() { [[ -n $(dxb_gw_installed_version) ]]; }
app_graywolf_version() { dxb_gw_installed_version; }
app_graywolf_needs_service_restart() { echo no; }
app_graywolf_wait_ready() { dxb_gw_wait_ready; }

_dxb_gwapp_session() { rm -f "$DXB_GW_COOKIES"; ( umask 077; : > "$DXB_GW_COOKIES" ); dxb_gw_login_any; }
_dxb_gwapp_end() { dxb_gw_api POST /auth/logout > /dev/null 2>&1 || true; rm -f "$DXB_GW_COOKIES"; }

# dxb_gwapp_find_id PATH NAME: id of the item named NAME in GET PATH, empty if not found, or
# returns 7 (nothing printed) when the list itself could not be fetched - the caller must not
# treat that the same as "not found", or a transient GET failure turns into a DELETE with no id.
dxb_gwapp_find_id() {
  local list
  list=$(dxb_gw_api GET "$1") || return 7
  jq -e 'type == "array"' <<< "$list" > /dev/null 2>&1 || return 7
  jq -r --arg n "$2" 'map(select(.name == $n)) | .[0].id // empty' <<< "$list"
}

# dxb_gwapp_upsert PATH NAME BODY [CREATE_DEFAULTS]: PUT over the existing item (merged, id stripped) or POST a new
# one. Prints "ID created" after a POST, "ID updated" after a PUT (it runs in a command
# substitution, so a flag variable would never reach the caller). Fetches the list exactly once, so a transient GET failure can never fall
# through to a duplicating POST, and the PUT is always built from the object this call actually
# read (never from a second, possibly-failed, GET).
dxb_gwapp_upsert() {
  local path=$1 name=$2 body=$3 list cur id merged defaults='{}'
  (( $# < 4 )) || defaults=$4
  list=$(dxb_gw_api GET "$path") || return 7
  jq -e 'type == "array"' <<< "$list" > /dev/null 2>&1 || return 7
  cur=$(jq -c --arg n "$name" 'map(select(.name == $n)) | .[0] // empty' <<< "$list")
  if [[ -n $cur ]]; then
    id=$(jq -r '.id' <<< "$cur")
    if [[ $path == /audio-devices ]] && ! jq -e --argjson o "$body" '.direction == $o.direction' <<< "$cur" > /dev/null; then
      dxb_error "graywolf audio device $name has the wrong direction; it was not changed"
      return 7
    fi
    # Channel listings include computed backing/PTT summaries. They are not
    # writable request fields and Graywolf rejects them on a full-resource PUT.
    merged=$(jq -c --argjson o "$body" '. + $o | del(.id,.backing,.ptt)' <<< "$cur")
    dxb_gw_api PUT "$path/$id" "$merged" > /dev/null || return 7
    # Graywolf's full-device PUT interprets output gain 0 as its -12 dB default.
    # The focused gain endpoint accepts 0 literally, preserving an operator's setting.
    if [[ $path == /audio-devices ]] && jq -e '.direction == "output" and .gain_db == 0' <<< "$cur" > /dev/null; then
      dxb_gw_api PUT "$path/$id/gain" '{"gain_db":0}' > /dev/null || return 7
    fi
    printf '%s updated\n' "$id"
  else
    merged=$(jq -c --argjson o "$body" '. + $o' <<< "$defaults")
    id=$(dxb_gw_api POST "$path" "$merged" | jq -r '.id // empty') || return 7
    [[ -n $id ]] || return 7
    printf '%s created\n' "$id"
  fi
}

# dxb_gwapp_ptt_payload NAME RADIO_JSON CHANNEL_ID
dxb_gwapp_ptt_payload() {
  local name=$1 r=$2 ch=$3
  jq -cn --arg n "$name" --argjson r "$r" --argjson ch "$ch" '
    {channel_id: $ch, method: $r.ptt.method}
    + (if $r.ptt.method == "rigctld" then {device_path: ("127.0.0.1:" + ($r.rigctld_port | tostring))}
       elif $r.ptt.method == "cm108" then {device_path: ("/dev/dxberry/" + $n + "-hid"), gpio_pin: 3}
       elif $r.ptt.method == "gpio" then {device_path: "/dev/gpiochip0", gpio_line: ($r.ptt.gpio_line // 0)}
       else {} end)
    + {invert: false}'
}

# Graywolf's modem only picks up the channel table at start (measured on 0.14.13: channels
# deleted through the API kept receiving, so every frame arrived once per channel that had ever
# existed and the digipeater's dedup swallowed the real one). A created or deleted channel is
# therefore followed by a restart (a re-created audio device too: the channel would keep the
# old device id in the modem); an update of an existing channel applies live. The restart is
# --no-block: this can run inside dxberry-radio-hotplug.service, which graywolf.service is
# ordered After=, and a blocking restart from there waits on itself.
app_graywolf_wire() {
  local name=$1 r rx tx ch cur port errf pcm restart=0
  r=$(dxb_radio_get "$name") || return 3
  _dxb_gwapp_session || { _dxb_gwapp_end; return 7; }
  # Keep the existing input name/id and its gain. A space in the TX suffix keeps
  # it distinct from another radio's valid identifier (for example radio1-tx).
  pcm="plughw:CARD=$(tr '[:lower:]' '[:upper:]' <<< "$name"),DEV=0"
  rx=$(dxb_gwapp_upsert /audio-devices "$name" "$(jq -cn --arg n "$name" --arg p "$pcm" '{name:$n,direction:"input",source_type:"soundcard",device_path:$p,sample_rate:48000,channels:1,format:"s16le"}')") || { _dxb_gwapp_end; return 7; }
  [[ ${rx#* } == created ]] && restart=1
  rx=${rx%% *}
  tx=$(dxb_gwapp_upsert /audio-devices "$name TX" "$(jq -cn --arg n "$name TX" --arg p "$pcm" '{name:$n,direction:"output",source_type:"soundcard",device_path:$p,sample_rate:48000,channels:1,format:"s16le"}')") || { _dxb_gwapp_end; return 7; }
  [[ ${tx#* } == created ]] && restart=1
  tx=${tx%% *}
  ch=$(dxb_gwapp_upsert /channels "$name" "$(jq -cn --arg n "$name" --argjson rx "$rx" --argjson tx "$tx" '{name:$n,input_device_id:$rx,output_device_id:$tx,input_channel:0,output_channel:0}')" \
    '{"modem_type":"afsk","bit_rate":1200,"mark_freq":1200,"space_freq":2200}') || { _dxb_gwapp_end; return 7; }
  [[ ${ch#* } == created ]] && restart=1
  ch=${ch%% *}
  errf=$(mktemp) || { _dxb_gwapp_end; return 7; }
  if cur=$(dxb_gw_api GET "/ptt/$ch" 2> "$errf"); then
    rm -f "$errf"
    jq -e --argjson ch "$ch" '.channel_id == $ch' <<< "$cur" > /dev/null 2>&1 || { _dxb_gwapp_end; return 7; }
    dxb_gw_api PUT "/ptt/$ch" "$(jq -c --argjson o "$(dxb_gwapp_ptt_payload "$name" "$r" "$ch")" '. + $o | del(.id)' <<< "$cur")" > /dev/null || { _dxb_gwapp_end; return 7; }
  else
    # Only a missing resource warrants creation. A transient read failure must
    # not overwrite an existing channel's operator-tuned CSMA settings.
    if ! grep -qE 'error: 404\b' "$errf"; then rm -f "$errf"; _dxb_gwapp_end; return 7; fi
    rm -f "$errf"
    dxb_gw_api POST /ptt "$(dxb_gwapp_ptt_payload "$name" "$r" "$ch")" > /dev/null || { _dxb_gwapp_end; return 7; }
  fi
  if [[ $(jq -r '.ptt.method' <<< "$r") == rigctld ]]; then
    port=$(jq -r '.rigctld_port' <<< "$r")
    if ! dxb_gw_api POST /ptt/test-rigctld "$(jq -cn --argjson p "$port" '{host: "127.0.0.1", port: $p}')" 2> /dev/null | jq -e '.ok == true' > /dev/null 2>&1; then
      dxb_warn "graywolf cannot reach rigctld for $name on 127.0.0.1:$port yet (it may still be starting)"
    fi
  fi
  dxb_info "graywolf wired to $name (input $rx, output $tx, channel $ch)"
  _dxb_gwapp_end
  if (( restart )); then
    systemctl --no-block restart "$(app_graywolf_unit)" || { dxb_error "could not restart graywolf after adding channel $name"; return 7; }
    dxb_info "graywolf restarting so the modem opens channel $name"
  fi
  return 0
}

app_graywolf_unwire() {
  local name=$1 id audio_name deleted=0
  _dxb_gwapp_session || { _dxb_gwapp_end; return 7; }
  if id=$(dxb_gwapp_find_id /channels "$name"); then
    if [[ -n $id ]]; then
      if dxb_gw_api DELETE "/channels/$id?cascade=true" > /dev/null; then deleted=1; else dxb_warn "could not delete graywolf channel $name"; fi
    fi
  else
    dxb_warn "could not list graywolf channels to unwire $name"
  fi
  for audio_name in "$name" "$name TX"; do
    if id=$(dxb_gwapp_find_id /audio-devices "$audio_name"); then
      [[ -z $id ]] || dxb_gw_api DELETE "/audio-devices/$id" > /dev/null || dxb_warn "could not delete graywolf audio device $audio_name"
    else
      dxb_warn "could not list graywolf audio devices to unwire $audio_name"
    fi
  done
  _dxb_gwapp_end
  # try-restart: only if it is running; a hand-over that leaves graywolf idle stops it right after
  (( deleted )) && { systemctl --no-block try-restart "$(app_graywolf_unit)" || dxb_warn "could not restart graywolf after removing channel $name"; }
  return 0
}
