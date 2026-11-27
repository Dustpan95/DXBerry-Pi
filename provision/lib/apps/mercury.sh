#!/bin/bash
# shellcheck shell=bash
app_mercury_kind() { echo radio; }
app_mercury_session() { echo yes; }
app_mercury_label() { echo "Mercury HF"; }
app_mercury_multi() { echo no; }   # the built-in TNC uses fixed ports
app_mercury_audio() { echo pulse; }
app_mercury_screen() { echo yes; }
app_mercury_needs_service_restart() { echo no; }
app_mercury_installed() { dxb_apps_installed mercury; }
app_mercury_companion_package() { echo mercury; }
app_mercury_version() { dxb_apps_version mercury; }
app_mercury_unit() { dxb_app_session_unit mercury "$1"; }
app_mercury_unwire() { return 0; }

# Limit the upstream signing key to this source and the archive to these two packages.
app_mercury_prepare_source() {
  local apt=${DXB_ETC_APT:-/etc/apt} key=$DXB_SHARE/hermes-archive.asc source pin
  [[ -r $key ]] || return 6
  source="deb [arch=arm64 signed-by=$key] https://debian.hermes.radio/hermes trixie main"
  pin=$'Package: mercury mercury-ui\nPin: origin debian.hermes.radio\nPin-Priority: 500\n\nPackage: *\nPin: origin debian.hermes.radio\nPin-Priority: -1'
  dxb_write_if_changed "$apt/preferences.d/dxberry-mercury" "$pin" 644 || [[ $(< "$apt/preferences.d/dxberry-mercury") == "$pin" ]] || return 6
  dxb_write_if_changed "$apt/sources.list.d/dxberry-mercury.list" "$source" 644 || [[ $(< "$apt/sources.list.d/dxberry-mercury.list") == "$source" ]] || return 6
}

app_mercury_settings_file() { printf '%s/.config/dxberry-mercury/%s.ini\n' "$DXB_APPS_HOME" "$1"; }

app_mercury_firstfill() {
  local f call
  f=$(app_mercury_settings_file "$1")
  [[ ! -e $f ]] || return 0
  mkdir -p "${f%/*}" || return 6
  call=$(dxb_apps_station | jq -er .call) || return 6
  printf '[main]\nsound_system = pulse\ncallsign = %s\n' "$call" > "$f"
}

# The GUI also writes this file. Update only the radio-owned keys on a wiring change.
app_mercury_wire() {
  local w f method=none
  f=$(app_mercury_settings_file "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  mkdir -p "${f%/*}" || return 6
  if [[ $(jq -r .ptt <<< "$w") == cat ]]; then
    method=hamlib
    crudini --set "$f" ptt hamlib_model 2 || return 6
    crudini --set "$f" ptt device "$(jq -r .rigctld <<< "$w")" || return 6
  fi
  crudini --set "$f" ptt method "$method" &&
    crudini --set "$f" main sound_system pulse &&
    crudini --set "$f" main input_device default &&
    crudini --set "$f" main output_device default
}

app_mercury_command() {
  printf '%s\n' mercury-ui -C "$(app_mercury_settings_file "$1")"
}
