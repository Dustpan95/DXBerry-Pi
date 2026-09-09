#!/bin/bash
# shellcheck shell=bash
# GPS receiver -> gpsd -> chrony (time) and Graywolf (position); fix readout (spec section 8).

: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_GPSD_DEFAULT:=/etc/default/gpsd}"
: "${DXB_CHRONY_DROPIN:=/etc/chrony/conf.d/dxberry.conf}"
: "${DXB_GPSPIPE:=gpspipe}"
: "${DXB_RPI_CONFIG_TXT:=$(dxb_boot_dir)/config.txt}"

# GPS_BAUD applies to a receiver DXBerry names itself (uart or an explicit /dev/tty path);
# a hotplugged USB receiver is gpsd's own business, so -s is left off there.
dxb_gps_gpsd_default() {
  local start=true devices='' options='-n'
  (( DXB_CFG[_GPS] )) || start=false
  devices=${DXB_CFG[_GPS_PATH]}
  [[ -z ${DXB_CFG[_GPS_PATH]} ]] || options="-n -s ${DXB_CFG[GPS_BAUD]}"
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || devices="${devices:+$devices }/dev/pps0"
  dxb_render "$DXB_TEMPLATES/gpsd-default.tmpl" "START=$start" "DEVICES=$devices" "OPTIONS=$options"
}

dxb_gps_chrony_conf() {
  local ns=''
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || ns=' noselect'
  dxb_render "$DXB_TEMPLATES/chrony-dxberry.conf" "NOSELECT=$ns"
}

# Writes gpsd and chrony configuration and (re)starts what changed. 0 ok, 1 a failed step was recorded.
# Every template is rendered (and checked) before either file is written, so a missing/broken
# template never leaves one file changed on disk while the other step never ran.
dxb_gps_configure() {
  local gpsd_content chrony_content='' changed=0 rc=0
  gpsd_content=$(dxb_gps_gpsd_default) || { dxb_step_failed gps "gpsd template missing"; return 1; }
  if (( DXB_CFG[_GPS] )); then
    chrony_content=$(dxb_gps_chrony_conf) || { dxb_step_failed gps "chrony template missing"; return 1; }
  fi
  mkdir -p "$(dirname "$DXB_GPSD_DEFAULT")" "$(dirname "$DXB_CHRONY_DROPIN")" 2> /dev/null
  if dxb_write_if_changed "$DXB_GPSD_DEFAULT" "$gpsd_content" 644; then
    if [[ -f $DXB_GPSD_DEFAULT && $(< "$DXB_GPSD_DEFAULT") == "$gpsd_content" ]]; then changed=1
    else dxb_step_failed gps "could not write $DXB_GPSD_DEFAULT"; return 1; fi
  fi
  if (( DXB_CFG[_GPS] )); then
    if dxb_write_if_changed "$DXB_CHRONY_DROPIN" "$chrony_content" 644; then
      if [[ -f $DXB_CHRONY_DROPIN && $(< "$DXB_CHRONY_DROPIN") == "$chrony_content" ]]; then changed=1
      else dxb_step_failed gps "could not write $DXB_CHRONY_DROPIN"; return 1; fi
    fi
    systemctl enable gpsd.socket > /dev/null 2>&1 || { dxb_step_failed gps "could not enable gpsd.socket"; rc=1; }
    if (( changed )); then
      systemctl restart gpsd > /dev/null 2>&1 || { dxb_step_failed gps "could not restart gpsd"; rc=1; }
      systemctl restart chrony > /dev/null 2>&1 || { dxb_step_failed gps "could not restart chrony"; rc=1; }
      dxb_info "gpsd and chrony configured (GPS_DEVICE=${DXB_CFG[GPS_DEVICE]})"
    fi
  else
    if [[ -f $DXB_CHRONY_DROPIN ]]; then rm -f "$DXB_CHRONY_DROPIN"; changed=1; fi
    systemctl disable --now gpsd.socket gpsd > /dev/null 2>&1 || true
    (( changed )) && { systemctl restart chrony > /dev/null 2>&1 || true; dxb_info "GPS disabled (GPS_DEVICE=none)"; }
  fi
  return $rc
}

# config.txt lines for a UART GPS and/or PPS. 0 changed (reboot needed), 1 unchanged.
dxb_gps_boot_config() {
  local changed=1
  if [[ ${DXB_CFG[GPS_DEVICE]} == uart ]]; then
    dxb_ensure_line "$DXB_RPI_CONFIG_TXT" 'enable_uart=1' && changed=0
    dxb_ensure_line "$DXB_RPI_CONFIG_TXT" 'dtoverlay=disable-bt' && changed=0
  fi
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || { dxb_ensure_line "$DXB_RPI_CONFIG_TXT" "dtoverlay=pps-gpio,gpiopin=${DXB_CFG[GPS_PPS]}" && changed=0; }
  return $changed
}

# dxb_maidenhead LAT LON: 6-character grid square.
dxb_maidenhead() {
  awk -v lat="$1" -v lon="$2" 'BEGIN {
    lon += 180; lat += 90
    printf "%c%c%d%d%c%c\n", 65 + int(lon / 20), 65 + int(lat / 10), int((lon % 20) / 2), int(lat % 10),
      97 + int(((lon % 20) % 2) * 12), 97 + int((lat % 1) * 24) }'
}

# dxb_gps_fix: one JSON object from gpsd, {"fix":0,"receiver":false} when there is no daemon or
# no fix. "receiver" comes from the DEVICES report gpsd sends on connect: it separates "gpsd is
# not running / has nothing plugged in" from "the receiver is there but has not found the sky yet".
dxb_gps_fix() {
  local raw tpv sky recv=false
  raw=$(timeout 3 "$DXB_GPSPIPE" -w -n 20 2> /dev/null) || raw=''
  jq -se 'map(select(.class == "DEVICES")) | last // empty | (.devices // []) | length > 0' <<< "$raw" > /dev/null 2>&1 && recv=true
  tpv=$(jq -cs 'map(select(.class == "TPV" and (.mode // 0) >= 2)) | last // empty' <<< "$raw" 2> /dev/null)
  [[ -n $tpv ]] || { printf '{"fix":0,"receiver":%s}\n' "$recv"; return 0; }
  sky=$(jq -cs 'map(select(.class == "SKY")) | last // {}' <<< "$raw" 2> /dev/null)
  jq -cn --argjson t "$tpv" --argjson s "$sky" --argjson recv "$recv" --arg grid "$(dxb_maidenhead "$(jq -r .lat <<< "$tpv")" "$(jq -r .lon <<< "$tpv")")" '
    {fix: $t.mode, receiver: $recv, lat: $t.lat, lon: $t.lon,
     alt_ft: (((($t.altHAE // $t.altMSL // $t.alt // 0) * 3.28084) + 0.5) | floor),
     speed_mph: ((($t.speed // 0) * 2.23694 * 10 + 0.5) | floor / 10),
     sats_used: ($s.uSat // 0), sats_seen: ($s.nSat // 0), time: ($t.time // ""), grid: $grid}'
}

# dxb_gps_state [FIX_JSON]: "no receiver", "no fix", or "3D fix DM97hd (lat, lon), 8/12 satellites".
dxb_gps_state() {
  local j=${1:-}
  [[ -n $j ]] || j=$(dxb_gps_fix)
  if [[ $(jq -r '.fix >= 2' <<< "$j") == true ]]; then
    jq -r '(.fix|tostring) + "D fix " + .grid + " (" + (.lat|tostring) + ", " + (.lon|tostring) + "), " + (.sats_used|tostring) + "/" + (.sats_seen|tostring) + " satellites"' <<< "$j"
  elif [[ $(jq -r '.receiver' <<< "$j") == true ]]; then echo "no fix"
  else echo "no receiver"; fi
}

# dxb_gps_status_line [FIX_JSON]: the status-file line - the configured policy, then the state.
dxb_gps_status_line() { printf 'gps: %s, %s\n' "${DXB_CFG[GPS_DEVICE]:-auto}" "$(dxb_gps_state "${1:-}")"; }
