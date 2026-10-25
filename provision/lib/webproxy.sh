#!/bin/bash
# shellcheck shell=bash
# The web proxy (applications spec): nginx in front of a program's own web page, letting a request
# through only with a live station login. Three snippets, from two templates:
#   snippets/dxberry-auth-gate.conf       auth_request + error_page: the gate itself, valid in a
#                                         server or in one location (Wavelog's /install)
#   snippets/dxberry-auth-endpoints.conf  the two locations the gate asks and sends to (server
#                                         level, once per site that uses the gate anywhere)
#   snippets/dxberry-auth.conf            both together: a whole server behind the login (Pat)
# Runs inside the app-install job, which already holds the update and config locks,
# so nothing here takes a lock. Ruling 11: installing nginx never takes port 80 before the station site is ready (a
# policy-rc.d that refuses service starts is in place for the install; Debian's default site is
# removed; nginx then starts with DXBerry's sites only).
# Expects common.sh and apps.sh to be sourced.

: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_NGINX_DIR:=/etc/nginx}"
: "${DXB_NGINX:=nginx}"
: "${DXB_POLICY_RC:=/usr/sbin/policy-rc.d}"
: "${DXB_POLICY_MARK:=# DXBerry: temporary, removed after the nginx install}"
: "${DXB_STATION_TLS:=/etc/dxberry/tls}"

# dxb_webproxy_install: nginx, installed without starting, DXBerry's auth snippets, nginx enabled.
# Idempotent. 0 ok, 6 failed.
dxb_webproxy_install() {
  local gate ends name text
  # a policy-rc.d of ours left by an install that died (power loss) would block every service start
  if [[ -f $DXB_POLICY_RC ]] && grep -qxF -- "$DXB_POLICY_MARK" "$DXB_POLICY_RC"; then rm -f "$DXB_POLICY_RC"; fi
  if [[ -z $(_dxb_apps_dpkg_version nginx) ]]; then
    # a policy-rc.d that already exists (an image build's own) is left exactly as it is
    (
      if [[ ! -e $DXB_POLICY_RC ]]; then
        trap 'rm -f "$DXB_POLICY_RC"' EXIT
        trap 'exit 143' TERM INT HUP
        printf '#!/bin/sh\n%s\nexit 101\n' "$DXB_POLICY_MARK" > "$DXB_POLICY_RC" && chmod 755 "$DXB_POLICY_RC" \
          || { echo "could not write $DXB_POLICY_RC" >&2; exit 6; }
      fi
      echo "installing nginx"
      _dxb_apps_apt install -y --no-install-recommends nginx
    ) || { echo "apt-get could not install nginx" >&2; return 6; }
  fi
  rm -f "$DXB_NGINX_DIR/sites-enabled/default"
  if ! { gate=$(< "$DXB_TEMPLATES/nginx-dxberry-auth-gate.conf") && ends=$(< "$DXB_TEMPLATES/nginx-dxberry-auth-endpoints.conf"); }; then
    echo "the nginx auth templates are missing" >&2; return 6
  fi
  mkdir -p "$DXB_NGINX_DIR/snippets" || return 6
  for name in auth-gate auth-endpoints auth; do
    case $name in
      auth-gate) text=$gate ;;
      auth-endpoints) text=$ends ;;
      auth) text=$gate$'\n'$ends ;;
    esac
    dxb_write_if_changed "$DXB_NGINX_DIR/snippets/dxberry-$name.conf" "$text" 644
    [[ $(< "$DXB_NGINX_DIR/snippets/dxberry-$name.conf") == "$text" ]] || { echo "could not write the nginx snippet dxberry-$name.conf" >&2; return 6; }
  done
  "$DXB_SYSTEMCTL" enable --now nginx || { echo "could not enable nginx" >&2; return 6; }
  return 0
}

# dxb_webproxy_cert: the station's own certificate and key (station.crt and station.key in
# DXB_STATION_TLS, made by dxberryd), two lines: cert path, key path. 6 when either is missing.
dxb_webproxy_cert() {
  local c=$DXB_STATION_TLS/station.crt k=$DXB_STATION_TLS/station.key
  [[ -f $c && -f $k ]] || { echo "no certificate and key pair found in $DXB_STATION_TLS" >&2; return 6; }
  printf '%s\n%s\n' "$c" "$k"
}

# dxb_webproxy_render NAME [PLACEHOLDER=VALUE...]: nginx-NAME.conf with CERT and KEY (the station
# certificate) and the site's own placeholders filled in, on stdout. 6 failed.
dxb_webproxy_render() {
  local name=$1 cert key
  local -a pair
  shift
  [[ $name =~ ^[a-z][a-z0-9_]{0,15}$ ]] || { echo "not a site name: $name" >&2; return 6; }
  mapfile -t pair < <(dxb_webproxy_cert) && (( ${#pair[@]} == 2 )) || return 6
  cert=${pair[0]}; key=${pair[1]}
  dxb_render "$DXB_TEMPLATES/nginx-$name.conf" "CERT=$cert" "KEY=$key" "$@" || { echo "could not render the $name site" >&2; return 6; }
}

# dxb_webproxy_site NAME [PLACEHOLDER=VALUE...]: render nginx-NAME.conf (CERT and KEY: the station
# certificate; the site's own placeholders as given) into sites-available/dxberry-NAME.conf,
# enable it, check nginx's configuration and reload. A failing check unlinks the site again. 0
# ok, 6 failed.
dxb_webproxy_site() {
  local name=$1 avail enabled content
  shift
  content=$(dxb_webproxy_render "$name" "$@") || return 6
  avail=$DXB_NGINX_DIR/sites-available/dxberry-$name.conf; enabled=$DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf
  mkdir -p "$DXB_NGINX_DIR/sites-available" "$DXB_NGINX_DIR/sites-enabled" || return 6
  dxb_write_if_changed "$avail" "$content" 644
  [[ $(< "$avail") == "$content" ]] || { echo "could not write $avail" >&2; return 6; }
  ln -sfn "$avail" "$enabled" || { echo "could not enable the $name site" >&2; return 6; }
  if ! "$DXB_NGINX" -t >&2; then
    rm -f "$enabled"
    echo "nginx rejected the $name site; it is not enabled" >&2
    return 6
  fi
  "$DXB_SYSTEMCTL" reload nginx || { echo "nginx could not reload with the $name site" >&2; return 6; }
  return 0
}

# dxb_webproxy_site_stale NAME [PLACEHOLDER=VALUE...]: 0 when the installed site differs from what
# would be rendered now, or the station certificate or key is newer than the site file (renewed in
# place); 1 when it is current. 6 when it cannot be rendered.
dxb_webproxy_site_stale() {
  local name=$1 avail content cert key
  local -a pair
  shift
  avail=$DXB_NGINX_DIR/sites-available/dxberry-$name.conf
  content=$(dxb_webproxy_render "$name" "$@") || return 6
  [[ -f $avail && $(< "$avail") == "$content" ]] || return 0
  mapfile -t pair < <(dxb_webproxy_cert); cert=${pair[0]:-}; key=${pair[1]:-}
  [[ -n $cert && ( $cert -nt $avail || $key -nt $avail ) ]]
}

# dxb_webproxy_site_refresh NAME [PLACEHOLDER=VALUE...]: render the site again over an enabled one.
# Unlike dxb_webproxy_site, a failing nginx check puts the previous file back and keeps the
# link: a working site is never turned off. Once nginx has reloaded, the site file's time is set
# to now, so a certificate that was renewed in place counts as picked up (a failed reload leaves it
# stale: the next run tries again). 0 ok, 6 failed (the old site stays).
dxb_webproxy_site_refresh() {
  local name=$1 avail old
  shift
  avail=$DXB_NGINX_DIR/sites-available/dxberry-$name.conf
  [[ -f $avail && -e $DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf ]] || { echo "the $name site is not enabled" >&2; return 6; }
  old=$(< "$avail")
  local content
  content=$(dxb_webproxy_render "$name" "$@") || return 6
  dxb_write_if_changed "$avail" "$content" 644
  [[ $(< "$avail") == "$content" ]] || { echo "could not write $avail" >&2; return 6; }
  if ! "$DXB_NGINX" -t >&2; then
    dxb_write_if_changed "$avail" "$old" 644
    echo "nginx rejected the new $name site; the previous one stays" >&2
    return 6
  fi
  "$DXB_SYSTEMCTL" reload nginx || { echo "nginx could not reload with the $name site" >&2; return 6; }
  touch "$avail"
  return 0
}

# dxb_webproxy_site_remove NAME: disable and delete the site; nginx reloads when it is installed.
dxb_webproxy_site_remove() {
  local name=$1
  [[ $name =~ ^[a-z][a-z0-9_]{0,15}$ ]] || { echo "not a site name: $name" >&2; return 6; }
  rm -f "$DXB_NGINX_DIR/sites-enabled/dxberry-$name.conf" "$DXB_NGINX_DIR/sites-available/dxberry-$name.conf"
  if [[ -n $(_dxb_apps_dpkg_version nginx) ]]; then
    if ! { "$DXB_NGINX" -t >&2 && "$DXB_SYSTEMCTL" reload nginx; }; then
      dxb_warn "nginx was not reloaded after removing the $name site"
    fi
  fi
  return 0
}
