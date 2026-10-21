#!/bin/bash
# shellcheck shell=bash
# Wavelog, the logbook on this Pi, as a program of its own (applications spec; ruling 10). Its
# files are the newest release's tag tarball, unpacked into $DXB_APPS_OPT/wavelog by the
# app-install job (dxb_apps_install: a temp directory beside it, then swapped in), which carries
# over what app_wavelog_keep names from an earlier install - the operator's configuration, user
# files, uploads and backups - on every update and removal. This module adds the rest:
#   - the tree belongs to www-data (Wavelog writes its configuration, logs, uploads and backups:
#     wavelog_docs, docs/getting-started/installation/linux.md, "Set Directory Ownership");
#   - a MariaDB database "wavelog" and a MariaDB user www-data that logs in through MariaDB's unix
#     socket (only the www-data account, that is PHP, can use it: there is no password to show or
#     leak);
#   - nginx (through the web proxy, so it never fights Cockpit for port 80: ruling 11) with the
#     site nginx-wavelog.conf: https on 8082 for the LAN, http on 127.0.0.1:8083 for the logging
#     bridge, the installer behind the console's login.
# The first Wavelog user is made by Wavelog's own installer at https://<pi>:8082/install, which
# asks for the database: localhost, wavelog, www-data, no password.
# Expects common.sh, apps.sh and webproxy.sh to be sourced.

: "${DXB_MARIADB:=mariadb}"

app_wavelog_kind() { echo service; }
app_wavelog_label() { echo Wavelog; }
app_wavelog_installed() { dxb_apps_installed wavelog; }
app_wavelog_version() { dxb_apps_version wavelog; }
# the page opens Wavelog in a browser tab
app_wavelog_open() { printf '%s\n' '{"kind":"url","port":8082,"path":"/"}'; }
# what the Programs row says once Wavelog is installed
app_wavelog_setup_note() {
  echo "Finish setting up Wavelog: open it, and on the database step enter localhost, wavelog, www-data and no password. Then create an API key in Wavelog and choose it under Logbook."
}
# what an update and a removal keep (relative to $DXB_APPS_OPT/wavelog): the configuration
# Wavelog's installer wrote, and the operator's own files
app_wavelog_keep() { printf '%s\n' application/config/config.php application/config/database.php userdata uploads backup; }

# the database and its user (MariaDB reads them on stdin; nothing here is a secret anyway)
DXB_WAVELOG_SQL="CREATE DATABASE IF NOT EXISTS wavelog CHARACTER SET utf8mb4;
CREATE USER IF NOT EXISTS 'www-data'@'localhost' IDENTIFIED VIA unix_socket;
GRANT ALL PRIVILEGES ON wavelog.* TO 'www-data'@'localhost';"

# app_wavelog_install_extra: after the files are in place (install and update alike): the tree to
# www-data, MariaDB running with the database and user, nginx and the site. Idempotent. 0 ok, 6
# failed.
app_wavelog_install_extra() {
  local dir=$DXB_APPS_OPT/wavelog
  [[ -f $dir/index.php ]] || { echo "Wavelog's files are not in $dir" >&2; return 6; }
  "$DXB_CHOWN" -R www-data:www-data "$dir" || { echo "could not give $dir to www-data" >&2; return 6; }
  "$DXB_SYSTEMCTL" enable --now mariadb > /dev/null 2>&1 || { echo "could not start MariaDB" >&2; return 6; }
  printf '%s\n' "$DXB_WAVELOG_SQL" | "$DXB_MARIADB" || { echo "could not create Wavelog's database in MariaDB" >&2; return 6; }
  dxb_webproxy_install || return 6
  dxb_webproxy_site wavelog "ROOT=$dir" || return 6
}

# app_wavelog_remove_extra: the site only; Wavelog's kept files and its database stay, so a
# reinstall continues where the operator left off.
app_wavelog_remove_extra() { dxb_webproxy_site_remove wavelog; }
