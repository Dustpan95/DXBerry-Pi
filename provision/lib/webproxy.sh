#!/bin/bash
# shellcheck shell=bash
# The web proxy (applications spec): nginx in front of a program's own web page, letting a request
# through only with a live Cockpit login (snippets/dxberry-auth.conf, the include every DXBerry
# site uses). Runs inside the app-install job, which already holds the update and config locks,
# so nothing here takes a lock. Ruling 11: installing nginx never fights Cockpit for port 80 (a
# policy-rc.d that refuses service starts is in place for the install; Debian's default site is
# removed; nginx then starts with DXBerry's sites only).
# Expects common.sh and apps.sh to be sourced.

: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_NGINX_DIR:=/etc/nginx}"
: "${DXB_NGINX:=nginx}"
: "${DXB_POLICY_RC:=/usr/sbin/policy-rc.d}"
: "${DXB_POLICY_MARK:=# DXBerry: temporary, removed after the nginx install}"
: "${DXB_COCKPIT_CERTS:=/etc/cockpit/ws-certs.d}"

# dxb_webproxy_install: nginx, installed without starting, DXBerry's auth snippet, nginx enabled.
# Idempotent. 0 ok, 6 failed.
dxb_webproxy_install() {
  local snip
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
  snip=$(< "$DXB_TEMPLATES/nginx-dxberry-auth.conf") || { echo "the nginx auth template is missing" >&2; return 6; }
  mkdir -p "$DXB_NGINX_DIR/snippets" || return 6
  dxb_write_if_changed "$DXB_NGINX_DIR/snippets/dxberry-auth.conf" "$snip" 644
  [[ $(< "$DXB_NGINX_DIR/snippets/dxberry-auth.conf") == "$snip" ]] || { echo "could not write the nginx auth snippet" >&2; return 6; }
  "$DXB_SYSTEMCTL" enable --now nginx || { echo "could not enable nginx" >&2; return 6; }
  return 0
}

# dxb_webproxy_cert: Cockpit's own certificate and key (the newest *.cert that has a matching
# *.key), two lines: cert path, key path. 6 when there is none.
dxb_webproxy_cert() {
  local c k best=''
  for c in "$DXB_COCKPIT_CERTS"/*.cert; do
    [[ -f $c ]] || continue
    k=${c%.cert}.key
    [[ -f $k ]] || continue
    if [[ -z $best || $c -nt $best ]]; then best=$c; fi
  done
  [[ -n $best ]] || { echo "no certificate and key pair found in $DXB_COCKPIT_CERTS" >&2; return 6; }
  printf '%s\n%s\n' "$best" "${best%.cert}.key"
}

# dxb_webproxy_site NAME: render nginx-NAME.conf into sites-available/dxberry-NAME.conf, enable it,
# check nginx's configuration and reload. A failing check unlinks the site again. 0 ok, 6 failed.
dxb_webproxy_site() {
  local name=$1 cert key avail enabled content
  local -a pair
  [[ $name =~ ^[a-z][a-z0-9_]{0,15}$ ]] || { echo "not a site name: $name" >&2; return 6; }
  mapfile -t pair < <(dxb_webproxy_cert) && (( ${#pair[@]} == 2 )) || return 6
  cert=${pair[0]}; key=${pair[1]}
  content=$(dxb_render "$DXB_TEMPLATES/nginx-$name.conf" "CERT=$cert" "KEY=$key") || { echo "could not render the $name site" >&2; return 6; }
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
