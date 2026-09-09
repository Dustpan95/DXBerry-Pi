# DXBerry-Pi Radio Plumbing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plug radios into the Pi and get stable device names, one hamlib `rigctld` per radio, a GPS feeding time and position, and a `dxberry-radio` command that records which application owns which radio and hands it over on request.

**Architecture:** `dxberry-radio` (bash) scans sysfs for USB audio/serial/HID functions, groups them by USB port into candidates, and pins chosen functions into `/var/lib/dxberry/radios.json`. `apply` derives everything from that record: a generated udev rules file (ALSA card ids, `/dev/dxberry/*` symlinks, hotplug trigger), `rigctld@NAME` instances fed by env files, and a runtime mirror in `/run/dxberry`. Application modules under `provision/lib/apps/` expose `app_<name>_{unit,wire,unwire}`; `claim` stops the old owner's use, starts the new one, and wires it. gpsd + chrony give time; Graywolf reads gpsd for position.

**Tech Stack:** bash 5, jq, awk, sysfs, udev (`udevadm`), systemd template units, hamlib `libhamlib-utils` (`rigctld`, `rigctl`), `gpsd` + `gpsd-clients` (`gpspipe`), `chrony`, Graywolf REST API (cookie auth, `/api`).

**Spec:** `docs/design/2026-09-09-radio-plumbing.md` (read it first; "§" below refers to it). Base image spec: `docs/design/2026-09-07-base-image.md`.

## Global Constraints

- License GPL-2.0-or-later. **No author or attribution lines of any kind** in files or commit messages: no AI-assistant or model-vendor names, no co-author or session trailers, no "Generated with" footers. CI greps for them and fails. Commit messages: imperative subject + plain body, nothing else.
- No secrets in the tree. **No version pins** (packages installed by name only). Nothing runs against a Pi from a test.
- All scripts `#!/bin/bash`, `set -uo pipefail` (no `set -e`), shellcheck-clean: `shellcheck -x provision/bin/* provision/lib/*.sh provision/lib/apps/*.sh build/build-image.sh boot/*.sh tests/*.sh`. `build/build-image.sh --check` prints `tree ok`. `tests/run.sh` prints `ok` for every test and `0 failures` with no stray stderr.
- Tests need only bash 5, awk, jq; every external command (`udevadm`, `systemctl`, `rigctl`, `gpspipe`, `apt-get`, `ip`, `curl`) is stubbed as a shell function or by `DXB_*` overrides. Tests must not need root.
- Library functions are prefixed `dxb_` (private `_dxb_`); application modules use `app_<name>_`. Every path and external command a library touches has a `DXB_*` override with the production default (`: "${DXB_X:=default}"`).
- File writes go through `dxb_write_if_changed FILE CONTENT [MODE]` (0 written, 1 unchanged; **it returns 0 even if the final `mv` failed**, so re-read the file to confirm) or `dxb_set_kv`. State dir `/var/lib/dxberry` (0700), runtime dir `/run/dxberry`.
- Radio names match `^[a-z][a-z0-9]{0,11}$`; the ALSA id is the name upper-cased; symlinks are `/dev/dxberry/<name>-cat|-ptt|-hid`; rigctld ports are even numbers from 4532 bound to `127.0.0.1`.
- `ptt.method` ∈ `rigctld cm108 gpio vox digirig_tone none`; `rig.ptt_type` ∈ `RIG RTS DTR NONE`; `wiring` ∈ `full names`.
- Exit codes of `dxberry-radio`: 0 ok, 2 usage, 3 no such radio/app, 4 device absent, 5 claim failed (radio released), 6 apply failed, 7 re-wire failed.
- Graywolf API base `DXB_GW_API` (`http://127.0.0.1:8080/api`), cookie jar `DXB_GW_COOKIES`, client `dxb_gw_api METHOD PATH [BODY]`; PUT bodies get `del(.id)`. PTT config lives at `/ptt/{channel_id}` and the rigctld target is `device_path: "host:port"`.
- Every task ends with `tests/run.sh` passing, shellcheck clean, `--check` ok, and a commit.

## File Structure

| File | Responsibility |
|---|---|
| `provision/lib/config.sh` | + `GPS_DEVICE/GPS_BAUD/GPS_PPS` validation, bare `STATIC_IP` |
| `provision/lib/common.sh` | + `dxb_ensure_line FILE LINE` |
| `provision/bin/dxberry-preboot` | + `CONFIG_NTP_MODE=0`, config.txt UART/PPS lines |
| `provision/bin/dxberry-netwatch` | `--status` shows the live address holder |
| `provision/share/radio-profiles.tsv` | vid:pid → defaults (data) |
| `provision/lib/radio.sh` | record load/save/validate, scan, add/set/remove, presence, apply, claim/release, `provision_radio` |
| `provision/lib/radio_udev.sh` | udev rules + modprobe content and install |
| `provision/lib/rigctld.sh` | env files, unit install, start/stop by presence, freq/mode query |
| `provision/lib/gps.sh` | gpsd/chrony config, boot config lines, fix readout, Maidenhead |
| `provision/lib/apps/graywolf.sh` | `app_graywolf_*` |
| `provision/lib/graywolf.sh` | + stored admin secret, `dxb_gw_login_any`, `dxb_gw_seed_gps` |
| `provision/bin/dxberry-radio` | CLI: parse, dispatch, `--json`, exit codes |
| `provision/templates/*` | `rigctld@.service`, `dxberry-radio-hotplug.service`, `70-dxberry-radio.rules.head`, `dxberry-audio.conf`, `dxberry-radio.tmpfiles` |
| `tests/fixtures/sysfs.sh` | fake sysfs tree builders (`fx_*`) |
| `tests/test_radio*.sh`, `test_rigctld.sh`, `test_gps.sh`, `test_app_graywolf.sh` | one test file per library |

---

### Task 1: Carry-overs from v0.1.0 (bare STATIC_IP, live netwatch status)

**Files:**
- Modify: `provision/lib/config.sh` (STATIC_IP block, lines ~119-133)
- Modify: `provision/bin/dxberry-netwatch` (`--status` in `main`)
- Modify: `boot/dxberry.txt.example`, `boot/README-DXBERRY.txt`
- Test: `tests/test_config.sh`, `tests/test_netwatch.sh`

**Interfaces:**
- Produces: `DXB_CFG[STATIC_IP]` is always CIDR after `dxb_config_validate` (consumers like `network.sh:53` rely on it).

- [ ] **Step 1: Failing tests**

Append to `tests/test_config.sh`:

```bash
cfg_from() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; export DXB_ZONEINFO_DIR=$TEST_TMP/nozone; }

test_config_bare_static_ip_defaults_to_24() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0.90' 'GATEWAY=10.0.0.1'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[STATIC_IP]}" "10.0.0.90/24"
  assert_eq "${DXB_CFG[_IP]}" "10.0.0.90"
  assert_eq "${DXB_CFG[_PREFIX]}" "24"
}

test_config_static_ip_with_prefix_unchanged() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0.90/16' 'GATEWAY=10.0.1.1'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[STATIC_IP]}" "10.0.0.90/16"
  assert_eq "${DXB_CFG[_PREFIX]}" "16"
}

test_config_static_ip_garbage_still_rejected() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0' 'GATEWAY=10.0.0.1'
  assert_fails dxb_config_validate
  assert_contains "${DXB_CFG_ERRORS[*]}" "STATIC_IP must be an IPv4 address"
}
```

Append to `tests/test_netwatch.sh` (inside the file's existing pattern; `nw_env` stubs `ip` already, so override it per test):

```bash
test_status_reports_live_holder() {
  nw_env
  echo WIFI > "$STATE_FILE"
  ip() { echo '2: eth0    inet 10.0.0.90/24 brd 10.0.0.255 scope global eth0'; }
  assert_eq "$(main --status)" "WIFI (eth0 holds the address)"
}

test_status_without_holder_prints_state_only() {
  nw_env
  echo NONE > "$STATE_FILE"
  ip() { :; }
  assert_eq "$(main --status)" "NONE"
}
```

- [ ] **Step 2: Run, expect failures**

Run: `tests/run.sh 2>&1 | grep -E 'FAIL|failures'`
Expected: the five new tests FAIL (bare IP rejected; status prints `WIFI` only).

- [ ] **Step 3: Implement**

In `provision/lib/config.sh` replace the STATIC_IP regex line and normalize:

```bash
    if [[ ${DXB_CFG[STATIC_IP]} =~ ^([0-9.]+)(/([0-9]{1,2}))?$ ]]; then
      ip=${BASH_REMATCH[1]}; prefix=$(( 10#${BASH_REMATCH[3]:-24} ))
    fi
    if [[ -n $ip ]] && _dxb_is_ipv4 "$ip" && (( prefix >= 8 && prefix <= 30 )); then
      DXB_CFG[STATIC_IP]="$ip/$prefix"
      DXB_CFG[_IP]=$ip
```

Change the error text to `'must be an IPv4 address, optionally with a prefix length, e.g. 192.168.1.90 or 192.168.1.90/24'` and keep the test's substring `STATIC_IP must be an IPv4 address` true (errors are formatted `KEY message`).

In `provision/bin/dxberry-netwatch` add above `main`:

```bash
# Interface (eth0/wlan0) currently carrying an IPv4 address, or nothing.
nw_holder() { ip -o -4 addr show up 2> /dev/null | awk -v e="$ETH" -v w="$WLAN" '$2 == e || $2 == w { print $2; exit }'; }
nw_status() {
  local st holder
  st=$(cat "$STATE_FILE" 2> /dev/null || echo unknown)
  holder=$(nw_holder)
  if [[ -n $holder ]]; then echo "$st ($holder holds the address)"; else echo "$st"; fi
}
```

and change the `--status)` case to `nw_status ;;`.

`boot/dxberry.txt.example`: change the STATIC_IP comment to say a bare address is accepted and means `/24`. `boot/README-DXBERRY.txt`: add one line after the status-file sentence: `If the Pi does not come up at the address you set, read dxberry-ERROR.txt on this partition first: it names the exact dxberry.txt line that was rejected.`

- [ ] **Step 4: Run tests, shellcheck, check**

Run: `tests/run.sh && shellcheck -x provision/bin/* provision/lib/*.sh build/build-image.sh boot/*.sh tests/*.sh && build/build-image.sh --check`
Expected: all ok, `0 failures`, `tree ok`.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/config.sh provision/bin/dxberry-netwatch boot/dxberry.txt.example boot/README-DXBERRY.txt tests/test_config.sh tests/test_netwatch.sh
git commit -m "Accept a bare STATIC_IP and show which interface holds the address"
```

---

### Task 2: GPS configuration keys and first-boot settings

**Files:**
- Modify: `provision/lib/config.sh` (`DXB_KNOWN_KEYS`, `DXB_RESERVED_PREFIXES`, `dxb_config_validate`)
- Modify: `provision/lib/common.sh` (+ `dxb_ensure_line`)
- Modify: `provision/bin/dxberry-preboot` (after the serial-console line)
- Modify: `provision/bin/dxberry-provision` (`--check` summary line)
- Modify: `boot/dxberry.txt.example` (document the three keys)
- Test: `tests/test_config.sh`, `tests/test_common.sh`, `tests/test_preboot.sh`

**Interfaces:**
- Produces: `DXB_CFG[GPS_DEVICE]` (`auto|none|uart|/dev/tty…`), `DXB_CFG[GPS_BAUD]`, `DXB_CFG[GPS_PPS]` (`''` or `0-27`), derived `DXB_CFG[_GPS]` (0/1) and `DXB_CFG[_GPS_PATH]` (`''` for auto/none, `/dev/ttyAMA0` for uart, else the path). `dxb_ensure_line FILE LINE` → 0 appended, 1 already present, 2 write error. `dxb_gps_boot_lines` is NOT here (Task 10); preboot writes the lines directly.

- [ ] **Step 1: Failing tests**

`tests/test_config.sh`:

```bash
test_config_gps_defaults() {
  cfg_from 'PASSWORD=examplepass'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[GPS_DEVICE]}" "auto"; assert_eq "${DXB_CFG[GPS_BAUD]}" "9600"; assert_eq "${DXB_CFG[GPS_PPS]}" ""
  assert_eq "${DXB_CFG[_GPS]}" "1"; assert_eq "${DXB_CFG[_GPS_PATH]}" ""
}
test_config_gps_uart_and_pps() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=uart' 'GPS_PPS=18'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[_GPS_PATH]}" "/dev/ttyAMA0"; assert_eq "${DXB_CFG[GPS_PPS]}" "18"
}
test_config_gps_none_and_explicit_path() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=none'
  assert_ok dxb_config_validate; assert_eq "${DXB_CFG[_GPS]}" "0"
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=/dev/ttyUSB3' 'GPS_BAUD=4800'
  assert_ok dxb_config_validate; assert_eq "${DXB_CFG[_GPS_PATH]}" "/dev/ttyUSB3"
}
test_config_gps_rejections() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=uart' 'SERIAL_CONSOLE=on'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_DEVICE uart needs SERIAL_CONSOLE=off"
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=ttyUSB0'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_DEVICE must be auto, none, uart or a /dev/tty path"
  cfg_from 'PASSWORD=examplepass' 'GPS_BAUD=1234'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_BAUD must be one of"
  cfg_from 'PASSWORD=examplepass' 'GPS_PPS=40'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_PPS must be a BCM GPIO number 0-27"
}
test_config_gps_keys_are_known_not_reserved() {
  cfg_from 'PASSWORD=examplepass' 'GPS_FOO=1'
  assert_ok dxb_config_validate
  assert_contains "${DXB_CFG_WARNINGS[*]}" "GPS_FOO"
}
```

`tests/test_common.sh`:

```bash
test_ensure_line_appends_once() {
  local f=$TEST_TMP/config.txt
  printf 'arm_64bit=1\n' > "$f"
  assert_ok dxb_ensure_line "$f" 'enable_uart=1'
  assert_fails dxb_ensure_line "$f" 'enable_uart=1'
  assert_eq "$(grep -c '^enable_uart=1$' "$f")" "1"
  assert_eq "$(head -1 "$f")" "arm_64bit=1"
}
```

`tests/test_preboot.sh` (follow the file's existing fixture helper; it sets `DXB_BOOT_DIR` and a stock `dietpi.txt`):

```bash
test_preboot_writes_ntp_mode_and_gps_boot_lines() {
  preboot_env   # the file's existing helper name; adapt if different
  printf 'PASSWORD=examplepass\nGPS_DEVICE=uart\nGPS_PPS=18\n' > "$DXB_BOOT_DIR/dxberry.txt"
  : > "$DXB_BOOT_DIR/config.txt"
  main
  assert_file_contains "$DXB_BOOT_DIR/dietpi.txt" "CONFIG_NTP_MODE=0"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "enable_uart=1"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "dtoverlay=disable-bt"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "dtoverlay=pps-gpio,gpiopin=18"
}
test_preboot_auto_gps_leaves_config_txt_alone() {
  preboot_env
  printf 'PASSWORD=examplepass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  printf 'arm_64bit=1\n' > "$DXB_BOOT_DIR/config.txt"
  main
  assert_eq "$(cat "$DXB_BOOT_DIR/config.txt")" "arm_64bit=1"
  assert_file_contains "$DXB_BOOT_DIR/dietpi.txt" "CONFIG_NTP_MODE=0"
}
```

- [ ] **Step 2: Run, expect failures**

Run: `tests/run.sh 2>&1 | grep -E 'FAIL|failures'`

- [ ] **Step 3: Implement**

`config.sh`: add `GPS_DEVICE GPS_BAUD GPS_PPS` to `DXB_KNOWN_KEYS`; remove `GPS_` from `DXB_RESERVED_PREFIXES`. In `dxb_config_validate` after the `SERIAL_CONSOLE` on/off check:

```bash
  _dxb_default GPS_DEVICE auto
  _dxb_default GPS_BAUD 9600
  : "${DXB_CFG[GPS_PPS]:=}"
  v=${DXB_CFG[GPS_DEVICE]}
  DXB_CFG[_GPS]=1; DXB_CFG[_GPS_PATH]=''
  case $v in
    auto) ;;
    none) DXB_CFG[_GPS]=0 ;;
    uart) DXB_CFG[_GPS_PATH]=/dev/ttyAMA0
          [[ ${DXB_CFG[SERIAL_CONSOLE]} == off ]] || _dxb_err GPS_DEVICE 'uart needs SERIAL_CONSOLE=off (the GPS uses the same pins)' ;;
    /dev/tty[A-Za-z0-9]*) DXB_CFG[_GPS_PATH]=$v ;;
    *) _dxb_err GPS_DEVICE 'must be auto, none, uart or a /dev/tty path' ;;
  esac
  case ${DXB_CFG[GPS_BAUD]} in 4800|9600|19200|38400|57600|115200) ;; *) _dxb_err GPS_BAUD 'must be one of 4800 9600 19200 38400 57600 115200' ;; esac
  v=${DXB_CFG[GPS_PPS]}
  if [[ -n $v ]] && ! { [[ $v =~ ^[0-9]{1,2}$ ]] && (( 10#$v <= 27 )); }; then _dxb_err GPS_PPS 'must be a BCM GPIO number 0-27'; fi
```

`common.sh` (after `dxb_set_kv`):

```bash
# dxb_ensure_line FILE LINE: append LINE unless an identical line exists. 0 appended, 1 present, 2 error.
dxb_ensure_line() {
  local file=$1 line=$2
  [[ -f $file ]] || : > "$file" || return 2
  grep -qxF -- "$line" "$file" && return 1
  printf '%s\n' "$line" >> "$file" || return 2
}
```

`dxberry-preboot`, after the `CONFIG_SERIAL_CONSOLE_ENABLE` line:

```bash
  dxb_set_kv "$dietpi_txt" CONFIG_NTP_MODE 0
  local cfgtxt="$boot/config.txt"
  if [[ ${DXB_CFG[GPS_DEVICE]} == uart ]]; then
    dxb_ensure_line "$cfgtxt" 'enable_uart=1'; dxb_ensure_line "$cfgtxt" 'dtoverlay=disable-bt'
  fi
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || dxb_ensure_line "$cfgtxt" "dtoverlay=pps-gpio,gpiopin=${DXB_CFG[GPS_PPS]}"
```

(`boot` is whatever variable `main` already holds for the boot dir; declare `cfgtxt` with the function's other locals.)

`dxberry-provision` `--check` line: append ` gps: ${DXB_CFG[GPS_DEVICE]}`.

`boot/dxberry.txt.example`: add under the advanced section:

```
# GPS receiver. auto (default) = a USB GPS is picked up when plugged in; none = off;
# uart = a GPS on GPIO pins 8/10 (needs SERIAL_CONSOLE=off, turns off onboard Bluetooth);
# or a /dev/tty path.
#GPS_DEVICE=auto
# Baud rate for uart or an explicit path (USB receivers are auto-detected). Default 9600.
#GPS_BAUD=9600
# BCM GPIO number carrying a PPS pulse from the GPS, for sub-millisecond time. Blank = none.
#GPS_PPS=
```

- [ ] **Step 4: Run tests, shellcheck, check** (same command as Task 1). Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/config.sh provision/lib/common.sh provision/bin/dxberry-preboot provision/bin/dxberry-provision boot/dxberry.txt.example tests/test_config.sh tests/test_common.sh tests/test_preboot.sh
git commit -m "Add GPS_DEVICE, GPS_BAUD and GPS_PPS and prepare the boot files for a GPS"
```

---

### Task 3: Profile table and the sysfs scanner

**Files:**
- Create: `provision/share/radio-profiles.tsv`
- Create: `provision/lib/radio.sh` (scanner part; later tasks extend it)
- Create: `tests/fixtures/sysfs.sh`
- Create: `tests/test_radio.sh`

**Interfaces:**
- Produces: `dxb_radio_profiles_json` → JSON array `[{vidpid,name,ptt,ptt_type,cat,model,baud}]`. `dxb_radio_scan` → prints a JSON array of candidates:
  `{index, port:"usb-0:1.3", profile:"0d8c:013c"|"generic", name, defaults:{ptt,ptt_type,cat,model,baud}, functions:[{kind:audio|serial|hid, kernel, path:"usb-0:1.3:1.0", vidpid, serial, product}]}` sorted by port; functions sorted by path. `dxb_radio_scan_cache` stores it in `DXB_RADIO_SCAN`.
- Env: `DXB_SYSFS_ROOT` (default `/sys`), `DXB_RADIO_PROFILES` (default `${DXB_SHARE:-/opt/dxberry/share}/radio-profiles.tsv`).

- [ ] **Step 1: Fixture builders**

`tests/fixtures/sysfs.sh` (sourced by tests):

```bash
#!/usr/bin/env bash
# Fake sysfs trees for the radio scanner. Layout mirrors a Pi 4:
#   devices/platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb1/1-1/<dev>/<dev>:1.N/...
# shellcheck disable=SC2034
FX_USB_BASE=devices/platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb1/1-1

# fx_usb_device ROOT DEV VID PID SERIAL PRODUCT   e.g. fx_usb_device "$S" 1-1.3 0d8c 013c "" "USB Audio Device"
fx_usb_device() {
  local root=$1 dev=$2 d="$1/$FX_USB_BASE/$2"
  mkdir -p "$d"
  printf '%s\n' "$3" > "$d/idVendor"; printf '%s\n' "$4" > "$d/idProduct"
  [[ -z $5 ]] || printf '%s\n' "$5" > "$d/serial"
  printf '%s\n' "$6" > "$d/product"
}
# fx_usb_function ROOT DEV IFNUM KIND KERNEL: KIND audio|serial|hid; KERNEL card1|ttyUSB0|ttyACM0|hidraw1
fx_usb_function() {
  local root=$1 dev=$2 ifn=$3 kind=$4 kernel=$5 base="$1/$FX_USB_BASE/$2/$2:1.$3" leaf cls
  case $kind in
    audio) leaf="$base/sound/$kernel"; cls=sound ;;
    serial) leaf="$base/$kernel/tty/$kernel"; cls=tty ;;
    hid) leaf="$base/0003:0D8C:013C.0002/hidraw/$kernel"; cls=hidraw ;;
  esac
  mkdir -p "$leaf" "$root/class/$cls"
  ln -sfn "$(realpath --relative-to="$root/class/$cls" "$leaf")" "$root/class/$cls/$kernel"
}
# fx_onboard_card ROOT KERNEL NAME: a non-USB sound card (vc4-hdmi, bcm2835)
fx_onboard_card() {
  local d="$1/devices/platform/soc/$3/sound/$2"
  mkdir -p "$d" "$1/class/sound"
  ln -sfn "$(realpath --relative-to="$1/class/sound" "$d")" "$1/class/sound/$2"
}
# fx_systemctl ARGS...: a systemctl stand-in shared by the radio tests. Records every call in
# $TEST_TMP/calls and keeps an "active units" list in $TEST_TMP/active. is-active prints the
# state (unless --quiet) and returns 0/3 like the real command.
fx_systemctl() {
  echo "systemctl $*" >> "$TEST_TMP/calls"
  local u=${*: -1}
  case $1 in
    is-active)
      if grep -qx "$u" "$TEST_TMP/active" 2> /dev/null; then [[ $2 == --quiet ]] || echo active; return 0
      else [[ $2 == --quiet ]] || echo inactive; return 3; fi ;;
    start|restart) grep -qx "$u" "$TEST_TMP/active" 2> /dev/null || echo "$u" >> "$TEST_TMP/active" ;;
    stop) grep -vx "$u" "$TEST_TMP/active" > "$TEST_TMP/active.n" 2> /dev/null; mv -f "$TEST_TMP/active.n" "$TEST_TMP/active" ;;
  esac
  return 0
}

# fx_scene ROOT NAME: digirig | ic7300 | split | two-digirigs | none
fx_scene() {
  local r=$1
  mkdir -p "$r/class/sound" "$r/class/tty" "$r/class/hidraw"
  fx_onboard_card "$r" card0 fef00700.hdmi
  case $2 in
    digirig)
      fx_usb_device "$r" 1-1.3 0d8c 013c "" "USB Audio Device"
      fx_usb_function "$r" 1-1.3 0 audio card1
      fx_usb_function "$r" 1-1.3 3 hid hidraw1
      fx_usb_device "$r" 1-1.4 10c4 ea60 0001 "CP2102 USB to UART Bridge Controller"
      fx_usb_function "$r" 1-1.4 0 serial ttyUSB0 ;;
    ic7300)
      fx_usb_device "$r" 1-1.2 0c26 0036 IC-7300_02011234 "IC-7300"
      fx_usb_function "$r" 1-1.2 0 audio card1
      fx_usb_function "$r" 1-1.2 2 serial ttyUSB0
      fx_usb_function "$r" 1-1.2 4 serial ttyUSB1 ;;
    split)
      fx_usb_device "$r" 1-1.1 08bb 29b6 "" "USB Audio CODEC"
      fx_usb_function "$r" 1-1.1 0 audio card1
      fx_usb_device "$r" 1-1.2 0403 6001 FTA1B2C3 "FT232R USB UART"
      fx_usb_function "$r" 1-1.2 0 serial ttyUSB0 ;;
    two-digirigs)
      fx_scene "$r" digirig
      fx_usb_device "$r" 1-1.1 0d8c 013c "" "USB Audio Device"
      fx_usb_function "$r" 1-1.1 0 audio card2
      fx_usb_function "$r" 1-1.1 3 hid hidraw2
      fx_usb_device "$r" 1-1.2 10c4 ea60 0001 "CP2102 USB to UART Bridge Controller"
      fx_usb_function "$r" 1-1.2 0 serial ttyUSB1 ;;
    none) ;;
  esac
}
```

Note the `digirig` scene deliberately models a real DigiRig Mobile the way Linux sees it: the codec and the CP2102 hang off an internal hub, so they are sibling USB *devices* (1-1.3 and 1-1.4), not interfaces of one device. The scanner therefore reports them as two candidates and the operator pins `--audio 1 --cat 2`; §5.1's "one candidate" wording is corrected in Task 12.

- [ ] **Step 2: Failing tests**

`tests/test_radio.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

radio_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv
  mkdir -p "$DXB_STATE_DIR"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}

test_profiles_tsv_parses() {
  radio_env
  local j; j=$(dxb_radio_profiles_json)
  assert_eq "$(jq -r '.[] | select(.vidpid=="0d8c:013c") | .name' <<< "$j")" "DigiRig Mobile"
  assert_eq "$(jq -r '.[] | select(.vidpid=="0d8c:013c") | .ptt_type' <<< "$j")" "RTS"
  assert_eq "$(jq -r '.[] | select(.vidpid=="0c26:0036") | .model' <<< "$j")" "3073"
}

test_scan_digirig_two_candidates() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq 'length' <<< "$j")" "2"
  assert_eq "$(jq -r '.[0].port' <<< "$j")" "usb-0:1.3"
  assert_eq "$(jq -r '.[0].name' <<< "$j")" "DigiRig Mobile"
  assert_eq "$(jq -r '.[0].functions | map(.kind) | join(",")' <<< "$j")" "audio,hid"
  assert_eq "$(jq -r '.[0].functions[0].path' <<< "$j")" "usb-0:1.3:1.0"
  assert_eq "$(jq -r '.[0].functions[0].kernel' <<< "$j")" "card1"
  assert_eq "$(jq -r '.[1].functions[0].kind + " " + .[1].functions[0].serial' <<< "$j")" "serial 0001"
  assert_eq "$(jq -r '.[1].profile' <<< "$j")" "10c4:ea60"
  assert_eq "$(jq -r '.[1].name' <<< "$j")" "CP2102 serial"
  assert_eq "$(jq -r '.[1].defaults.ptt' <<< "$j")" "rigctld"
}

test_scan_ic7300_one_candidate_two_serials() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq 'length' <<< "$j")" "1"
  assert_eq "$(jq -r '.[0].functions | map(.kind) | join(",")' <<< "$j")" "audio,serial,serial"
  assert_eq "$(jq -r '.[0].functions[1].path' <<< "$j")" "usb-0:1.2:1.2"
  assert_eq "$(jq -r '.[0].defaults.model' <<< "$j")" "3073"
  assert_eq "$(jq -r '.[0].functions[0].serial' <<< "$j")" "IC-7300_02011234"
}

test_scan_ignores_onboard_audio_and_orphan_hid() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" none
  # a USB keyboard-like HID with no sound function must not appear
  fx_usb_device "$DXB_SYSFS_ROOT" 1-1.1 046d c31c "" "Keyboard"
  fx_usb_function "$DXB_SYSFS_ROOT" 1-1.1 0 hid hidraw0
  assert_eq "$(dxb_radio_scan)" "[]"
}

test_scan_two_digirigs_distinct_ports() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq -r 'map(.port) | join(" ")' <<< "$j")" "usb-0:1.1 usb-0:1.2 usb-0:1.3 usb-0:1.4"
  assert_eq "$(jq -r 'map(.index) | join(" ")' <<< "$j")" "1 2 3 4"
}
```

- [ ] **Step 3: Run, expect failures** — `tests/run.sh 2>&1 | grep -E 'FAIL|failures'` (sourcing fails until radio.sh exists; that is the RED run).

- [ ] **Step 4: Implement**

`provision/share/radio-profiles.tsv` (tabs between columns; verify each id against vendor documentation or a published `lsusb` line before committing and put the source in the notes column; mark any you cannot verify `unverified`):

```
# vid:pid	name	ptt	ptt_type	cat	model	baud	notes
0d8c:013c	DigiRig Mobile	rigctld	RTS	separate	1	57600	CM108 codec; the CP2102 CAT/PTT port is a sibling USB device
0d8c:0012	DigiRig Lite	digirig_tone	NONE	none	1	0	tone keyed on the right channel
1209:7388	AIOC	cm108	NONE	none	1	0	all-in-one cable, HID PTT
08bb:29b6	SignaLink USB	vox	NONE	none	1	0	TI PCM2906 codec
0c26:0036	Icom USB codec	rigctld	RIG	same	3073	115200	IC-7300 default model and baud
10c4:ea60	CP2102 serial	rigctld	RTS	same	1	57600	serial-only candidate (CAT cable or DigiRig port)
0403:6001	FTDI serial	rigctld	RIG	same	1	38400	serial-only candidate
```

`provision/lib/radio.sh`:

```bash
#!/bin/bash
# shellcheck shell=bash
# Radio plumbing: discovery, the radio record, derived state, ownership. Spec: docs/design/2026-09-09-radio-plumbing.md

: "${DXB_SYSFS_ROOT:=/sys}"
: "${DXB_SHARE:=/opt/dxberry/share}"
: "${DXB_RADIO_PROFILES:=$DXB_SHARE/radio-profiles.tsv}"
: "${DXB_RADIOS_FILE:=$DXB_STATE_DIR/radios.json}"
: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_RADIOS_STATE:=$DXB_RUN_DIR/radios-state.json}"
: "${DXB_APPS_DIR:=${DXB_LIB:-/opt/dxberry/lib}/apps}"
DXB_RADIO_SCAN='[]'
DXB_RADIOS=''

# ---- profiles ------------------------------------------------------------------------------
dxb_radio_profiles_json() {
  [[ -f $DXB_RADIO_PROFILES ]] || { echo '[]'; return 0; }
  awk -F'\t' '
    /^[[:blank:]]*#/ || NF < 7 { next }
    { printf "%s{\"vidpid\":\"%s\",\"name\":\"%s\",\"ptt\":\"%s\",\"ptt_type\":\"%s\",\"cat\":\"%s\",\"model\":%d,\"baud\":%d}", (n++ ? "," : "["), $1, $2, $3, $4, $5, $6, $7 }
    END { print (n ? "]" : "[]") }' "$DXB_RADIO_PROFILES"
}

# ---- scanner -------------------------------------------------------------------------------
_dxb_radio_attr() { local v=''; [[ -f $1/$2 ]] && read -r v < "$1/$2"; printf '%s' "$v"; }

# _dxb_radio_usb_ctx RESOLVED_SYSFS_PATH: prints "IFACE_PORT DEV_PORT DEVDIR" for the USB interface
# and device the node hangs off (udev ID_PATH style: 1-1.3:1.0 -> usb-0:1.3:1.0), or returns 1.
_dxb_radio_usb_ctx() {
  local acc='' comp iface='' dev='' devdir='' rest=$1
  while [[ -n $rest ]]; do
    comp=${rest%%/*}
    if [[ $rest == */* ]]; then rest=${rest#*/}; else rest=''; fi
    [[ -n $comp ]] || continue
    acc="$acc/$comp"
    if [[ $comp =~ ^[0-9]+-[0-9.]+:[0-9]+\.[0-9]+$ ]]; then iface=$comp
    elif [[ $comp =~ ^[0-9]+-[0-9.]+$ ]]; then dev=$comp; devdir=$acc; fi
  done
  [[ -n $iface && -n $dev ]] || return 1
  printf '%s %s %s\n' "usb-0:${iface#*-}" "usb-0:${dev#*-}" "$devdir"
}

# dxb_radio_scan: JSON array of USB candidates grouped by device port (spec section 5.1).
dxb_radio_scan() {
  local d real kernel kind ifport devport devdir funcs='[]'
  for d in "$DXB_SYSFS_ROOT"/class/sound/card* "$DXB_SYSFS_ROOT"/class/tty/ttyUSB* "$DXB_SYSFS_ROOT"/class/tty/ttyACM* "$DXB_SYSFS_ROOT"/class/hidraw/hidraw*; do
    [[ -e $d ]] || continue
    kernel=${d##*/}
    case $d in */class/sound/*) kind=audio ;; */class/tty/*) kind=serial ;; *) kind=hid ;; esac
    real=$(readlink -f "$d") || continue
    read -r ifport devport devdir < <(_dxb_radio_usb_ctx "$real") || continue
    [[ -n ${ifport:-} ]] || continue
    funcs=$(jq -c --arg kind "$kind" --arg kernel "$kernel" --arg path "$ifport" --arg dev "$devport" \
      --arg vid "$(_dxb_radio_attr "$devdir" idVendor)" --arg pid "$(_dxb_radio_attr "$devdir" idProduct)" \
      --arg serial "$(_dxb_radio_attr "$devdir" serial)" --arg product "$(_dxb_radio_attr "$devdir" product)" \
      '. + [{kind: $kind, kernel: $kernel, path: $path, dev: $dev, vidpid: ($vid + ":" + $pid), serial: $serial, product: $product}]' <<< "$funcs")
  done
  jq -c --argjson profiles "$(dxb_radio_profiles_json)" '
    group_by(.dev) | sort_by(.[0].dev) | to_entries | map(
      .value as $f
      | ($f | (if any(.kind == "audio") then . else map(select(.kind != "hid")) end) | sort_by(.path) | map(del(.dev))) as $fn
      | ($fn | map(select(.kind == "audio")) | .[0].vidpid // "") as $a
      | ($fn | map(select(.kind == "serial")) | .[0].vidpid // "") as $s
      | (($profiles | map(select(.vidpid == $a)) | .[0]) // ($profiles | map(select(.vidpid == $s)) | .[0]) // null) as $p
      | {index: (.key + 1), port: $f[0].dev,
         profile: ($p.vidpid // "generic"),
         name: ($p.name // (if $a != "" then "Unknown USB audio device" else "Unknown USB serial device" end)),
         defaults: (if $p then ($p | {ptt, ptt_type, cat, model, baud})
                    else {ptt: (if $s != "" then "rigctld" else "vox" end), ptt_type: "NONE", cat: (if $s != "" then "same" else "none" end), model: 1, baud: 0} end),
         functions: $fn})
    | map(select(.functions | length > 0))' <<< "$funcs"
}
dxb_radio_scan_cache() { DXB_RADIO_SCAN=$(dxb_radio_scan) || DXB_RADIO_SCAN='[]'; }
```

Note the `read -r … < <(…)` form: `_dxb_radio_usb_ctx` printing nothing leaves `ifport` empty, which the next line skips.

- [ ] **Step 5: Run tests, shellcheck (add `provision/lib/apps/*.sh` to the shellcheck list only once that directory exists), check.** Expected: all ok.

- [ ] **Step 6: Commit**

```bash
git add provision/share/radio-profiles.tsv provision/lib/radio.sh tests/fixtures/sysfs.sh tests/test_radio.sh
git commit -m "Scan sysfs for USB radio candidates and match them against a profile table"
```

---

### Task 4: The radio record: load, save, validate, add, set, remove

**Files:**
- Modify: `provision/lib/radio.sh`
- Test: `tests/test_radio.sh`

**Interfaces:**
- Consumes: `DXB_RADIO_SCAN` (Task 3), `dxb_write_if_changed`.
- Produces: `dxb_radio_empty_record`; `dxb_radio_load` (sets `DXB_RADIOS`, returns 6 on unparsable file); `dxb_radio_save JSON` (validates, writes 0600, re-reads; 2 invalid, 6 write error); `dxb_radio_validate JSON`; `dxb_radio_get NAME` (prints the radio object, 3 if absent); `dxb_radio_names`; `dxb_radio_alloc_port`; `dxb_radio_add NAME OPTS`; `dxb_radio_set NAME OPTS`; `dxb_radio_remove NAME`; `_dxb_radio_set_owner NAME APP`. OPTS is a JSON object with any of `audio`, `cat`, `hid`, `ptt_serial` (candidate selector strings `"N"` or `"N:K"` for the K-th function of that kind, or `"none"`), `ptt`, `ptt_type`, `model`, `baud`, `wiring`, `label`, `gpio_line`.
- Record shape: exactly §6.1 plus optional `ptt_serial` pin and `ptt.gpio_line`.

- [ ] **Step 1: Failing tests** (append to `tests/test_radio.sh`)

```bash
test_record_roundtrip_and_validation() {
  radio_env
  assert_ok dxb_radio_load
  assert_eq "$(jq -c '.radios' <<< "$DXB_RADIOS")" "{}"
  assert_ok dxb_radio_save "$DXB_RADIOS"
  assert_eq "$(stat -c %a "$DXB_RADIOS_FILE")" "600"
  echo 'not json' > "$DXB_RADIOS_FILE"
  dxb_radio_load; assert_eq "$?" "6"
  radio_env
  local bad; bad=$(jq -c '.radios.radio1 = {label:"x",audio:null,cat:null,hid:null,ptt:{method:"laser",gpio_line:null},rig:{model:1,baud:0,ptt_type:"NONE"},rigctld_port:4532,wiring:"full",owner:""}' <<< "$(dxb_radio_empty_record)")
  dxb_radio_validate "$bad"; assert_eq "$?" "2"
}

test_add_digirig_pins_functions_and_profile_defaults() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add radio1 '{"audio":"1","cat":"2","label":"TM-V71"}'
  local r; r=$(dxb_radio_get radio1)
  assert_eq "$(jq -r '.audio.path' <<< "$r")" "usb-0:1.3:1.0"
  assert_eq "$(jq -r '.cat.path' <<< "$r")" "usb-0:1.4:1.0"
  assert_eq "$(jq -r '.cat.serial' <<< "$r")" "0001"
  assert_eq "$(jq -r '.hid.path' <<< "$r")" "usb-0:1.3:1.3"
  assert_eq "$(jq -r '.profile' <<< "$r")" "0d8c:013c"
  assert_eq "$(jq -r '.ptt.method + " " + .rig.ptt_type + " " + (.rig.baud|tostring)' <<< "$r")" "rigctld RTS 57600"
  assert_eq "$(jq -r '.rigctld_port' <<< "$r")" "4532"
  assert_eq "$(jq -r '.wiring + "/" + .owner' <<< "$r")" "full/"
  assert_eq "$(jq -r '.radios.radio1.label' "$DXB_RADIOS_FILE")" "TM-V71"
}

test_add_ic705_second_serial_and_overrides() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add hf '{"audio":"1","cat":"1:2","model":3073,"baud":19200,"wiring":"names"}'
  local r; r=$(dxb_radio_get hf)
  assert_eq "$(jq -r '.cat.path' <<< "$r")" "usb-0:1.2:1.4"
  assert_eq "$(jq -r '.rig.baud' <<< "$r")" "19200"
  assert_eq "$(jq -r '.rig.ptt_type' <<< "$r")" "RIG"
  assert_eq "$(jq -r '.hid' <<< "$r")" "null"
}

test_add_rejects_bad_name_duplicate_and_missing_function() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" split; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add Radio1 '{"audio":"1"}'; assert_eq "$?" "2"
  dxb_radio_add r9 '{"audio":"2"}'; assert_eq "$?" "2"          # candidate 2 has no audio function
  dxb_radio_add r9 '{"audio":"7"}'; assert_eq "$?" "2"          # no such candidate
  assert_ok dxb_radio_add r9 '{"audio":"1","cat":"2"}'
  dxb_radio_add r9 '{"audio":"1"}'; assert_eq "$?" "2"          # duplicate
}

test_ports_allocate_lowest_free_even() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add a '{"audio":"3","cat":"4"}'
  assert_ok dxb_radio_add b '{"audio":"1","cat":"2"}'
  assert_eq "$(jq -r '.rigctld_port' <<< "$(dxb_radio_get b)")" "4534"
  assert_ok dxb_radio_remove a
  assert_ok dxb_radio_add c '{"audio":"3","cat":"4"}'
  assert_eq "$(jq -r '.rigctld_port' <<< "$(dxb_radio_get c)")" "4532"
  dxb_radio_get a > /dev/null; assert_eq "$?" "3"
}

test_set_changes_fields_keeps_port_and_owner() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add radio1 '{"audio":"1","cat":"2"}'
  assert_ok _dxb_radio_set_owner radio1 graywolf
  assert_ok dxb_radio_set radio1 '{"ptt":"cm108","label":"HT","cat":"none"}'
  local r; r=$(dxb_radio_get radio1)
  assert_eq "$(jq -r '.ptt.method + " " + .label + " " + (.cat|tostring) + " " + .owner + " " + (.rigctld_port|tostring)' <<< "$r")" "cm108 HT null graywolf 4532"
  dxb_radio_set radio1 '{"wiring":"sideways"}'; assert_eq "$?" "2"
  dxb_radio_set nope '{"label":"x"}'; assert_eq "$?" "3"
}
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement** (append to `radio.sh`)

```bash
# ---- record --------------------------------------------------------------------------------
DXB_RADIO_NAME_RE='^[a-z][a-z0-9]{0,11}$'
dxb_radio_empty_record() { printf '{"version":1,"radios":{},"gps":{"device":"auto","baud":9600,"pps":""}}\n'; }

dxb_radio_load() {
  if [[ -f $DXB_RADIOS_FILE ]]; then
    DXB_RADIOS=$(jq -c . "$DXB_RADIOS_FILE" 2> /dev/null) || { dxb_error "$DXB_RADIOS_FILE is not valid JSON"; return 6; }
  else
    DXB_RADIOS=$(dxb_radio_empty_record)
  fi
}

# dxb_radio_validate JSON: 0 valid, 2 invalid (reasons on stderr).
dxb_radio_validate() {
  local errs
  errs=$(jq -r '
    def bad(m): "  " + m;
    [ (if .version != 1 then bad("version must be 1") else empty end),
      (.radios | to_entries[] | .key as $n | .value as $r |
        (if ($n | test("^[a-z][a-z0-9]{0,11}$") | not) then bad("bad radio name " + $n) else empty end),
        (if ($r.ptt.method as $m | ["rigctld","cm108","gpio","vox","digirig_tone","none"] | index($m)) == null then bad($n + ": bad ptt method") else empty end),
        (if ($r.rig.ptt_type as $t | ["RIG","RTS","DTR","NONE"] | index($t)) == null then bad($n + ": bad ptt_type") else empty end),
        (if ($r.wiring as $w | ["full","names"] | index($w)) == null then bad($n + ": bad wiring") else empty end),
        (if ($r.rig.model | type) != "number" or $r.rig.model < 1 then bad($n + ": bad model") else empty end),
        (if ($r.rigctld_port | type) != "number" or $r.rigctld_port < 4532 or ($r.rigctld_port % 2) != 0 then bad($n + ": bad port") else empty end),
        (if ($r.owner | type) != "string" then bad($n + ": bad owner") else empty end),
        (["audio","cat","hid","ptt_serial"][] as $k | if ($r[$k] != null) and (($r[$k].path // "") == "") then bad($n + ": pin " + $k + " has no path") else empty end)
      ),
      (if ([.radios[].rigctld_port] | unique | length) != ([.radios[].rigctld_port] | length) then bad("duplicate rigctld ports") else empty end)
    ] | .[]' <<< "$1" 2>&1)
  [[ -z $errs ]] && return 0
  dxb_error "invalid radio record:"; printf '%s\n' "$errs" >&2
  return 2
}

dxb_radio_save() {
  local j
  j=$(jq -S . <<< "$1" 2> /dev/null) || return 2
  dxb_radio_validate "$j" || return 2
  mkdir -p "$(dirname "$DXB_RADIOS_FILE")" 2> /dev/null
  dxb_write_if_changed "$DXB_RADIOS_FILE" "$j" 600
  [[ -f $DXB_RADIOS_FILE && $(< "$DXB_RADIOS_FILE") == "$j" ]] || { dxb_error "could not write $DXB_RADIOS_FILE"; return 6; }
  DXB_RADIOS=$(jq -c . <<< "$j")
}

dxb_radio_names() { jq -r '.radios | keys[]' <<< "$DXB_RADIOS"; }
dxb_radio_get() { jq -ce --arg n "$1" '.radios[$n] // empty' <<< "$DXB_RADIOS" || { dxb_error "no such radio: $1"; return 3; }; }
dxb_radio_alloc_port() { jq -r '[.radios[].rigctld_port] as $u | [range(4532; 4600; 2)] | map(select(. as $p | $u | index($p) | not)) | .[0]' <<< "$DXB_RADIOS"; }

# _dxb_radio_pin SELECTOR KIND: resolves "N" / "N:K" against DXB_RADIO_SCAN to a pin object; "none" -> null.
_dxb_radio_pin() {
  local sel=$1 kind=$2 n k
  [[ $sel == none || -z $sel ]] && { echo null; return 0; }
  [[ $sel =~ ^([0-9]+)(:([0-9]+))?$ ]] || { dxb_error "bad candidate selector '$sel' (use N or N:K)"; return 2; }
  n=${BASH_REMATCH[1]}; k=${BASH_REMATCH[3]:-1}
  jq -ce --argjson n "$n" --argjson k "$k" --arg kind "$kind" \
    '(.[] | select(.index == $n) | .functions | map(select(.kind == $kind)) | .[$k - 1]) // empty | {path, vidpid, serial}' <<< "$DXB_RADIO_SCAN" \
    || { dxb_error "candidate $sel has no $kind function"; return 2; }
}

# _dxb_radio_build OPTS BASE: merge OPTS (candidate selectors + overrides) into BASE (an existing radio or {}).
_dxb_radio_build() {
  local opts=$1 base=$2 k sel pin cand defaults='{}' r
  r=$base
  for k in audio cat hid ptt_serial; do
    sel=$(jq -r --arg k "$k" '.[$k] // empty' <<< "$opts")
    [[ -n $sel ]] || continue
    pin=$(_dxb_radio_pin "$sel" "$( [[ $k == audio ]] && echo audio || { [[ $k == hid ]] && echo hid || echo serial; } )") || return 2
    r=$(jq -c --arg k "$k" --argjson p "$pin" '.[$k] = $p' <<< "$r")
  done
  # profile defaults come from the audio candidate, else the cat candidate, only when creating
  if [[ $(jq -r '.profile // empty' <<< "$r") == "" ]]; then
    cand=$(jq -r '.audio // .cat // empty' <<< "$opts"); cand=${cand%%:*}
    if [[ -n $cand && $cand != none ]]; then
      defaults=$(jq -c --argjson n "$cand" '.[] | select(.index == $n) | {profile: .profile, defaults: .defaults}' <<< "$DXB_RADIO_SCAN")
    fi
    [[ -n $defaults ]] || defaults='{}'
    r=$(jq -c --argjson d "$defaults" '
      ($d.defaults // {ptt: "vox", ptt_type: "NONE", model: 1, baud: 0}) as $df
      | {label: "", profile: ($d.profile // "generic"), audio: null, cat: null, hid: null, ptt_serial: null,
         ptt: {method: $df.ptt, gpio_line: null}, rig: {model: $df.model, baud: $df.baud, ptt_type: $df.ptt_type},
         wiring: "full", owner: ""} + .' <<< "$r")
    # the DigiRig hid pin is implied by the audio candidate when the operator did not choose one
    if [[ $(jq -r '.hid' <<< "$r") == null ]]; then
      cand=$(jq -r '.audio // empty' <<< "$opts"); cand=${cand%%:*}
      [[ -n $cand && $cand != none ]] && pin=$(_dxb_radio_pin "$cand" hid 2> /dev/null) && r=$(jq -c --argjson p "$pin" '.hid = $p' <<< "$r")
    fi
  fi
  jq -c --argjson o "$opts" '
    . + (if $o.label != null then {label: $o.label} else {} end)
      + (if $o.wiring != null then {wiring: $o.wiring} else {} end)
    | .ptt.method = ($o.ptt // .ptt.method) | .ptt.gpio_line = (if $o.gpio_line != null then $o.gpio_line else .ptt.gpio_line end)
    | .rig.model = ($o.model // .rig.model) | .rig.baud = ($o.baud // .rig.baud) | .rig.ptt_type = ($o.ptt_type // .rig.ptt_type)
    | if .cat == null and .rig.ptt_type == "RIG" then .rig.ptt_type = "NONE" else . end' <<< "$r"
}

dxb_radio_add() {
  local name=$1 opts=$2 radio
  [[ $name =~ $DXB_RADIO_NAME_RE ]] || { dxb_error "radio name must match $DXB_RADIO_NAME_RE"; return 2; }
  jq -e --arg n "$name" '.radios[$n]' <<< "$DXB_RADIOS" > /dev/null 2>&1 && { dxb_error "radio $name already exists"; return 2; }
  jq -e 'type == "object"' <<< "$opts" > /dev/null 2>&1 || { dxb_error "options must be a JSON object"; return 2; }
  radio=$(_dxb_radio_build "$opts" '{}') || return 2
  radio=$(jq -c --argjson p "$(dxb_radio_alloc_port)" '. + {rigctld_port: $p, owner: ""}' <<< "$radio")
  dxb_radio_save "$(jq -c --arg n "$name" --argjson r "$radio" '.radios[$n] = $r' <<< "$DXB_RADIOS")" && dxb_info "radio $name added"
}

dxb_radio_set() {
  local name=$1 opts=$2 cur radio
  cur=$(dxb_radio_get "$name") || return 3
  radio=$(_dxb_radio_build "$opts" "$cur") || return 2
  dxb_radio_save "$(jq -c --arg n "$name" --argjson r "$radio" '.radios[$n] = $r' <<< "$DXB_RADIOS")" || return $?
  dxb_info "radio $name updated"
}

dxb_radio_remove() {
  dxb_radio_get "$1" > /dev/null || return 3
  dxb_radio_save "$(jq -c --arg n "$1" 'del(.radios[$n])' <<< "$DXB_RADIOS")" && dxb_info "radio $1 removed"
}

_dxb_radio_set_owner() { dxb_radio_save "$(jq -c --arg n "$1" --arg a "$2" '.radios[$n].owner = $a' <<< "$DXB_RADIOS")"; }
```

`_dxb_radio_build` on `set`: the `for k` loop only touches pins named in OPTS (`"none"` clears one), the profile block is skipped because `.profile` already exists, and overrides merge over the existing values.

- [ ] **Step 4: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/radio.sh tests/test_radio.sh
git commit -m "Keep the radio record: add, set and remove pinned radios with profile defaults"
```

---

### Task 5: Generated udev rules and onboard audio pinning

**Files:**
- Create: `provision/lib/radio_udev.sh`, `provision/templates/70-dxberry-radio.rules.head`, `provision/templates/dxberry-audio.conf`
- Test: `tests/test_radio_udev.sh`, expected files under `tests/fixtures/udev/`

**Interfaces:**
- Consumes: record JSON (Task 4), `dxb_write_if_changed`, `DXB_TEMPLATES`.
- Produces: `dxb_radio_udev_rules JSON` (prints the rules file); `dxb_radio_udev_write JSON` (0 written, 1 unchanged, 6 error; on 0 runs `udevadm control --reload` and `udevadm trigger --action=add --subsystem-match=sound --subsystem-match=tty --subsystem-match=hidraw`); `dxb_radio_modprobe_install` (0/1/6). Env: `DXB_UDEV_RULES_FILE` (`/etc/udev/rules.d/70-dxberry-radio.rules`), `DXB_MODPROBE_FILE` (`/etc/modprobe.d/dxberry-audio.conf`), `DXB_UDEVADM` (`udevadm`).

- [ ] **Step 1: Templates**

`provision/templates/70-dxberry-radio.rules.head`:

```
# Generated by dxberry-radio apply from /var/lib/dxberry/radios.json. Do not edit; it is overwritten.
# Stable names for pinned radios: ALSA card ids (hw:RADIO1), /dev/dxberry/<radio>-cat|-ptt|-hid symlinks.
# ID_PATH is imported here because this file sorts before udev's own 78-sound-card.rules.
SUBSYSTEM=="sound", KERNEL=="card*", IMPORT{builtin}="path_id"
SUBSYSTEM=="tty", IMPORT{builtin}="path_id"
SUBSYSTEM=="hidraw", IMPORT{builtin}="path_id"
```

`provision/templates/dxberry-audio.conf`:

```
# Installed by DXBerry-Pi: USB sound cards take ALSA indexes 0-3 so onboard HDMI/headphone audio never lands first.
options snd slots=snd_usb_audio,snd_usb_audio,snd_usb_audio,snd_usb_audio
```

- [ ] **Step 2: Failing tests**

`tests/fixtures/udev/digirig.rules` (expected output for the record built in the test; TAB-free, exact):

```
# Generated by dxberry-radio apply from /var/lib/dxberry/radios.json. Do not edit; it is overwritten.
# Stable names for pinned radios: ALSA card ids (hw:RADIO1), /dev/dxberry/<radio>-cat|-ptt|-hid symlinks.
# ID_PATH is imported here because this file sorts before udev's own 78-sound-card.rules.
SUBSYSTEM=="sound", KERNEL=="card*", IMPORT{builtin}="path_id"
SUBSYSTEM=="tty", IMPORT{builtin}="path_id"
SUBSYSTEM=="hidraw", IMPORT{builtin}="path_id"

# radio1 - TM-V71
SUBSYSTEM=="sound", KERNEL=="card*", ENV{ID_PATH}=="*-usb-0:1.3:1.0", ATTR{id}="RADIO1", TAG+="dxberry-radio"
SUBSYSTEM=="tty", ENV{ID_PATH}=="*-usb-0:1.4:1.0", SYMLINK+="dxberry/radio1-cat", TAG+="dxberry-radio"
SUBSYSTEM=="hidraw", ENV{ID_PATH}=="*-usb-0:1.3:1.3", SYMLINK+="dxberry/radio1-hid", TAG+="dxberry-radio"

TAG=="dxberry-radio", ACTION=="add|remove", RUN+="/bin/systemctl --no-block start dxberry-radio-hotplug.service"
```

`tests/test_radio_udev.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

udev_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules \
    DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=fake_udevadm
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc"; : > "$TEST_TMP/calls"
  fake_udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
digirig_record() {
  fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2","label":"TM-V71"}' > /dev/null
}

test_udev_rules_match_expected_file() {
  udev_env; digirig_record
  dxb_radio_udev_rules "$DXB_RADIOS" > "$TEST_TMP/out.rules"
  cmp -s "$TEST_TMP/out.rules" "$DXB_ROOT/tests/fixtures/udev/digirig.rules" || { _fail "rules differ"; diff "$DXB_ROOT/tests/fixtures/udev/digirig.rules" "$TEST_TMP/out.rules" >&2; }
}

test_udev_rules_empty_record_has_head_and_trailer_only() {
  udev_env; dxb_radio_load
  local out; out=$(dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$out" 'IMPORT{builtin}="path_id"'
  assert_contains "$out" 'TAG=="dxberry-radio", ACTION=="add|remove"'
  assert_not_contains "$out" 'SYMLINK'
}

test_udev_rules_ptt_serial_and_no_audio() {
  udev_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add cat1 '{"cat":"2","ptt_serial":"4","ptt_type":"RTS"}' > /dev/null
  local out; out=$(dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$out" 'SYMLINK+="dxberry/cat1-cat"'
  assert_contains "$out" 'ENV{ID_PATH}=="*-usb-0:1.4:1.0", SYMLINK+="dxberry/cat1-ptt"'
  assert_not_contains "$out" 'ATTR{id}'
}

test_udev_write_reloads_only_on_change() {
  udev_env; digirig_record
  assert_ok dxb_radio_udev_write "$DXB_RADIOS"
  assert_contains "$(cat "$TEST_TMP/calls")" "udevadm control --reload"
  assert_contains "$(cat "$TEST_TMP/calls")" "udevadm trigger --action=add --subsystem-match=sound --subsystem-match=tty --subsystem-match=hidraw"
  : > "$TEST_TMP/calls"
  dxb_radio_udev_write "$DXB_RADIOS"; assert_eq "$?" "1"
  assert_eq "$(cat "$TEST_TMP/calls")" ""
}

test_modprobe_install_idempotent() {
  udev_env
  assert_ok dxb_radio_modprobe_install
  assert_file_contains "$DXB_MODPROBE_FILE" "options snd slots=snd_usb_audio"
  dxb_radio_modprobe_install; assert_eq "$?" "1"
}
```

- [ ] **Step 3: Run, expect failures.**

- [ ] **Step 4: Implement** `provision/lib/radio_udev.sh`:

```bash
#!/bin/bash
# shellcheck shell=bash
# Derived udev rules and ALSA slot pinning for pinned radios (spec sections 7.1, 7.2).

: "${DXB_UDEV_RULES_FILE:=/etc/udev/rules.d/70-dxberry-radio.rules}"
: "${DXB_MODPROBE_FILE:=/etc/modprobe.d/dxberry-audio.conf}"
: "${DXB_UDEVADM:=udevadm}"

dxb_radio_udev_rules() {
  cat "$DXB_TEMPLATES/70-dxberry-radio.rules.head"
  jq -r '.radios | to_entries[] | .key as $n | .value as $r
    | "", "# \($n) - \($r.label // "")",
      (if $r.audio then "SUBSYSTEM==\"sound\", KERNEL==\"card*\", ENV{ID_PATH}==\"*-\($r.audio.path)\", ATTR{id}=\"\($n | ascii_upcase)\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.cat then "SUBSYSTEM==\"tty\", ENV{ID_PATH}==\"*-\($r.cat.path)\", SYMLINK+=\"dxberry/\($n)-cat\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.ptt_serial then "SUBSYSTEM==\"tty\", ENV{ID_PATH}==\"*-\($r.ptt_serial.path)\", SYMLINK+=\"dxberry/\($n)-ptt\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.hid then "SUBSYSTEM==\"hidraw\", ENV{ID_PATH}==\"*-\($r.hid.path)\", SYMLINK+=\"dxberry/\($n)-hid\", TAG+=\"dxberry-radio\"" else empty end)' <<< "$1"
  printf '\nTAG=="dxberry-radio", ACTION=="add|remove", RUN+="/bin/systemctl --no-block start dxberry-radio-hotplug.service"\n'
}

# 0 written (udev reloaded and re-triggered), 1 unchanged, 6 could not write.
dxb_radio_udev_write() {
  local content
  content=$(dxb_radio_udev_rules "$1") || return 6
  mkdir -p "$(dirname "$DXB_UDEV_RULES_FILE")" 2> /dev/null
  if dxb_write_if_changed "$DXB_UDEV_RULES_FILE" "$content" 644; then
    [[ $(< "$DXB_UDEV_RULES_FILE") == "$content" ]] || { dxb_error "could not write $DXB_UDEV_RULES_FILE"; return 6; }
    dxb_info "udev rules written to $DXB_UDEV_RULES_FILE"
    "$DXB_UDEVADM" control --reload || dxb_warn "udevadm control --reload failed"
    "$DXB_UDEVADM" trigger --action=add --subsystem-match=sound --subsystem-match=tty --subsystem-match=hidraw || dxb_warn "udevadm trigger failed"
    return 0
  fi
  return 1
}

dxb_radio_modprobe_install() {
  local content
  content=$(< "$DXB_TEMPLATES/dxberry-audio.conf") || return 6
  mkdir -p "$(dirname "$DXB_MODPROBE_FILE")" 2> /dev/null
  if dxb_write_if_changed "$DXB_MODPROBE_FILE" "$content" 644; then
    [[ $(< "$DXB_MODPROBE_FILE") == "$content" ]] || return 6
    dxb_info "ALSA slot pinning installed ($DXB_MODPROBE_FILE; takes effect at next boot)"
    return 0
  fi
  return 1
}
```

Note `dxb_write_if_changed` appends a trailing newline to CONTENT; `$(< file)` strips it, so the comparison holds. `cmp` in the test compares the raw `dxb_radio_udev_rules` output, which ends with a newline from the `printf`.

- [ ] **Step 5: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 6: Commit**

```bash
git add provision/lib/radio_udev.sh provision/templates/70-dxberry-radio.rules.head provision/templates/dxberry-audio.conf tests/test_radio_udev.sh tests/fixtures/udev
git commit -m "Generate udev rules for stable radio names and pin USB audio to the low ALSA slots"
```

---

### Task 6: rigctld instances

**Files:**
- Create: `provision/lib/rigctld.sh`, `provision/templates/rigctld@.service`, `provision/templates/dxberry-radio-hotplug.service`, `provision/templates/dxberry-radio.tmpfiles`
- Test: `tests/test_rigctld.sh`

**Interfaces:**
- Consumes: radio object JSON (Task 4), `DXB_SYSTEMD_DIR` (already defined in `network.sh`; define it again with the same default here so the module stands alone), `dxb_render`.
- Produces: `dxb_rigctld_env NAME RADIO_JSON` (prints env file content); `dxb_rigctld_sync NAME RADIO_JSON PRESENT` (writes `$DXB_RIGCTLD_RUN_DIR/NAME.env`, starts or stops `rigctld@NAME`; restarts it when the env changed while active; 0 ok, 6 error); `dxb_rigctld_state NAME` (`active|inactive|failed|unknown`); `dxb_rigctld_query PORT` (prints `FREQ MODE` or `? ?`); `dxb_rigctld_install_units` (0/1/6: copies the three templates, `daemon-reload`, `enable dxberry-radio-hotplug.service`, applies tmpfiles); `dxb_rigctld_stop_all_except NAMES…` (stops instances for radios no longer in the record). Env: `DXB_RIGCTLD_RUN_DIR` (`/run/dxberry/rigctld`), `DXB_RIGCTL` (`rigctl`), `DXB_TMPFILES_DIR` (`/etc/tmpfiles.d`).

- [ ] **Step 1: Templates**

`provision/templates/rigctld@.service` (`$VAR` unbraced on purpose: systemd word-splits unbraced variables, so `RIG_ARGS` may carry several arguments):

```
[Unit]
Description=Hamlib rigctld for radio %i (DXBerry-Pi)
Documentation=file:///opt/dxberry/bin/dxberry-radio
After=dxberry-radio-hotplug.service
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
EnvironmentFile=/run/dxberry/rigctld/%i.env
ExecStart=/usr/bin/rigctld -m $MODEL -T 127.0.0.1 -t $PORT $RIG_ARGS $PTT_ARGS
Restart=on-failure
RestartSec=5
```

`provision/templates/dxberry-radio-hotplug.service`:

```
[Unit]
Description=DXBerry-Pi radio hotplug: refresh stable names, rigctld instances and runtime state
Documentation=file:///opt/dxberry/bin/dxberry-radio
After=local-fs.target systemd-udevd.service
Before=graywolf.service

[Service]
Type=oneshot
ExecStart=/opt/dxberry/bin/dxberry-radio hotplug

[Install]
WantedBy=multi-user.target
```

`provision/templates/dxberry-radio.tmpfiles`:

```
d /run/dxberry 0755 root root -
d /run/dxberry/rigctld 0755 root root -
```

- [ ] **Step 2: Failing tests**

`tests/test_rigctld.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

rig_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld \
    DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles DXB_RIGCTL=fake_rigctl
  mkdir -p "$DXB_STATE_DIR"; : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  systemctl() { fx_systemctl "$@"; }
  systemd-tmpfiles() { echo "systemd-tmpfiles $*" >> "$TEST_TMP/calls"; }
  fake_rigctl() { echo "rigctl $*" >> "$TEST_TMP/calls"; printf '145390000\nFM\n15000\n'; }
  timeout() { shift; "$@"; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
R_DIGIRIG='{"cat":{"path":"usb-0:1.4:1.0"},"ptt_serial":null,"rig":{"model":1,"baud":57600,"ptt_type":"RTS"},"rigctld_port":4532}'
R_IC7300='{"cat":{"path":"usb-0:1.2:1.2"},"ptt_serial":null,"rig":{"model":3073,"baud":115200,"ptt_type":"RIG"},"rigctld_port":4534}'
R_NOCAT='{"cat":null,"ptt_serial":null,"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4536}'
R_SPLITPTT='{"cat":{"path":"usb-0:1.2:1.0"},"ptt_serial":{"path":"usb-0:1.4:1.0"},"rig":{"model":1,"baud":38400,"ptt_type":"DTR"},"rigctld_port":4538}'

test_rigctld_env_shapes() {
  rig_env
  assert_eq "$(dxb_rigctld_env radio1 "$R_DIGIRIG")" $'MODEL=1\nPORT=4532\nRIG_ARGS=-r /dev/dxberry/radio1-cat -s 57600\nPTT_ARGS=-P RTS -p /dev/dxberry/radio1-cat'
  assert_eq "$(dxb_rigctld_env hf "$R_IC7300")" $'MODEL=3073\nPORT=4534\nRIG_ARGS=-r /dev/dxberry/hf-cat -s 115200\nPTT_ARGS=-P RIG'
  assert_eq "$(dxb_rigctld_env ht "$R_NOCAT")" $'MODEL=1\nPORT=4536\nRIG_ARGS=\nPTT_ARGS='
  assert_eq "$(dxb_rigctld_env sp "$R_SPLITPTT")" $'MODEL=1\nPORT=4538\nRIG_ARGS=-r /dev/dxberry/sp-cat -s 38400\nPTT_ARGS=-P DTR -p /dev/dxberry/sp-ptt'
}

test_rigctld_sync_starts_when_present_stops_when_absent() {
  rig_env
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 1
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/radio1.env" "PORT=4532"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
  assert_eq "$(dxb_rigctld_state radio1)" "active"
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"      # already active, env unchanged
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 0
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_eq "$(dxb_rigctld_state radio1)" "inactive"
}

test_rigctld_sync_restarts_on_env_change() {
  rig_env
  dxb_rigctld_sync radio1 "$R_DIGIRIG" 1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$(jq -c '.rig.baud = 9600' <<< "$R_DIGIRIG")" 1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart rigctld@radio1"
}

test_rigctld_query_and_failure() {
  rig_env
  assert_eq "$(dxb_rigctld_query 4532)" "145390000 FM"
  assert_contains "$(cat "$TEST_TMP/calls")" "rigctl -m 2 -r 127.0.0.1:4532 f m"
  fake_rigctl() { return 2; }
  assert_eq "$(dxb_rigctld_query 4532)" "? ?"
}

test_rigctld_install_units_and_stop_stale() {
  rig_env
  assert_ok dxb_rigctld_install_units
  assert_file_contains "$DXB_SYSTEMD_DIR/rigctld@.service" 'ExecStart=/usr/bin/rigctld -m $MODEL'
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-hotplug.service" "dxberry-radio hotplug"
  assert_file_contains "$DXB_TMPFILES_DIR/dxberry-radio.conf" "/run/dxberry/rigctld"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl daemon-reload"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  dxb_rigctld_install_units; assert_eq "$?" "1"
  dxb_rigctld_sync old "$R_NOCAT" 1 > /dev/null; dxb_rigctld_sync keep "$R_DIGIRIG" 1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_stop_all_except keep
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@old"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "rigctld@keep"
  [[ -f $DXB_RIGCTLD_RUN_DIR/old.env ]] && _fail "stale env file not removed"
}
```

- [ ] **Step 3: Run, expect failures.**

- [ ] **Step 4: Implement** `provision/lib/rigctld.sh`:

```bash
#!/bin/bash
# shellcheck shell=bash
# One hamlib rigctld per radio, driven by env files under /run (spec section 7.3).

: "${DXB_RIGCTLD_RUN_DIR:=/run/dxberry/rigctld}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TMPFILES_DIR:=/etc/tmpfiles.d}"
: "${DXB_RIGCTL:=rigctl}"

# dxb_rigctld_env NAME RADIO_JSON: the EnvironmentFile content for rigctld@NAME.
dxb_rigctld_env() {
  local name=$1 r=$2 model port baud ptt cat='' pttdev='' rig_args='' ptt_args=''
  model=$(jq -r '.rig.model' <<< "$r"); port=$(jq -r '.rigctld_port' <<< "$r")
  baud=$(jq -r '.rig.baud' <<< "$r"); ptt=$(jq -r '.rig.ptt_type' <<< "$r")
  [[ $(jq -r '.cat' <<< "$r") == null ]] || cat="/dev/dxberry/$name-cat"
  [[ $(jq -r '.ptt_serial' <<< "$r") == null ]] || pttdev="/dev/dxberry/$name-ptt"
  [[ -z $cat ]] || rig_args="-r $cat -s $baud"
  case $ptt in
    RIG) ptt_args='-P RIG' ;;
    RTS|DTR) [[ -n ${pttdev:-$cat} ]] && ptt_args="-P $ptt -p ${pttdev:-$cat}" ;;
  esac
  printf 'MODEL=%s\nPORT=%s\nRIG_ARGS=%s\nPTT_ARGS=%s\n' "$model" "$port" "$rig_args" "$ptt_args"
}

dxb_rigctld_state() {
  local s
  s=$(systemctl is-active "rigctld@$1.service" 2> /dev/null) && { echo "$s"; return 0; }
  case $s in inactive|failed|activating|deactivating) echo "$s" ;; *) echo unknown ;; esac
}

# dxb_rigctld_sync NAME RADIO_JSON PRESENT(0|1): env file + start/stop/restart. 0 ok, 6 error.
dxb_rigctld_sync() {
  local name=$1 r=$2 present=$3 env f changed=0 unit="rigctld@$1.service"
  f="$DXB_RIGCTLD_RUN_DIR/$name.env"
  mkdir -p "$DXB_RIGCTLD_RUN_DIR" 2> /dev/null || { dxb_error "cannot create $DXB_RIGCTLD_RUN_DIR"; return 6; }
  env=$(dxb_rigctld_env "$name" "$r")
  if dxb_write_if_changed "$f" "$env" 644; then
    [[ $(< "$f") == "$env" ]] || { dxb_error "could not write $f"; return 6; }
    changed=1
  fi
  if (( present )); then
    if systemctl is-active --quiet "$unit"; then
      (( changed )) && { systemctl restart "$unit" || { dxb_error "restart $unit failed"; return 6; }; dxb_info "$unit restarted (configuration changed)"; }
    else
      systemctl start "$unit" || { dxb_error "start $unit failed"; return 6; }
      dxb_info "$unit started on port $(jq -r '.rigctld_port' <<< "$r")"
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
    dxb_info "rigctld@$n stopped and removed (radio no longer in the record)"
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
  local rc=1 t dest content
  mkdir -p "$DXB_SYSTEMD_DIR" "$DXB_TMPFILES_DIR" 2> /dev/null
  for t in rigctld@.service dxberry-radio-hotplug.service; do
    content=$(< "$DXB_TEMPLATES/$t") || return 6
    dest="$DXB_SYSTEMD_DIR/$t"
    if dxb_write_if_changed "$dest" "$content" 644; then
      [[ $(< "$dest") == "$content" ]] || { dxb_error "could not write $dest"; return 6; }
      rc=0
    fi
  done
  content=$(< "$DXB_TEMPLATES/dxberry-radio.tmpfiles") || return 6
  if dxb_write_if_changed "$DXB_TMPFILES_DIR/dxberry-radio.conf" "$content" 644; then rc=0; fi
  if (( rc == 0 )); then
    systemctl daemon-reload || { dxb_error "systemctl daemon-reload failed"; return 6; }
    systemd-tmpfiles --create "$DXB_TMPFILES_DIR/dxberry-radio.conf" 2> /dev/null || mkdir -p "$DXB_RIGCTLD_RUN_DIR"
    dxb_info "rigctld@ and dxberry-radio-hotplug units installed"
  fi
  systemctl enable dxberry-radio-hotplug.service > /dev/null 2>&1 || { dxb_error "systemctl enable dxberry-radio-hotplug.service failed"; return 6; }
  return $rc
}
```

- [ ] **Step 5: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 6: Commit**

```bash
git add provision/lib/rigctld.sh provision/templates/rigctld@.service provision/templates/dxberry-radio-hotplug.service provision/templates/dxberry-radio.tmpfiles tests/test_rigctld.sh
git commit -m "Run one rigctld per radio from env files and start or stop it by presence"
```

---

### Task 7: apply, presence and the runtime mirror

**Files:**
- Modify: `provision/lib/radio.sh`
- Test: `tests/test_radio.sh`

**Interfaces:**
- Consumes: Tasks 3–6.
- Produces: `dxb_radio_present NAME` (0 present, 4 absent; uses `DXB_RADIO_SCAN`); `dxb_radio_kernel_names NAME` (JSON `{audio,cat,hid,ptt_serial}` of kernel names or null); `dxb_radio_wire_hash RADIO_JSON` (sha256 of the wiring inputs); `dxb_radio_write_state` (writes `DXB_RADIOS_STATE`); `dxb_radio_apply [hotplug]` (0 ok, 6 apply error, 7 re-wire error). Re-wiring calls `dxb_app_rewire NAME` which Task 8 defines; until then `apply` calls it only if `declare -F dxb_app_rewire` succeeds.

- [ ] **Step 1: Failing tests** (append to `tests/test_radio.sh`; source `radio_udev.sh` and `rigctld.sh` at the top of the file and extend `radio_env` with the udev/rigctld env vars and stubs from Tasks 5–6: `DXB_UDEV_RULES_FILE`, `DXB_MODPROBE_FILE`, `DXB_UDEVADM=fake_udevadm`, `DXB_RIGCTLD_RUN_DIR`, `DXB_SYSTEMD_DIR`, `DXB_TMPFILES_DIR`, `DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json`, `: > "$TEST_TMP/active"`, and the stubs `systemctl() { fx_systemctl "$@"; }`, `fake_udevadm`, `systemd-tmpfiles`)

```bash
test_presence_follows_scan() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  assert_ok dxb_radio_present radio1
  assert_eq "$(jq -c . <<< "$(dxb_radio_kernel_names radio1)")" '{"audio":"card1","cat":"ttyUSB0","hid":"hidraw1","ptt_serial":null}'
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; dxb_radio_scan_cache
  dxb_radio_present radio1; assert_eq "$?" "4"
}

test_apply_writes_rules_syncs_rigctld_and_state() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  assert_ok dxb_radio_apply
  assert_file_contains "$DXB_UDEV_RULES_FILE" 'ATTR{id}="RADIO1"'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
  assert_eq "$(jq -r '.radios.radio1.present' "$DXB_RADIOS_STATE")" "true"
  assert_eq "$(jq -r '.radios.radio1.rigctld' "$DXB_RADIOS_STATE")" "active"
  assert_eq "$(jq -r '.radios.radio1.kernel.audio' "$DXB_RADIOS_STATE")" "card1"
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"     # unchanged: no reload
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"
}

test_apply_hotplug_stops_absent_and_skips_udev() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null; dxb_radio_apply > /dev/null
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; rm -f "$DXB_UDEV_RULES_FILE"; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"
  [[ -f $DXB_UDEV_RULES_FILE ]] && _fail "hotplug must not regenerate udev rules"
  assert_eq "$(jq -r '.radios.radio1.present' "$DXB_RADIOS_STATE")" "false"
}

test_apply_rewires_owner_when_inputs_change() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  REWIRED=''; dxb_app_rewire() { REWIRED+="$1;"; return 0; }
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" ""            # no owner: nothing to rewire
  _dxb_radio_set_owner radio1 fakeapp > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;"    # owner set, hash new
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;"    # unchanged: not again
  dxb_radio_set radio1 '{"baud":9600}' > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;radio1;"
  dxb_app_rewire() { return 7; }
  dxb_radio_set radio1 '{"baud":4800}' > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$?" "7"
  source "$DXB_LIB/radio.sh"     # restore the real dxb_app_rewire (Task 8) for later tests in this process
}

test_apply_removes_stale_rigctld() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null; dxb_radio_apply > /dev/null
  dxb_radio_remove radio1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_eq "$(jq -c '.radios' "$DXB_RADIOS_STATE")" "{}"
}
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement** (append to `radio.sh`; it now needs `radio_udev.sh` and `rigctld.sh` sourced by its callers)

```bash
# ---- presence and runtime state ------------------------------------------------------------
_dxb_radio_kernel_for() { jq -r --arg p "$1" --arg k "$2" '[.[].functions[] | select(.path == $p and .kind == $k)] | .[0].kernel // empty' <<< "$DXB_RADIO_SCAN"; }

# dxb_radio_kernel_names NAME: {"audio":"card1","cat":"ttyUSB0","hid":null,"ptt_serial":null} for this boot.
dxb_radio_kernel_names() {
  local r k kind path out='{}'
  r=$(dxb_radio_get "$1") || return 3
  for k in audio cat hid ptt_serial; do
    case $k in audio) kind=audio ;; hid) kind=hid ;; *) kind=serial ;; esac
    path=$(jq -r --arg k "$k" '.[$k].path // empty' <<< "$r")
    if [[ -n $path ]]; then
      out=$(jq -c --arg k "$k" --arg v "$(_dxb_radio_kernel_for "$path" "$kind")" '.[$k] = (if $v == "" then null else $v end)' <<< "$out")
    else
      out=$(jq -c --arg k "$k" '.[$k] = null' <<< "$out")
    fi
  done
  printf '%s\n' "$out"
}

# dxb_radio_present NAME: 0 when every pinned function was found in the current scan, else 4.
dxb_radio_present() {
  local r names
  r=$(dxb_radio_get "$1") || return 3
  names=$(dxb_radio_kernel_names "$1")
  jq -e --argjson n "$names" '[["audio","cat","hid","ptt_serial"][] as $k | select(.[$k] != null) | $n[$k]] | all(. != null)' <<< "$r" > /dev/null || return 4
}

dxb_radio_wire_hash() { jq -c '{audio, cat, hid, ptt_serial, ptt, rig, rigctld_port, wiring}' <<< "$1" | sha256sum | cut -c1-16; }

# dxb_radio_write_state: the runtime mirror the console and status read.
dxb_radio_write_state() {
  local n out='{}' present names
  for n in $(dxb_radio_names); do
    if dxb_radio_present "$n"; then present=true; else present=false; fi
    names=$(dxb_radio_kernel_names "$n")
    out=$(jq -c --arg n "$n" --argjson p "$present" --argjson k "$names" --arg s "$(dxb_rigctld_state "$n")" \
      --arg h "$(dxb_radio_wire_hash "$(dxb_radio_get "$n")")" --arg w "$(jq -r --arg n "$n" '.radios[$n].wired_hash // ""' "$DXB_RADIOS_STATE" 2> /dev/null)" \
      '.[$n] = {present: $p, kernel: $k, rigctld: $s, wire_hash: $h, wired_hash: $w}' <<< "$out")
  done
  mkdir -p "$(dirname "$DXB_RADIOS_STATE")" 2> /dev/null
  dxb_write_if_changed "$DXB_RADIOS_STATE" "$(jq -c --argjson r "$out" '{generated: (now | todate), radios: $r}' <<< '{}')" 644
  [[ -f $DXB_RADIOS_STATE ]] || { dxb_error "could not write $DXB_RADIOS_STATE"; return 6; }
  return 0
}
_dxb_radio_mark_wired() { # NAME HASH: remember that the owner was wired with these inputs
  local j; j=$(jq -c --arg n "$1" --arg h "$2" '.radios[$n].wired_hash = $h' "$DXB_RADIOS_STATE" 2> /dev/null) || return 0
  printf '%s\n' "$j" > "$DXB_RADIOS_STATE"
}

# dxb_radio_apply [hotplug]: derive everything from the record. 0 ok, 6 derived-state error, 7 re-wire error.
dxb_radio_apply() {
  local mode=${1:-full} n r present rc=0 wrc h wired owner
  dxb_radio_load || return 6
  dxb_radio_scan_cache
  if [[ $mode != hotplug ]]; then
    dxb_radio_udev_write "$DXB_RADIOS"; wrc=$?; (( wrc == 6 )) && rc=6
    dxb_radio_modprobe_install > /dev/null; (( $? == 6 )) && rc=6
  fi
  for n in $(dxb_radio_names); do
    r=$(dxb_radio_get "$n")
    if dxb_radio_present "$n"; then present=1; else present=0; dxb_info "radio $n: device absent"; fi
    dxb_rigctld_sync "$n" "$r" "$present" || rc=6
  done
  # shellcheck disable=SC2046
  dxb_rigctld_stop_all_except $(dxb_radio_names)
  dxb_radio_write_state || rc=6
  for n in $(dxb_radio_names); do
    r=$(dxb_radio_get "$n"); owner=$(jq -r '.owner' <<< "$r")
    [[ -n $owner ]] && dxb_radio_present "$n" || continue
    h=$(dxb_radio_wire_hash "$r"); wired=$(jq -r --arg n "$n" '.radios[$n].wired_hash // ""' "$DXB_RADIOS_STATE")
    [[ $h == "$wired" ]] && continue
    if declare -F dxb_app_rewire > /dev/null; then
      if dxb_app_rewire "$n"; then _dxb_radio_mark_wired "$n" "$h"; else dxb_error "radio $n: re-wiring owner $owner failed"; (( rc == 0 )) && rc=7; fi
    fi
  done
  return $rc
}
```

- [ ] **Step 4: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/radio.sh tests/test_radio.sh
git commit -m "Derive udev rules, rigctld instances and runtime state from the radio record"
```

---

### Task 8: Application modules and hand-over

**Files:**
- Modify: `provision/lib/radio.sh`
- Modify: `docs/design/2026-09-09-radio-plumbing.md` §9.1 (add the optional `app_<app>_wait_ready` function to the contract table)
- Test: `tests/test_radio.sh` with fake modules written under `$TEST_TMP/apps`

**Interfaces:**
- Consumes: `_dxb_radio_set_owner`, `dxb_radio_present`, `dxb_radio_write_state`, `_dxb_radio_mark_wired`, `dxb_radio_wire_hash`.
- Produces: `dxb_app_load APP` (sources `$DXB_APPS_DIR/APP.sh`; 3 if missing); `dxb_app_list`; `dxb_app_owned APP` (count); `dxb_app_unit APP`; `dxb_app_wire APP NAME` (skips when `wiring == names`); `dxb_app_unwire APP NAME`; `dxb_app_start APP` (start unit if inactive, then `app_<app>_wait_ready` if defined; 5 on failure); `dxb_app_stop_if_idle APP`; `dxb_app_rewire NAME` (used by `apply`); `dxb_radio_claim NAME APP` (0/3/4/5/6); `dxb_radio_release NAME` (0/3/6).
- Module contract (§9.1 + `wait_ready`): `app_<app>_unit`, `app_<app>_wire NAME`, `app_<app>_unwire NAME`, `app_<app>_needs_service_restart` (prints `yes`/`no`), optional `app_<app>_wait_ready`.

- [ ] **Step 1: Failing tests** (append to `tests/test_radio.sh`)

```bash
fake_apps() {
  export DXB_APPS_DIR=$TEST_TMP/apps; mkdir -p "$DXB_APPS_DIR"; : > "$TEST_TMP/appcalls"
  cat > "$DXB_APPS_DIR/alpha.sh" <<'EOF'
app_alpha_unit() { echo alpha.service; }
app_alpha_wire() { echo "alpha wire $1" >> "$TEST_TMP/appcalls"; return "${ALPHA_WIRE_RC:-0}"; }
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/appcalls"; }
app_alpha_needs_service_restart() { echo no; }
app_alpha_wait_ready() { echo "alpha ready" >> "$TEST_TMP/appcalls"; return "${ALPHA_READY_RC:-0}"; }
EOF
  cat > "$DXB_APPS_DIR/beta.sh" <<'EOF'
app_beta_unit() { echo beta.service; }
app_beta_wire() { echo "beta wire $1" >> "$TEST_TMP/appcalls"; }
app_beta_unwire() { echo "beta unwire $1" >> "$TEST_TMP/appcalls"; }
app_beta_needs_service_restart() { echo yes; }
EOF
}
appcalls() { tr '\n' ';' < "$TEST_TMP/appcalls"; }
two_radios() {
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add r1 '{"audio":"3","cat":"4"}' > /dev/null; dxb_radio_add r2 '{"audio":"1","cat":"2"}' > /dev/null
}

test_claim_starts_wires_and_records_owner() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(appcalls)" "alpha ready;alpha wire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "alpha"
  assert_eq "$(jq -r '.radios.r1.wired_hash' "$DXB_RADIOS_STATE")" "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_claim r1 alpha                                 # same owner: re-wire only
  assert_eq "$(appcalls)" "alpha wire r1;"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"
}

test_claim_hands_over_and_stops_idle_old_owner() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 alpha > /dev/null; dxb_radio_claim r2 alpha > /dev/null
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_claim r1 beta
  assert_eq "$(appcalls)" "alpha unwire r1;beta wire r1;"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "stop alpha.service"   # alpha still owns r2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart beta.service"  # needs_service_restart yes
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r2 beta
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_eq "$(jq -r '[.radios[].owner] | join(",")' <<< "$DXB_RADIOS")" "beta,beta"
}

test_claim_failure_rolls_back_to_released() {
  radio_env; fake_apps; two_radios
  ALPHA_WIRE_RC=7
  dxb_radio_claim r1 alpha; assert_eq "$?" "5"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
  assert_contains "$(appcalls)" "alpha unwire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  unset ALPHA_WIRE_RC; ALPHA_READY_RC=1
  dxb_radio_claim r1 alpha; assert_eq "$?" "5"
  unset ALPHA_READY_RC
}

test_claim_errors_absent_radio_unknown_app() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim nope alpha; assert_eq "$?" "3"
  dxb_radio_claim r1 gamma; assert_eq "$?" "3"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; dxb_radio_scan_cache
  dxb_radio_claim r1 alpha; assert_eq "$?" "4"
}

test_release_unwires_and_stops_when_idle() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 alpha > /dev/null; : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_release r1
  assert_eq "$(appcalls)" "alpha unwire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
  assert_ok dxb_radio_release r1                                      # already released: no-op
  assert_not_contains "$(cat "$TEST_TMP/calls")" "rigctld"           # rigctld untouched by hand-over
}

test_names_wiring_skips_wire_but_starts_unit() {
  radio_env; fake_apps; two_radios
  dxb_radio_set r1 '{"wiring":"names"}' > /dev/null
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(appcalls)" "alpha ready;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
}

test_app_list_and_rewire() {
  radio_env; fake_apps; two_radios
  assert_eq "$(dxb_app_list | tr '\n' ' ')" "alpha beta "
  dxb_radio_claim r1 alpha > /dev/null; : > "$TEST_TMP/appcalls"
  assert_ok dxb_app_rewire r1
  assert_eq "$(appcalls)" "alpha wire r1;"
  dxb_app_rewire r2; assert_eq "$?" "0"                               # no owner: nothing to do
}
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement** (append to `radio.sh`)

```bash
# ---- applications --------------------------------------------------------------------------
dxb_app_list() { local f; for f in "$DXB_APPS_DIR"/*.sh; do [[ -f $f ]] || continue; f=${f##*/}; echo "${f%.sh}"; done; }
dxb_app_load() {
  [[ $1 =~ ^[a-z][a-z0-9_]{0,15}$ && -f $DXB_APPS_DIR/$1.sh ]] || { dxb_error "no such application: $1 (available: $(dxb_app_list | tr '\n' ' '))"; return 3; }
  # shellcheck disable=SC1090
  source "$DXB_APPS_DIR/$1.sh"
}
dxb_app_owned() { jq -r --arg a "$1" '[.radios[] | select(.owner == $a)] | length' <<< "$DXB_RADIOS"; }
dxb_app_unit() { "app_$1_unit"; }
dxb_app_wire() {
  [[ $(jq -r --arg n "$2" '.radios[$n].wiring' <<< "$DXB_RADIOS") == names ]] && return 0
  "app_$1_wire" "$2" || return 7
  if [[ $("app_$1_needs_service_restart") == yes ]]; then systemctl restart "$(dxb_app_unit "$1")" || return 7; fi
  return 0
}
dxb_app_unwire() { [[ $(jq -r --arg n "$2" '.radios[$n].wiring' <<< "$DXB_RADIOS") == names ]] && return 0; "app_$1_unwire" "$2"; }
dxb_app_start() {
  local u; u=$(dxb_app_unit "$1")
  systemctl is-active --quiet "$u" || systemctl start "$u" || { dxb_error "could not start $u"; return 5; }
  if declare -F "app_$1_wait_ready" > /dev/null; then "app_$1_wait_ready" || { dxb_error "$u did not become ready"; return 5; }; fi
  return 0
}
dxb_app_stop_if_idle() { (( $(dxb_app_owned "$1") == 0 )) || return 0; systemctl stop "$(dxb_app_unit "$1")" && dxb_info "$1 stopped (owns no radio)"; }

# dxb_app_rewire NAME: re-run the current owner's wiring (apply calls this when the inputs changed).
dxb_app_rewire() {
  local owner; owner=$(jq -r --arg n "$1" '.radios[$n].owner // ""' <<< "$DXB_RADIOS")
  [[ -n $owner ]] || return 0
  dxb_app_load "$owner" || return 3
  dxb_app_wire "$owner" "$1"
}

# dxb_radio_claim NAME APP (spec section 9.2). 0 ok, 3 unknown, 4 absent, 5 failed (released), 6 record error.
dxb_radio_claim() {
  local name=$1 app=$2 cur h
  dxb_radio_get "$name" > /dev/null || return 3
  dxb_app_load "$app" || return 3
  dxb_radio_present "$name" || { dxb_error "radio $name is not plugged in"; return 4; }
  cur=$(jq -r --arg n "$name" '.radios[$n].owner' <<< "$DXB_RADIOS")
  if [[ $cur == "$app" ]]; then
    dxb_app_wire "$app" "$name" || return 5
    _dxb_radio_mark_wired "$name" "$(dxb_radio_wire_hash "$(dxb_radio_get "$name")")"
    return 0
  fi
  if [[ -n $cur ]]; then
    if dxb_app_load "$cur"; then dxb_app_unwire "$cur" "$name"; fi
    _dxb_radio_set_owner "$name" "" || return 6
    dxb_app_load "$cur" 2> /dev/null && dxb_app_stop_if_idle "$cur"
    dxb_info "radio $name released by $cur"
  fi
  _dxb_radio_set_owner "$name" "$app" || return 6
  if ! dxb_app_start "$app" || ! dxb_app_wire "$app" "$name"; then
    dxb_app_unwire "$app" "$name"
    _dxb_radio_set_owner "$name" ""
    dxb_app_stop_if_idle "$app"
    dxb_error "radio $name: $app could not take it; left released"
    dxb_radio_write_state
    return 5
  fi
  dxb_radio_write_state
  _dxb_radio_mark_wired "$name" "$(dxb_radio_wire_hash "$(dxb_radio_get "$name")")"
  dxb_info "radio $name now owned by $app"
}

dxb_radio_release() {
  local name=$1 cur
  dxb_radio_get "$name" > /dev/null || return 3
  cur=$(jq -r --arg n "$name" '.radios[$n].owner' <<< "$DXB_RADIOS")
  [[ -n $cur ]] || return 0
  dxb_app_load "$cur" && dxb_app_unwire "$cur" "$name"
  _dxb_radio_set_owner "$name" "" || return 6
  dxb_app_load "$cur" 2> /dev/null && dxb_app_stop_if_idle "$cur"
  dxb_radio_write_state
  dxb_info "radio $name released"
}
```

Update spec §9.1's table with a row: `app_<app>_wait_ready` | optional; blocks until the application accepts configuration (Graywolf: its API answers); failure makes `claim` return 5.

- [ ] **Step 4: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/radio.sh tests/test_radio.sh docs/design/2026-09-09-radio-plumbing.md
git commit -m "Hand radios between applications through per-app wiring modules"
```

---

### Task 9: Graywolf application module and stored admin credentials

**Files:**
- Create: `provision/lib/apps/graywolf.sh`
- Modify: `provision/lib/graywolf.sh` (secret file, `dxb_gw_login_any`)
- Modify: `docs/design/2026-09-09-radio-plumbing.md` §9.3 and base spec §13 (one bullet: the Graywolf admin credentials are kept root-only in `/var/lib/dxberry/graywolf.secret` so `dxberry-radio` and the console can log in without prompting)
- Test: `tests/test_app_graywolf.sh`, `tests/test_graywolf.sh`

**Interfaces:**
- Consumes: `dxb_gw_api`, `dxb_gw_wait_ready`, `dxb_radio_get`, `fake_curl` pattern from `tests/test_graywolf.sh` (copy the helper into the new test file; test files share one process, so name the copy `gwapp_curl` and its env `gwapp_env`).
- Produces in `graywolf.sh`: `DXB_GW_SECRET_FILE` (`$DXB_STATE_DIR/graywolf.secret`, mode 0600, lines `USER=` and `PASSWORD=`); `dxb_gw_secret_save USER PASSWORD`; `dxb_gw_login_any` (secret file → `DXB_CFG[WEBUI_PASSWORD]` if loaded and not `<applied>` → tty prompt; 0 logged in, 1 failed). `dxb_gw_seed` calls `dxb_gw_secret_save` after a successful `/auth/setup` and after a successful `dxb_gw_login`.
- Produces in `apps/graywolf.sh`: `app_graywolf_unit` (`graywolf.service`), `app_graywolf_needs_service_restart` (`no`), `app_graywolf_wait_ready` (`dxb_gw_wait_ready`), `app_graywolf_wire NAME`, `app_graywolf_unwire NAME`, helpers `dxb_gwapp_find_id PATH NAME`, `dxb_gwapp_upsert PATH NAME BODY` (prints id), `dxb_gwapp_ptt_payload NAME RADIO_JSON CHANNEL_ID`.

- [ ] **Step 1: Failing tests**

`tests/test_app_graywolf.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/apps/graywolf.sh"

gwapp_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GW_COOKIES=$TEST_TMP/cookies \
    DXB_GW_API=http://gw/api DXB_GW_SECRET_FILE=$TEST_TMP/state/graywolf.secret DXB_RADIOS_FILE=$TEST_TMP/state/radios.json
  mkdir -p "$DXB_STATE_DIR"; : > "$TEST_TMP/calls"
  printf 'USER=admin\nPASSWORD=hunter2hunter2\n' > "$DXB_GW_SECRET_FILE"
  DXB_CURL=gwapp_curl; sleep() { :; }
  GW_AUDIO='[]'; GW_CHANNELS='[]'; GW_PTT_404=1; GW_FAIL_PATH=''
  DXB_RADIOS='{"version":1,"radios":{"radio1":{"label":"TM-V71","audio":{"path":"usb-0:1.3:1.0"},"cat":{"path":"usb-0:1.4:1.0"},"hid":{"path":"usb-0:1.3:1.3"},"ptt_serial":null,"ptt":{"method":"rigctld","gpio_line":null},"rig":{"model":1,"baud":57600,"ptt_type":"RTS"},"rigctld_port":4532,"wiring":"full","owner":""}},"gps":{}}'
}
# Records "METHOD PATH BODY"; answers from GW_AUDIO / GW_CHANNELS; POST returns {"id":N}; GW_FAIL_PATH forces one path to fail.
gwapp_curl() {
  local url='' m=GET data='' via=0
  while (( $# )); do case $1 in -X) m=$2; shift ;; --data-binary) data=$2; shift ;; -b|-c) via=1 ;; http*) url=$1 ;; esac; shift; done
  [[ $data == @-* ]] && data=$(cat)
  local p=${url#"$DXB_GW_API"}
  echo "$m $p $data" >> "$TEST_TMP/calls"
  [[ -n $GW_FAIL_PATH && $p == "$GW_FAIL_PATH" ]] && return 22
  case "$m $p" in
    "GET /auth/setup") echo '{"needs_setup":false}' ;;
    "POST /auth/login"|"POST /auth/logout") echo '{}' ;;
    "GET /audio-devices") echo "$GW_AUDIO" ;;
    "POST /audio-devices") echo '{"id":11}' ;;
    "PUT /audio-devices/"*) echo '{"id":11}' ;;
    "DELETE /audio-devices/"*) echo '{}' ;;
    "GET /channels") echo "$GW_CHANNELS" ;;
    "POST /channels") echo '{"id":21}' ;;
    "PUT /channels/"*) echo '{"id":21}' ;;
    "GET /channels/"*) echo '{"id":21,"name":"radio1"}' ;;
    "DELETE /channels/"*) echo '{}' ;;
    "GET /ptt/"*) (( GW_PTT_404 )) && return 22; echo '{"id":31,"channel_id":21,"method":"vox","dwait_ms":30}' ;;
    "POST /ptt"|"PUT /ptt/"*) echo '{"id":31}' ;;
    "POST /ptt/test-rigctld") echo '{"ok":true,"message":"","latency_ms":3}' ;;
    *) return 22 ;;
  esac
}
calls() { cat "$TEST_TMP/calls"; }

test_gwapp_wire_creates_device_channel_and_rigctld_ptt() {
  gwapp_env
  assert_ok app_graywolf_wire radio1
  assert_contains "$(calls)" 'POST /auth/login {"username":"admin","password":"hunter2hunter2"}'
  assert_contains "$(calls)" 'POST /audio-devices {"name":"radio1","source_type":"soundcard","source_path":"plughw:CARD=RADIO1,DEV=0","sample_rate":48000}'
  assert_contains "$(calls)" 'POST /channels {"name":"radio1","input_device_id":11,"output_device_id":11,"input_channel":0,"output_channel":0}'
  assert_contains "$(calls)" 'POST /ptt {"channel_id":21,"method":"rigctld","device_path":"127.0.0.1:4532","invert":false,"persist":true}'
  assert_contains "$(calls)" 'POST /ptt/test-rigctld {"host":"127.0.0.1","port":4532}'
  assert_contains "$(calls)" 'POST /auth/logout'
}

test_gwapp_wire_updates_existing_by_name_and_keeps_tuning() {
  gwapp_env
  GW_AUDIO='[{"id":5,"name":"other"},{"id":11,"name":"radio1","source_path":"plughw:0,0","gain_db":-6}]'
  GW_CHANNELS='[{"id":21,"name":"radio1","input_device_id":5,"output_device_id":5,"modem_type":"afsk1200","num_slicers":5}]'
  GW_PTT_404=0
  assert_ok app_graywolf_wire radio1
  assert_contains "$(calls)" 'PUT /audio-devices/11 {"name":"radio1","source_path":"plughw:CARD=RADIO1,DEV=0","gain_db":-6,"source_type":"soundcard","sample_rate":48000}'
  assert_contains "$(calls)" 'PUT /channels/21 {"name":"radio1","input_device_id":11,"output_device_id":11,"modem_type":"afsk1200","num_slicers":5,"input_channel":0,"output_channel":0}'
  assert_contains "$(calls)" 'PUT /ptt/21 {"channel_id":21,"method":"rigctld","dwait_ms":30,"device_path":"127.0.0.1:4532","invert":false,"persist":true}'
  assert_not_contains "$(calls)" 'POST /audio-devices'
}

test_gwapp_ptt_payloads_per_method() {
  gwapp_env
  local r; r=$(jq -c '.radios.radio1' <<< "$DXB_RADIOS")
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="cm108"' <<< "$r")" 21)" '{"channel_id":21,"method":"cm108","device_path":"/dev/dxberry/radio1-hid","gpio_pin":3,"invert":false,"persist":true}'
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="gpio" | .ptt.gpio_line=17' <<< "$r")" 21)" '{"channel_id":21,"method":"gpio","device_path":"/dev/gpiochip0","gpio_line":17,"invert":false,"persist":true}'
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="vox"' <<< "$r")" 21)" '{"channel_id":21,"method":"vox","invert":false,"persist":true}'
}

test_gwapp_wire_fails_with_7_on_api_error() {
  gwapp_env; GW_FAIL_PATH=/channels
  app_graywolf_wire radio1; assert_eq "$?" "7"
  gwapp_env; GW_FAIL_PATH=/ptt/test-rigctld
  assert_ok app_graywolf_wire radio1                                   # connectivity test failure is a warning only
}

test_gwapp_unwire_deletes_by_name_and_tolerates_absence() {
  gwapp_env
  GW_AUDIO='[{"id":11,"name":"radio1"}]'; GW_CHANNELS='[{"id":21,"name":"radio1"}]'
  assert_ok app_graywolf_unwire radio1
  assert_contains "$(calls)" 'DELETE /channels/21?cascade=true'
  assert_contains "$(calls)" 'DELETE /audio-devices/11'
  gwapp_env
  assert_ok app_graywolf_unwire radio1
  assert_not_contains "$(calls)" 'DELETE'
}

test_gwapp_contract_functions() {
  gwapp_env
  assert_eq "$(app_graywolf_unit)" "graywolf.service"
  assert_eq "$(app_graywolf_needs_service_restart)" "no"
  assert_ok app_graywolf_wait_ready
}
```

`tests/test_graywolf.sh` additions (use the file's existing `gw_env`/`fake_curl`; add `DXB_GW_SECRET_FILE=$TEST_TMP/state/graywolf.secret` to `gw_env`):

```bash
test_gw_seed_saves_admin_secret() {
  gw_env
  # (build the same minimal config the existing seed tests use, with WEBUI_USER admin and a password)
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\nCALLSIGN=W0BTE\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_eq "$(stat -c %a "$DXB_GW_SECRET_FILE")" "600"
  assert_eq "$(cat "$DXB_GW_SECRET_FILE")" $'USER=admin\nPASSWORD=hunter2hunter2'
}

test_gw_login_any_prefers_secret_file_then_config() {
  gw_env
  printf 'USER=admin\nPASSWORD=fromfile12\n' > "$DXB_GW_SECRET_FILE"; chmod 600 "$DXB_GW_SECRET_FILE"
  assert_ok dxb_gw_login_any
  assert_contains "$(cat "$TEST_TMP/calls")" 'POST /auth/login {"username":"admin","password":"fromfile12"}'
  rm -f "$DXB_GW_SECRET_FILE"; : > "$TEST_TMP/calls"
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=fromconfig1\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  assert_ok dxb_gw_login_any
  assert_contains "$(cat "$TEST_TMP/calls")" '"password":"fromconfig1"'
  assert_eq "$(cat "$DXB_GW_SECRET_FILE")" $'USER=admin\nPASSWORD=fromconfig1'   # a successful config login is saved too
}
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement**

`provision/lib/graywolf.sh` additions (near the other `DXB_GW_*` defaults and the login code):

```bash
: "${DXB_GW_SECRET_FILE:=$DXB_STATE_DIR/graywolf.secret}"

# The admin credentials stay root-only on the Pi so dxberry-radio and the console can log in
# without prompting after dxberry.txt has been scrubbed.
dxb_gw_secret_save() {
  ( umask 077; printf 'USER=%s\nPASSWORD=%s\n' "$1" "$2" > "$DXB_GW_SECRET_FILE.tmp" ) && mv -f "$DXB_GW_SECRET_FILE.tmp" "$DXB_GW_SECRET_FILE" && chmod 600 "$DXB_GW_SECRET_FILE"
}

# dxb_gw_login_any: stored secret, else the config's WEBUI_PASSWORD, else a terminal prompt.
dxb_gw_login_any() {
  local u pw
  if [[ -r $DXB_GW_SECRET_FILE ]]; then
    u=$(sed -n 's/^USER=//p' "$DXB_GW_SECRET_FILE" | head -1); pw=$(sed -n 's/^PASSWORD=//p' "$DXB_GW_SECRET_FILE" | head -1)
    if [[ -n $u && -n $pw ]] && dxb_gw_api POST /auth/login "$(PW=$pw jq -cn --arg u "$u" '{username: $u, password: env.PW}')" > /dev/null; then return 0; fi
    dxb_warn "stored Graywolf credentials were rejected; trying dxberry.txt"
  fi
  [[ -n ${DXB_CFG[WEBUI_USER]:-} ]] || { dxb_error "no Graywolf credentials available (no $DXB_GW_SECRET_FILE and no dxberry.txt loaded)"; return 1; }
  dxb_gw_login || return 1
  dxb_gw_secret_save "${DXB_CFG[WEBUI_USER]}" "${DXB_CFG[WEBUI_PASSWORD]}" 2> /dev/null || true
}
```

In `dxb_gw_login`, after the successful `POST /auth/login`, add `dxb_gw_secret_save "${DXB_CFG[WEBUI_USER]}" "$pw" || dxb_warn "could not save Graywolf credentials to $DXB_GW_SECRET_FILE"`. In `dxb_gw_seed`, after the successful `/auth/setup`, add the same call with `${DXB_CFG[WEBUI_PASSWORD]}`. (`dxb_gw_login` prompts into `pw` when the config value is `<applied>`; saving `$pw` there covers the `--reseed` path.)

`provision/lib/apps/graywolf.sh`:

```bash
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
```

The field order in the PUT assertions follows jq's `. + $o` semantics (existing keys keep their position, new keys append); if the implementation's order differs, fix the implementation, not the test, so the merge really is "existing plus ours".

- [ ] **Step 4: Run tests, shellcheck (now including `provision/lib/apps/*.sh`), check.** Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add provision/lib/apps/graywolf.sh provision/lib/graywolf.sh tests/test_app_graywolf.sh tests/test_graywolf.sh docs/design/2026-09-09-radio-plumbing.md docs/design/2026-09-07-base-image.md
git commit -m "Wire a claimed radio into Graywolf and keep the admin credentials root-only for later logins"
```

---

### Task 10: GPS and time: gpsd, chrony, readout, Graywolf seed

**Files:**
- Create: `provision/lib/gps.sh`, `provision/templates/chrony-dxberry.conf`, `provision/templates/gpsd-default.tmpl`
- Modify: `provision/lib/graywolf.sh` (+ `dxb_gw_seed_gps`, called from `dxb_gw_seed` after the station block regardless of CALLSIGN)
- Test: `tests/test_gps.sh`, `tests/test_graywolf.sh`

**Interfaces:**
- Consumes: `DXB_CFG[_GPS]`, `DXB_CFG[_GPS_PATH]`, `DXB_CFG[GPS_BAUD]`, `DXB_CFG[GPS_PPS]`, `dxb_ensure_line`, `dxb_render`, `dxb_gw_seed_state_get/set`.
- Produces: `dxb_gps_gpsd_default` (prints `/etc/default/gpsd` content); `dxb_gps_chrony_conf` (prints the drop-in); `dxb_gps_configure` (writes both, enables/disables units, restarts on change; 0 ok, 1 failed step recorded); `dxb_gps_boot_config BOOT_DIR` (config.txt lines; 0 changed, 1 unchanged); `dxb_gps_fix` (JSON: `{"fix":0}` or `{"fix":3,"lat":…,"lon":…,"alt_ft":…,"speed_mph":…,"sats_used":…,"sats_seen":…,"time":"…","grid":"…"}`); `dxb_maidenhead LAT LON` (6 chars); `dxb_gps_status_line`. Env: `DXB_GPSD_DEFAULT` (`/etc/default/gpsd`), `DXB_CHRONY_DROPIN` (`/etc/chrony/conf.d/dxberry.conf`), `DXB_GPSPIPE` (`gpspipe`), `DXB_RPI_CONFIG_TXT` (defined in `network.sh`; default it again here as `$(dxb_boot_dir)/config.txt`).

- [ ] **Step 1: Templates**

`provision/templates/gpsd-default.tmpl`:

```
# Generated by DXBerry-Pi from dxberry.txt (GPS_DEVICE, GPS_BAUD, GPS_PPS). Edit dxberry.txt and re-run dxberry-provision.
START_DAEMON="@START@"
USBAUTO="true"
DEVICES="@DEVICES@"
GPSD_OPTIONS="-n"
```

`provision/templates/chrony-dxberry.conf`:

```
# DXBerry-Pi: GPS time from gpsd via shared memory (device-name independent, survives USB hotplug).
refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2@NOSELECT@
refclock SHM 1 refid PPS precision 1e-7 prefer
```

- [ ] **Step 2: Failing tests**

`tests/test_gps.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"

gps_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GPSD_DEFAULT=$TEST_TMP/etc/default/gpsd \
    DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony/conf.d/dxberry.conf DXB_GPSPIPE=fake_gpspipe DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_RPI_CONFIG_TXT=$TEST_TMP/boot/config.txt
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/boot"; : > "$TEST_TMP/calls"; : > "$DXB_RPI_CONFIG_TXT"
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  timeout() { shift; "$@"; }
  fake_gpspipe() { cat "$TEST_TMP/gpspipe.out" 2> /dev/null; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
gps_cfg() { printf 'PASSWORD=examplepass\n%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }

test_gpsd_default_shapes() {
  gps_env
  gps_cfg ''; assert_eq "$(dxb_gps_gpsd_default | grep -E '^(START_DAEMON|DEVICES)=')" $'START_DAEMON="true"\nDEVICES=""'
  gps_cfg 'GPS_DEVICE=uart' 'GPS_PPS=18'; assert_eq "$(dxb_gps_gpsd_default | grep '^DEVICES=')" 'DEVICES="/dev/ttyAMA0 /dev/pps0"'
  gps_cfg 'GPS_DEVICE=none'; assert_eq "$(dxb_gps_gpsd_default | grep '^START_DAEMON=')" 'START_DAEMON="false"'
}

test_chrony_conf_noselect_only_with_pps() {
  gps_env
  gps_cfg ''; assert_eq "$(dxb_gps_chrony_conf | grep 'SHM 0')" "refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2"
  gps_cfg 'GPS_PPS=18'; assert_eq "$(dxb_gps_chrony_conf | grep 'SHM 0')" "refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2 noselect"
}

test_gps_configure_writes_enables_restarts_once() {
  gps_env; gps_cfg 'GPS_DEVICE=uart'
  assert_ok dxb_gps_configure
  assert_file_contains "$DXB_GPSD_DEFAULT" 'DEVICES="/dev/ttyAMA0"'
  assert_file_contains "$DXB_CHRONY_DROPIN" "refclock SHM 1"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable gpsd.socket"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart gpsd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart chrony"
  : > "$TEST_TMP/calls"
  assert_ok dxb_gps_configure
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
  gps_cfg 'GPS_DEVICE=none'
  assert_ok dxb_gps_configure
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now gpsd.socket gpsd"
  [[ -f $DXB_CHRONY_DROPIN ]] && _fail "chrony drop-in should be removed when GPS is off"
}

test_gps_boot_config_lines() {
  gps_env; gps_cfg 'GPS_DEVICE=uart' 'GPS_PPS=4'
  assert_ok dxb_gps_boot_config
  assert_eq "$(cat "$DXB_RPI_CONFIG_TXT")" $'enable_uart=1\ndtoverlay=disable-bt\ndtoverlay=pps-gpio,gpiopin=4'
  dxb_gps_boot_config; assert_eq "$?" "1"
}

test_maidenhead() {
  assert_eq "$(dxb_maidenhead 37.145833 -101.375)" "DM97hd"
  assert_eq "$(dxb_maidenhead 51.5 -0.1)" "IO91wm"
  assert_eq "$(dxb_maidenhead -33.8688 151.2093)" "QF56od"
}

test_gps_fix_parses_tpv_and_sky() {
  gps_env
  cat > "$TEST_TMP/gpspipe.out" <<'EOF'
{"class":"VERSION","release":"3.25"}
{"class":"TPV","mode":3,"time":"2026-09-09T02:00:00.000Z","lat":37.145833,"lon":-101.375,"altHAE":1000.0,"speed":2.0}
{"class":"SKY","nSat":12,"uSat":8}
EOF
  local j; j=$(dxb_gps_fix)
  assert_eq "$(jq -r '.fix' <<< "$j")" "3"
  assert_eq "$(jq -r '.grid' <<< "$j")" "DM97hd"
  assert_eq "$(jq -r '.alt_ft' <<< "$j")" "3281"
  assert_eq "$(jq -r '.speed_mph' <<< "$j")" "4.5"
  assert_eq "$(jq -r '.sats_used, .sats_seen' <<< "$j" | tr '\n' ' ')" "8 12 "
  rm -f "$TEST_TMP/gpspipe.out"
  assert_eq "$(dxb_gps_fix)" '{"fix":0}'
  assert_eq "$(dxb_gps_status_line)" "gps: no fix"
}
```

`tests/test_graywolf.sh` addition:

```bash
test_gw_seed_gps_uses_gpsd_when_enabled_once() {
  gw_env
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps {"enabled":true,"source_type":"gpsd","gpsd_host":"localhost","gpsd_port":2947}'
  : > "$TEST_TMP/calls"; GW_NEEDS_SETUP=false
  dxb_gw_seed 0 > /dev/null
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps'
  dxb_gw_seed 1 > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps'
}
test_gw_seed_gps_skipped_when_none() {
  gw_env
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\nGPS_DEVICE=none\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_not_contains "$(cat "$TEST_TMP/calls")" '/gps'
}
```

(`fake_curl` must answer `PUT /gps` with `{}`; add that case. `dxb_gw_seed` currently returns early with "not reseeding" when set up and `reseed=0`; the GPS seed sits after the login inside the reseed/setup path, which gives exactly the behavior asserted.)

- [ ] **Step 3: Run, expect failures.**

- [ ] **Step 4: Implement** `provision/lib/gps.sh`:

```bash
#!/bin/bash
# shellcheck shell=bash
# GPS receiver -> gpsd -> chrony (time) and Graywolf (position); fix readout (spec section 8).

: "${DXB_GPSD_DEFAULT:=/etc/default/gpsd}"
: "${DXB_CHRONY_DROPIN:=/etc/chrony/conf.d/dxberry.conf}"
: "${DXB_GPSPIPE:=gpspipe}"
: "${DXB_RPI_CONFIG_TXT:=$(dxb_boot_dir)/config.txt}"

dxb_gps_gpsd_default() {
  local start=true devices=''
  (( DXB_CFG[_GPS] )) || start=false
  devices=${DXB_CFG[_GPS_PATH]}
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || devices="${devices:+$devices }/dev/pps0"
  dxb_render "$DXB_TEMPLATES/gpsd-default.tmpl" "START=$start" "DEVICES=$devices"
}

dxb_gps_chrony_conf() {
  local ns=''
  [[ -z ${DXB_CFG[GPS_PPS]} ]] || ns=' noselect'
  dxb_render "$DXB_TEMPLATES/chrony-dxberry.conf" "NOSELECT=$ns"
}

# Writes gpsd and chrony configuration and (re)starts what changed. 0 ok, 1 a failed step was recorded.
dxb_gps_configure() {
  local content changed=0 rc=0
  mkdir -p "$(dirname "$DXB_GPSD_DEFAULT")" "$(dirname "$DXB_CHRONY_DROPIN")" 2> /dev/null
  content=$(dxb_gps_gpsd_default) || { dxb_step_failed gps "gpsd template missing"; return 1; }
  if dxb_write_if_changed "$DXB_GPSD_DEFAULT" "$content" 644; then changed=1; fi
  if (( DXB_CFG[_GPS] )); then
    content=$(dxb_gps_chrony_conf) || { dxb_step_failed gps "chrony template missing"; return 1; }
    if dxb_write_if_changed "$DXB_CHRONY_DROPIN" "$content" 644; then changed=1; fi
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

# dxb_gps_fix: one JSON object from gpsd, {"fix":0} when there is no daemon or no fix.
dxb_gps_fix() {
  local raw tpv sky
  raw=$(timeout 3 "$DXB_GPSPIPE" -w -n 20 2> /dev/null) || raw=''
  tpv=$(jq -cs 'map(select(.class == "TPV" and (.mode // 0) >= 2)) | last // empty' <<< "$raw" 2> /dev/null)
  [[ -n $tpv ]] || { echo '{"fix":0}'; return 0; }
  sky=$(jq -cs 'map(select(.class == "SKY")) | last // {}' <<< "$raw" 2> /dev/null)
  jq -cn --argjson t "$tpv" --argjson s "$sky" --arg grid "$(dxb_maidenhead "$(jq -r .lat <<< "$tpv")" "$(jq -r .lon <<< "$tpv")")" '
    {fix: $t.mode, lat: $t.lat, lon: $t.lon,
     alt_ft: (((($t.altHAE // $t.altMSL // $t.alt // 0) * 3.28084) + 0.5) | floor),
     speed_mph: ((($t.speed // 0) * 2.23694 * 10 + 0.5) | floor / 10),
     sats_used: ($s.uSat // 0), sats_seen: ($s.nSat // 0), time: ($t.time // ""), grid: $grid}'
}

dxb_gps_status_line() {
  local j; j=$(dxb_gps_fix)
  if [[ $(jq -r .fix <<< "$j") == 0 ]]; then echo "gps: no fix"
  else jq -r '"gps: " + (.fix|tostring) + "D fix " + .grid + " (" + (.lat|tostring) + ", " + (.lon|tostring) + "), " + (.sats_used|tostring) + "/" + (.sats_seen|tostring) + " satellites"' <<< "$j"; fi
}
```

`graywolf.sh`:

```bash
dxb_gw_payload_gps() { jq -cn '{enabled: true, source_type: "gpsd", gpsd_host: "localhost", gpsd_port: 2947}'; }
# Seeds Graywolf's position source from gpsd when a GPS is configured. Once per box; --reseed repeats it.
dxb_gw_seed_gps() {
  (( DXB_CFG[_GPS] )) || return 0
  dxb_gw_api PUT /gps "$(dxb_gw_payload_gps)" > /dev/null || { dxb_step_failed graywolf "GPS source update failed"; return 1; }
  dxb_status_add "graywolf: position from gpsd (GPS_DEVICE=${DXB_CFG[GPS_DEVICE]})"
}
```

Call `dxb_gw_seed_gps` in `dxb_gw_seed` right after `dxb_gw_login || return 1` (before the CALLSIGN block) so it runs with or without a callsign. When `_GPS` is 1 and the fixed-position beacon is also configured, Graywolf's GPS source wins (spec §8.2); leave `dxb_gw_seed_beacon` as it is.

- [ ] **Step 5: Run tests, shellcheck, check.** Expected: all ok.

- [ ] **Step 6: Commit**

```bash
git add provision/lib/gps.sh provision/templates/chrony-dxberry.conf provision/templates/gpsd-default.tmpl provision/lib/graywolf.sh tests/test_gps.sh tests/test_graywolf.sh
git commit -m "Feed GPS time to chrony and position to Graywolf through gpsd, with a fix readout"
```

---

### Task 11: The `dxberry-radio` command

**Files:**
- Create: `provision/bin/dxberry-radio` (executable)
- Test: `tests/test_dxberry_radio.sh`

**Interfaces:**
- Consumes: everything above. Sources `common config graywolf radio radio_udev rigctld gps` from `DXB_LIB`; loads `dxberry.txt` through `dxb_config_load`/`dxb_config_validate` when present (needed for GPS and Graywolf credentials fallback), silently tolerating its absence.
- Produces: subcommands exactly as §6.2; `--json` on every subcommand; exit codes per Global Constraints. `main "$@"` returns the exit code; the file ends with `if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; exit $?; fi` so tests can source it.

- [ ] **Step 1: Failing tests**

`tests/test_dxberry_radio.sh` (each test runs `main` in a subshell so the sourced `main`/`usage` never leak into the shared test process; stubs are exported functions):

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

cli_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_LIB=$DXB_ROOT/provision/lib \
    DXB_TEMPLATES=$DXB_ROOT/provision/templates DXB_SHARE=$DXB_ROOT/provision/share DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules \
    DXB_MODPROBE_FILE=$TEST_TMP/etc/audio.conf DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_APPS_DIR=$TEST_TMP/apps \
    DXB_BOOT_DIR=$TEST_TMP/boot DXB_GPSD_DEFAULT=$TEST_TMP/etc/gpsd DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony.conf DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc" "$DXB_APPS_DIR" "$DXB_BOOT_DIR"; : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  cat > "$DXB_APPS_DIR/alpha.sh" <<'EOF'
app_alpha_unit() { echo alpha.service; }
app_alpha_wire() { echo "alpha wire $1" >> "$TEST_TMP/calls"; }
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/calls"; }
app_alpha_needs_service_restart() { echo no; }
EOF
  fx_scene "$DXB_SYSFS_ROOT" digirig
}
# cli ARGS...: run the command in a subshell with stubs; stdout to $TEST_TMP/out, exit code returned.
cli() {
  (
    source "$DXB_ROOT/provision/bin/dxberry-radio"     # first: the libraries define the real dxb_require_root
    dxb_require_root() { :; }
    systemctl() { fx_systemctl "$@"; }
    udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
    systemd-tmpfiles() { :; }
    rigctl() { printf '145390000\nFM\n'; }
    gpspipe() { :; }
    timeout() { shift; "$@"; }
    main "$@"
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
}
out() { cat "$TEST_TMP/out"; }

test_cli_scan_table_and_json() {
  cli_env
  assert_ok cli scan
  assert_contains "$(out)" "DigiRig Mobile"
  assert_contains "$(out)" "usb-0:1.3"
  assert_ok cli scan --json
  assert_eq "$(jq -r '.[0].functions[0].kernel' "$TEST_TMP/out")" "card1"
}

test_cli_add_applies_and_status_shows_radio() {
  cli_env
  assert_ok cli add radio1 --audio 1 --cat 2 --label "TM-V71"
  assert_file_contains "$DXB_UDEV_RULES_FILE" 'ATTR{id}="RADIO1"'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
  assert_ok cli status --json
  assert_eq "$(jq -r '.radios.radio1.present, .radios.radio1.rigctld, .radios.radio1.freq, .radios.radio1.mode' "$TEST_TMP/out" | tr '\n' ' ')" "true active 145390000 FM "
  assert_ok cli status
  assert_contains "$(out)" "radio1"
  assert_contains "$(out)" "145390000"
}

test_cli_usage_and_error_codes() {
  cli_env
  cli; assert_eq "$?" "2"
  cli frobnicate; assert_eq "$?" "2"
  cli add; assert_eq "$?" "2"
  cli add radio1 --audio 9; assert_eq "$?" "2"
  cli set nope --label x; assert_eq "$?" "3"
  cli claim nope alpha; assert_eq "$?" "3"
  cli add radio1 --audio 1 --cat 2 > /dev/null
  cli claim radio1 nosuchapp; assert_eq "$?" "3"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none
  cli claim radio1 alpha; assert_eq "$?" "4"
}

test_cli_claim_release_remove() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli claim radio1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "alpha wire radio1"
  assert_ok cli status --json; assert_eq "$(jq -r '.radios.radio1.owner' "$TEST_TMP/out")" "alpha"
  assert_ok cli release radio1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_ok cli remove radio1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_ok cli status --json; assert_eq "$(jq -c '.radios' "$TEST_TMP/out")" "{}"
}

test_cli_set_and_hotplug() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli set radio1 --ptt cm108 --wiring names
  assert_eq "$(jq -r '.radios.radio1.ptt.method + " " + .radios.radio1.wiring' "$DXB_STATE_DIR/radios.json")" "cm108 names"
  cli set radio1 --wiring sideways; assert_eq "$?" "2"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; : > "$TEST_TMP/calls"
  assert_ok cli hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"
}

test_cli_gps_without_daemon() {
  cli_env
  assert_ok cli gps
  assert_eq "$(out)" "no fix"
  assert_ok cli gps --json
  assert_eq "$(out)" '{"fix":0}'
}
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement** `provision/bin/dxberry-radio` (`chmod +x`):

```bash
#!/bin/bash
# dxberry-radio: discover, pin, and hand radios between applications. Spec: docs/design/2026-09-09-radio-plumbing.md
#   scan                       list USB candidates (audio / serial / HID functions grouped by port)
#   add NAME [pins] [opts]     pin a radio: --audio N --cat N[:K] [--hid N] [--ptt-serial N[:K]]
#   set NAME [pins] [opts]     change a radio ( --audio none clears a pin )
#   remove NAME                forget a radio
#   apply                      regenerate udev rules, rigctld instances and runtime state
#   claim NAME APP             give the radio to an application (stops the previous owner's use)
#   release NAME               take the radio away from its owner
#   status [NAME]              record + live state; frequency and mode from rigctld
#   hotplug                    udev entry point (apply without touching udev)
#   gps                        GPS fix, satellites and grid square
# Options: --ptt M --ptt-type T --model K --baud B --wiring full|names --label S --gpio-line N; --json on any subcommand.
set -uo pipefail

DXB_LIB=${DXB_LIB:-/opt/dxberry/lib}
for _m in common config graywolf radio radio_udev rigctld gps; do
  # shellcheck disable=SC1090
  source "$DXB_LIB/$_m.sh"
done

usage() { sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; }
JSON=0

# Loads dxberry.txt when present (GPS keys, Graywolf credentials fallback); absence is fine.
load_config_if_any() {
  local f; f="$(dxb_boot_dir)/dxberry.txt"
  [[ -f $f ]] || { DXB_CFG[_GPS]=1; DXB_CFG[GPS_DEVICE]=auto; DXB_CFG[GPS_PPS]=''; DXB_CFG[_GPS_PATH]=''; DXB_CFG[GPS_BAUD]=9600; return 0; }
  dxb_config_load "$f"; dxb_config_validate > /dev/null 2>&1 || true
}

# parse_opts ARGS... -> prints an OPTS JSON object; returns 2 on a bad flag.
parse_opts() {
  local o='{}' k v
  while (( $# )); do
    case $1 in
      --audio|--cat|--hid|--ptt-serial|--ptt|--ptt-type|--wiring|--label)
        [[ $# -ge 2 ]] || { echo "$1 needs a value" >&2; return 2; }
        k=${1#--}; k=${k//-/_}; v=$2; shift
        o=$(jq -c --arg k "$k" --arg v "$v" '.[$k] = $v' <<< "$o") ;;
      --model|--baud|--gpio-line)
        [[ $# -ge 2 && $2 =~ ^[0-9]+$ ]] || { echo "$1 needs a whole number" >&2; return 2; }
        k=${1#--}; k=${k//-/_}
        o=$(jq -c --arg k "$k" --argjson v "$2" '.[$k] = $v' <<< "$o"); shift ;;
      *) echo "unknown option: $1" >&2; return 2 ;;
    esac
    shift
  done
  printf '%s\n' "$o"
}

cmd_scan() {
  dxb_radio_scan_cache
  if (( JSON )); then printf '%s\n' "$DXB_RADIO_SCAN"; return 0; fi
  [[ $DXB_RADIO_SCAN == '[]' ]] && { echo "no USB audio, serial or HID devices found"; return 0; }
  jq -r '.[] | "\(.index)  \(.port)  \(.name) [\(.profile)]", (.functions[] | "     \(.kind): \(.kernel) \(.path) \(.vidpid)\(if .serial != "" then " serial " + .serial else "" end) \(.product)")' <<< "$DXB_RADIO_SCAN"
}

cmd_status() {
  local only=${1:-} out n r st
  dxb_radio_load || return 6
  dxb_radio_scan_cache
  dxb_radio_write_state > /dev/null || return 6
  out='{}'
  for n in $(dxb_radio_names); do
    [[ -z $only || $only == "$n" ]] || continue
    r=$(dxb_radio_get "$n"); st=$(jq -c --arg n "$n" '.radios[$n]' "$DXB_RADIOS_STATE")
    if [[ $(jq -r .rigctld <<< "$st") == active ]]; then read -r f m < <(dxb_rigctld_query "$(jq -r .rigctld_port <<< "$r")"); else f='?'; m='?'; fi
    out=$(jq -c --arg n "$n" --argjson r "$r" --argjson s "$st" --arg f "$f" --arg m "$m" '.[$n] = ($r + $s + {freq: $f, mode: $m})' <<< "$out")
  done
  [[ -n $only ]] && [[ $(jq 'length' <<< "$out") == 0 ]] && { echo "no such radio: $only" >&2; return 3; }
  if (( JSON )); then jq -c --argjson r "$out" '{radios: $r, gps: '"$(dxb_gps_fix)"'}' <<< '{}'; return 0; fi
  [[ $out == '{}' ]] && { echo "no radios pinned; run: dxberry-radio scan"; }
  jq -r 'to_entries[] | "\(.key)  \(.value.label)  \(if .value.present then "present" else "ABSENT" end)  rigctld:\(.value.rigctld) port \(.value.rigctld_port)  owner:\(if .value.owner == "" then "-" else .value.owner end)  \(.value.freq) \(.value.mode)  ptt:\(.value.ptt.method)"' <<< "$out"
  dxb_gps_status_line
}

cmd_gps() { if (( JSON )); then dxb_gps_fix; else local j; j=$(dxb_gps_fix); if [[ $(jq -r .fix <<< "$j") == 0 ]]; then echo "no fix"; else dxb_gps_status_line | sed 's/^gps: //'; fi; fi; }

main() {
  local cmd='' args=() a rc opts
  for a in "$@"; do case $a in --json) JSON=1 ;; *) args+=("$a") ;; esac; done
  cmd=${args[0]:-}; args=("${args[@]:1}")
  case $cmd in
    -h|--help) usage; return 0 ;;
    scan|add|set|remove|apply|claim|release|status|hotplug|gps) ;;
    '') usage >&2; return 2 ;;
    *) echo "unknown subcommand: $cmd" >&2; usage >&2; return 2 ;;
  esac
  dxb_require_root
  mkdir -p "$DXB_STATE_DIR" 2> /dev/null; chmod 700 "$DXB_STATE_DIR" 2> /dev/null
  DXB_LOG_FILE=${DXB_RADIO_LOG:-$DXB_STATE_DIR/radio.log}; : >> "$DXB_LOG_FILE" 2> /dev/null; chmod 600 "$DXB_LOG_FILE" 2> /dev/null
  load_config_if_any
  case $cmd in
    scan) cmd_scan ;;
    add|set)
      [[ ${#args[@]} -ge 1 ]] || { echo "usage: dxberry-radio $cmd NAME [options]" >&2; return 2; }
      opts=$(parse_opts "${args[@]:1}") || return 2
      dxb_radio_load || return 6; dxb_radio_scan_cache
      "dxb_radio_$cmd" "${args[0]}" "$opts" || return $?
      dxb_radio_apply; rc=$?
      (( JSON )) && cmd_status "${args[0]}"
      return $rc ;;
    remove)
      [[ ${#args[@]} -eq 1 ]] || { echo "usage: dxberry-radio remove NAME" >&2; return 2; }
      dxb_radio_load || return 6; dxb_radio_scan_cache
      dxb_radio_release "${args[0]}" || return $?
      dxb_radio_remove "${args[0]}" || return $?
      dxb_radio_apply ;;
    apply) dxb_radio_apply ;;
    hotplug) dxb_radio_apply hotplug ;;
    claim)
      [[ ${#args[@]} -eq 2 ]] || { echo "usage: dxberry-radio claim NAME APP" >&2; return 2; }
      dxb_radio_load || return 6; dxb_radio_scan_cache
      dxb_radio_claim "${args[0]}" "${args[1]}" || return $?
      (( JSON )) && cmd_status "${args[0]}"; return 0 ;;
    release)
      [[ ${#args[@]} -eq 1 ]] || { echo "usage: dxberry-radio release NAME" >&2; return 2; }
      dxb_radio_load || return 6; dxb_radio_scan_cache
      dxb_radio_release "${args[0]}" ;;
    status) cmd_status "${args[0]:-}" ;;
    gps) cmd_gps ;;
  esac
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; exit $?; fi
```

`cmd_status`'s `--json` branch splices `dxb_gps_fix` output into a jq program string; if shellcheck or the implementer prefers, build it with `--argjson g "$(dxb_gps_fix)"` instead. Either way the output is `{"radios":{…},"gps":{…}}`.

- [ ] **Step 4: Run tests, shellcheck, check.** Expected: all ok (`--check` still passes because Task 12 adds the file to `REQUIRED_FILES`).

- [ ] **Step 5: Commit**

```bash
git add provision/bin/dxberry-radio tests/test_dxberry_radio.sh
git commit -m "Add the dxberry-radio command"
```

---

### Task 12: Provisioning integration, image build, documentation

**Files:**
- Modify: `provision/lib/radio.sh` (+ `provision_radio`)
- Modify: `provision/bin/dxberry-provision` (source the new modules; call `provision_radio`)
- Modify: `build/build-image.sh` (`REQUIRED_FILES`, `EXECUTABLE_FILES`)
- Modify: `boot/dietpi.overrides.txt` (`AUTO_SETUP_APT_INSTALLS`)
- Modify: `boot/README-DXBERRY.txt`, `README.md` (radio section), `provision/VERSION` → `0.2.0`
- Modify: `docs/design/2026-09-09-radio-plumbing.md` (§5.1 DigiRig-as-two-candidates correction; drop the `ID_PATH` prefix sentence in §7.1; §12 item 6 rewritten to "verify the `*-usb-…` wildcard matches on the Pi 4"), `docs/design/2026-09-07-base-image.md` (§5 STATIC_IP note, §15 reserved keys)
- Modify: `.github/workflows/*.yml` (shellcheck list gains `provision/lib/apps/*.sh`)
- Test: `tests/test_provision.sh`, `tests/test_build.sh`

**Interfaces:**
- Produces: `provision_radio` (spec §10): installs packages (`libhamlib-utils gpsd gpsd-clients chrony alsa-utils`), units, modprobe file, gpsd/chrony config, disables/masks `systemd-timesyncd`, enables `chrony`, runs `dxb_radio_apply`, adds status lines `radio: N radios, M present`, `time: chrony (gps …)`, and the gps line. Sets `DXB_RADIO_REBOOT_NEEDED=1` when `dxb_gps_boot_config` changed config.txt in run mode (driver warns like it does for wlan0).

- [ ] **Step 1: Failing tests**

`tests/test_provision.sh` (use the file's `full_env`-style helper; add stubs `apt-get`, `udevadm`, `systemd-tmpfiles`, `rigctl`, `gpspipe`, `timeout`, and the `DXB_*` overrides from the CLI test):

```bash
test_provision_radio_installs_and_reports() {
  full_env; provision_cfg 'PASSWORD=examplepass' 'GPS_DEVICE=uart'      # the file's config helper
  fx_scene "$DXB_SYSFS_ROOT" none
  provision_radio
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y libhamlib-utils gpsd gpsd-clients chrony alsa-utils"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now systemd-timesyncd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl mask systemd-timesyncd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable chrony"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  assert_file_contains "$DXB_UDEV_RULES_FILE" "IMPORT{builtin}=\"path_id\""
  assert_file_contains "$DXB_MODPROBE_FILE" "snd_usb_audio"
  assert_file_contains "$DXB_GPSD_DEFAULT" 'DEVICES="/dev/ttyAMA0"'
  assert_contains "${DXB_STATUS_LINES[*]}" "radio: 0 radios, 0 present"
  assert_contains "${DXB_STATUS_LINES[*]}" "time: chrony"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  assert_eq "$DXB_RADIO_REBOOT_NEEDED" "1"          # uart lines were added to config.txt in run mode
}

test_provision_radio_apt_failure_is_a_failed_step_not_fatal() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; return 100; }
  provision_radio
  assert_contains "${DXB_FAILED_STEPS[*]}" "radio: could not install"
  assert_file_contains "$DXB_MODPROBE_FILE" "snd_usb_audio"      # the rest still ran
}

test_provision_driver_runs_radio_step_after_graywolf() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  # (call main as the existing driver tests do; then:)
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  local gw radio; gw=$(grep -n 'enable --now graywolf' "$TEST_TMP/calls" | head -1 | cut -d: -f1); radio=$(grep -n 'dxberry-radio-hotplug' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  (( gw < radio )) || _fail "radio step must run after graywolf"
}
```

`tests/test_build.sh`: extend the existing required-files test (or add one) asserting `build/build-image.sh --check` still prints `tree ok` and that `REQUIRED_FILES` names `provision/bin/dxberry-radio`, `provision/lib/radio.sh`, `provision/lib/apps/graywolf.sh`, `provision/share/radio-profiles.tsv`, and every new template.

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Implement**

`radio.sh`:

```bash
# ---- provisioning step ---------------------------------------------------------------------
DXB_RADIO_PACKAGES='libhamlib-utils gpsd gpsd-clients chrony alsa-utils'
DXB_RADIO_REBOOT_NEEDED=0

provision_radio() {
  local n present=0 total=0 gps_state
  # shellcheck disable=SC2086
  if ! DEBIAN_FRONTEND=noninteractive apt-get install -y $DXB_RADIO_PACKAGES > /dev/null 2>&1; then
    dxb_step_failed radio "could not install $DXB_RADIO_PACKAGES (no network?); rigctld and gpsd will be missing until a re-run"
  fi
  dxb_rigctld_install_units; (( $? == 6 )) && dxb_step_failed radio "could not install the rigctld or hotplug units"
  dxb_radio_modprobe_install > /dev/null; (( $? == 6 )) && dxb_step_failed radio "could not write $DXB_MODPROBE_FILE"
  systemctl disable --now systemd-timesyncd > /dev/null 2>&1 || true
  systemctl mask systemd-timesyncd > /dev/null 2>&1 || true
  systemctl enable chrony > /dev/null 2>&1 || dxb_step_failed radio "could not enable chrony"
  dxb_gps_configure
  if dxb_gps_boot_config; then
    if [[ ${DXB_MODE:-run} == run ]]; then DXB_RADIO_REBOOT_NEEDED=1; dxb_status_add "gps: reboot required (config.txt changed for GPS_DEVICE=${DXB_CFG[GPS_DEVICE]}${DXB_CFG[GPS_PPS]:+, PPS})"; fi
  fi
  dxb_radio_apply; case $? in 6) dxb_step_failed radio "apply failed; see $DXB_LOG_FILE" ;; 7) dxb_step_failed radio "an owner could not be re-wired; see $DXB_LOG_FILE" ;; esac
  for n in $(dxb_radio_names); do total=$(( total + 1 )); dxb_radio_present "$n" && present=$(( present + 1 )); done
  dxb_status_add "radio: $total radios, $present present (manage with: sudo dxberry-radio scan)"
  if (( DXB_CFG[_GPS] )); then gps_state=$(dxb_gps_status_line); else gps_state='gps: off (GPS_DEVICE=none)'; fi
  dxb_status_add "$gps_state"
  dxb_status_add "time: chrony (gps $( [[ $gps_state == *fix* && $gps_state != *"no fix"* ]] && echo present || echo absent ))"
  return 0
}
```

`dxberry-provision`: extend the module list to `common config system network storage graywolf radio radio_udev rigctld gps scrub`; after the Graywolf `if … fi` block and before `provision_scrub`, add `provision_radio`; extend the run-mode reboot warning: `if [[ $DXB_MODE == run ]] && (( DXB_NET_REBOOT_NEEDED || DXB_RADIO_REBOOT_NEEDED )); then dxb_warn "reboot required; run: sudo reboot"; fi` (keep the wlan0 wording when only the network flag is set).

`build/build-image.sh`: add to `REQUIRED_FILES`: `provision/bin/dxberry-radio provision/lib/radio.sh provision/lib/radio_udev.sh provision/lib/rigctld.sh provision/lib/gps.sh provision/lib/apps/graywolf.sh provision/share/radio-profiles.tsv provision/templates/rigctld@.service provision/templates/dxberry-radio-hotplug.service provision/templates/dxberry-radio.tmpfiles provision/templates/70-dxberry-radio.rules.head provision/templates/dxberry-audio.conf provision/templates/chrony-dxberry.conf provision/templates/gpsd-default.tmpl`; add `provision/bin/dxberry-radio` to `EXECUTABLE_FILES`; extend the `bash -n` loop glob with `"$ROOT"/provision/lib/apps/*.sh`. The existing `cp -r "$ROOT/provision/."` already ships `share/` and `apps/`.

`boot/dietpi.overrides.txt`: `AUTO_SETUP_APT_INSTALLS=curl ca-certificates jq wpasupplicant libhamlib-utils gpsd gpsd-clients chrony alsa-utils`.

`provision/VERSION`: `0.2.0`.

`boot/README-DXBERRY.txt`: add a short "Radios" paragraph: plug in, `sudo dxberry-radio scan`, `add`, `claim radio1 graywolf`; names `hw:RADIO1` and `/dev/dxberry/radio1-cat`; `status`. `README.md`: a "Radio plumbing" section with the same commands, the ownership model in three sentences, GPS keys, and a link to the spec.

Spec amendments (edit the design docs in place, no placeholders):
- radio spec §5.1: DigiRig Mobile appears as two candidates (codec and CP2102 are sibling devices behind its internal hub); the operator pins `--audio N --cat M`. §5.2 profile row for the DigiRig sets `cat` to `separate`.
- radio spec §7.1: remove the sentence about recording the `ID_PATH` prefix; keep the wildcard rationale.
- radio spec §12: add item "DigiRig internal-hub topology as seen on the Pi 4" to the hardware list.
- base spec §5: `STATIC_IP` row: "CIDR or a bare address (bare = /24)". §15: `GPS_` is no longer reserved (implemented in sub-project 2).

CI: both workflows' shellcheck lines add `provision/lib/apps/*.sh`.

- [ ] **Step 4: Run tests, shellcheck, check.** Expected: all ok, `tree ok`.

- [ ] **Step 5: Commit**

```bash
git add -A provision build boot README.md docs tests .github
git commit -m "Provision radio plumbing on first boot and ship it in the image"
```

---

### Task 13: Hardware acceptance on the Pi 4 (operator present)

**Files:**
- Create: `.superpowers/sdd/2026-09-09-radio-plumbing/acceptance.md` (gitignored ledger entry; results only, never secrets)

This task is run by the controller with the operator, not by an implementer subagent. Nothing here changes the repository except fixes found, which go through their own brief/review cycle.

- [ ] **Step 1: Deploy the tree** to the Pi at 10.0.0.90 the way rc3 was deployed (`git archive HEAD provision | ssh … tar -x -C /opt/dxberry`), then `sudo dxberry-provision` (run mode). Confirm: packages present (`rigctld --version`, `gpsd -V`, `chronyc tracking`), `systemd-timesyncd` masked, `dxberry-radio-hotplug.service` enabled, `/run/dxberry/rigctld` exists, status file lists the radio/gps/time lines.
- [ ] **Step 2: Spec §12 items 5–6** with whatever interface the operator plugs in: `dxberry-radio scan` shows it with the expected functions; after `add`, `udevadm info -q property /dev/snd/controlC*` shows `ID_PATH` matching the rule, `cat /sys/class/sound/card*/id` shows `RADIO1` after replug, `/dev/dxberry/radio1-cat` exists, `aplay -l` lists USB first.
- [ ] **Step 3: Item 7** `rigctl -m 2 -r 127.0.0.1:4532 T 1` keys the radio (watch the TX LED), `T 0` unkeys.
- [ ] **Step 4: Items 8–9** `claim radio1 graywolf`; Graywolf's web UI shows device `radio1` on `plughw:CARD=RADIO1,DEV=0`, channel `radio1`, PTT rigctld `127.0.0.1:4532`; packets decode; `POST /ptt/test-rigctld` ok; `release radio1` then `fuser /dev/snd/*` shows nothing from graywolf.
- [ ] **Step 5: Item 10** with a USB GPS: `gpspipe -w -n 5` streams, `chronyc sources` lists `GPS`, `dxberry-radio gps` prints the grid, Graywolf's GPS page shows gpsd with a fix.
- [ ] **Step 6: Item 11** unplug/replug: `rigctld@radio1` stops and starts (journal), names return, `status` follows within a few seconds.
- [ ] **Step 7: Record** every result in `acceptance.md`; each defect becomes a fix brief (same review loop as the base image); when all pass, tag `v0.2.0-rc1` (pre-release; the release workflow builds the image).

---

### Task 14: Release v0.2.0

- [ ] **Step 1:** Operator flashes `v0.2.0-rcN`, first boot passes the base-image acceptance harness plus Task 13's checks on a fresh image.
- [ ] **Step 2:** Update `dxberry-pi.md` memory and the SDD ledger; delete the SDD workspace at the end per finishing-a-development-branch.
- [ ] **Step 3:** With the operator's explicit approval, tag `v0.2.0` (no hyphen → full release) and confirm the release run succeeds.
