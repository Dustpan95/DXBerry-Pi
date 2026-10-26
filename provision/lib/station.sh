#!/bin/bash
# shellcheck shell=bash
# The station app (design 5.1-5.3, 6.3, 15): dxberryd behind nginx on 443 and 80, its PAM service
# and unit, and Cockpit removed. Expects common.sh, apps.sh, settings.sh and webproxy.sh sourced.

: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_STATION_BIN:=/opt/dxberry/bin/dxberryd}"
: "${DXB_STATION_UNIT:=dxberryd.service}"
: "${DXB_PAM_DIR:=/etc/pam.d}"
: "${DXB_SYSTEMCTL:=systemctl}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_STATION_TLS:=/etc/dxberry/tls}"
: "${DXB_STATION_WAIT_S:=20}"
: "${DXB_STATION_API_URL:=http://127.0.0.1:8090/api/version}"
: "${DXB_STATION_API_WAIT_S:=10}"
: "${DXB_STATION_LISTEN_WAIT_S:=5}"
: "${DXB_STATION_CURL:=curl}"
: "${DXB_SS:=ss}"
: "${DXB_COCKPIT_PACKAGES:=cockpit-ws cockpit-bridge cockpit-system}"
: "${DXB_COCKPIT_LINK:=/usr/share/cockpit/dxberry}"
: "${DXB_COCKPIT_DROPIN_DIR:=/etc/systemd/system/cockpit.socket.d}"

# DietPi masks systemd-logind; without it the terminal's login sessions and the Restart and Shut
# down buttons fail. Re-asserted on every run, since a DietPi update could mask it again.
dxb_station_logind() {
  if [[ $("$DXB_SYSTEMCTL" is-enabled systemd-logind 2> /dev/null) == masked ]]; then
    "$DXB_SYSTEMCTL" unmask systemd-logind > /dev/null 2>&1 || { dxb_step_failed station "could not unmask systemd-logind"; return 1; }
    dxb_info "systemd-logind unmasked"
  fi
  "$DXB_SYSTEMCTL" start systemd-logind > /dev/null 2>&1 || { dxb_step_failed station "could not start systemd-logind"; return 1; }
}

# _dxb_station_write DEST TEMPLATE: the template into DEST when it differs. 0 written, 1 unchanged,
# 2 failed (step recorded).
_dxb_station_write() {
  local content
  content=$(< "$DXB_TEMPLATES/$2") || { dxb_step_failed station "template $2 is missing"; return 2; }
  mkdir -p "$(dirname "$1")" 2> /dev/null
  dxb_write_if_changed "$1" "$content" 644 || return 1
  [[ -f $1 && $(< "$1") == "$content" ]] && return 0
  dxb_step_failed station "could not write $1"
  return 2
}

# dxb_station_cockpit_installed: 0 when the Cockpit server package is installed.
dxb_station_cockpit_installed() { [[ -n $(_dxb_apps_dpkg_version cockpit-ws) ]]; }

# dxb_station_remove_cockpit: Cockpit held ports 443 and 80, so it goes first. 0 done, 1 not purged
# (ports are free), 2 not stopped (ports still held). Steps recorded. Only called once the station app is up.
dxb_station_remove_cockpit() {
  local rc=0
  # still running, it keeps 443 and 80: nothing may be loaded over it
  if ! "$DXB_SYSTEMCTL" disable --now cockpit.socket > /dev/null 2>&1; then
    dxb_step_failed station "could not stop cockpit.socket; it keeps ports 443 and 80 until a re-run"
    return 2
  fi
  # shellcheck disable=SC2086
  if ! _dxb_apps_apt purge -y $DXB_COCKPIT_PACKAGES > /dev/null 2>&1; then
    dxb_step_failed station "could not remove Cockpit ($DXB_COCKPIT_PACKAGES); it keeps ports 443 and 80 until a re-run"
    rc=1
  fi
  rm -f "$DXB_COCKPIT_LINK"
  rm -rf "$DXB_COCKPIT_DROPIN_DIR"
  (( rc )) || dxb_info "Cockpit removed"
  return "$rc"
}

# _dxb_station_wait_cert: the app makes its certificate before it listens; wait for it.
_dxb_station_wait_cert() {
  local i
  for (( i = 0; i <= DXB_STATION_WAIT_S; i++ )); do
    [[ -s $DXB_STATION_TLS/station.crt && -s $DXB_STATION_TLS/station.key ]] && return 0
    (( i < DXB_STATION_WAIT_S )) && sleep 1
  done
  return 1
}

# _dxb_station_site NAME [PLACEHOLDER=VALUE...]: NAME's nginx site current with the station
# certificate: made if absent, re-rendered if it differs or the certificate is newer, left (and
# nginx not reloaded) otherwise. 0 ok, 6 failed.
_dxb_station_site() {
  local name=$1 rc
  if [[ ! -e $DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf ]]; then
    dxb_webproxy_site "$@"
    return
  fi
  dxb_webproxy_site_stale "$@"; rc=$?
  case $rc in
    1) return 0 ;;
    0) dxb_webproxy_site_refresh "$@" ;;
    *) return 6 ;;
  esac
}

# _dxb_station_wait_api: dxberryd answers its version on loopback (a few tries over
# DXB_STATION_API_WAIT_S seconds). 0 answers, 1 not.
_dxb_station_wait_api() {
  local i
  for (( i = 0; i <= DXB_STATION_API_WAIT_S; i++ )); do
    "$DXB_STATION_CURL" -fsS --max-time 2 -o /dev/null "$DXB_STATION_API_URL" > /dev/null 2>&1 && return 0
    (( i < DXB_STATION_API_WAIT_S )) && sleep 1
  done
  return 1
}

# _dxb_station_wait_listen: something listens on port 443 (nginx, once it has loaded the site).
_dxb_station_wait_listen() {
  local i
  for (( i = 0; i <= DXB_STATION_LISTEN_WAIT_S; i++ )); do
    [[ -n $("$DXB_SS" -Hltn 'sport = :443' 2> /dev/null) ]] && return 0
    (( i < DXB_STATION_LISTEN_WAIT_S )) && sleep 1
  done
  return 1
}

# _dxb_station_site_prepare: the station's nginx site written, enabled and checked with nginx -t,
# but NOT loaded: while Cockpit holds 443 and 80, nginx could not bind them anyway. A failing
# check puts the site back as it was (unlinked when it was new). 0 changed, 1 current, 6 failed.
_dxb_station_site_prepare() {
  local avail=$DXB_NGINX_DIR/sites-available/dxberry-dxberry.conf enabled=$DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf
  local content old='' had=0 rc
  content=$(dxb_webproxy_render dxberry) || return 6
  if [[ -e $enabled ]]; then
    had=1
    dxb_webproxy_site_stale dxberry; rc=$?
    case $rc in 1) return 1 ;; 0) ;; *) return 6 ;; esac
    old=$(< "$avail")
  fi
  mkdir -p "$DXB_NGINX_DIR/sites-available" "$DXB_NGINX_DIR/sites-enabled" || return 6
  dxb_write_if_changed "$avail" "$content" 644
  [[ $(< "$avail") == "$content" ]] || { echo "could not write $avail" >&2; return 6; }
  ln -sfn "$avail" "$enabled" || { echo "could not enable the dxberry site" >&2; return 6; }
  if ! "$DXB_NGINX" -t >&2; then
    if (( had )); then dxb_write_if_changed "$avail" "$old" 644; else rm -f "$enabled"; fi
    echo "nginx rejected the dxberry site; it is not enabled" >&2
    return 6
  fi
  # the site file is stamped as current (touched) by provision_station once nginx has reloaded, so
  # a certificate renewed in place counts as picked up only when it really was. A site that was
  # rewritten but not reloaded is retried on the next run only while the stamp is still old; once
  # the content is on disk it is "current" by content, so it retries when the content changes again
  return 0
}

# _dxb_station_cockpit_certs_left: the first enabled site whose certificate is Cockpit's or is
# gone (name on stdout, 0), else 1. Removing Cockpit under such a site would make nginx -t reject
# every site, the station's included.
_dxb_station_cockpit_certs_left() {
  local f c
  for f in "$DXB_NGINX_DIR"/sites-enabled/*; do
    [[ -f $f ]] || continue
    while IFS= read -r c; do
      if [[ $c == */cockpit/* || ! -f $c ]]; then basename "$f" .conf; return 0; fi
    done < <(sed -n 's/^[[:space:]]*ssl_certificate\(_key\)\{0,1\}[[:space:]]*"\{0,1\}\([^";]*\)"\{0,1\};.*/\2/p' "$f")
  done
  return 1
}

# _dxb_station_audio_sync: the radios' sound servers brought in line once their unit (or the
# packages it runs) has just been installed. An update's radio apply ran before this step, when
# there was no unit to start (radio.sh, dxb_audio_sync).
_dxb_station_audio_sync() {
  if declare -F dxb_audio_sync_all > /dev/null; then dxb_audio_sync_all; fi
  return 0
}

# provision_station: never fatal; every problem is a failed step named station and the run goes on.
provision_station() {
  local rc cockpit site_new reload=0 unit_changed=0 audio_changed=0 audio_installed=0 want_restart=0 was_active=0 restamp=0 sum='' stamp=$DXB_STATE_DIR/dxberryd.sha256 name msg
  local -a extra
  # the network undo's boot unit is a safety net, not a station feature
  dxb_netsafe_install_unit || dxb_step_failed station "could not install dxberry-config-boot.service"

  if ! msg=$(dxb_webproxy_install 2>&1); then
    dxb_step_failed station "nginx is not available: ${msg##*$'\n'}"
    dxb_station_logind
    dxb_status_add "station: unavailable (nginx is not installed; see FAILED STEPS)"
    return 0
  fi
  if [[ -z $(_dxb_apps_dpkg_version libpam-runtime) ]]; then
    _dxb_apps_apt install -y --no-install-recommends libpam-runtime > /dev/null 2>&1 \
      || dxb_step_failed station "could not install libpam-runtime"
  fi
  # the browser audio's runtime: Opus (the app links libopus), the sound server the audio unit
  # runs, and its parec/pacat/pactl tools. Installed only when missing
  local -a audio_missing=()
  for name in libopus0 pulseaudio pulseaudio-utils; do
    [[ -n $(_dxb_apps_dpkg_version "$name") ]] || audio_missing+=("$name")
  done
  if (( ${#audio_missing[@]} )); then
    if _dxb_apps_apt install -y --no-install-recommends "${audio_missing[@]}" > /dev/null 2>&1; then audio_installed=1
    else dxb_step_failed station "could not install ${audio_missing[*]} (browser audio needs them)"; fi
  fi
  if ! { mkdir -p "$DXB_STATION_TLS" && chmod 700 "$DXB_STATION_TLS"; }; then dxb_step_failed station "could not create $DXB_STATION_TLS"; fi
  _dxb_station_write "$DXB_PAM_DIR/dxberry" pam-dxberry; rc=$?
  _dxb_station_write "$DXB_SYSTEMD_DIR/$DXB_STATION_UNIT" dxberryd.service; rc=$?
  (( rc == 0 )) && unit_changed=1
  # the radios' sound server unit (dxberry-audio): systemd reads it, the app is not restarted for it
  _dxb_station_write "$DXB_SYSTEMD_DIR/dxberry-audio@.service" dxberry-audio@.service; rc=$?
  (( rc == 0 )) && audio_changed=1
  dxb_station_logind

  if [[ ! -x $DXB_STATION_BIN ]]; then
    dxb_warn "station: $DXB_STATION_BIN is missing; the station app is not started and Cockpit is left alone"
    (( unit_changed || audio_changed )) && { "$DXB_SYSTEMCTL" daemon-reload > /dev/null 2>&1 || dxb_step_failed station "systemctl daemon-reload failed"; }
    (( audio_changed || audio_installed )) && _dxb_station_audio_sync
    dxb_status_add "station: unavailable (dxberryd is not installed)"
    return 0
  fi

  if (( unit_changed || audio_changed )); then
    "$DXB_SYSTEMCTL" daemon-reload > /dev/null 2>&1 || { dxb_step_failed station "systemctl daemon-reload failed"; return 0; }
    (( unit_changed )) && want_restart=1
  fi
  (( audio_changed || audio_installed )) && _dxb_station_audio_sync
  # a new binary (an update) needs a restart, which enable --now alone never does
  sum=$(sha256sum "$DXB_STATION_BIN" 2> /dev/null | cut -d' ' -f1)
  [[ -f $stamp && $(< "$stamp") == "$sum" ]] || want_restart=1
  "$DXB_SYSTEMCTL" is-active --quiet "$DXB_STATION_UNIT" 2> /dev/null && was_active=1
  if ! "$DXB_SYSTEMCTL" enable --now "$DXB_STATION_UNIT" > /dev/null 2>&1; then
    dxb_step_failed station "could not start $DXB_STATION_UNIT"
    dxb_status_add "station: unavailable (see FAILED STEPS)"
    return 0
  fi
  # the stamp says "this binary and unit are running": written only once that is true
  if (( want_restart && was_active )); then
    if "$DXB_SYSTEMCTL" restart "$DXB_STATION_UNIT" > /dev/null 2>&1; then restamp=1
    else dxb_step_failed station "could not restart $DXB_STATION_UNIT"; fi
  else
    restamp=1
  fi
  if (( restamp )) && [[ -n $sum && ( ! -f $stamp || $(< "$stamp") != "$sum" ) ]]; then printf '%s\n' "$sum" > "$stamp" 2> /dev/null; fi

  if ! _dxb_station_wait_cert; then
    dxb_step_failed station "no certificate in $DXB_STATION_TLS ($DXB_STATION_UNIT did not make one); Cockpit is left alone"
    dxb_status_add "station: unavailable (see FAILED STEPS)"
    return 0
  fi

  # Pat and Wavelog (8081, 8082) move onto the station certificate first: Cockpit's purge can
  # remove the certificate they were rendered against, and nginx -t would then reject every site
  for name in pat wavelog; do
    [[ -e $DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf ]] || continue
    extra=()
    [[ $name == wavelog ]] && extra=("ROOT=$DXB_APPS_OPT/wavelog")
    _dxb_station_site "$name" "${extra[@]}" > /dev/null 2>&1 || dxb_warn "station: the $name nginx site could not be re-rendered"
  done

  # Cockpit stays until the station is proven: the app answers, and nginx accepts its site (-t
  # only validates, it binds nothing, so it works while Cockpit still holds 443 and 80)
  if ! _dxb_station_wait_api; then
    dxb_step_failed station "$DXB_STATION_UNIT did not answer at $DXB_STATION_API_URL; Cockpit is left alone"
    dxb_status_add "station: unavailable (see FAILED STEPS)"
    return 0
  fi
  cockpit=0; dxb_station_cockpit_installed && cockpit=1
  if (( cockpit )) && name=$(_dxb_station_cockpit_certs_left); then
    dxb_step_failed station "site ${name#dxberry-} still uses Cockpit's certificate; not removing Cockpit"
    dxb_status_add "station: unavailable (see FAILED STEPS)"
    return 0
  fi
  site_new=0
  [[ -e $DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf ]] || site_new=1
  msg=$(_dxb_station_site_prepare 2>&1); rc=$?
  case $rc in
    0) reload=1 ;;
    1) ;;
    *)
      dxb_step_failed station "the nginx site failed: ${msg##*$'\n'}"
      (( cockpit )) && dxb_warn "station: Cockpit is left alone"
      dxb_status_add "station: unavailable (see FAILED STEPS)"
      return 0 ;;
  esac
  # Cockpit holds 443 and 80 until it is gone, so nginx loads the site only after this
  rc=0
  if (( cockpit )); then
    dxb_station_remove_cockpit; case $? in 0) reload=1 ;; 1) rc=1; reload=1 ;; *) rc=2 ;; esac
  fi
  if (( rc == 2 )); then
    # Cockpit still holds the ports: a site that did not exist before is not left enabled for the
    # next reload to trip over
    (( site_new )) && rm -f "$DXB_NGINX_DIR/sites-enabled/dxberry-dxberry.conf"
    dxb_status_add "station: unavailable (see FAILED STEPS)"
    return 0
  fi
  if (( reload )); then
    if "$DXB_SYSTEMCTL" reload nginx > /dev/null 2>&1; then
      touch "$DXB_NGINX_DIR/sites-available/dxberry-dxberry.conf"
    else
      dxb_step_failed station "nginx could not reload with the dxberry site"
      rc=1
    fi
  fi
  if (( rc == 0 )) && ! _dxb_station_wait_listen; then
    dxb_step_failed station "nothing is listening on port 443 after nginx loaded the dxberry site"
    rc=1
  fi
  if (( rc )); then
    dxb_status_add "station: unavailable (see FAILED STEPS)"
  else
    dxb_status_add "station: https://${DXB_CFG[_IP]:-<this-pi>}/ (log in as dietpi)"
  fi
  return 0
}
