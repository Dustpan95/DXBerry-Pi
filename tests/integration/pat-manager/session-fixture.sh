#!/bin/bash
# Invoked by the tagged production-session test after verifying the guest.
set -euo pipefail
trap 'echo "Fixture phase $1 failed on line $LINENO" >&2' ERR
# shellcheck source=tests/fixtures/sysfs.sh
source /fixture/sysfs.sh
unit=dxberry-screen@pat-fixture2.service
home=/var/lib/dxberry-apps
session=/run/dxberry/sessions/pat-fixture2
sys=/run/fixture2-sysfs
settings=$home/.config/pat/config.json

case "$1" in
  setup)
    systemd-tmpfiles --create /usr/lib/tmpfiles.d/dxberry-radio.conf
    install -d -m 0750 -o dxbapps -g dxbapps "$home" "$home/.config" "$home/.config/pat"
    install -m 0600 -o dxbapps -g dxbapps /dev/stdin "$settings"
    install -d -m 0755 "/etc/systemd/system/$unit.d" "$sys"
    cat > "/etc/systemd/system/$unit.d/fixture.conf" <<UNIT
[Service]
Environment=DXB_SYSFS_ROOT=$sys
PrivateDevices=yes
UNIT
    # Only the class link models presence. Nothing creates a device node.
    cat > /var/lib/dxberry/radios.json <<'RECORD'
{"version":1,"gps":{"device":"auto","baud":9600,"pps":""},"radios":{
  "fixture2":{"owner":"pat","label":"Fixture two","wiring":"full","rigctld_port":4534,
    "rig":{"model":1,"ptt_type":"NONE"},"ptt":{"method":"none"},
    "ptt_serial":{"path":"usb-0:1.3:1.0","vidpid":"f00d:0002","serial":"fixture2"}}}}
RECORD
    chmod 0600 /var/lib/dxberry/radios.json
    systemctl daemon-reload
    systemctl start "$unit"
    [[ $(systemctl show "$unit" -p ActiveState --value) == inactive ]]
    [[ -f $session.waiting && ! -e $session.env && ! -e $session.d ]]
    ;;
  start)
    fx_usb_device "$sys" 1-1.3 f00d 0002 fixture2 'Fixture serial function'
    fx_usb_function "$sys" 1-1.3 0 serial ttyUSB0
    DXB_SYSFS_ROOT=$sys /opt/dxberry/bin/dxberry-app start fixture2
    [[ ! -e $session.waiting ]]
    [[ -f /var/lib/dxberry/apps/firstfill/pat-fixture2 ]]
    [[ -s /var/lib/dxberry/apps/wired/pat-fixture2 ]]
    [[ $(stat -c '%U:%G:%a' "$session.env") == root:dxbscreen:640 ]]
    [[ $(stat -c '%U:%G:%a' "$session.d") == dxbapps:dxbapps:700 ]]
    [[ $(stat -c '%U:%a' "$settings") == dxbapps:600 ]]
    jq -e '.http_addr == "127.0.0.1:8180" and .ardop.addr == "localhost:8515"
      and .ardop.ptt_ctrl == false and .hamlib_rigs.fixture2.address == "127.0.0.1:4534"
      and .auto_download_size_limit == 321 and .listen == []' "$settings" >/dev/null
    ;;
  stop)
    rm "$sys/class/tty/ttyUSB0"
    systemctl stop "$unit"
    [[ $(systemctl show "$unit" -p Result --value) == success ]]
    [[ -f $session.waiting && ! -e $session.d ]]
    ;;
  *) exit 2 ;;
esac
