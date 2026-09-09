#!/bin/bash
# shellcheck shell=bash
# Graywolf as a radio owner: one audio device + channel + PTT per radio, through the REST API (spec 9.3).
# Expects common.sh, graywolf.sh and radio.sh to be sourced.

app_graywolf_unit() { echo graywolf.service; }
app_graywolf_needs_service_restart() { echo no; }
app_graywolf_wait_ready() { dxb_gw_wait_ready; }

_dxb_gwapp_session() { rm -f "$DXB_GW_COOKIES"; ( umask 077; : > "$DXB_GW_COOKIES" ); dxb_gw_login_any; }
_dxb_gwapp_end() { dxb_gw_api POST /auth/logout > /dev/null 2>&1 || true; rm -f "$DXB_GW_COOKIES"; }

# dxb_gwapp_find_id PATH NAME: id of the item named NAME in GET PATH, or empty.
dxb_gwapp_find_id() { dxb_gw_api GET "$1" 2> /dev/null | jq -r --arg n "$2" 'if type == "array" then (map(select(.name == $n)) | .[0].id // empty) else empty end'; }

# dxb_gwapp_upsert PATH NAME BODY: PUT over the existing item (merged, id stripped) or POST a new one. Prints the id.
dxb_gwapp_upsert() {
  local path=$1 name=$2 body=$3 id cur
  id=$(dxb_gwapp_find_id "$path" "$name")
  if [[ -n $id ]]; then
    cur=$(dxb_gw_api GET "$path" 2> /dev/null | jq -c --arg n "$name" 'map(select(.name == $n)) | .[0] // {}')
    dxb_gw_api PUT "$path/$id" "$(jq -c --argjson o "$body" '. + $o | del(.id)' <<< "$cur")" > /dev/null || return 7
    printf '%s\n' "$id"
  else
    id=$(dxb_gw_api POST "$path" "$body" | jq -r '.id // empty') || return 7
    [[ -n $id ]] || return 7
    printf '%s\n' "$id"
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
    + {invert: false, persist: true}'
}

app_graywolf_wire() {
  local name=$1 r dev ch cur port
  r=$(dxb_radio_get "$name") || return 3
  _dxb_gwapp_session || return 7
  dev=$(dxb_gwapp_upsert /audio-devices "$name" "$(jq -cn --arg n "$name" --arg p "plughw:CARD=$(tr '[:lower:]' '[:upper:]' <<< "$name"),DEV=0" '{name: $n, source_type: "soundcard", source_path: $p, sample_rate: 48000}')") || { _dxb_gwapp_end; return 7; }
  ch=$(dxb_gwapp_upsert /channels "$name" "$(jq -cn --arg n "$name" --argjson d "$dev" '{name: $n, input_device_id: $d, output_device_id: $d, input_channel: 0, output_channel: 0}')") || { _dxb_gwapp_end; return 7; }
  if cur=$(dxb_gw_api GET "/ptt/$ch" 2> /dev/null) && jq -e '.channel_id' <<< "$cur" > /dev/null 2>&1; then
    dxb_gw_api PUT "/ptt/$ch" "$(jq -c --argjson o "$(dxb_gwapp_ptt_payload "$name" "$r" "$ch")" '. + $o | del(.id)' <<< "$cur")" > /dev/null || { _dxb_gwapp_end; return 7; }
  else
    dxb_gw_api POST /ptt "$(dxb_gwapp_ptt_payload "$name" "$r" "$ch")" > /dev/null || { _dxb_gwapp_end; return 7; }
  fi
  if [[ $(jq -r '.ptt.method' <<< "$r") == rigctld ]]; then
    port=$(jq -r '.rigctld_port' <<< "$r")
    if ! dxb_gw_api POST /ptt/test-rigctld "$(jq -cn --argjson p "$port" '{host: "127.0.0.1", port: $p}')" 2> /dev/null | jq -e '.ok == true' > /dev/null 2>&1; then
      dxb_warn "graywolf cannot reach rigctld for $name on 127.0.0.1:$port yet (it may still be starting)"
    fi
  fi
  dxb_info "graywolf wired to $name (audio device $dev, channel $ch)"
  _dxb_gwapp_end
  return 0
}

app_graywolf_unwire() {
  local name=$1 id
  _dxb_gwapp_session || return 7
  id=$(dxb_gwapp_find_id /channels "$name"); [[ -z $id ]] || dxb_gw_api DELETE "/channels/$id?cascade=true" > /dev/null || dxb_warn "could not delete graywolf channel $name"
  id=$(dxb_gwapp_find_id /audio-devices "$name"); [[ -z $id ]] || dxb_gw_api DELETE "/audio-devices/$id" > /dev/null || dxb_warn "could not delete graywolf audio device $name"
  _dxb_gwapp_end
  return 0
}
