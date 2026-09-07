# DXBerry-Pi Base Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A flashable image (official DietPi + injected first-boot provisioning) that boots a Raspberry Pi 4 to a static address with Ethernet-primary/WiFi-failover networking and a seeded Graywolf APRS station, configured from one file on the boot partition.

**Architecture:** `build/build-image.sh` loop-mounts the stock DietPi image and injects `/opt/dxberry` (bash provisioner) plus DietPi automation hooks. On first boot, `dxberry-preboot` turns `dxberry.txt` into DietPi settings before the network comes up; after DietPi's automated install, `dxberry-provision --first-boot` applies network (ifupdown files + the `dxberry-netwatch` service), storage tweaks, installs and seeds Graywolf via its REST API, scrubs secrets and reboots. Every step is idempotent and re-runnable over SSH.

**Tech Stack:** bash 5, ifupdown + wpa_supplicant + iproute2 (DietPi defaults), systemd, curl + jq, Graywolf REST API, losetup/xz for the build, GitHub Actions.

**Spec:** `docs/design/2026-09-07-base-image.md` (read it first; section numbers below refer to it).

## Global Constraints

- License: GPL-2.0-or-later. No author/attribution lines of any kind in files or commit messages.
- Never commit a real `dxberry.txt`, `dietpi-wifi.txt`, or built image (`.gitignore` already covers them).
- All scripts: `#!/bin/bash` (or `#!/usr/bin/env bash` for host tools), `set -uo pipefail` (no `set -e` in the provisioner — every step decides its own failure), shellcheck-clean (`shellcheck -x`).
- Paths on the Pi: provisioner at `/opt/dxberry/{bin,lib,templates}`, state at `/var/lib/dxberry/` (0700), user files on the FAT partition found by `dxb_boot_dir` (`/boot/firmware` on RPi), DietPi's own files at `/boot/dietpi.txt`, `/boot/dietpi-wifi.txt`, `/boot/dietpi/func/…`.
- Graywolf is always the latest upstream release unless `GRAYWOLF_VERSION` pins a tag; downloads are verified against upstream `checksums.txt`. API base `http://127.0.0.1:8080/api`, cookie `graywolf_session`.
- Exactly one of eth0/wlan0 carries an address at any time; only `dxberry-netwatch` runs `ifup`/`ifdown`.
- Every library function is prefixed `dxb_`; provisioning entry points are `provision_<concern>`; derived config keys start with `_`.
- Secrets in `dxberry.txt` (`PASSWORD`, `WIFI_PASSWORD`, `WEBUI_PASSWORD`) become `<applied>` after use; a value of `<applied>` means "skip".
- Tests: `tests/run.sh` (bash + awk + jq only). Every task ends with `tests/run.sh` passing and a commit.
- Commit messages: imperative, no trailers.

## File Structure

| File | Responsibility |
|---|---|
| `tests/run.sh`, `tests/lib.sh` | dependency-free test runner and assert helpers |
| `tests/test_*.sh` | one file per library/binary under test |
| `provision/lib/config.sh` | parse/validate `dxberry.txt`, defaults, derived values (pure; no side effects) |
| `provision/lib/common.sh` | logging, status file, `dxb_set_kv`, `dxb_render`, `dxb_write_if_changed`, `dxb_boot_dir` |
| `provision/lib/network.sh` | interfaces files, resolv.conf, WiFi import, netwatch unit install |
| `provision/lib/system.sh` | hostname, timezone, password, serial console, SSH key |
| `provision/lib/storage.sh` | journald drop-in, zram check |
| `provision/lib/graywolf.sh` | download/verify/install Graywolf, API client, seeding |
| `provision/lib/scrub.sh` | replace secrets with `<applied>` |
| `provision/bin/dxberry-preboot` | first-boot pre-network hook |
| `provision/bin/dxberry-provision` | driver (`--first-boot`, `--reseed`, `--check`) |
| `provision/bin/dxberry-netwatch` | failover daemon (`run`, `--simulate`, `--status`) |
| `provision/templates/*` | interfaces stanzas, journald drop-in, netwatch unit |
| `provision/VERSION` | provisioner version string |
| `boot/*` | DietPi overrides, hooks, `dxberry.txt.example`, `README-DXBERRY.txt` |
| `build/build-image.sh` | image build (`--check` without root) |
| `.github/workflows/{ci,release}.yml` | CI and release |
| `README.md` | user documentation |

---

### Task 1: Test harness and `dxberry.txt` parser

**Files:**
- Create: `tests/lib.sh`, `tests/run.sh`, `tests/test_config.sh`
- Create: `provision/lib/config.sh` (load only; validation is Task 2)

**Interfaces:**
- Produces: `dxb_config_load FILE` → fills `DXB_CFG` (assoc), `DXB_CFG_LINES` (assoc key→line), `DXB_CFG_ERRORS` (array), `DXB_CFG_WARNINGS` (array); returns 1 only if the file is unreadable. Constants `DXB_KNOWN_KEYS`, `DXB_SECRET_KEYS`, `DXB_RESERVED_PREFIXES`, `DXB_APPLIED='<applied>'`.
- Test helpers: `assert_eq GOT EXPECTED`, `assert_contains HAYSTACK NEEDLE`, `assert_not_contains`, `assert_ok CMD…`, `assert_fails CMD…`, `assert_file_contains FILE TEXT`, `assert_file_not_contains FILE TEXT`; `$TEST_TMP` is a fresh temp dir per test; `$DXB_LIB` and `$DXB_TEMPLATES` point into the repo.

- [ ] **Step 1: Write the harness**

`tests/lib.sh`:

```bash
#!/usr/bin/env bash
# Assertion helpers for tests/run.sh. Sourced, never executed.
TESTS_RUN=0
TESTS_FAILED=0

_fail() {
  TESTS_FAILED=$((TESTS_FAILED + 1))
  printf '    FAIL in %s: %s\n' "${FUNCNAME[2]:-?}" "$1" >&2
}
assert_eq() { [[ "$1" == "$2" ]] || _fail "expected '$2', got '$1'"; }
assert_contains() { [[ "$1" == *"$2"* ]] || _fail "expected to find '$2' in: $1"; }
assert_not_contains() { [[ "$1" != *"$2"* ]] || _fail "did not expect '$2' in: $1"; }
assert_ok() { "$@" || _fail "expected success: $*"; }
assert_fails() { if "$@"; then _fail "expected failure: $*"; fi; }
assert_file_contains() { grep -qF -- "$2" "$1" || _fail "expected $1 to contain '$2'"; }
assert_file_not_contains() { if grep -qF -- "$2" "$1"; then _fail "did not expect $1 to contain '$2'"; fi; }
```

`tests/run.sh`:

```bash
#!/usr/bin/env bash
# Runs every test_* function defined in tests/test_*.sh. Needs only bash 5, awk and jq.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
export DXB_ROOT=$PWD
export DXB_LIB=$DXB_ROOT/provision/lib
export DXB_TEMPLATES=$DXB_ROOT/provision/templates
# shellcheck source=tests/lib.sh
source "$DXB_ROOT/tests/lib.sh"
for f in "$DXB_ROOT"/tests/test_*.sh; do
  # shellcheck disable=SC1090
  source "$f"
done
for t in $(declare -F | awk '{print $3}' | grep '^test_' | sort); do
  TESTS_RUN=$((TESTS_RUN + 1))
  TEST_TMP=$(mktemp -d)
  export TEST_TMP
  before=$TESTS_FAILED
  "$t"
  rm -rf "$TEST_TMP"
  if (( TESTS_FAILED == before )); then echo "ok   $t"; else echo "FAIL $t"; fi
done
echo "$TESTS_RUN tests, $TESTS_FAILED failures"
(( TESTS_FAILED == 0 ))
```

Run: `chmod +x tests/run.sh && tests/run.sh`
Expected: `0 tests, 0 failures`

- [ ] **Step 2: Write the failing parser tests**

`tests/test_config.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/config.sh"

write_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; }

test_config_load_parses_keys_quotes_crlf_and_comments() {
  printf 'HOSTNAME=pi-one\r\n# comment\r\n\r\n  PASSWORD = "spaces ok"  \r\nCALLSIGN=N0CALL-2\n' > "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${DXB_CFG[HOSTNAME]}" "pi-one"
  assert_eq "${DXB_CFG[PASSWORD]}" "spaces ok"
  assert_eq "${DXB_CFG[CALLSIGN]}" "N0CALL-2"
  assert_eq "${DXB_CFG_LINES[CALLSIGN]}" "5"
  assert_eq "${#DXB_CFG_ERRORS[@]}" "0"
}

test_config_load_warns_on_unknown_key_and_ignores_reserved_prefix() {
  write_cfg 'PASSWORD=secretpass' 'FOO=bar' 'PAT_MYCALL=X'
  dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${#DXB_CFG_WARNINGS[@]}" "1"
  assert_contains "${DXB_CFG_WARNINGS[0]}" "line 2: unknown key 'FOO'"
  assert_eq "${DXB_CFG[PAT_MYCALL]:-unset}" "unset"
}

test_config_load_reports_malformed_lines() {
  write_cfg 'PASSWORD=secretpass' 'this is not a key' 'lower=case'
  dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${#DXB_CFG_ERRORS[@]}" "2"
  assert_contains "${DXB_CFG_ERRORS[0]}" "line 2: expected KEY=value"
  assert_contains "${DXB_CFG_ERRORS[1]}" "line 3: invalid key 'lower'"
}

test_config_load_missing_file() {
  assert_fails dxb_config_load "$TEST_TMP/nope.txt"
  assert_contains "${DXB_CFG_ERRORS[0]}" "cannot read"
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `tests/run.sh`
Expected: errors about `config.sh: No such file` and FAIL lines.

- [ ] **Step 4: Implement the parser**

`provision/lib/config.sh`:

```bash
#!/bin/bash
# shellcheck shell=bash
# dxberry.txt: parsing, validation, defaults and derived values.
#
#   dxb_config_load FILE       fills DXB_CFG / DXB_CFG_LINES; unknown keys -> DXB_CFG_WARNINGS
#   dxb_config_validate        applies defaults, checks every rule, fills DXB_CFG_ERRORS; 0 = valid
#   dxb_config_get KEY         prints a value ('' if unset)
#   dxb_config_print_masked    prints the effective config with secrets masked
#
# Derived keys set by dxb_config_validate (leading underscore, never read from the file):
#   _MODE static|dhcp  _IP  _PREFIX  _WIFI 0|1  _BEACON 0|1  _SEND_PATH is_only|rf|both
#   _SYMBOL_TABLE  _SYMBOL  _INTERVAL_S

declare -gA DXB_CFG=() DXB_CFG_LINES=()
declare -ga DXB_CFG_ERRORS=() DXB_CFG_WARNINGS=()

DXB_KNOWN_KEYS='HOSTNAME PASSWORD TIMEZONE STATIC_IP GATEWAY DNS WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY CALLSIGN LATITUDE LONGITUDE BEACON_COMMENT BEACON_INTERVAL_MIN IGATE_SERVER WEBUI_USER WEBUI_PASSWORD SSH_PUBKEY BEACON_SEND BEACON_PATH BEACON_SYMBOL DIGIPEATER IGATE_RF_TO_IS IGATE_IS_TO_RF GRAYWOLF_VERSION SERIAL_CONSOLE'
DXB_SECRET_KEYS='PASSWORD WIFI_PASSWORD WEBUI_PASSWORD'
DXB_RESERVED_PREFIXES='PAT_ WSJTX_ JS8CALL_ FLDIGI_ RIG_ GPS_ CONSOLE_'
DXB_APPLIED='<applied>'

_dxb_key_known() { local k; for k in $DXB_KNOWN_KEYS; do [[ $k == "$1" ]] && return 0; done; return 1; }
_dxb_key_reserved() { local p; for p in $DXB_RESERVED_PREFIXES; do [[ $1 == "$p"* ]] && return 0; done; return 1; }

dxb_config_load() {
  local file=$1 line key val n=0
  DXB_CFG=(); DXB_CFG_LINES=(); DXB_CFG_ERRORS=(); DXB_CFG_WARNINGS=()
  [[ -r $file ]] || { DXB_CFG_ERRORS+=("cannot read $file"); return 1; }
  while IFS= read -r line || [[ -n $line ]]; do
    n=$((n + 1))
    line=${line%$'\r'}
    line=${line#"${line%%[![:space:]]*}"}
    [[ -z $line || $line == \#* ]] && continue
    if [[ $line != *=* ]]; then
      DXB_CFG_ERRORS+=("line $n: expected KEY=value, got: $line")
      continue
    fi
    key=${line%%=*}
    val=${line#*=}
    key=${key%"${key##*[![:space:]]}"}
    val=${val#"${val%%[![:space:]]*}"}
    val=${val%"${val##*[![:space:]]}"}
    if [[ ${#val} -ge 2 && $val == \"*\" ]]; then val=${val:1:${#val}-2}; fi
    if [[ ! $key =~ ^[A-Z][A-Z0-9_]*$ ]]; then
      DXB_CFG_ERRORS+=("line $n: invalid key '$key'")
      continue
    fi
    _dxb_key_reserved "$key" && continue
    if ! _dxb_key_known "$key"; then
      DXB_CFG_WARNINGS+=("line $n: unknown key '$key' ignored")
      continue
    fi
    DXB_CFG[$key]=$val
    DXB_CFG_LINES[$key]=$n
  done < "$file"
  return 0
}

dxb_config_get() { printf '%s' "${DXB_CFG[$1]:-}"; }

dxb_config_print_masked() {
  local k v
  for k in $DXB_KNOWN_KEYS; do
    v=${DXB_CFG[$k]:-}
    case " $DXB_SECRET_KEYS " in
      *" $k "*) [[ -z $v || $v == "$DXB_APPLIED" ]] || v='********' ;;
    esac
    printf '%s=%s\n' "$k" "$v"
  done
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `tests/run.sh`
Expected: `4 tests, 0 failures`

- [ ] **Step 6: Commit**

```bash
git add tests/lib.sh tests/run.sh tests/test_config.sh provision/lib/config.sh
git commit -m "Add test harness and dxberry.txt parser"
```

---

### Task 2: Config validation, defaults and derived values

**Files:**
- Modify: `provision/lib/config.sh` (append validation)
- Modify: `tests/test_config.sh` (append tests)

**Interfaces:**
- Produces: `dxb_config_validate` (returns 0 when `DXB_CFG_ERRORS` is empty) and the derived keys listed in the file header. `DXB_ZONEINFO_DIR` (default `/usr/share/zoneinfo`) is consulted for `TIMEZONE` only when that directory exists. Every error message is `[line N: ]KEY <reason>` so `dxberry-ERROR.txt` can be written verbatim.

- [ ] **Step 1: Append the failing validation tests**

Append to `tests/test_config.sh`:

```bash
load_and_validate() { dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }
errors_text() { printf '%s\n' "${DXB_CFG_ERRORS[@]}"; }

test_validate_minimal_valid_config_applies_defaults() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[HOSTNAME]}" "dxberry-pi"
  assert_eq "${DXB_CFG[TIMEZONE]}" "UTC"
  assert_eq "${DXB_CFG[_MODE]}" "dhcp"
  assert_eq "${DXB_CFG[_WIFI]}" "0"
  assert_eq "${DXB_CFG[_BEACON]}" "0"
  assert_eq "${DXB_CFG[WEBUI_USER]}" "admin"
  assert_eq "${DXB_CFG[WEBUI_PASSWORD]}" "secretpass"
  assert_eq "${DXB_CFG[IGATE_SERVER]}" "rotate.aprs2.net"
  assert_eq "${DXB_CFG[_SEND_PATH]}" "is_only"
  assert_eq "${DXB_CFG[_SYMBOL_TABLE]}" "R"
  assert_eq "${DXB_CFG[_SYMBOL]}" "&"
  assert_eq "${DXB_CFG[_INTERVAL_S]}" "1800"
  assert_eq "${DXB_CFG[SERIAL_CONSOLE]}" "off"
}

test_validate_password_required_and_length() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'HOSTNAME=x'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "PASSWORD is required"
  write_cfg 'PASSWORD=short'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "line 1: PASSWORD must be 8-100 characters"
  write_cfg 'PASSWORD=<applied>'
  assert_ok load_and_validate
}

test_validate_static_ip_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_MODE]}" "static"
  assert_eq "${DXB_CFG[_IP]}" "192.168.1.90"
  assert_eq "${DXB_CFG[_PREFIX]}" "24"
  assert_eq "${DXB_CFG[DNS]}" "192.168.1.1"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "GATEWAY is required when STATIC_IP is set"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=10.0.0.1'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "GATEWAY is not inside 192.168.1.90/24"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90' 'GATEWAY=192.168.1.1'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "STATIC_IP must be an IPv4 address with prefix length"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'DNS=1.1.1.1 999.1.1.1'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "DNS '999.1.1.1' is not a valid IPv4 address"
}

test_validate_wifi_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "WIFI_PASSWORD is required when WIFI_SSID is set"
  assert_contains "$(errors_text)" "WIFI_COUNTRY must be a two-letter uppercase country code"
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_WIFI]}" "1"
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  assert_ok load_and_validate
}

test_validate_station_and_beacon_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'CALLSIGN=n0call' 'LATITUDE=37.1' 'BEACON_INTERVAL_MIN=0' 'BEACON_SEND=maybe' 'DIGIPEATER=yes' 'BEACON_SYMBOL=abc' 'GRAYWOLF_VERSION=latest' 'IGATE_IS_TO_RF=yes'
  assert_fails load_and_validate
  local e; e=$(errors_text)
  assert_contains "$e" "CALLSIGN must look like N0CALL or N0CALL-10"
  assert_contains "$e" "LATITUDE and LONGITUDE must be given together"
  assert_contains "$e" "BEACON_INTERVAL_MIN must be a whole number of minutes, 1-120"
  assert_contains "$e" "BEACON_SEND must be is, rf or both"
  assert_contains "$e" "DIGIPEATER must be off, fillin or wide"
  assert_contains "$e" "BEACON_SYMBOL must be exactly two characters"
  assert_contains "$e" "GRAYWOLF_VERSION must look like v0.14.13"
  assert_contains "$e" "IGATE_IS_TO_RF must be on or off"
  write_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.145833' 'LONGITUDE=-101.375' 'BEACON_INTERVAL_MIN=10' 'BEACON_SEND=both' 'BEACON_SYMBOL=/#' 'DIGIPEATER=wide' 'GRAYWOLF_VERSION=v0.14.13'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_BEACON]}" "1"
  assert_eq "${DXB_CFG[_INTERVAL_S]}" "600"
  assert_eq "${DXB_CFG[_SEND_PATH]}" "both"
  assert_eq "${DXB_CFG[_SYMBOL_TABLE]}" "/"
  assert_eq "${DXB_CFG[_SYMBOL]}" "#"
}

test_validate_timezone_checked_against_zoneinfo_when_present() {
  mkdir -p "$TEST_TMP/zi/America"; : > "$TEST_TMP/zi/America/Chicago"
  export DXB_ZONEINFO_DIR=$TEST_TMP/zi
  write_cfg 'PASSWORD=secretpass' 'TIMEZONE=America/Chicago'
  assert_ok load_and_validate
  write_cfg 'PASSWORD=secretpass' 'TIMEZONE=Mars/Phobos'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "TIMEZONE 'Mars/Phobos' is not a known time zone"
}

test_validate_hostname_and_ssh_key() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'HOSTNAME=Bad_Name' 'SSH_PUBKEY=not a key'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "HOSTNAME must be lowercase letters, digits and hyphens"
  assert_contains "$(errors_text)" "SSH_PUBKEY must be a single OpenSSH public key"
  write_cfg 'PASSWORD=secretpass' 'HOSTNAME=dx-berry-2' 'SSH_PUBKEY=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample+key/here= me@host'
  assert_ok load_and_validate
}

test_print_masked_hides_secrets() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  load_and_validate
  local out; out=$(dxb_config_print_masked)
  assert_contains "$out" "PASSWORD=********"
  assert_contains "$out" "WIFI_PASSWORD=<applied>"
  assert_contains "$out" "WEBUI_PASSWORD=********"
  assert_not_contains "$out" "secretpass"
}
```

- [ ] **Step 2: Run to verify the new tests fail**

Run: `tests/run.sh`
Expected: the `test_validate_*` and `test_print_masked_*` tests FAIL (`dxb_config_validate: command not found`).

- [ ] **Step 3: Append validation to `provision/lib/config.sh`**

```bash
_dxb_err() { local key=$1; shift; local n=${DXB_CFG_LINES[$key]:-}; DXB_CFG_ERRORS+=("${n:+line $n: }$key $*"); }
_dxb_default() { [[ -n ${DXB_CFG[$1]:-} ]] || DXB_CFG[$1]=$2; }
_dxb_len_between() { local n=${#1}; (( n >= $2 && n <= $3 )); }
_dxb_is_onoff() { [[ $1 == on || $1 == off ]]; }
_dxb_is_ipv4() {
  [[ $1 =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
  local o
  for o in "${BASH_REMATCH[@]:1}"; do (( 10#$o <= 255 )) || return 1; done
}
_dxb_ip2int() { local IFS=. a b c d; read -r a b c d <<< "$1"; echo $(( (10#$a << 24) | (10#$b << 16) | (10#$c << 8) | 10#$d )); }
_dxb_same_subnet() {
  local a b mask
  a=$(_dxb_ip2int "$1"); b=$(_dxb_ip2int "$2")
  mask=$(( (0xFFFFFFFF << (32 - $3)) & 0xFFFFFFFF ))
  (( (a & mask) == (b & mask) ))
}
_dxb_in_range() {
  [[ $1 =~ ^-?[0-9]+(\.[0-9]+)?$ ]] || return 1
  awk -v v="$1" -v lo="$2" -v hi="$3" 'BEGIN { exit !(v + 0 >= lo + 0 && v + 0 <= hi + 0) }'
}

dxb_config_validate() {
  local v ip='' prefix='' lat lon zi
  _dxb_default HOSTNAME dxberry-pi
  _dxb_default TIMEZONE UTC
  _dxb_default BEACON_COMMENT 'DXBerry-Pi iGate'
  _dxb_default BEACON_INTERVAL_MIN 30
  _dxb_default IGATE_SERVER rotate.aprs2.net
  _dxb_default WEBUI_USER admin
  _dxb_default BEACON_SEND is
  _dxb_default BEACON_PATH 'WIDE1-1,WIDE2-1'
  _dxb_default BEACON_SYMBOL 'R&'
  _dxb_default DIGIPEATER off
  _dxb_default IGATE_RF_TO_IS on
  _dxb_default IGATE_IS_TO_RF off
  _dxb_default SERIAL_CONSOLE off

  v=${DXB_CFG[PASSWORD]:-}
  if [[ -z $v ]]; then _dxb_err PASSWORD 'is required'
  elif [[ $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 100; then _dxb_err PASSWORD 'must be 8-100 characters'; fi

  [[ ${DXB_CFG[HOSTNAME]} =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || _dxb_err HOSTNAME 'must be lowercase letters, digits and hyphens, 1-63 characters'

  v=${DXB_CFG[TIMEZONE]}; zi=${DXB_ZONEINFO_DIR:-/usr/share/zoneinfo}
  if [[ ! $v =~ ^[A-Za-z0-9_+/-]+$ ]]; then _dxb_err TIMEZONE 'is not a valid time zone name'
  elif [[ -d $zi && ! -f $zi/$v ]]; then _dxb_err TIMEZONE "'$v' is not a known time zone (e.g. America/Chicago)"; fi

  if [[ -n ${DXB_CFG[STATIC_IP]:-} ]]; then
    DXB_CFG[_MODE]=static
    if [[ ${DXB_CFG[STATIC_IP]} =~ ^([0-9.]+)/([0-9]{1,2})$ ]]; then ip=${BASH_REMATCH[1]}; prefix=$(( 10#${BASH_REMATCH[2]} )); fi
    if [[ -n $ip ]] && _dxb_is_ipv4 "$ip" && (( prefix >= 8 && prefix <= 30 )); then
      DXB_CFG[_IP]=$ip
      DXB_CFG[_PREFIX]=$prefix
      v=${DXB_CFG[GATEWAY]:-}
      if [[ -z $v ]]; then _dxb_err GATEWAY 'is required when STATIC_IP is set'
      elif ! _dxb_is_ipv4 "$v"; then _dxb_err GATEWAY 'is not a valid IPv4 address'
      elif ! _dxb_same_subnet "$ip" "$v" "$prefix"; then _dxb_err GATEWAY "is not inside ${DXB_CFG[STATIC_IP]}"; fi
      [[ -n ${DXB_CFG[DNS]:-} ]] || DXB_CFG[DNS]=${DXB_CFG[GATEWAY]:-}
      for v in ${DXB_CFG[DNS]}; do _dxb_is_ipv4 "$v" || _dxb_err DNS "'$v' is not a valid IPv4 address"; done
    else
      _dxb_err STATIC_IP 'must be an IPv4 address with prefix length, e.g. 192.168.1.90/24'
    fi
  else
    DXB_CFG[_MODE]=dhcp
    [[ -z ${DXB_CFG[GATEWAY]:-} ]] || DXB_CFG_WARNINGS+=('GATEWAY is ignored because STATIC_IP is not set (DHCP)')
  fi

  if [[ -n ${DXB_CFG[WIFI_SSID]:-} ]]; then
    DXB_CFG[_WIFI]=1
    _dxb_len_between "${DXB_CFG[WIFI_SSID]}" 1 32 || _dxb_err WIFI_SSID 'must be 1-32 characters'
    v=${DXB_CFG[WIFI_PASSWORD]:-}
    if [[ -z $v ]]; then _dxb_err WIFI_PASSWORD 'is required when WIFI_SSID is set'
    elif [[ $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 63; then _dxb_err WIFI_PASSWORD 'must be 8-63 characters'; fi
    [[ ${DXB_CFG[WIFI_COUNTRY]:-} =~ ^[A-Z]{2}$ ]] || _dxb_err WIFI_COUNTRY 'must be a two-letter uppercase country code (e.g. US)'
  else
    DXB_CFG[_WIFI]=0
  fi

  if [[ -n ${DXB_CFG[CALLSIGN]:-} ]]; then
    [[ ${DXB_CFG[CALLSIGN]} =~ ^[A-Z0-9]{3,7}(-[0-9]{1,2})?$ ]] || _dxb_err CALLSIGN 'must look like N0CALL or N0CALL-10 (uppercase)'
  fi
  lat=${DXB_CFG[LATITUDE]:-}; lon=${DXB_CFG[LONGITUDE]:-}
  if [[ -n $lat || -n $lon ]]; then
    DXB_CFG[_BEACON]=1
    if [[ -z $lat || -z $lon ]]; then _dxb_err LATITUDE 'LATITUDE and LONGITUDE must be given together'; DXB_CFG[_BEACON]=0; fi
    [[ -z $lat ]] || _dxb_in_range "$lat" -90 90 || _dxb_err LATITUDE 'must be decimal degrees between -90 and 90'
    [[ -z $lon ]] || _dxb_in_range "$lon" -180 180 || _dxb_err LONGITUDE 'must be decimal degrees between -180 and 180'
  else
    DXB_CFG[_BEACON]=0
  fi
  _dxb_len_between "${DXB_CFG[BEACON_COMMENT]}" 0 43 || _dxb_err BEACON_COMMENT 'must be 43 characters or fewer'
  v=${DXB_CFG[BEACON_INTERVAL_MIN]}
  if [[ $v =~ ^[0-9]+$ ]] && (( 10#$v >= 1 && 10#$v <= 120 )); then DXB_CFG[_INTERVAL_S]=$(( 10#$v * 60 ))
  else _dxb_err BEACON_INTERVAL_MIN 'must be a whole number of minutes, 1-120'; DXB_CFG[_INTERVAL_S]=1800; fi
  [[ ${DXB_CFG[IGATE_SERVER]} =~ ^[A-Za-z0-9.-]+$ ]] || _dxb_err IGATE_SERVER 'must be a hostname'
  [[ ${DXB_CFG[WEBUI_USER]} =~ ^[A-Za-z0-9_.-]{1,32}$ ]] || _dxb_err WEBUI_USER 'must be 1-32 letters, digits, _ . -'
  [[ -n ${DXB_CFG[WEBUI_PASSWORD]:-} ]] || DXB_CFG[WEBUI_PASSWORD]=${DXB_CFG[PASSWORD]:-}
  v=${DXB_CFG[WEBUI_PASSWORD]}
  if [[ -n $v && $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 100; then _dxb_err WEBUI_PASSWORD 'must be 8-100 characters'; fi
  case ${DXB_CFG[BEACON_SEND]} in
    is) DXB_CFG[_SEND_PATH]=is_only ;;
    rf|both) DXB_CFG[_SEND_PATH]=${DXB_CFG[BEACON_SEND]} ;;
    *) _dxb_err BEACON_SEND 'must be is, rf or both'; DXB_CFG[_SEND_PATH]=is_only ;;
  esac
  [[ ${DXB_CFG[BEACON_PATH]} =~ ^[A-Z0-9-]+(,[A-Z0-9-]+)*$ ]] || _dxb_err BEACON_PATH 'must be comma-separated path elements like WIDE1-1,WIDE2-1'
  v=${DXB_CFG[BEACON_SYMBOL]}
  if (( ${#v} == 2 )); then DXB_CFG[_SYMBOL_TABLE]=${v:0:1}; DXB_CFG[_SYMBOL]=${v:1:1}
  else _dxb_err BEACON_SYMBOL 'must be exactly two characters (table/overlay + symbol)'; DXB_CFG[_SYMBOL_TABLE]=R; DXB_CFG[_SYMBOL]='&'; fi
  case ${DXB_CFG[DIGIPEATER]} in off|fillin|wide) ;; *) _dxb_err DIGIPEATER 'must be off, fillin or wide' ;; esac
  for v in IGATE_RF_TO_IS IGATE_IS_TO_RF SERIAL_CONSOLE; do _dxb_is_onoff "${DXB_CFG[$v]}" || _dxb_err "$v" 'must be on or off'; done
  if [[ -n ${DXB_CFG[GRAYWOLF_VERSION]:-} ]]; then
    [[ ${DXB_CFG[GRAYWOLF_VERSION]} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || _dxb_err GRAYWOLF_VERSION 'must look like v0.14.13'
  fi
  if [[ -n ${DXB_CFG[SSH_PUBKEY]:-} ]]; then
    [[ ${DXB_CFG[SSH_PUBKEY]} =~ ^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp[0-9]+|sk-ssh-ed25519@openssh\.com)\ [A-Za-z0-9+/=]+ ]] || _dxb_err SSH_PUBKEY 'must be a single OpenSSH public key'
  fi
  (( ${#DXB_CFG_ERRORS[@]} == 0 ))
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `tests/run.sh`
Expected: `12 tests, 0 failures`

- [ ] **Step 5: Commit**

```bash
git add provision/lib/config.sh tests/test_config.sh
git commit -m "Validate dxberry.txt and derive network and beacon values"
```

---

### Task 3: `common.sh` — logging, status file, key/value editing, templates

**Files:**
- Create: `provision/lib/common.sh`, `tests/test_common.sh`

**Interfaces:**
- Produces:
  - `dxb_boot_dir` → prints `$DXB_BOOT_DIR` if set, else `/boot/firmware` when a vfat filesystem is mounted there, else `/boot`.
  - `dxb_info MSG`, `dxb_warn MSG`, `dxb_error MSG` → stderr + `$DXB_LOG_FILE` (if its directory exists).
  - `dxb_step_failed STEP MSG` → appends to `DXB_FAILED_STEPS`, logs error.
  - `dxb_status_add LINE`, `dxb_status_write FILE` → status file with failed steps section.
  - `dxb_set_kv FILE KEY VALUE` → replaces first `KEY=` line (any leading blanks) or appends; regex-safe for keys like `aWIFI_SSID[0]`.
  - `dxb_render TEMPLATE NAME=VALUE…` → prints template with `@NAME@` replaced; fails on unresolved `@X@`.
  - `dxb_write_if_changed FILE CONTENT [MODE]` → writes only on difference; returns 0 if written, 1 if unchanged.
  - `dxb_require_root`, `dxb_squote STRING` (single-quote for bash-array files).
  - Globals: `DXB_STATE_DIR` (default `/var/lib/dxberry`), `DXB_LOG_FILE`, `DXB_FAILED_STEPS`, `DXB_STATUS_LINES`.

- [ ] **Step 1: Write the failing tests**

`tests/test_common.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"

test_set_kv_replaces_first_match_keeps_rest_and_appends() {
  printf '# c\nA=1\n  A=2\nB=x\n' > "$TEST_TMP/f"
  dxb_set_kv "$TEST_TMP/f" A new
  assert_eq "$(cat "$TEST_TMP/f")" $'# c\nA=new\n  A=2\nB=x'
  dxb_set_kv "$TEST_TMP/f" C 'v=with=equals and $dollar \backslash'
  assert_file_contains "$TEST_TMP/f" 'C=v=with=equals and $dollar \backslash'
}

test_set_kv_handles_bracketed_keys_and_missing_file() {
  printf "aWIFI_SSID[0]=''\naWIFI_SSID0=keep\n" > "$TEST_TMP/w"
  dxb_set_kv "$TEST_TMP/w" 'aWIFI_SSID[0]' "'Home'"
  assert_eq "$(cat "$TEST_TMP/w")" $'aWIFI_SSID[0]=\'Home\'\naWIFI_SSID0=keep'
  dxb_set_kv "$TEST_TMP/new" K V
  assert_eq "$(cat "$TEST_TMP/new")" "K=V"
}

test_render_substitutes_and_rejects_unresolved() {
  printf 'iface @IFACE@ inet @METHOD@\n@ADDRESS@\n' > "$TEST_TMP/t.tmpl"
  assert_eq "$(dxb_render "$TEST_TMP/t.tmpl" IFACE=eth0 METHOD=static 'ADDRESS=address 10.0.0.5/24')" $'iface eth0 inet static\naddress 10.0.0.5/24'
  assert_fails dxb_render "$TEST_TMP/t.tmpl" IFACE=eth0
}

test_write_if_changed_reports_change() {
  assert_ok dxb_write_if_changed "$TEST_TMP/o" "hello" 600
  assert_fails dxb_write_if_changed "$TEST_TMP/o" "hello"
  assert_ok dxb_write_if_changed "$TEST_TMP/o" "hello2"
  assert_eq "$(stat -c %a "$TEST_TMP/o")" "600"
  ln -s /nonexistent "$TEST_TMP/link"
  assert_ok dxb_write_if_changed "$TEST_TMP/link" "x"
  [[ -L $TEST_TMP/link ]] && _fail "symlink should have been replaced by a file"
}

test_status_file_lists_failures() {
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  DXB_LOG_FILE=$TEST_TMP/log
  dxb_status_add "hostname: x"
  dxb_step_failed graywolf "download failed"
  dxb_status_write "$TEST_TMP/status.txt"
  assert_file_contains "$TEST_TMP/status.txt" "hostname: x"
  assert_file_contains "$TEST_TMP/status.txt" "FAILED STEPS:"
  assert_file_contains "$TEST_TMP/status.txt" "graywolf: download failed"
  assert_file_contains "$TEST_TMP/log" "[ERROR] graywolf: download failed"
}

test_boot_dir_override_and_squote() {
  DXB_BOOT_DIR=$TEST_TMP/bootfs
  assert_eq "$(dxb_boot_dir)" "$TEST_TMP/bootfs"
  unset DXB_BOOT_DIR
  assert_eq "$(dxb_squote "it's")" "'it'\\''s'"
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: `common.sh: No such file` and FAIL lines.

- [ ] **Step 3: Implement `provision/lib/common.sh`**

```bash
#!/bin/bash
# shellcheck shell=bash
# Shared helpers for the DXBerry-Pi provisioner.

: "${DXB_STATE_DIR:=/var/lib/dxberry}"
: "${DXB_LOG_FILE:=$DXB_STATE_DIR/provision.log}"
declare -ga DXB_FAILED_STEPS=() DXB_STATUS_LINES=()

# Directory of the user-editable boot (FAT) partition.
dxb_boot_dir() {
  if [[ -n ${DXB_BOOT_DIR:-} ]]; then printf '%s' "$DXB_BOOT_DIR"; return; fi
  if findmnt -no FSTYPE /boot/firmware 2> /dev/null | grep -q vfat; then printf '/boot/firmware'; else printf '/boot'; fi
}

dxb_log() {
  local lvl=$1; shift
  local msg
  msg="$(date '+%F %T') [$lvl] $*"
  printf '%s\n' "$msg" >&2
  [[ -d $(dirname "$DXB_LOG_FILE") ]] && printf '%s\n' "$msg" >> "$DXB_LOG_FILE"
  return 0
}
dxb_info() { dxb_log INFO "$@"; }
dxb_warn() { dxb_log WARN "$@"; }
dxb_error() { dxb_log ERROR "$@"; }
dxb_step_failed() { DXB_FAILED_STEPS+=("$1: $2"); dxb_error "$1: $2"; }
dxb_status_add() { DXB_STATUS_LINES+=("$1"); }

dxb_status_write() {
  local f=$1
  {
    echo "DXBerry-Pi status - $(date '+%F %T %Z')"
    printf '%s\n' "${DXB_STATUS_LINES[@]}"
    echo
    if (( ${#DXB_FAILED_STEPS[@]} )); then
      echo "FAILED STEPS:"
      printf '  - %s\n' "${DXB_FAILED_STEPS[@]}"
      echo "Fix the cause, then run: sudo dxberry-provision"
    else
      echo "All steps completed."
    fi
  } > "$f"
}

dxb_require_root() { (( EUID == 0 )) || { echo "this command must run as root (use sudo)" >&2; exit 1; }; }

# Single-quote STRING for a bash-array style file such as dietpi-wifi.txt.
dxb_squote() { printf "'%s'" "${1//\'/\'\\\'\'}"; }

# dxb_set_kv FILE KEY VALUE: replace the first "KEY=" line or append one.
dxb_set_kv() {
  local file=$1 key=$2 val=$3 kre
  kre=$(printf '%s' "$key" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
  [[ -f $file ]] || : > "$file"
  if grep -qE "^[[:blank:]]*${kre}=" "$file"; then
    k="$key" kre="$kre" v="$val" awk '
      BEGIN { done = 0 }
      {
        if (!done && $0 ~ ("^[[:blank:]]*" ENVIRON["kre"] "=")) { print ENVIRON["k"] "=" ENVIRON["v"]; done = 1 }
        else print
      }' "$file" > "$file.dxbtmp" && mv "$file.dxbtmp" "$file"
  else
    printf '%s=%s\n' "$key" "$val" >> "$file"
  fi
}

# dxb_render TEMPLATE NAME=VALUE...: print TEMPLATE with @NAME@ placeholders replaced.
dxb_render() {
  local file=$1 content kv
  shift
  content=$(< "$file")
  for kv in "$@"; do content=${content//"@${kv%%=*}@"/${kv#*=}}; done
  if [[ $content =~ @[A-Z_]+@ ]]; then
    echo "dxb_render: unresolved placeholder ${BASH_REMATCH[0]} in $file" >&2
    return 1
  fi
  printf '%s\n' "$content"
}

# dxb_write_if_changed FILE CONTENT [MODE]: 0 = written, 1 = already identical.
dxb_write_if_changed() {
  local dest=$1 content=$2 mode=${3:-644}
  if [[ -f $dest && ! -L $dest && $(< "$dest") == "$content" ]]; then return 1; fi
  [[ -L $dest ]] && rm -f "$dest"
  printf '%s\n' "$content" > "$dest"
  chmod "$mode" "$dest"
  return 0
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `tests/run.sh`
Expected: `18 tests, 0 failures`

- [ ] **Step 5: Commit**

```bash
git add provision/lib/common.sh tests/test_common.sh
git commit -m "Add provisioner common helpers"
```

---

### Task 4: `dxberry-preboot` — first-boot pre-network hook

**Files:**
- Create: `provision/bin/dxberry-preboot`, `tests/test_preboot.sh`
- Create: `provision/lib/network.sh` (only `dxb_net_write_wifi_txt` for now; the rest is Task 5)

**Interfaces:**
- Consumes: `dxb_config_load/validate`, `dxb_set_kv`, `dxb_squote`, `dxb_boot_dir`, logging.
- Produces: `dxb_net_write_wifi_txt FILE SSID KEY` (writes slot 0 of a `dietpi-wifi.txt`); the preboot `main` honoring env overrides `DXB_LIB`, `DXB_BOOT_DIR`, `DXB_DIETPI_TXT`, `DXB_DIETPI_WIFI`, `DXB_STATE_DIR`. Writes `<boot>/dxberry-ERROR.txt` on invalid/missing config and never exits non-zero (DietPi must continue booting).

- [ ] **Step 1: Write the failing tests**

`tests/test_preboot.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/network.sh"
source "$DXB_ROOT/provision/bin/dxberry-preboot"

preboot_env() {
  export DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_DIETPI_TXT=$TEST_TMP/dietpi.txt DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt \
    DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_BOOT_DIR"
  printf 'AUTO_SETUP_NET_HOSTNAME=DietPi\nAUTO_SETUP_GLOBAL_PASSWORD=dietpi\nAUTO_SETUP_TIMEZONE=UTC\nAUTO_SETUP_NET_ETHERNET_ENABLED=1\nAUTO_SETUP_NET_WIFI_ENABLED=0\nAUTO_SETUP_NET_WIFI_COUNTRY_CODE=GB\nAUTO_SETUP_NET_USESTATIC=0\nAUTO_SETUP_NET_STATIC_IP=192.168.0.100/24\nAUTO_SETUP_NET_STATIC_GATEWAY=192.168.0.1\nAUTO_SETUP_NET_STATIC_DNS=9.9.9.9 149.112.112.112\nCONFIG_SERIAL_CONSOLE_ENABLE=0\n' > "$DXB_DIETPI_TXT"
  printf "aWIFI_SSID[0]=''\naWIFI_KEY[0]=''\naWIFI_KEYMGR[0]='WPA-PSK'\naWIFI_SSID[1]=''\n" > "$DXB_DIETPI_WIFI"
}

test_preboot_writes_dietpi_settings_from_config() {
  preboot_env
  printf 'HOSTNAME=station\nPASSWORD=secretpass\nTIMEZONE=America/Chicago\nSTATIC_IP=192.168.1.90/24\nGATEWAY=192.168.1.1\nDNS=192.168.1.1 1.1.1.1\nWIFI_SSID=Home\nWIFI_PASSWORD=wifipass1\nWIFI_COUNTRY=US\nSERIAL_CONSOLE=on\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=station"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=secretpass"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_TIMEZONE=America/Chicago"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_USESTATIC=1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_IP=192.168.1.90/24"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_GATEWAY=192.168.1.1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_DNS=192.168.1.1 1.1.1.1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_WIFI_ENABLED=0"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_WIFI_COUNTRY_CODE=US"
  assert_file_contains "$DXB_DIETPI_TXT" "CONFIG_SERIAL_CONSOLE_ENABLE=1"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]='Home'"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_KEY[0]='wifipass1'"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[1]=''"
  [[ -f $DXB_BOOT_DIR/dxberry-ERROR.txt ]] && _fail "no error file expected"
  [[ -f $DXB_STATE_DIR/preboot.done ]] || _fail "marker missing"
}

test_preboot_dhcp_and_no_wifi_leaves_wifi_file_alone() {
  preboot_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_USESTATIC=0"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=dxberry-pi"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]=''"
}

test_preboot_applied_password_is_not_written() {
  preboot_env
  printf 'PASSWORD=<applied>\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=dietpi"
}

test_preboot_invalid_config_writes_error_file_and_leaves_dietpi_txt() {
  preboot_env
  printf 'HOSTNAME=Bad\nSTATIC_IP=nope\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "PASSWORD is required"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "line 1: HOSTNAME must be"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "line 2: STATIC_IP must be"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=DietPi"
}

test_preboot_missing_config_writes_hint() {
  preboot_env
  assert_ok main
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "Rename dxberry.txt.example to dxberry.txt"
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: `network.sh`/`dxberry-preboot: No such file` and FAIL lines.

- [ ] **Step 3: Create `provision/lib/network.sh` with the WiFi writer only**

```bash
#!/bin/bash
# shellcheck shell=bash
# Network provisioning: interfaces files, resolv.conf, WiFi credentials, dxberry-netwatch.

: "${DXB_IFACES_DIR:=/etc/network/interfaces.d}"
: "${DXB_RESOLV_CONF:=/etc/resolv.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_DIETPI_WIFIDB:=/boot/dietpi/func/dietpi-wifidb}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"
DXB_NET_CHANGED=0

# dxb_net_write_wifi_txt FILE SSID KEY: fill slot 0 of a DietPi dietpi-wifi.txt.
dxb_net_write_wifi_txt() {
  local file=$1 ssid=$2 key=$3
  dxb_set_kv "$file" 'aWIFI_SSID[0]' "$(dxb_squote "$ssid")"
  dxb_set_kv "$file" 'aWIFI_KEY[0]' "$(dxb_squote "$key")"
  dxb_set_kv "$file" 'aWIFI_KEYMGR[0]' "'WPA-PSK'"
}
```

- [ ] **Step 4: Implement `provision/bin/dxberry-preboot`**

```bash
#!/bin/bash
# DXBerry-Pi first-boot hook. Runs from /boot/Automation_Custom_PreScript.sh before the network is up:
# turns <boot>/dxberry.txt into DietPi's own dietpi.txt / dietpi-wifi.txt settings.
# Always exits 0 so DietPi keeps booting; problems go to <boot>/dxberry-ERROR.txt.
set -uo pipefail

DXB_LIB=${DXB_LIB:-/opt/dxberry/lib}
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
# shellcheck disable=SC1091
source "$DXB_LIB/config.sh"
# shellcheck disable=SC1091
source "$DXB_LIB/network.sh"

main() {
  local dietpi_txt=${DXB_DIETPI_TXT:-/boot/dietpi.txt}
  local dietpi_wifi=${DXB_DIETPI_WIFI:-/boot/dietpi-wifi.txt}
  local boot cfg errf w
  boot=$(dxb_boot_dir); cfg="$boot/dxberry.txt"; errf="$boot/dxberry-ERROR.txt"
  mkdir -p "$DXB_STATE_DIR" 2> /dev/null || true

  if [[ ! -f $cfg ]]; then
    {
      echo "DXBerry-Pi: no dxberry.txt was found on the boot partition."
      echo "Rename dxberry.txt.example to dxberry.txt, fill it in, and reboot."
      echo "This boot used DietPi defaults: DHCP address, hostname DietPi, login root / dietpi."
    } > "$errf"
    dxb_warn "no $cfg; DietPi defaults kept"
    return 0
  fi

  dxb_config_load "$cfg"
  if ! dxb_config_validate; then
    {
      echo "DXBerry-Pi: dxberry.txt has errors. Fix them and reboot."
      echo
      printf '%s\n' "${DXB_CFG_ERRORS[@]}"
      echo
      echo "This boot used DietPi defaults: DHCP address, hostname DietPi, login root / dietpi."
    } > "$errf"
    dxb_error "dxberry.txt invalid; see $errf"
    return 0
  fi
  rm -f "$errf"
  for w in "${DXB_CFG_WARNINGS[@]}"; do dxb_warn "$w"; done

  dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_HOSTNAME "${DXB_CFG[HOSTNAME]}"
  [[ ${DXB_CFG[PASSWORD]} == "$DXB_APPLIED" ]] || dxb_set_kv "$dietpi_txt" AUTO_SETUP_GLOBAL_PASSWORD "${DXB_CFG[PASSWORD]}"
  dxb_set_kv "$dietpi_txt" AUTO_SETUP_TIMEZONE "${DXB_CFG[TIMEZONE]}"
  dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_ETHERNET_ENABLED 1
  dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_WIFI_ENABLED 0
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_USESTATIC 1
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_STATIC_IP "${DXB_CFG[STATIC_IP]}"
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_STATIC_GATEWAY "${DXB_CFG[GATEWAY]}"
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_STATIC_DNS "${DXB_CFG[DNS]}"
  else
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_USESTATIC 0
  fi
  [[ ${DXB_CFG[SERIAL_CONSOLE]} == on ]] && w=1 || w=0
  dxb_set_kv "$dietpi_txt" CONFIG_SERIAL_CONSOLE_ENABLE "$w"
  if (( DXB_CFG[_WIFI] )); then
    dxb_set_kv "$dietpi_txt" AUTO_SETUP_NET_WIFI_COUNTRY_CODE "${DXB_CFG[WIFI_COUNTRY]}"
    [[ ${DXB_CFG[WIFI_PASSWORD]} == "$DXB_APPLIED" ]] || dxb_net_write_wifi_txt "$dietpi_wifi" "${DXB_CFG[WIFI_SSID]}" "${DXB_CFG[WIFI_PASSWORD]}"
  fi
  touch "$DXB_STATE_DIR/preboot.done" 2> /dev/null || true
  dxb_info "preboot: dietpi.txt prepared for ${DXB_CFG[HOSTNAME]} (${DXB_CFG[_MODE]})"
  return 0
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; exit 0; fi
```

- [ ] **Step 5: Run to verify it passes**

Run: `chmod +x provision/bin/dxberry-preboot && tests/run.sh`
Expected: `23 tests, 0 failures`

- [ ] **Step 6: Commit**

```bash
git add provision/bin/dxberry-preboot provision/lib/network.sh tests/test_preboot.sh
git commit -m "Add first-boot pre-network hook"
```

---

### Task 5: `network.sh` — interfaces files, resolv.conf, WiFi import, netwatch install

**Files:**
- Modify: `provision/lib/network.sh` (append)
- Create: `provision/templates/interfaces-eth0.tmpl`, `provision/templates/interfaces-wlan0.tmpl`, `provision/templates/dxberry-netwatch.service`, `tests/test_network.sh`

**Interfaces:**
- Consumes: `DXB_CFG` derived keys, `dxb_render`, `dxb_write_if_changed`, `dxb_set_kv`, `dxb_squote`, logging, `dxb_status_add`.
- Produces: `dxb_net_render_iface eth0|wlan0` (prints stanza), `provision_network` (sets `DXB_NET_CHANGED=1` when any interface file changed), `dxb_net_import_wifi`, `dxb_net_install_netwatch`. Env overrides for tests: `DXB_IFACES_DIR`, `DXB_RESOLV_CONF`, `DXB_TEMPLATES`, `DXB_SYSTEMD_DIR`, `DXB_DIETPI_WIFIDB`, `DXB_DIETPI_WIFI`. `DXB_MODE` (`first-boot` or `run`) is set by the driver (Task 9).

- [ ] **Step 1: Write the templates**

`provision/templates/interfaces-eth0.tmpl`:

```
# Location: /etc/network/interfaces.d/eth0.conf
# Managed by DXBerry-Pi: dxberry-netwatch brings this interface up and down.
# No "auto"/"allow-hotplug" line on purpose - see /opt/dxberry and docs/design in the DXBerry-Pi repo.
iface eth0 inet @METHOD@
@ADDRESS@
@GATEWAY@
```

`provision/templates/interfaces-wlan0.tmpl`:

```
# Location: /etc/network/interfaces.d/wlan0.conf
# Managed by DXBerry-Pi: dxberry-netwatch brings this interface up only while eth0 has no link.
# No "auto"/"allow-hotplug" line on purpose - see /opt/dxberry and docs/design in the DXBerry-Pi repo.
iface wlan0 inet @METHOD@
@ADDRESS@
@GATEWAY@
wpa-conf /etc/wpa_supplicant/wpa_supplicant.conf
pre-up iw dev wlan0 set power_save off || true
post-down iw dev wlan0 set power_save on || true
```

`provision/templates/dxberry-netwatch.service`:

```
[Unit]
Description=DXBerry-Pi network watcher (Ethernet primary, WiFi failover)
Documentation=file:///opt/dxberry/bin/dxberry-netwatch
After=networking.service
Before=network-online.target

[Service]
Type=notify
NotifyAccess=all
ExecStart=/opt/dxberry/bin/dxberry-netwatch run
Restart=always
RestartSec=3
TimeoutStartSec=90

[Install]
WantedBy=multi-user.target network-online.target
```

- [ ] **Step 2: Write the failing tests**

`tests/test_network.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/network.sh"

net_env() {
  export DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_RESOLV_CONF=$TEST_TMP/resolv.conf DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_DIETPI_WIFIDB=$TEST_TMP/wifidb DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt DXB_LOG_FILE=$TEST_TMP/log DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_SYSTEMD_DIR"
  printf '#!/bin/bash\necho "wifidb $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  printf "aWIFI_SSID[0]=''\naWIFI_KEY[0]=''\naWIFI_KEYMGR[0]='WPA-PSK'\n" > "$DXB_DIETPI_WIFI"
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_NET_CHANGED=0
}
net_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }

test_render_static_and_dhcp_stanzas() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  local out; out=$(dxb_net_render_iface eth0)
  assert_contains "$out" $'iface eth0 inet static\naddress 192.168.1.90/24\ngateway 192.168.1.1'
  assert_not_contains "$out" "allow-hotplug"
  out=$(dxb_net_render_iface wlan0)
  assert_contains "$out" $'iface wlan0 inet static\naddress 192.168.1.90/24\ngateway 192.168.1.1\nwpa-conf /etc/wpa_supplicant/wpa_supplicant.conf'
  net_cfg 'PASSWORD=secretpass'
  out=$(dxb_net_render_iface eth0)
  assert_contains "$out" "iface eth0 inet dhcp"
  assert_not_contains "$out" "address"
}

test_provision_network_static_with_wifi_writes_everything_once() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'DNS=192.168.1.1 1.1.1.1' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=first-boot provision_network
  assert_eq "$DXB_NET_CHANGED" "1"
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "address 192.168.1.90/24"
  assert_file_contains "$DXB_IFACES_DIR/wlan0.conf" "wpa-conf"
  assert_eq "$(cat "$DXB_RESOLV_CONF")" $'nameserver 192.168.1.1\nnameserver 1.1.1.1'
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]='Home'"
  assert_file_contains "$TEST_TMP/calls" "wifidb 1"
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-netwatch.service" "ExecStart=/opt/dxberry/bin/dxberry-netwatch run"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable dxberry-netwatch"
  DXB_NET_CHANGED=0; : > "$TEST_TMP/calls"
  DXB_CFG[WIFI_PASSWORD]=$DXB_APPLIED   # what provision_scrub does after the first run
  DXB_MODE=run provision_network
  assert_eq "$DXB_NET_CHANGED" "0"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
}

test_provision_network_applied_wifi_password_skips_import() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]=''"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "iface eth0 inet dhcp"
  [[ -f $DXB_RESOLV_CONF ]] && _fail "resolv.conf must not be written in DHCP mode"
}

test_provision_network_removes_wlan0_when_wifi_unset() {
  net_env
  mkdir -p "$DXB_IFACES_DIR"; echo old > "$DXB_IFACES_DIR/wlan0.conf"
  net_cfg 'PASSWORD=secretpass'
  DXB_MODE=run provision_network
  [[ -f $DXB_IFACES_DIR/wlan0.conf ]] && _fail "wlan0.conf should be removed"
  assert_eq "$DXB_NET_CHANGED" "1"
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `tests/run.sh`
Expected: `dxb_net_render_iface: command not found` / `provision_network: command not found` FAIL lines.

- [ ] **Step 4: Append to `provision/lib/network.sh`**

```bash
# dxb_net_render_iface eth0|wlan0: print the ifupdown stanza for the configured mode.
dxb_net_render_iface() {
  local iface=$1 method addr gw
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    method=static; addr="address ${DXB_CFG[STATIC_IP]}"; gw="gateway ${DXB_CFG[GATEWAY]}"
  else
    method=dhcp; addr='# address assigned by DHCP'; gw='# gateway assigned by DHCP'
  fi
  dxb_render "$DXB_TEMPLATES/interfaces-$iface.tmpl" "METHOD=$method" "ADDRESS=$addr" "GATEWAY=$gw"
}

# Import WiFi credentials through DietPi's own tool so its WiFi menu keeps working.
dxb_net_import_wifi() {
  [[ ${DXB_CFG[WIFI_PASSWORD]} == "$DXB_APPLIED" ]] && return 0
  dxb_net_write_wifi_txt "$DXB_DIETPI_WIFI" "${DXB_CFG[WIFI_SSID]}" "${DXB_CFG[WIFI_PASSWORD]}"
  if [[ -x $DXB_DIETPI_WIFIDB ]]; then
    "$DXB_DIETPI_WIFIDB" 1 > /dev/null 2>&1 || dxb_step_failed network "dietpi-wifidb failed to import WiFi credentials"
    dxb_info "WiFi credentials imported for ${DXB_CFG[WIFI_SSID]}"
  else
    dxb_step_failed network "$DXB_DIETPI_WIFIDB not found; WiFi credentials not imported"
  fi
}

dxb_net_install_netwatch() {
  local unit="$DXB_SYSTEMD_DIR/dxberry-netwatch.service" content
  content=$(< "$DXB_TEMPLATES/dxberry-netwatch.service")
  if dxb_write_if_changed "$unit" "$content"; then
    systemctl daemon-reload
    systemctl enable dxberry-netwatch > /dev/null 2>&1
    dxb_info "dxberry-netwatch unit installed"
    return 0
  fi
  return 1
}

provision_network() {
  local content
  mkdir -p "$DXB_IFACES_DIR"
  content=$(dxb_net_render_iface eth0) || { dxb_step_failed network "eth0 template failed"; return 1; }
  dxb_write_if_changed "$DXB_IFACES_DIR/eth0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote eth0.conf (${DXB_CFG[_MODE]})"; }
  if (( DXB_CFG[_WIFI] )); then
    content=$(dxb_net_render_iface wlan0) || { dxb_step_failed network "wlan0 template failed"; return 1; }
    dxb_write_if_changed "$DXB_IFACES_DIR/wlan0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote wlan0.conf"; }
    dxb_net_import_wifi
  elif [[ -f $DXB_IFACES_DIR/wlan0.conf ]]; then
    rm -f "$DXB_IFACES_DIR/wlan0.conf"; DXB_NET_CHANGED=1; dxb_info "removed wlan0.conf (no WIFI_SSID)"
  fi
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    # shellcheck disable=SC2086
    content=$(printf 'nameserver %s\n' ${DXB_CFG[DNS]})
    dxb_write_if_changed "$DXB_RESOLV_CONF" "$content" && dxb_info "wrote resolv.conf"
  fi
  dxb_net_install_netwatch || true
  if (( DXB_CFG[_WIFI] )); then dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 primary, wlan0 (${DXB_CFG[WIFI_SSID]}) failover"
  else dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 only"; fi
  return 0
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `tests/run.sh`
Expected: `27 tests, 0 failures`

- [ ] **Step 6: Commit**

```bash
git add provision/lib/network.sh provision/templates tests/test_network.sh
git commit -m "Provision ifupdown interfaces, DNS, WiFi import and netwatch unit"
```

---

### Task 6: `dxberry-netwatch` — the failover daemon

**Files:**
- Create: `provision/bin/dxberry-netwatch`, `tests/test_netwatch.sh`

**Interfaces:**
- Produces: `dxberry-netwatch run` (daemon), `--simulate eth0-down|eth0-up|off`, `--status`. Pure `nw_decide STATE ETH_CARRIER WIFI_DEFINED WLAN_CARRIER` prints `NEWSTATE [down_eth|up_eth|down_wifi|up_wifi…]`. Env overrides: `DXB_ETH`, `DXB_WLAN`, `DXB_IFACES_DIR`, `DXB_SYS_NET`, `DXB_NW_SIM_FILE`, `DXB_NW_STATE_FILE`, `DXB_NW_DEBOUNCE`, `DXB_NW_WIFI_GRACE`, `DXB_NW_RETRY`, `DXB_NW_POLL`. Sourcing the file does not run `main`.

- [ ] **Step 1: Write the failing tests**

`tests/test_netwatch.sh`:

```bash
#!/usr/bin/env bash
export DXB_NW_DEBOUNCE=0 DXB_NW_WIFI_GRACE=0 DXB_NW_RETRY=0 DXB_NW_POLL=1
# shellcheck disable=SC1091
source "$DXB_ROOT/provision/bin/dxberry-netwatch"

nw_env() {
  export DXB_SYS_NET=$TEST_TMP/sys DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_NW_SIM_FILE=$TEST_TMP/sim DXB_NW_STATE_FILE=$TEST_TMP/state
  SYS_NET=$DXB_SYS_NET; IFACES_DIR=$DXB_IFACES_DIR; SIM_FILE=$DXB_NW_SIM_FILE; STATE_FILE=$DXB_NW_STATE_FILE
  mkdir -p "$SYS_NET/eth0" "$SYS_NET/wlan0" "$IFACES_DIR"
  echo 1 > "$SYS_NET/eth0/carrier"; echo 1 > "$SYS_NET/wlan0/carrier"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/ifstate"
  ifup() { echo "ifup $1" >> "$TEST_TMP/calls"; echo "$1" >> "$TEST_TMP/ifstate"; }
  ifdown() { echo "ifdown $1" >> "$TEST_TMP/calls"; grep -vx "$1" "$TEST_TMP/ifstate" > "$TEST_TMP/ifstate.n"; mv "$TEST_TMP/ifstate.n" "$TEST_TMP/ifstate"; }
  ifquery() { grep -qx "$2" "$TEST_TMP/ifstate"; }
  ip() { echo "ip $*" >> "$TEST_TMP/calls"; }
  sleep() { :; }
  NW_STATE=NONE; NW_WIFI_SINCE=0; NW_LAST_RETRY=0
}
calls() { tr '\n' ';' < "$TEST_TMP/calls"; }

test_decide_truth_table() {
  assert_eq "$(nw_decide ETH 1 1 1)" "ETH"
  assert_eq "$(nw_decide ETH 0 1 0)" "WIFI down_eth up_wifi"
  assert_eq "$(nw_decide ETH 0 0 0)" "NONE down_eth"
  assert_eq "$(nw_decide WIFI 1 1 1)" "ETH down_wifi up_eth"
  assert_eq "$(nw_decide WIFI 0 1 1)" "WIFI"
  assert_eq "$(nw_decide WIFI 0 1 0)" "NONE down_wifi"
  assert_eq "$(nw_decide NONE 1 0 0)" "ETH up_eth"
  assert_eq "$(nw_decide NONE 0 1 0)" "WIFI up_wifi"
  assert_eq "$(nw_decide NONE 0 0 0)" "NONE"
}

test_carrier_reads_sysfs_and_simulation() {
  nw_env
  assert_eq "$(nw_carrier eth0)" "1"
  echo 0 > "$SYS_NET/eth0/carrier"; assert_eq "$(nw_carrier eth0)" "0"
  rm "$SYS_NET/eth0/carrier"; assert_eq "$(nw_carrier eth0)" "0"
  echo 1 > "$SIM_FILE"; assert_eq "$(nw_carrier eth0)" "1"
  echo 0 > "$SIM_FILE"; assert_eq "$(nw_carrier wlan0)" "1"
}

test_failover_sequence_never_overlaps() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  nw_startup
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ip link set eth0 up;ifup eth0;"
  echo 0 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" "ifdown eth0;ip link set eth0 up;ifup wlan0;"
  echo 1 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ifdown wlan0;ifup eth0;"
  assert_eq "$(cat "$STATE_FILE")" "ETH"
}

test_no_wifi_goes_to_none_and_back() {
  nw_env
  nw_startup
  echo 0 > "$SYS_NET/eth0/carrier"; nw_tick 0
  assert_eq "$NW_STATE" "NONE"
  echo 1 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ifup eth0;"
}

test_startup_adopts_existing_state_without_cycling() {
  nw_env
  echo eth0 >> "$TEST_TMP/ifstate"
  nw_startup
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ip link set eth0 up;"
}

test_wifi_association_loss_returns_to_none_then_retries() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  echo 0 > "$SYS_NET/eth0/carrier"
  nw_startup
  assert_eq "$NW_STATE" "WIFI"
  echo 0 > "$SYS_NET/wlan0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "NONE"
  assert_eq "$(calls)" "ifdown wlan0;"
  echo 1 > "$SYS_NET/wlan0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" "ifup wlan0;"
}

test_simulate_writes_and_clears_file() {
  nw_env
  nw_simulate eth0-down > /dev/null; assert_eq "$(cat "$SIM_FILE")" "0"
  nw_simulate eth0-up > /dev/null; assert_eq "$(cat "$SIM_FILE")" "1"
  nw_simulate off > /dev/null; [[ -f $SIM_FILE ]] && _fail "sim file should be removed"
  assert_fails nw_simulate bogus
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: `dxberry-netwatch: No such file` and FAIL lines.

- [ ] **Step 3: Implement `provision/bin/dxberry-netwatch`**

```bash
#!/bin/bash
# dxberry-netwatch: Ethernet-primary / WiFi-failover with exactly one configured interface at a time.
# Design: docs/design/2026-09-07-base-image.md section 8 in the DXBerry-Pi repository.
set -uo pipefail

ETH=${DXB_ETH:-eth0}
WLAN=${DXB_WLAN:-wlan0}
IFACES_DIR=${DXB_IFACES_DIR:-/etc/network/interfaces.d}
SYS_NET=${DXB_SYS_NET:-/sys/class/net}
SIM_FILE=${DXB_NW_SIM_FILE:-/run/dxberry-netwatch.simulate}
STATE_FILE=${DXB_NW_STATE_FILE:-/run/dxberry-netwatch.state}
DEBOUNCE=${DXB_NW_DEBOUNCE:-2}
WIFI_GRACE=${DXB_NW_WIFI_GRACE:-30}
RETRY=${DXB_NW_RETRY:-30}
POLL=${DXB_NW_POLL:-5}

NW_STATE=NONE
NW_WIFI_SINCE=0
NW_LAST_RETRY=0

log() { printf 'dxberry-netwatch: %s\n' "$*" >&2; }

nw_wifi_defined() { [[ -f $IFACES_DIR/$WLAN.conf && -d $SYS_NET/$WLAN ]]; }

# Prints 1 when IFACE has carrier, else 0. The Ethernet reading can be overridden by --simulate.
nw_carrier() {
  local v=''
  if [[ $1 == "$ETH" && -f $SIM_FILE ]]; then
    read -r v 2> /dev/null < "$SIM_FILE" || v=''
  else
    read -r v 2> /dev/null < "$SYS_NET/$1/carrier" || v=''
  fi
  [[ $v == 1 ]] && printf 1 || printf 0
}

nw_configured() { ifquery --state "$1" > /dev/null 2>&1; }
nw_up() { log "ifup $1"; ifup "$1" || log "ifup $1 failed"; }
nw_down() {
  log "ifdown $1"
  ifdown "$1" 2> /dev/null || ifdown --force "$1" 2> /dev/null || true
  # A link that stays administratively down reports no carrier, so eth0 is re-raised without an address.
  [[ $1 == "$ETH" ]] && ip link set "$ETH" up 2> /dev/null
  return 0
}

# Pure decision: nw_decide STATE ETH_CARRIER WIFI_DEFINED WLAN_CARRIER -> "NEWSTATE [actions...]"
nw_decide() {
  local state=$1 ec=$2 wd=$3 wc=$4
  case $state in
    ETH)  if (( ec )); then echo ETH; elif (( wd )); then echo 'WIFI down_eth up_wifi'; else echo 'NONE down_eth'; fi ;;
    WIFI) if (( ec )); then echo 'ETH down_wifi up_eth'; elif (( wc )); then echo WIFI; else echo 'NONE down_wifi'; fi ;;
    *)    if (( ec )); then echo 'ETH up_eth'; elif (( wd )); then echo 'WIFI up_wifi'; else echo NONE; fi ;;
  esac
}

nw_do() {
  local a
  for a in "$@"; do
    case $a in
      down_eth)  nw_down "$ETH" ;;
      up_eth)    nw_up "$ETH" ;;
      down_wifi) nw_down "$WLAN" ;;
      up_wifi)   nw_up "$WLAN"; NW_WIFI_SINCE=$(date +%s) ;;
    esac
  done
}

nw_set_state() { NW_STATE=$1; printf '%s\n' "$1" > "$STATE_FILE" 2> /dev/null || true; }

# One evaluation of the state machine. $1=1 skips the debounce (start-up).
nw_tick() {
  local immediate=${1:-0} ec wd wc decision new actions now
  now=$(date +%s)
  ec=$(nw_carrier "$ETH")
  nw_wifi_defined && wd=1 || wd=0
  wc=$(nw_carrier "$WLAN")
  # Right after ifup, association takes a few seconds: treat the link as up during the grace period.
  if [[ $NW_STATE == WIFI ]] && (( now - NW_WIFI_SINCE < WIFI_GRACE )); then wc=1; fi
  decision=$(nw_decide "$NW_STATE" "$ec" "$wd" "$wc")
  new=${decision%% *}
  actions=${decision#"$new"}
  [[ $new == "$NW_STATE" ]] && return 0
  if [[ $NW_STATE == NONE && $new == WIFI ]]; then
    (( now - NW_LAST_RETRY >= RETRY )) || return 0
    NW_LAST_RETRY=$now
  fi
  if (( ! immediate )) && [[ $actions == *eth* ]]; then
    sleep "$DEBOUNCE"
    [[ $(nw_carrier "$ETH") == "$ec" ]] || return 0
  fi
  log "state $NW_STATE -> $new (eth carrier=$ec, wifi defined=$wd, wlan carrier=$wc)"
  # shellcheck disable=SC2086
  nw_do $actions
  if [[ $new == WIFI ]] && ! nw_configured "$WLAN"; then
    log "$WLAN did not come up; will retry in ${RETRY}s"
    nw_set_state NONE
    return 0
  fi
  nw_set_state "$new"
}

nw_startup() {
  ip link set "$ETH" up 2> /dev/null || true
  if nw_configured "$ETH"; then NW_STATE=ETH
  elif nw_configured "$WLAN"; then NW_STATE=WIFI; NW_WIFI_SINCE=$(date +%s)
  else NW_STATE=NONE; fi
  log "startup: ifupdown reports $NW_STATE"
  nw_tick 1
  nw_set_state "$NW_STATE"
}

nw_run() {
  nw_startup
  systemd-notify --ready 2> /dev/null || true
  exec 3< <(ip monitor link 2> /dev/null)
  local rc
  while true; do
    read -r -t "$POLL" -u 3 _; rc=$?
    (( rc == 0 || rc > 128 )) || sleep "$POLL"
    nw_tick 0
  done
}

nw_simulate() {
  case ${1:-} in
    eth0-down) echo 0 > "$SIM_FILE"; echo "simulating: $ETH carrier lost (takes effect within $((POLL + DEBOUNCE))s)" ;;
    eth0-up)   echo 1 > "$SIM_FILE"; echo "simulating: $ETH carrier present" ;;
    off)       rm -f "$SIM_FILE"; echo "simulation off; using the real carrier state" ;;
    *)         echo "usage: dxberry-netwatch --simulate eth0-down|eth0-up|off" >&2; return 2 ;;
  esac
}

main() {
  case ${1:-run} in
    run)        nw_run ;;
    --simulate) nw_simulate "${2:-}" ;;
    --status)   cat "$STATE_FILE" 2> /dev/null || echo unknown ;;
    -h|--help)  echo "usage: dxberry-netwatch [run | --simulate eth0-down|eth0-up|off | --status]" ;;
    *)          echo "unknown argument: $1" >&2; exit 2 ;;
  esac
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
```

- [ ] **Step 4: Run to verify it passes**

Run: `chmod +x provision/bin/dxberry-netwatch && tests/run.sh`
Expected: `34 tests, 0 failures`

- [ ] **Step 5: Commit**

```bash
git add provision/bin/dxberry-netwatch tests/test_netwatch.sh
git commit -m "Add dxberry-netwatch failover daemon"
```

---

### Task 7: `system.sh`, `storage.sh`, `scrub.sh`

**Files:**
- Create: `provision/lib/system.sh`, `provision/lib/storage.sh`, `provision/lib/scrub.sh`, `provision/templates/journald-dxberry.conf`, `tests/test_system_storage_scrub.sh`

**Interfaces:**
- Consumes: `DXB_CFG`, `DXB_MODE`, `dxb_set_kv`, `dxb_write_if_changed`, `dxb_boot_dir`, logging/status.
- Produces: `provision_system`, `provision_storage`, `provision_scrub`, `dxb_sys_add_authorized_key HOME KEY [OWNER]`, `dxb_sys_set_hostname NAME`, `dxb_scrub_key FILE KEY`. Env overrides: `DXB_HOSTNAME_FILE`, `DXB_HOSTS_FILE`, `DXB_LOCALTIME`, `DXB_TIMEZONE_FILE`, `DXB_ZONEINFO_DIR`, `DXB_DIETPI_FUNC`, `DXB_DIETPI_TXT`, `DXB_JOURNALD_DROPIN`.

- [ ] **Step 1: Write the template**

`provision/templates/journald-dxberry.conf`:

```
# Installed by DXBerry-Pi: keep the journal in RAM to spare the SD card / USB drive.
[Journal]
Storage=volatile
RuntimeMaxUse=32M
```

- [ ] **Step 2: Write the failing tests**

`tests/test_system_storage_scrub.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/system.sh"
source "$DXB_LIB/storage.sh"
source "$DXB_LIB/scrub.sh"

sys_env() {
  export DXB_HOSTNAME_FILE=$TEST_TMP/hostname DXB_HOSTS_FILE=$TEST_TMP/hosts DXB_LOCALTIME=$TEST_TMP/localtime \
    DXB_TIMEZONE_FILE=$TEST_TMP/timezone DXB_ZONEINFO_DIR=$TEST_TMP/zi DXB_DIETPI_FUNC=$TEST_TMP/nofunc \
    DXB_DIETPI_TXT=$TEST_TMP/dietpi.txt DXB_JOURNALD_DROPIN=$TEST_TMP/journald.d/dxberry.conf \
    DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_LOG_FILE=$TEST_TMP/log DXB_MODE=run
  mkdir -p "$DXB_ZONEINFO_DIR/America" "$DXB_BOOT_DIR"; : > "$DXB_ZONEINFO_DIR/America/Chicago"; : > "$DXB_ZONEINFO_DIR/UTC"
  echo DietPi > "$DXB_HOSTNAME_FILE"; printf '127.0.0.1 localhost\n127.0.1.1 DietPi\n' > "$DXB_HOSTS_FILE"
  printf 'CONFIG_SERIAL_CONSOLE_ENABLE=0\nAUTO_SETUP_GLOBAL_PASSWORD=dietpi\n' > "$DXB_DIETPI_TXT"
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  chpasswd() { cat >> "$TEST_TMP/chpasswd"; }
  swapon() { echo "NAME"; echo "/dev/zram0"; }
  hostname() { :; }
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
sys_cfg() { printf '%s\n' "$@" > "$DXB_BOOT_DIR/dxberry.txt"; dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"; dxb_config_validate; }

test_system_sets_hostname_timezone_password_and_key() {
  sys_env
  sys_cfg 'PASSWORD=secretpass' 'HOSTNAME=station' 'TIMEZONE=America/Chicago' 'SSH_PUBKEY=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample+key/here= me@host'
  export DXB_SSH_HOMES="$TEST_TMP/root:root $TEST_TMP/home/dietpi:dietpi"
  mkdir -p "$TEST_TMP/root" "$TEST_TMP/home/dietpi"
  chown() { :; }
  provision_system
  assert_eq "$(cat "$DXB_HOSTNAME_FILE")" "station"
  assert_file_contains "$DXB_HOSTS_FILE" "127.0.1.1 station"
  assert_eq "$(readlink "$DXB_LOCALTIME")" "$DXB_ZONEINFO_DIR/America/Chicago"
  assert_eq "$(cat "$DXB_TIMEZONE_FILE")" "America/Chicago"
  assert_eq "$(cat "$TEST_TMP/chpasswd")" $'root:secretpass\ndietpi:secretpass'
  assert_file_contains "$TEST_TMP/root/.ssh/authorized_keys" "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample+key/here= me@host"
  assert_file_contains "$TEST_TMP/home/dietpi/.ssh/authorized_keys" "ssh-ed25519"
  provision_system
  assert_eq "$(grep -c ssh-ed25519 "$TEST_TMP/root/.ssh/authorized_keys")" "1"
  assert_eq "$(stat -c %a "$TEST_TMP/root/.ssh/authorized_keys")" "600"
}

test_system_skips_password_when_applied_or_first_boot() {
  sys_env
  sys_cfg 'PASSWORD=<applied>'
  provision_system
  [[ -f $TEST_TMP/chpasswd ]] && _fail "chpasswd must not run for <applied>"
  sys_cfg 'PASSWORD=secretpass'
  DXB_MODE=first-boot provision_system
  [[ -f $TEST_TMP/chpasswd ]] && _fail "chpasswd must not run on first boot (DietPi applied it)"
}

test_system_serial_console_updates_dietpi_txt_on_rerun() {
  sys_env
  sys_cfg 'PASSWORD=<applied>' 'SERIAL_CONSOLE=on'
  provision_system
  assert_file_contains "$DXB_DIETPI_TXT" "CONFIG_SERIAL_CONSOLE_ENABLE=1"
}

test_storage_writes_journald_dropin_once_and_reports_zram() {
  sys_env
  provision_storage
  assert_file_contains "$DXB_JOURNALD_DROPIN" "Storage=volatile"
  assert_file_contains "$TEST_TMP/calls" "systemctl restart systemd-journald"
  assert_contains "${DXB_STATUS_LINES[*]}" "swap: zram"
  : > "$TEST_TMP/calls"
  provision_storage
  assert_file_not_contains "$TEST_TMP/calls" "restart systemd-journald"
}

test_scrub_replaces_secrets_in_place_and_dietpi_password() {
  sys_env
  printf 'HOSTNAME=x\r\nPASSWORD = "secretpass"\r\nWIFI_SSID=Home\r\nWIFI_PASSWORD=wifipass1\r\nWEBUI_PASSWORD=<applied>\r\n' > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"; DXB_CFG[WIFI_COUNTRY]=US; dxb_config_validate
  provision_scrub
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=<applied>"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_PASSWORD=<applied>"
  assert_file_not_contains "$DXB_BOOT_DIR/dxberry.txt" "secretpass"
  assert_file_not_contains "$DXB_BOOT_DIR/dxberry.txt" "wifipass1"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "HOSTNAME=x"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD="
  assert_file_not_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=dietpi"
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `tests/run.sh`
Expected: `system.sh: No such file` and FAIL lines.

- [ ] **Step 4: Implement `provision/lib/system.sh`**

```bash
#!/bin/bash
# shellcheck shell=bash
# System settings: hostname, time zone, login password, serial console, SSH keys.

: "${DXB_HOSTNAME_FILE:=/etc/hostname}"
: "${DXB_HOSTS_FILE:=/etc/hosts}"
: "${DXB_LOCALTIME:=/etc/localtime}"
: "${DXB_TIMEZONE_FILE:=/etc/timezone}"
: "${DXB_ZONEINFO_DIR:=/usr/share/zoneinfo}"
: "${DXB_DIETPI_FUNC:=/boot/dietpi/func}"
: "${DXB_DIETPI_TXT:=/boot/dietpi.txt}"
: "${DXB_SSH_HOMES:=/root:root /home/dietpi:dietpi}"

dxb_sys_set_hostname() {
  local name=$1
  if [[ -x $DXB_DIETPI_FUNC/change_hostname ]]; then
    "$DXB_DIETPI_FUNC/change_hostname" "$name" > /dev/null 2>&1 && return 0
  fi
  printf '%s\n' "$name" > "$DXB_HOSTNAME_FILE"
  if grep -q '^127\.0\.1\.1[[:blank:]]' "$DXB_HOSTS_FILE" 2> /dev/null; then
    n="$name" awk '{ if ($1 == "127.0.1.1") print "127.0.1.1 " ENVIRON["n"]; else print }' "$DXB_HOSTS_FILE" > "$DXB_HOSTS_FILE.dxbtmp" && mv "$DXB_HOSTS_FILE.dxbtmp" "$DXB_HOSTS_FILE"
  else
    printf '127.0.1.1 %s\n' "$name" >> "$DXB_HOSTS_FILE"
  fi
  hostname "$name" 2> /dev/null || true
}

# dxb_sys_add_authorized_key HOME KEY [OWNER]: idempotent append with safe permissions.
dxb_sys_add_authorized_key() {
  local home=$1 key=$2 owner=${3:-root} f="$1/.ssh/authorized_keys"
  [[ -d $home ]] || return 0
  mkdir -p "$home/.ssh"; chmod 700 "$home/.ssh"
  [[ -f $f ]] || : > "$f"
  chmod 600 "$f"
  grep -qxF -- "$key" "$f" || printf '%s\n' "$key" >> "$f"
  chown -R "$owner:" "$home/.ssh" 2> /dev/null || true
}

dxb_sys_serial_console() {
  local want cur
  [[ ${DXB_CFG[SERIAL_CONSOLE]} == on ]] && want=1 || want=0
  cur=$(sed -n 's/^[[:blank:]]*CONFIG_SERIAL_CONSOLE_ENABLE=//p' "$DXB_DIETPI_TXT" 2> /dev/null | head -1)
  [[ $cur == "$want" ]] && return 0
  if [[ -x $DXB_DIETPI_FUNC/dietpi-set_hardware ]]; then
    if (( want )); then "$DXB_DIETPI_FUNC/dietpi-set_hardware" serialconsole enable > /dev/null 2>&1
    else "$DXB_DIETPI_FUNC/dietpi-set_hardware" serialconsole disable > /dev/null 2>&1; fi
  fi
  dxb_set_kv "$DXB_DIETPI_TXT" CONFIG_SERIAL_CONSOLE_ENABLE "$want"
  dxb_info "serial console $( (( want )) && echo enabled || echo disabled )"
}

provision_system() {
  local tz=${DXB_CFG[TIMEZONE]} entry home owner
  if [[ $(cat "$DXB_HOSTNAME_FILE" 2> /dev/null) != "${DXB_CFG[HOSTNAME]}" ]]; then
    dxb_sys_set_hostname "${DXB_CFG[HOSTNAME]}"
    dxb_info "hostname set to ${DXB_CFG[HOSTNAME]}"
  fi
  if [[ -f $DXB_ZONEINFO_DIR/$tz ]]; then
    if [[ $(readlink "$DXB_LOCALTIME" 2> /dev/null) != "$DXB_ZONEINFO_DIR/$tz" ]]; then
      ln -sfn "$DXB_ZONEINFO_DIR/$tz" "$DXB_LOCALTIME"
      printf '%s\n' "$tz" > "$DXB_TIMEZONE_FILE"
      dxb_info "time zone set to $tz"
    fi
  else
    dxb_step_failed system "time zone $tz not found under $DXB_ZONEINFO_DIR"
  fi
  if [[ ${DXB_CFG[PASSWORD]} != "$DXB_APPLIED" && ${DXB_MODE:-run} != first-boot ]]; then
    printf 'root:%s\n' "${DXB_CFG[PASSWORD]}" | chpasswd
    printf 'dietpi:%s\n' "${DXB_CFG[PASSWORD]}" | chpasswd
    dxb_info "login password updated for root and dietpi"
  fi
  [[ ${DXB_MODE:-run} == first-boot ]] || dxb_sys_serial_console
  if [[ -n ${DXB_CFG[SSH_PUBKEY]:-} ]]; then
    for entry in $DXB_SSH_HOMES; do
      home=${entry%%:*}; owner=${entry#*:}
      dxb_sys_add_authorized_key "$home" "${DXB_CFG[SSH_PUBKEY]}" "$owner"
    done
    dxb_info "SSH public key installed"
  fi
  dxb_status_add "hostname: ${DXB_CFG[HOSTNAME]}"
  dxb_status_add "time zone: $tz"
  return 0
}
```

- [ ] **Step 5: Implement `provision/lib/storage.sh`**

```bash
#!/bin/bash
# shellcheck shell=bash
# Storage write-minimization (tier 1): journald in RAM, zram swap check.

: "${DXB_JOURNALD_DROPIN:=/etc/systemd/journald.conf.d/dxberry.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

provision_storage() {
  local content
  mkdir -p "$(dirname "$DXB_JOURNALD_DROPIN")"
  content=$(< "$DXB_TEMPLATES/journald-dxberry.conf")
  if dxb_write_if_changed "$DXB_JOURNALD_DROPIN" "$content"; then
    systemctl restart systemd-journald 2> /dev/null || true
    dxb_info "journald set to volatile storage"
  fi
  if swapon --show=NAME --noheadings 2> /dev/null | grep -q zram; then
    dxb_status_add "swap: zram"
  else
    dxb_warn "swap is not on zram; DietPi applies AUTO_SETUP_SWAPFILE_LOCATION=zram at first boot, or change it in dietpi-config"
    dxb_status_add "swap: not zram (see provision.log)"
  fi
  dxb_status_add "logs: RAM only (journald volatile, DietPi RAMlog)"
  return 0
}
```

- [ ] **Step 6: Implement `provision/lib/scrub.sh`**

```bash
#!/bin/bash
# shellcheck shell=bash
# Replace applied secrets with <applied> so plaintext never lingers on the FAT partition.

: "${DXB_DIETPI_TXT:=/boot/dietpi.txt}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"

# dxb_scrub_key FILE KEY: rewrite every "KEY = value" line as "KEY=<applied>".
dxb_scrub_key() {
  local file=$1
  k="$2" awk '{ if ($0 ~ ("^[[:blank:]]*" ENVIRON["k"] "[[:blank:]]*=")) print ENVIRON["k"] "=<applied>"; else print }' "$file" > "$file.dxbtmp" && mv "$file.dxbtmp" "$file"
}

provision_scrub() {
  local cfg k v
  cfg="$(dxb_boot_dir)/dxberry.txt"
  for k in $DXB_SECRET_KEYS; do
    v=${DXB_CFG[$k]:-}
    [[ -n $v && $v != "$DXB_APPLIED" ]] || continue
    dxb_scrub_key "$cfg" "$k"
    DXB_CFG[$k]=$DXB_APPLIED
  done
  if [[ -f $DXB_DIETPI_TXT ]]; then
    v=$(sed -n 's/^[[:blank:]]*AUTO_SETUP_GLOBAL_PASSWORD=//p' "$DXB_DIETPI_TXT" | head -1)
    [[ -z $v ]] || dxb_set_kv "$DXB_DIETPI_TXT" AUTO_SETUP_GLOBAL_PASSWORD ''
  fi
  if [[ -f $DXB_DIETPI_WIFI ]] && grep -qE "^aWIFI_KEY\[[0-9]\]='.+'" "$DXB_DIETPI_WIFI"; then
    dxb_warn "$DXB_DIETPI_WIFI still held a WiFi key; removed it"
    rm -f "$DXB_DIETPI_WIFI"
  fi
  dxb_status_add "secrets: scrubbed from dxberry.txt"
  return 0
}
```

- [ ] **Step 7: Run to verify it passes**

Run: `tests/run.sh`
Expected: `39 tests, 0 failures`

- [ ] **Step 8: Commit**

```bash
git add provision/lib/system.sh provision/lib/storage.sh provision/lib/scrub.sh provision/templates/journald-dxberry.conf tests/test_system_storage_scrub.sh
git commit -m "Provision system settings, storage tweaks and secret scrubbing"
```

---

### Task 8: `graywolf.sh` — download, verify, install, seed

**Files:**
- Create: `provision/lib/graywolf.sh`, `tests/test_graywolf.sh`

**Interfaces:**
- Consumes: `DXB_CFG`, `DXB_STATE_DIR`, logging/status.
- Produces: `dxb_gw_install` (0 on installed/already current), `dxb_gw_seed RESEED(0|1)`, `dxb_gw_installed_version`, pure helpers `dxb_gw_release_base`, `dxb_gw_pick_deb ARCH` (stdin: checksums.txt → `sha name`), payload builders `dxb_gw_payload_station|igate|beacon|digi`, `dxb_gw_payload_rule CHANNEL ALIAS TYPE MAX_HOPS PRIORITY`. Test hooks: `DXB_CURL` (command or function used for every HTTP call), `DXB_GW_API`, `DXB_GW_RELEASES`, `DXB_GW_COOKIES`, `DXB_GW_SEED_STATE`, `DXB_DPKG_ARCH`.

- [ ] **Step 1: Write the failing tests**

`tests/test_graywolf.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"

gw_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GW_COOKIES=$TEST_TMP/cookies \
    DXB_GW_SEED_STATE=$TEST_TMP/state/graywolf-seed.env DXB_GW_API=http://gw/api DXB_GW_RELEASES=http://rel DXB_DPKG_ARCH=arm64 DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/http"
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  DXB_CURL=fake_curl
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  sleep() { :; }
  GW_NEEDS_SETUP=true
}
# Fake curl: records "METHOD PATH BODY" per call and answers from canned responses.
fake_curl() {
  local url='' m=GET data='' a
  while (( $# )); do
    case $1 in -X) m=$2; shift ;; --data) data=$2; shift ;; http*) url=$1 ;; esac
    shift
  done
  if [[ $url == "$DXB_GW_RELEASES"* ]]; then
    local f=$TEST_TMP/http/${url##*/}
    [[ -f $f ]] || return 22
    cat "$f"; return 0
  fi
  local p=${url#"$DXB_GW_API"}
  echo "$m $p $data" >> "$TEST_TMP/calls"
  case "$m $p" in
    "GET /auth/setup")    echo "{\"needs_setup\":$GW_NEEDS_SETUP}" ;;
    "POST /beacons")      echo '{"id":7}' ;;
    "GET /igate/config")  echo '{"id":1,"server":"old.example","enabled":false,"read_only_thing":"keep"}' ;;
    "GET /beacons/7")     echo '{"id":7,"comment":"old","enabled":true}' ;;
    "GET /channels")      echo "${GW_CHANNELS:-[]}" ;;
    "GET /digipeater/rules") echo '[]' ;;
    *)                    echo '{}' ;;
  esac
}
gw_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }
calls() { cat "$TEST_TMP/calls"; }

test_release_base_latest_and_pinned() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  assert_eq "$(dxb_gw_release_base)" "http://rel/latest/download"
  gw_cfg 'PASSWORD=secretpass' 'GRAYWOLF_VERSION=v0.14.13'
  assert_eq "$(dxb_gw_release_base)" "http://rel/download/v0.14.13"
}

test_pick_deb_by_architecture() {
  local sums=$'aaa  graywolf_0.14.13_amd64.deb\nbbb  graywolf_0.14.13_arm64.deb\nccc  graywolf_0.14.13_linux_arm64.tar.gz'
  assert_eq "$(printf '%s\n' "$sums" | dxb_gw_pick_deb arm64)" "bbb graywolf_0.14.13_arm64.deb"
  assert_eq "$(printf '%s\n' "$sums" | dxb_gw_pick_deb armhf)" ""
}

test_install_downloads_verifies_and_installs() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  echo "deb-bytes" > "$TEST_TMP/http/graywolf_0.14.13_arm64.deb"
  printf '%s  graywolf_0.14.13_arm64.deb\n' "$(sha256sum "$TEST_TMP/http/graywolf_0.14.13_arm64.deb" | cut -d' ' -f1)" > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { return 1; }
  assert_ok dxb_gw_install
  assert_contains "$(calls)" "apt-get install -y"
  assert_contains "$(calls)" "graywolf_0.14.13_arm64.deb"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
}

test_install_rejects_checksum_mismatch_and_missing_release() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  echo "deb-bytes" > "$TEST_TMP/http/graywolf_0.14.13_arm64.deb"
  echo "0000000000000000000000000000000000000000000000000000000000000000  graywolf_0.14.13_arm64.deb" > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { return 1; }
  assert_fails dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[0]}" "checksum mismatch"
  assert_not_contains "$(calls)" "apt-get"
  rm "$TEST_TMP/http/checksums.txt"; DXB_FAILED_STEPS=()
  assert_fails dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[0]}" "could not download checksums.txt"
}

test_install_skips_when_current() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  printf 'x  graywolf_0.14.13_arm64.deb\n' > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { echo "0.14.13"; }
  assert_ok dxb_gw_install
  assert_not_contains "$(calls)" "apt-get"
}

test_seed_fresh_install_creates_admin_station_igate_beacon_digi() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=fillin' 'IGATE_SERVER=noam.aprs2.net' 'BEACON_INTERVAL_MIN=10' 'BEACON_COMMENT=hi'
  assert_ok dxb_gw_seed 0
  local c; c=$(calls)
  assert_contains "$c" 'POST /auth/setup {"username":"admin","password":"secretpass"}'
  assert_contains "$c" 'POST /auth/login {"username":"admin","password":"secretpass"}'
  assert_contains "$c" 'PUT /station/config {"callsign":"N0CALL-2"}'
  assert_contains "$c" '"server":"noam.aprs2.net"'
  assert_contains "$c" '"read_only_thing":"keep"'
  assert_contains "$c" '"gate_rf_to_is":true,"gate_is_to_rf":false'
  assert_contains "$c" 'POST /beacons {"type":"position","latitude":37.1,"longitude":-101.3,"comment":"hi","interval":600,"send_path":"is_only","path":"WIDE1-1,WIDE2-1","symbol_table":"R","symbol":"&","enabled":true}'
  assert_contains "$c" 'PUT /digipeater {"enabled":true,"my_call":"N0CALL-2","dedupe_window_seconds":30}'
  assert_not_contains "$c" "/digipeater/rules {"
  assert_contains "$c" "POST /auth/logout"
  assert_file_contains "$DXB_GW_SEED_STATE" "BEACON_ID=7"
  assert_contains "${DXB_STATUS_LINES[*]}" "digipeater rules pending"
  assert_eq "$(stat -c %a "$DXB_GW_SEED_STATE")" "600"
}

test_seed_skips_when_already_set_up_unless_reseed() {
  gw_env
  GW_NEEDS_SETUP=false
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2'
  assert_ok dxb_gw_seed 0
  assert_not_contains "$(calls)" "/auth/login"
  assert_ok dxb_gw_seed 1
  assert_contains "$(calls)" "/auth/login"
  assert_contains "$(calls)" "PUT /station/config"
}

test_reseed_updates_existing_beacon_and_creates_rules_when_channel_exists() {
  gw_env
  GW_NEEDS_SETUP=false
  GW_CHANNELS='[{"id":3,"name":"VHF"}]'
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=wide' 'BEACON_SEND=rf'
  echo "BEACON_ID=7" > "$DXB_GW_SEED_STATE"
  assert_ok dxb_gw_seed 1
  local c; c=$(calls)
  assert_contains "$c" 'PUT /beacons/7 {"id":7,"comment":"DXBerry-Pi iGate","enabled":true,"type":"position"'
  assert_contains "$c" '"send_path":"rf"'
  assert_contains "$c" '"channel":3'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"N0CALL-2","alias_type":"exact","max_hops":1,"priority":1,"action":"repeat","enabled":true}'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"WIDE","alias_type":"widen","max_hops":2,"priority":10,"action":"repeat","enabled":true}'
  assert_file_contains "$DXB_GW_SEED_STATE" "RULES_SEEDED=1"
}

test_seed_rf_beacon_without_channel_falls_back_to_is_only() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'BEACON_SEND=both'
  assert_ok dxb_gw_seed 0
  assert_contains "$(calls)" '"send_path":"is_only"'
  assert_contains "${DXB_STATUS_LINES[*]}" "APRS-IS only until a radio channel exists"
}

test_seed_without_callsign_only_creates_admin() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  assert_ok dxb_gw_seed 0
  assert_contains "$(calls)" "POST /auth/setup"
  assert_not_contains "$(calls)" "/station/config"
  assert_contains "${DXB_STATUS_LINES[*]}" "no CALLSIGN"
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: `graywolf.sh: No such file` and FAIL lines.

- [ ] **Step 3: Implement `provision/lib/graywolf.sh`**

```bash
#!/bin/bash
# shellcheck shell=bash
# Graywolf: install the latest release (checksum-verified) and seed it through its REST API.

: "${DXB_GW_API:=http://127.0.0.1:8080/api}"
: "${DXB_GW_RELEASES:=https://github.com/chrissnell/graywolf/releases}"
: "${DXB_GW_COOKIES:=/run/dxberry-graywolf.cookies}"
: "${DXB_GW_SEED_STATE:=$DXB_STATE_DIR/graywolf-seed.env}"
: "${DXB_CURL:=curl}"

dxb_gw_release_base() {
  local v=${DXB_CFG[GRAYWOLF_VERSION]:-}
  if [[ -n $v ]]; then printf '%s/download/%s' "$DXB_GW_RELEASES" "$v"; else printf '%s/latest/download' "$DXB_GW_RELEASES"; fi
}

# stdin: checksums.txt; $1: dpkg architecture -> "sha256 filename" of the matching .deb
dxb_gw_pick_deb() { awk -v a="$1" '$2 ~ ("^graywolf_[0-9.]+_" a "\\.deb$") { print $1, $2; exit }'; }

dxb_gw_installed_version() { dpkg-query -W -f '${Version}' graywolf 2> /dev/null || true; }

dxb_gw_fetch() {
  local i
  for i in 1 2 3; do
    "$DXB_CURL" -fsSL --connect-timeout 15 "$1" && return 0
    sleep $(( i * 5 ))
  done
  return 1
}

dxb_gw_install() {
  local base arch sums line sha name tmp v
  base=$(dxb_gw_release_base)
  arch=${DXB_DPKG_ARCH:-$(dpkg --print-architecture)}
  sums=$(dxb_gw_fetch "$base/checksums.txt") || { dxb_step_failed graywolf "could not download checksums.txt from $base"; return 1; }
  line=$(printf '%s\n' "$sums" | dxb_gw_pick_deb "$arch")
  [[ -n $line ]] || { dxb_step_failed graywolf "release has no .deb for architecture $arch"; return 1; }
  sha=${line%% *}; name=${line#* }
  v=$(sed -E 's/^graywolf_([0-9.]+)_.*/\1/' <<< "$name")
  if [[ $(dxb_gw_installed_version) == "$v" ]]; then dxb_info "graywolf $v already installed"; return 0; fi
  tmp=$(mktemp -d)
  if ! dxb_gw_fetch "$base/$name" > "$tmp/$name"; then dxb_step_failed graywolf "download of $name failed"; rm -rf "$tmp"; return 1; fi
  if [[ $(sha256sum "$tmp/$name" | cut -d' ' -f1) != "$sha" ]]; then dxb_step_failed graywolf "checksum mismatch for $name"; rm -rf "$tmp"; return 1; fi
  if ! DEBIAN_FRONTEND=noninteractive apt-get install -y "$tmp/$name" > /dev/null 2>&1; then dxb_step_failed graywolf "apt-get install of $name failed"; rm -rf "$tmp"; return 1; fi
  rm -rf "$tmp"
  dxb_info "installed graywolf $v"
  return 0
}

# ---- API client ----------------------------------------------------------------------------
dxb_gw_api() {
  local m=$1 p=$2 data=${3:-}
  local args=(-fsS -X "$m" -b "$DXB_GW_COOKIES" -c "$DXB_GW_COOKIES" -H 'Content-Type: application/json' "$DXB_GW_API$p")
  [[ -n $data ]] && args+=(--data "$data")
  "$DXB_CURL" "${args[@]}"
}
dxb_gw_wait_ready() { local i; for i in $(seq 1 60); do "$DXB_CURL" -fsS "$DXB_GW_API/auth/setup" > /dev/null 2>&1 && return 0; sleep 2; done; return 1; }
dxb_gw_needs_setup() { dxb_gw_api GET /auth/setup | jq -e '.needs_setup == true' > /dev/null; }

_dxb_bool() { [[ $1 == on ]] && echo true || echo false; }
dxb_gw_payload_station() { jq -cn --arg c "${DXB_CFG[CALLSIGN]}" '{callsign: $c}'; }
dxb_gw_payload_igate() {
  jq -cn --arg s "${DXB_CFG[IGATE_SERVER]}" --argjson r "$(_dxb_bool "${DXB_CFG[IGATE_RF_TO_IS]}")" --argjson t "$(_dxb_bool "${DXB_CFG[IGATE_IS_TO_RF]}")" \
    '{enabled: true, server: $s, port: 14580, gate_rf_to_is: $r, gate_is_to_rf: $t}'
}
# dxb_gw_payload_beacon SEND_PATH [CHANNEL_ID]
dxb_gw_payload_beacon() {
  local sp=$1 ch=${2:-}
  jq -cn --arg lat "${DXB_CFG[LATITUDE]}" --arg lon "${DXB_CFG[LONGITUDE]}" --arg c "${DXB_CFG[BEACON_COMMENT]}" \
    --argjson i "${DXB_CFG[_INTERVAL_S]}" --arg sp "$sp" --arg p "${DXB_CFG[BEACON_PATH]}" \
    --arg st "${DXB_CFG[_SYMBOL_TABLE]}" --arg sy "${DXB_CFG[_SYMBOL]}" --arg ch "$ch" \
    '{type: "position", latitude: ($lat | tonumber), longitude: ($lon | tonumber), comment: $c, interval: $i, send_path: $sp, path: $p, symbol_table: $st, symbol: $sy, enabled: true} + (if $ch == "" then {} else {channel: ($ch | tonumber)} end)'
}
dxb_gw_payload_digi() { jq -cn --arg c "${DXB_CFG[CALLSIGN]}" '{enabled: true, my_call: $c, dedupe_window_seconds: 30}'; }
# dxb_gw_payload_rule CHANNEL ALIAS TYPE MAX_HOPS PRIORITY
dxb_gw_payload_rule() {
  jq -cn --argjson ch "$1" --arg a "$2" --arg t "$3" --argjson h "$4" --argjson p "$5" \
    '{from_channel: $ch, to_channel: $ch, alias: $a, alias_type: $t, max_hops: $h, priority: $p, action: "repeat", enabled: true}'
}

dxb_gw_seed_state_get() { sed -n "s/^$1=//p" "$DXB_GW_SEED_STATE" 2> /dev/null | head -1; }
dxb_gw_seed_state_set() { ( umask 077; [[ -f $DXB_GW_SEED_STATE ]] || : > "$DXB_GW_SEED_STATE" ); dxb_set_kv "$DXB_GW_SEED_STATE" "$1" "$2"; chmod 600 "$DXB_GW_SEED_STATE"; }
dxb_gw_first_channel() { dxb_gw_api GET /channels 2> /dev/null | jq -r 'if type == "array" and length > 0 then .[0].id else "" end'; }

dxb_gw_login() {
  local pw=${DXB_CFG[WEBUI_PASSWORD]}
  if [[ $pw == "$DXB_APPLIED" ]]; then
    read -rsp "Graywolf password for ${DXB_CFG[WEBUI_USER]}: " pw < /dev/tty; echo
  fi
  dxb_gw_api POST /auth/login "$(jq -cn --arg u "${DXB_CFG[WEBUI_USER]}" --arg p "$pw" '{username: $u, password: $p}')" > /dev/null \
    || { dxb_step_failed graywolf "login as ${DXB_CFG[WEBUI_USER]} failed"; return 1; }
}

dxb_gw_seed_igate() {
  local cur merged
  cur=$(dxb_gw_api GET /igate/config 2> /dev/null) || cur='{}'
  jq -e 'type == "object"' <<< "$cur" > /dev/null 2>&1 || cur='{}'
  merged=$(jq -c --argjson ours "$(dxb_gw_payload_igate)" '. + $ours' <<< "$cur")
  dxb_gw_api PUT /igate/config "$merged" > /dev/null || dxb_step_failed graywolf "iGate config update failed"
}

dxb_gw_seed_beacon() {
  local sp=${DXB_CFG[_SEND_PATH]} ch='' id cur merged
  if [[ $sp != is_only ]]; then
    ch=$(dxb_gw_first_channel)
    if [[ -z $ch ]]; then
      sp=is_only
      dxb_status_add "beacon: created as APRS-IS only until a radio channel exists (BEACON_SEND=${DXB_CFG[BEACON_SEND]} needs one)"
    fi
  fi
  id=$(dxb_gw_seed_state_get BEACON_ID)
  if [[ -n $id ]] && cur=$(dxb_gw_api GET "/beacons/$id" 2> /dev/null) && jq -e '.id' <<< "$cur" > /dev/null 2>&1; then
    merged=$(jq -c --argjson ours "$(dxb_gw_payload_beacon "$sp" "$ch")" '. + $ours' <<< "$cur")
    dxb_gw_api PUT "/beacons/$id" "$merged" > /dev/null || dxb_step_failed graywolf "beacon $id update failed"
  else
    id=$(dxb_gw_api POST /beacons "$(dxb_gw_payload_beacon "$sp" "$ch")" | jq -r '.id // empty')
    [[ -n $id ]] && dxb_gw_seed_state_set BEACON_ID "$id" || dxb_step_failed graywolf "beacon creation failed"
  fi
}

dxb_gw_seed_digi() {
  local ch hops
  dxb_gw_api PUT /digipeater "$(dxb_gw_payload_digi)" > /dev/null || dxb_step_failed graywolf "digipeater config update failed"
  [[ $(dxb_gw_seed_state_get RULES_SEEDED) == 1 ]] && return 0
  ch=$(dxb_gw_first_channel)
  if [[ -z $ch ]]; then
    dxb_status_add "digipeater: enabled; digipeater rules pending until a radio channel exists - run 'sudo dxberry-provision --reseed' after adding one, or pick the preset in the Digipeater page"
    return 0
  fi
  if [[ $(dxb_gw_api GET /digipeater/rules 2> /dev/null | jq 'if type == "array" then length else 0 end') != 0 ]]; then
    dxb_info "digipeater rules already exist; not adding preset rules"
    return 0
  fi
  [[ ${DXB_CFG[DIGIPEATER]} == wide ]] && hops=2 || hops=1
  dxb_gw_api POST /digipeater/rules "$(dxb_gw_payload_rule "$ch" "${DXB_CFG[CALLSIGN]}" exact 1 1)" > /dev/null || dxb_step_failed graywolf "digipeater rule creation failed"
  dxb_gw_api POST /digipeater/rules "$(dxb_gw_payload_rule "$ch" WIDE widen "$hops" 10)" > /dev/null || dxb_step_failed graywolf "digipeater rule creation failed"
  dxb_gw_seed_state_set RULES_SEEDED 1
  dxb_status_add "digipeater: ${DXB_CFG[DIGIPEATER]} preset rules created on channel $ch"
}

# dxb_gw_seed RESEED(0|1)
dxb_gw_seed() {
  local reseed=${1:-0}
  dxb_gw_wait_ready || { dxb_step_failed graywolf "API at $DXB_GW_API not reachable"; return 1; }
  rm -f "$DXB_GW_COOKIES"; ( umask 077; : > "$DXB_GW_COOKIES" )
  if dxb_gw_needs_setup; then
    [[ ${DXB_CFG[WEBUI_PASSWORD]} != "$DXB_APPLIED" ]] || { dxb_step_failed graywolf "admin password already scrubbed; set WEBUI_PASSWORD in dxberry.txt and re-run"; return 1; }
    dxb_gw_api POST /auth/setup "$(jq -cn --arg u "${DXB_CFG[WEBUI_USER]}" --arg p "${DXB_CFG[WEBUI_PASSWORD]}" '{username: $u, password: $p}')" > /dev/null \
      || { dxb_step_failed graywolf "creating admin ${DXB_CFG[WEBUI_USER]} failed"; return 1; }
    dxb_info "graywolf admin '${DXB_CFG[WEBUI_USER]}' created"
  elif (( ! reseed )); then
    dxb_info "graywolf already set up; not reseeding (use --reseed)"
    dxb_status_add "graywolf: already configured (not reseeded)"
    return 0
  fi
  dxb_gw_login || return 1
  if [[ -n ${DXB_CFG[CALLSIGN]:-} ]]; then
    dxb_gw_api PUT /station/config "$(dxb_gw_payload_station)" > /dev/null || dxb_step_failed graywolf "station callsign update failed"
    dxb_gw_seed_igate
    (( DXB_CFG[_BEACON] )) && dxb_gw_seed_beacon
    [[ ${DXB_CFG[DIGIPEATER]} == off ]] || dxb_gw_seed_digi
    dxb_status_add "graywolf: seeded station ${DXB_CFG[CALLSIGN]}, iGate ${DXB_CFG[IGATE_SERVER]}"
  else
    dxb_status_add "graywolf: installed, no CALLSIGN in dxberry.txt - finish station setup in the web UI"
  fi
  dxb_gw_api POST /auth/logout > /dev/null 2>&1 || true
  rm -f "$DXB_GW_COOKIES"
  return 0
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `tests/run.sh`
Expected: `50 tests, 0 failures`

- [ ] **Step 5: Commit**

```bash
git add provision/lib/graywolf.sh tests/test_graywolf.sh
git commit -m "Install and seed Graywolf through its REST API"
```

---

### Task 9: `dxberry-provision` driver and the boot-partition files

**Files:**
- Create: `provision/bin/dxberry-provision`, `provision/VERSION`, `boot/dietpi.overrides.txt`, `boot/dxberry.txt.example`, `boot/README-DXBERRY.txt`, `boot/Automation_Custom_PreScript.sh`, `boot/Automation_Custom_Script.sh`, `tests/test_provision.sh`

**Interfaces:**
- Consumes: every `provision_*` function, `dxb_gw_install`, `dxb_gw_seed`, `DXB_NET_CHANGED`.
- Produces: `dxberry-provision [--first-boot] [--reseed] [--check]`; sets `DXB_MODE` (`first-boot`|`run`) before calling modules; writes `<boot>/dxberry-status.txt` and `$DXB_STATE_DIR/status.txt`; `--first-boot` ends with `reboot`; a run that changed interface files restarts `dxberry-netwatch` last (detached). `dxberry.txt.example` must parse and validate once `PASSWORD` is filled in.

- [ ] **Step 1: Write the failing test**

`tests/test_provision.sh`:

```bash
#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"

test_example_config_validates_with_password_only() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  sed 's/^PASSWORD=.*/PASSWORD=examplepass/' "$DXB_ROOT/boot/dxberry.txt.example" > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_validate
  assert_eq "${#DXB_CFG_WARNINGS[@]}" "0"
  assert_eq "${DXB_CFG[HOSTNAME]}" "dxberry-pi"
  assert_eq "${DXB_CFG[_MODE]}" "dhcp"
}

test_example_config_documents_every_known_key() {
  local k
  for k in $DXB_KNOWN_KEYS; do
    grep -qE "^#? ?$k=" "$DXB_ROOT/boot/dxberry.txt.example" || _fail "dxberry.txt.example does not mention $k"
  done
}

test_provision_check_prints_masked_config_without_root() {
  export DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_ZONEINFO_DIR=$TEST_TMP/nozone DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_TEMPLATES=$DXB_ROOT/provision/templates
  mkdir -p "$DXB_BOOT_DIR"
  printf 'PASSWORD=secretpass\nSTATIC_IP=192.168.1.90/24\nGATEWAY=192.168.1.1\n' > "$DXB_BOOT_DIR/dxberry.txt"
  local out; out=$("$DXB_ROOT/provision/bin/dxberry-provision" --check)
  assert_eq "$?" "0"
  assert_contains "$out" "PASSWORD=********"
  assert_contains "$out" "mode: static"
  printf 'HOSTNAME=Bad\n' > "$DXB_BOOT_DIR/dxberry.txt"
  out=$("$DXB_ROOT/provision/bin/dxberry-provision" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "PASSWORD is required"
}

test_dietpi_overrides_are_key_value_lines() {
  local line
  while IFS= read -r line; do
    [[ -z $line || $line == \#* ]] && continue
    [[ $line =~ ^[A-Z_]+=.*$ ]] || _fail "bad override line: $line"
  done < "$DXB_ROOT/boot/dietpi.overrides.txt"
  assert_file_contains "$DXB_ROOT/boot/dietpi.overrides.txt" "AUTO_SETUP_AUTOMATED=1"
  assert_file_contains "$DXB_ROOT/boot/dietpi.overrides.txt" "AUTO_SETUP_SSH_SERVER_INDEX=-2"
  assert_file_contains "$DXB_ROOT/boot/dietpi.overrides.txt" "AUTO_SETUP_SWAPFILE_LOCATION=zram"
  assert_file_not_contains "$DXB_ROOT/boot/dietpi.overrides.txt" "AUTO_SETUP_GLOBAL_PASSWORD"
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: FAIL lines (files missing).

- [ ] **Step 3: Write the boot files**

`provision/VERSION`:

```
0.1.0
```

`boot/dietpi.overrides.txt`:

```
# Applied onto the stock DietPi dietpi.txt by build/build-image.sh. No secrets here:
# per-station values come from dxberry.txt through /opt/dxberry/bin/dxberry-preboot.
AUTO_SETUP_AUTOMATED=1
AUTO_SETUP_SSH_SERVER_INDEX=-2
AUTO_SETUP_LOGGING_INDEX=-1
AUTO_SETUP_SWAPFILE_SIZE=1
AUTO_SETUP_SWAPFILE_LOCATION=zram
AUTO_SETUP_LOCALE=en_US.UTF-8
AUTO_SETUP_KEYBOARD_LAYOUT=us
AUTO_SETUP_CUSTOM_SCRIPT_EXEC=0
AUTO_SETUP_APT_INSTALLS=curl ca-certificates jq wpasupplicant
AUTO_SETUP_NET_ETHERNET_ENABLED=1
AUTO_SETUP_NET_WIFI_ENABLED=0
AUTO_SETUP_NET_HOSTNAME=dxberry-pi
SURVEY_OPTED_IN=0
CONFIG_SERIAL_CONSOLE_ENABLE=0
```

`boot/README-DXBERRY.txt`:

```
DXBerry-Pi
1. Rename dxberry.txt.example to dxberry.txt and fill it in (Notepad is fine).
2. Put this drive in the Pi, connect Ethernet, power on. First boot takes 5-8 minutes.
3. Open http://<your STATIC_IP>:8080 (or the DHCP address). SSH: root or dietpi with your PASSWORD.
If something went wrong, this partition will contain dxberry-ERROR.txt or dxberry-status.txt explaining it.
```

`boot/dxberry.txt.example` (every known key appears, either set or as `# KEY=` so the test in Step 1 passes):

```
# DXBerry-Pi station configuration. Rename this file to dxberry.txt and fill it in.
# One KEY=value per line. Lines starting with # are ignored. Leave a value blank for its default.
# Passwords are replaced with <applied> after the first boot; put a new value to change them.

# ---------------------------------------------------------------- System
# Name on the network: lowercase letters, digits, hyphens.
HOSTNAME=dxberry-pi
# Login password for root and dietpi (8-100 characters). REQUIRED.
PASSWORD=
# Time zone, e.g. America/Chicago or Europe/Berlin. Default UTC.
TIMEZONE=UTC

# ---------------------------------------------------------------- Network
# Fixed address with prefix length. Leave blank to use DHCP.
# Ethernet is primary; WiFi (if set below) takes over the same address when the cable is unplugged.
STATIC_IP=
# Required when STATIC_IP is set.
GATEWAY=
# DNS servers, space separated. Default: the gateway.
DNS=
# WiFi network for failover. Leave WIFI_SSID blank for Ethernet only.
WIFI_SSID=
WIFI_PASSWORD=
# Two-letter country code, required with WIFI_SSID (US, GB, DE, ...).
WIFI_COUNTRY=

# ---------------------------------------------------------------- Station
# Your callsign with SSID, e.g. N0CALL-10. Leave blank to finish station setup in the web UI.
CALLSIGN=
# Decimal degrees; both or neither. Enables a position beacon.
LATITUDE=
LONGITUDE=
BEACON_COMMENT=DXBerry-Pi iGate
# Minutes between position beacons, 1-120.
BEACON_INTERVAL_MIN=30
# APRS-IS server. rotate.aprs2.net picks a nearby server; regional: noam.aprs2.net, euro.aprs2.net ...
IGATE_SERVER=rotate.aprs2.net

# ---------------------------------------------------------------- Graywolf web UI account
WEBUI_USER=admin
# Leave blank to reuse PASSWORD.
WEBUI_PASSWORD=

# ---------------------------------------------------------------- Advanced (blank = default)
# One OpenSSH public key to add for root and dietpi.
# SSH_PUBKEY=
# Where beacons go: is (APRS-IS only, no radio needed), rf, or both.
# BEACON_SEND=is
# Digipeater path used for rf/both.
# BEACON_PATH=WIDE1-1,WIDE2-1
# Two characters: symbol table/overlay + symbol. R& = receive-only iGate, I& = iGate, /# = digipeater.
# BEACON_SYMBOL=R&
# off, fillin (WIDE1-1 only, home stations) or wide (WIDE1-1 + WIDE2-2, high sites only).
# DIGIPEATER=off
# IGATE_RF_TO_IS=on
# IGATE_IS_TO_RF=off
# Pin a Graywolf release instead of installing the latest, e.g. v0.14.13.
# GRAYWOLF_VERSION=
# on keeps the console on the GPIO UART; off (default) leaves the UART free for a GPS.
# SERIAL_CONSOLE=off
```

`boot/Automation_Custom_PreScript.sh`:

```bash
#!/bin/bash
# DXBerry-Pi: runs on first boot before the network is up. See /opt/dxberry.
exec /opt/dxberry/bin/dxberry-preboot
```

`boot/Automation_Custom_Script.sh`:

```bash
#!/bin/bash
# DXBerry-Pi: runs once DietPi's first-run installs are done. See /opt/dxberry.
exec /opt/dxberry/bin/dxberry-provision --first-boot
```

- [ ] **Step 4: Implement `provision/bin/dxberry-provision`**

```bash
#!/bin/bash
# dxberry-provision: apply <boot>/dxberry.txt to this system. Idempotent; safe to re-run over SSH.
#   --first-boot   full run followed by a reboot (used by /boot/Automation_Custom_Script.sh)
#   --reseed       also push station/iGate/beacon/digipeater values into an already set-up Graywolf
#   --check        validate the config and print it with secrets masked; no changes, no root needed
set -uo pipefail

DXB_LIB=${DXB_LIB:-/opt/dxberry/lib}
for _m in common config system network storage graywolf scrub; do
  # shellcheck disable=SC1090
  source "$DXB_LIB/$_m.sh"
done

usage() { sed -n '2,5p' "$0" | sed 's/^# \{0,1\}//'; }

main() {
  local reseed=0 check=0 boot cfg w
  DXB_MODE=run
  for a in "$@"; do
    case $a in
      --first-boot) DXB_MODE=first-boot ;;
      --reseed) reseed=1 ;;
      --check) check=1 ;;
      -h|--help) usage; return 0 ;;
      *) echo "unknown option: $a" >&2; usage >&2; return 2 ;;
    esac
  done
  boot=$(dxb_boot_dir); cfg="$boot/dxberry.txt"
  if [[ ! -f $cfg ]]; then echo "no $cfg found; see $boot/README-DXBERRY.txt" >&2; return 1; fi
  dxb_config_load "$cfg"
  if ! dxb_config_validate; then
    echo "dxberry.txt has errors:" >&2
    printf '  %s\n' "${DXB_CFG_ERRORS[@]}" >&2
    return 1
  fi
  for w in "${DXB_CFG_WARNINGS[@]}"; do echo "warning: $w" >&2; done
  if (( check )); then
    dxb_config_print_masked
    echo "mode: ${DXB_CFG[_MODE]}  wifi: ${DXB_CFG[_WIFI]}  beacon: ${DXB_CFG[_BEACON]}  send: ${DXB_CFG[_SEND_PATH]}"
    return 0
  fi
  dxb_require_root
  if [[ $DXB_MODE == first-boot && -f $DXB_STATE_DIR/provisioned ]]; then
    dxb_info "first-boot provisioning already completed; nothing to do"
    return 0
  fi
  mkdir -p "$DXB_STATE_DIR"; chmod 700 "$DXB_STATE_DIR"
  : >> "$DXB_LOG_FILE"; chmod 600 "$DXB_LOG_FILE"
  dxb_info "dxberry-provision starting (mode=$DXB_MODE, reseed=$reseed)"
  for w in "${DXB_CFG_WARNINGS[@]}"; do dxb_warn "$w"; done
  dxb_status_add "image: $(sed -n 's/^DXBERRY_VERSION=//p' /etc/dxberry-release 2> /dev/null || echo unknown)"

  provision_system
  provision_network
  provision_storage
  command -v jq > /dev/null 2>&1 || DEBIAN_FRONTEND=noninteractive apt-get install -y jq > /dev/null 2>&1 || dxb_step_failed system "jq is not installed"
  if dxb_gw_install; then
    systemctl enable --now graywolf > /dev/null 2>&1 || dxb_step_failed graywolf "could not start graywolf.service"
    dxb_gw_seed "$reseed"
  fi
  dxb_status_add "graywolf: version $(dxb_gw_installed_version) at http://${DXB_CFG[_IP]:-<this-pi>}:8080 (user ${DXB_CFG[WEBUI_USER]})"
  provision_scrub

  dxb_status_write "$boot/dxberry-status.txt"
  cp "$boot/dxberry-status.txt" "$DXB_STATE_DIR/status.txt" 2> /dev/null || true
  touch "$DXB_STATE_DIR/provisioned"
  if (( ${#DXB_FAILED_STEPS[@]} )); then dxb_warn "finished with ${#DXB_FAILED_STEPS[@]} failed step(s); see $boot/dxberry-status.txt"
  else dxb_info "finished: all steps completed"; fi

  if [[ $DXB_MODE == first-boot ]]; then
    dxb_info "rebooting to start dxberry-netwatch and graywolf cleanly"
    sync
    reboot
  elif (( DXB_NET_CHANGED )); then
    dxb_warn "network configuration changed; restarting dxberry-netwatch in 3s (an SSH session may drop and can reconnect at the new address)"
    systemd-run --quiet --on-active=3 systemctl restart dxberry-netwatch 2> /dev/null || systemctl restart dxberry-netwatch
  fi
  return 0
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
```

- [ ] **Step 5: Run to verify it passes**

Run: `chmod +x provision/bin/dxberry-provision boot/*.sh && tests/run.sh`
Expected: `54 tests, 0 failures`

- [ ] **Step 6: Commit**

```bash
git add provision/bin/dxberry-provision provision/VERSION boot tests/test_provision.sh
git commit -m "Add provisioning driver and boot partition files"
```

---

### Task 10: `build/build-image.sh`

**Files:**
- Create: `build/build-image.sh`, `tests/test_build.sh`

**Interfaces:**
- Produces: `build/build-image.sh [--version V] [--dietpi-image PATH] [--check] [--keep-work]`. `--check` needs no root or network and verifies the tree. The build needs root, `losetup`, `xz`, `sha256sum`, `curl`, `partprobe`; writes `out/DXBerry-Pi-<version>-rpi234-arm64.img.xz` and `.sha256`. Reuses `dxb_set_kv` from `provision/lib/common.sh` to apply `boot/dietpi.overrides.txt`.

- [ ] **Step 1: Write the failing test**

`tests/test_build.sh`:

```bash
#!/usr/bin/env bash

test_build_check_passes_on_complete_tree() {
  local out; out=$("$DXB_ROOT/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "0"
  assert_contains "$out" "tree ok"
}

test_build_check_fails_on_missing_file() {
  cp -r "$DXB_ROOT/boot" "$DXB_ROOT/provision" "$DXB_ROOT/build" "$TEST_TMP/"
  rm "$TEST_TMP/provision/bin/dxberry-netwatch"
  local out; out=$("$TEST_TMP/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "missing provision/bin/dxberry-netwatch"
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `tests/run.sh`
Expected: FAIL lines (`build-image.sh: No such file`).

- [ ] **Step 3: Implement `build/build-image.sh`**

```bash
#!/usr/bin/env bash
# Build a DXBerry-Pi image: official DietPi image + /opt/dxberry + first-boot hooks. Needs root for loop mounts.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIETPI_URL=${DIETPI_URL:-https://dietpi.com/downloads/images/DietPi_RPi234-ARMv8-Trixie.img.xz}
WORK=$ROOT/build/work
OUT=$ROOT/out
VERSION=''
DIETPI_IMG=''
CHECK=0
KEEP=0
MNT=''
LOOP=''

usage() {
  cat << 'EOF'
usage: build/build-image.sh [--version V] [--dietpi-image PATH] [--check] [--keep-work]
  --version V         image version (default: git describe)
  --dietpi-image P    use an already downloaded DietPi .img.xz instead of downloading
  --check             verify the repository tree only (no root, no network)
  --keep-work         keep build/work after a successful build
EOF
}

while (( $# )); do
  case $1 in
    --version) VERSION=$2; shift ;;
    --dietpi-image) DIETPI_IMG=$2; shift ;;
    --check) CHECK=1 ;;
    --keep-work) KEEP=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

REQUIRED_FILES=(
  boot/dietpi.overrides.txt boot/dxberry.txt.example boot/README-DXBERRY.txt
  boot/Automation_Custom_PreScript.sh boot/Automation_Custom_Script.sh
  provision/VERSION provision/bin/dxberry-preboot provision/bin/dxberry-provision provision/bin/dxberry-netwatch
  provision/lib/common.sh provision/lib/config.sh provision/lib/system.sh provision/lib/network.sh
  provision/lib/storage.sh provision/lib/graywolf.sh provision/lib/scrub.sh
  provision/templates/interfaces-eth0.tmpl provision/templates/interfaces-wlan0.tmpl
  provision/templates/journald-dxberry.conf provision/templates/dxberry-netwatch.service
)

check_tree() {
  local f ok=1
  for f in "${REQUIRED_FILES[@]}"; do
    [[ -f $ROOT/$f ]] || { echo "missing $f" >&2; ok=0; }
  done
  for f in "$ROOT"/provision/bin/* "$ROOT"/provision/lib/*.sh "$ROOT"/boot/*.sh; do
    [[ -f $f ]] && ! bash -n "$f" && { echo "syntax error in ${f#"$ROOT"/}" >&2; ok=0; }
  done
  (( ok )) || return 1
  echo "tree ok"
}

if (( CHECK )); then check_tree; exit $?; fi

check_tree > /dev/null
(( EUID == 0 )) || { echo "the build must run as root (sudo) for loop-device mounts" >&2; exit 1; }
for t in losetup xz sha256sum curl partprobe mount; do
  command -v "$t" > /dev/null || { echo "missing tool: $t" >&2; exit 1; }
done
# shellcheck source=provision/lib/common.sh
source "$ROOT/provision/lib/common.sh"

VERSION=${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2> /dev/null || echo dev)}
VERSION=${VERSION#v}
mkdir -p "$WORK" "$OUT"

cleanup() {
  set +e
  if [[ -n $MNT ]]; then umount "$MNT/boot" 2> /dev/null; umount "$MNT/root" 2> /dev/null; rmdir "$MNT/boot" "$MNT/root" "$MNT" 2> /dev/null; fi
  [[ -n $LOOP ]] && losetup -d "$LOOP" 2> /dev/null
}
trap cleanup EXIT

# 1. Official DietPi image, verified against DietPi's published checksum.
name=$(basename "$DIETPI_URL")
if [[ -z $DIETPI_IMG ]]; then
  DIETPI_IMG=$WORK/$name
  curl -fsSL -o "$WORK/$name.sha256" "$DIETPI_URL.sha256"
  if [[ ! -f $DIETPI_IMG ]] || ! (cd "$WORK" && sha256sum -c --quiet "$name.sha256"); then
    echo "downloading $DIETPI_URL"
    curl -fL -o "$DIETPI_IMG" "$DIETPI_URL"
    (cd "$WORK" && sha256sum -c --quiet "$name.sha256")
  fi
fi
dietpi_sha=$(sha256sum "$DIETPI_IMG" | cut -d' ' -f1)

# 2. Decompress and expose partitions.
img=$WORK/DXBerry-Pi-$VERSION-rpi234-arm64.img
echo "decompressing to $img"
xz -dkc "$DIETPI_IMG" > "$img"
LOOP=$(losetup -Pf --show "$img")
partprobe "$LOOP"
MNT=$(mktemp -d)
mkdir -p "$MNT/boot" "$MNT/root"
mount "${LOOP}p1" "$MNT/boot"
mount "${LOOP}p2" "$MNT/root"

# 3. DietPi automation defaults: the root copy is authoritative, the FAT copy is what users see.
#    DietPi imports the FAT copy at first boot only if it is newer (cp -u), so stamp it older.
apply_overrides() {
  local target=$1 line
  while IFS= read -r line; do
    [[ -z $line || $line == \#* ]] && continue
    dxb_set_kv "$target" "${line%%=*}" "${line#*=}"
  done < "$ROOT/boot/dietpi.overrides.txt"
}
apply_overrides "$MNT/root/boot/dietpi.txt"
cp "$MNT/root/boot/dietpi.txt" "$MNT/boot/dietpi.txt"
touch -d '1970-01-01 00:00:00 UTC' "$MNT/boot/dietpi.txt"
touch -d '1970-01-01 00:01:00 UTC' "$MNT/root/boot/dietpi.txt"
for s in Automation_Custom_PreScript.sh Automation_Custom_Script.sh; do
  install -m 755 -o 0 -g 0 "$ROOT/boot/$s" "$MNT/root/boot/$s"
done
cp "$ROOT/boot/dxberry.txt.example" "$ROOT/boot/README-DXBERRY.txt" "$MNT/boot/"

# 4. The provisioner.
rm -rf "$MNT/root/opt/dxberry"
mkdir -p "$MNT/root/opt/dxberry"
cp -r "$ROOT/provision/." "$MNT/root/opt/dxberry/"
chown -R 0:0 "$MNT/root/opt/dxberry"
find "$MNT/root/opt/dxberry" -type d -exec chmod 755 {} +
find "$MNT/root/opt/dxberry" -type f -exec chmod 644 {} +
chmod 755 "$MNT/root/opt/dxberry/bin/"*
mkdir -p "$MNT/root/usr/local/sbin"
ln -sf /opt/dxberry/bin/dxberry-provision "$MNT/root/usr/local/sbin/dxberry-provision"
ln -sf /opt/dxberry/bin/dxberry-netwatch "$MNT/root/usr/local/sbin/dxberry-netwatch"
cat > "$MNT/root/etc/dxberry-release" << EOF
DXBERRY_VERSION=$VERSION
DXBERRY_COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2> /dev/null || echo unknown)
DXBERRY_BUILD_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
DIETPI_IMAGE=$name
DIETPI_IMAGE_SHA256=$dietpi_sha
EOF

# 5. Unmount, compress, checksum.
sync
umount "$MNT/boot" "$MNT/root"
rmdir "$MNT/boot" "$MNT/root" "$MNT"; MNT=''
losetup -d "$LOOP"; LOOP=''
final="$OUT/$(basename "$img")"
mv "$img" "$final"
echo "compressing $final"
xz -T0 -f "$final"
(cd "$OUT" && sha256sum "$(basename "$final").xz" > "$(basename "$final").xz.sha256")
(( KEEP )) || rm -f "$WORK/DXBerry-Pi-"*.img
echo "built $final.xz"
echo "sha256: $(cut -d' ' -f1 "$final.xz.sha256")"
```

- [ ] **Step 4: Run tests**

Run: `chmod +x build/build-image.sh && tests/run.sh`
Expected: `56 tests, 0 failures`

- [ ] **Step 5: Build the first image locally**

Run: `sudo build/build-image.sh --version 0.1.0-dev`
Expected: downloads `DietPi_RPi234-ARMv8-Trixie.img.xz` (verified), prints `built …/out/DXBerry-Pi-0.1.0-dev-rpi234-arm64.img.xz` and a sha256. Then inspect without booting:

```bash
sudo bash -c 'set -e; xz -dkc out/DXBerry-Pi-0.1.0-dev-rpi234-arm64.img.xz > /tmp/dxb-inspect.img
l=$(losetup -Pf --show /tmp/dxb-inspect.img); m=$(mktemp -d); mount ${l}p1 $m; ls -la $m; grep -E "^(AUTO_SETUP_AUTOMATED|AUTO_SETUP_SSH_SERVER_INDEX|AUTO_SETUP_CUSTOM_SCRIPT_EXEC)=" $m/dietpi.txt; umount $m
mount ${l}p2 $m; ls -la $m/opt/dxberry/bin $m/boot/Automation_Custom_*.sh; cat $m/etc/dxberry-release; umount $m; losetup -d $l; rm -rf $m /tmp/dxb-inspect.img'
```

Expected: FAT partition lists `dietpi.txt`, `dxberry.txt.example`, `README-DXBERRY.txt`; the three keys show `1`, `-2`, `0`; root partition has executable `dxberry-*` binaries and both hooks; `/etc/dxberry-release` has the version and DietPi checksum.

- [ ] **Step 6: Commit**

```bash
git add build/build-image.sh tests/test_build.sh
git commit -m "Add image build script"
```

---

### Task 11: CI, release workflow and README

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `README.md`

**Interfaces:**
- CI runs on every push/PR: the "no real config committed" guard, `bash -n`, `shellcheck -x`, `tests/run.sh`, `build/build-image.sh --check`. Release runs on `v*` tags and attaches `out/*.img.xz` + `.sha256` to the GitHub Release.

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: CI
on:
  push:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: No real station config in the tree
        run: '! git ls-files | grep -qxE "boot/dxberry\.txt|boot/dietpi-wifi\.txt"'
      - name: Syntax
        run: |
          for f in provision/bin/* provision/lib/*.sh build/*.sh boot/*.sh tests/*.sh; do bash -n "$f"; done
      - name: shellcheck
        run: shellcheck -x provision/bin/* provision/lib/*.sh build/build-image.sh boot/*.sh tests/*.sh
      - name: Unit tests
        run: tests/run.sh
      - name: Build tree check
        run: build/build-image.sh --check
```

- [ ] **Step 2: Write `.github/workflows/release.yml`**

```yaml
name: Release image
on:
  push:
    tags: ['v*']
permissions:
  contents: write
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Build image
        run: sudo build/build-image.sh --version "${GITHUB_REF_NAME}"
      - name: Release notes
        run: |
          {
            echo "Flash \`DXBerry-Pi-${GITHUB_REF_NAME#v}-rpi234-arm64.img.xz\`, rename \`dxberry.txt.example\` to \`dxberry.txt\` on the boot partition, fill it in, boot."
            echo
            echo "Base image: $(cat build/work/*.img.xz.sha256)"
            echo "Graywolf: latest release at first boot (pin with GRAYWOLF_VERSION=)."
          } > notes.md
      - name: Publish
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release create "$GITHUB_REF_NAME" out/*.img.xz out/*.sha256 --title "DXBerry-Pi $GITHUB_REF_NAME" --notes-file notes.md
```

- [ ] **Step 3: Write `README.md`**

```markdown
# DXBerry-Pi

A Raspberry Pi image for amateur radio digital modes that is easy to stand up and impossible to outgrow.

Flash it, edit one text file, boot. Minutes later the Pi is on your network at a fixed address running
[Graywolf](https://github.com/chrissnell/graywolf) (APRS iGate / digipeater / TNC with a web UI). The OS is
stock [DietPi](https://dietpi.com) with SSH; every application keeps its own real configuration surface;
every default this image applies lives in this repository.

## Quick start

1. Download `DXBerry-Pi-<version>-rpi234-arm64.img.xz` from the Releases page (Raspberry Pi 2/3/4, 64-bit).
2. Flash it with Balena Etcher or Raspberry Pi Imager.
3. Open the boot partition. Rename `dxberry.txt.example` to `dxberry.txt` and fill it in — `PASSWORD` is
   the only required line. Windows Notepad is fine.
4. Insert the drive, connect Ethernet, power on. First boot needs internet and takes 5–8 minutes.
5. Open `http://<your STATIC_IP>:8080` for Graywolf. SSH as `root` or `dietpi` with your password.
6. In Graywolf, use **Detect Devices** to pick your sound card and PTT. Everything else is already seeded
   from `dxberry.txt` (callsign, iGate, position beacon).

If the Pi does not come up as expected, put the drive back in a PC: `dxberry-ERROR.txt` (config problem)
or `dxberry-status.txt` (what was applied, what failed) on the boot partition explains it.

## What `dxberry.txt` controls

Hostname, password, time zone; static address or DHCP; WiFi failover; callsign, position, beacon and
iGate server; the Graywolf admin account; and a few advanced seeds (SSH key, beacon path/symbol,
digipeater preset). See `boot/dxberry.txt.example` — every line is documented. Passwords are replaced with
`<applied>` after they are used.

It is a bootstrap, not a ceiling: Graywolf's web UI owns station configuration after first boot, and
`sudo dxberry-provision` re-applies an edited file without touching anything you changed in the UI
(`--reseed` pushes the file's station values again).

## Networking

Ethernet is primary. If `WIFI_SSID` is set, `dxberry-netwatch` brings WiFi up on the **same address** only
while the cable has no link, and hands back to Ethernet when it returns — exactly one interface is ever
configured. `dxberry-netwatch --status` shows the current state; `--simulate eth0-down|eth0-up|off`
exercises failover without touching cables. Network settings live in `/etc/network/interfaces.d/`; leave
`dietpi-config`'s network menu alone, it does not know about the failover service.

## Storage

Logs and the journal live in RAM, swap is on zram, and Graywolf prunes its own position log, so routine
operation barely writes to the card. Real state (Graywolf configuration, mail, logs you keep) is on disk.

## Building the image yourself

```
sudo build/build-image.sh            # downloads and verifies the official DietPi image, injects /opt/dxberry
build/build-image.sh --check         # verifies the tree only
tests/run.sh                         # unit tests (bash, awk, jq)
```

## Layout

- `boot/` — files for the boot partition and DietPi's automation hooks
- `provision/` — the provisioner installed at `/opt/dxberry` (`dxberry-preboot`, `dxberry-provision`, `dxberry-netwatch`)
- `build/` — image build
- `docs/design/` — design specifications; `docs/plans/` — implementation plans

## Credits and license

Built on [DietPi](https://github.com/MichaIng/DietPi) and [Graywolf](https://github.com/chrissnell/graywolf),
both GPL-2.0. DXBerry-Pi is licensed GPL-2.0-or-later; see `LICENSE`.
```

- [ ] **Step 4: Run the CI steps locally**

Run:

```bash
! git ls-files | grep -qxE "boot/dxberry\.txt|boot/dietpi-wifi\.txt" && echo guard-ok
for f in provision/bin/* provision/lib/*.sh build/*.sh boot/*.sh tests/*.sh; do bash -n "$f"; done && echo syntax-ok
tests/run.sh && build/build-image.sh --check
```

Expected: `guard-ok`, `syntax-ok`, `56 tests, 0 failures`, `tree ok`. `shellcheck` is not installed on the workstation; ask before installing it (`sudo apt-get install -y shellcheck`), then run `shellcheck -x provision/bin/* provision/lib/*.sh build/build-image.sh boot/*.sh tests/*.sh` and fix every finding (add a `# shellcheck disable=` line only where the warning is a false positive and say why in the same line).

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml README.md
git commit -m "Add CI, release workflow and README"
```

---

### Task 12: Acceptance on the Raspberry Pi 4 (spec §14)

This task needs the person with physical access: flashing the drive, editing the file, plugging cables. Every check below is run over SSH from the workstation unless marked *(physical)*. Record each result in the PR/commit message that closes this task.

**Files:**
- Modify: whatever the checks uncover. Each fix gets its own unit test where the logic is testable, then a rebuild (`sudo build/build-image.sh`) and re-flash.

- [ ] **Step 1: Flash and configure** *(physical)*

Flash `out/DXBerry-Pi-0.1.0-dev-rpi234-arm64.img.xz` with Etcher. On the boot partition, rename `dxberry.txt.example` → `dxberry.txt` and set at least: `PASSWORD`, `TIMEZONE=America/Chicago`, `STATIC_IP=<free address>/24`, `GATEWAY`, `DNS`, `WIFI_SSID`, `WIFI_PASSWORD`, `WIFI_COUNTRY=US`, `CALLSIGN`, `LATITUDE`, `LONGITUDE`, `IGATE_SERVER=noam.aprs2.net`. Insert, connect Ethernet, power on.

- [ ] **Step 2: First boot reaches the static address (§14.1)**

Run from the workstation, replacing the address:

```bash
for i in $(seq 1 60); do ping -c1 -W2 <STATIC_IP> > /dev/null 2>&1 && break; sleep 10; done; echo reachable
ssh root@<STATIC_IP> 'hostname; cat /etc/dxberry-release; dxberry-netwatch --status; ip -4 addr show eth0 | grep inet; ip -4 addr show wlan0 | grep inet || echo "wlan0: no address (correct)"; systemctl is-active dxberry-netwatch graywolf; cat /var/lib/dxberry/status.txt'
curl -s http://<STATIC_IP>:8080/api/auth/setup
```

Expected: hostname as configured; netwatch `ETH`; only eth0 has the address; both services `active`; status file shows all steps completed; the setup endpoint returns `{"needs_setup":false}`. In a browser, log in with `WEBUI_USER`/`WEBUI_PASSWORD`: station callsign, iGate server and a position beacon are present.

Then verify secrets: `ssh root@<STATIC_IP> 'grep -E "^(PASSWORD|WIFI_PASSWORD|WEBUI_PASSWORD)=" /boot/firmware/dxberry.txt; ls -l /boot/dietpi-wifi.txt 2>&1; grep AUTO_SETUP_GLOBAL_PASSWORD= /boot/dietpi.txt'` → all three read `<applied>`, `dietpi-wifi.txt` does not exist, the DietPi password key is empty.

- [ ] **Step 3: Re-run is a no-op (§14.2)**

`ssh root@<STATIC_IP> 'dxberry-provision; grep -c "wrote\|installed\|updated" /var/lib/dxberry/provision.log'` — the second run logs no "wrote"/"installed"/"updated" lines beyond those from the first boot, and ends with `finished: all steps completed`.

- [ ] **Step 4: Failover, simulated then physical (§14.3)**

```bash
ssh root@<STATIC_IP> 'dxberry-netwatch --simulate eth0-down'; sleep 10
ssh root@<STATIC_IP> 'dxberry-netwatch --status; ip -4 addr show wlan0 | grep inet; ip -4 addr show eth0 | grep inet || echo "eth0: no address (correct)"'
ssh root@<STATIC_IP> 'dxberry-netwatch --simulate eth0-up'; sleep 10
ssh root@<STATIC_IP> 'dxberry-netwatch --status; ip -4 addr show eth0 | grep inet; ip -4 addr show wlan0 | grep inet || echo "wlan0: no address (correct)"; dxberry-netwatch --simulate off'
```

Expected: `WIFI` with the address only on wlan0, then `ETH` with it only on eth0, SSH reachable at the same address throughout. Then *(physical)*: unplug the cable, wait 10 s, run the same status commands (expect `WIFI`); replug, wait 10 s (expect `ETH`). `journalctl -u dxberry-netwatch` shows one clean transition per event.

- [ ] **Step 5: Ethernet-only, DHCP, and invalid configs (§14.4–14.6)**

Re-flash three more times with these `dxberry.txt` variants and check:
1. No `WIFI_SSID`: comes up on the static address; unplugging the cable leaves `dxberry-netwatch --status` at `NONE`; replug restores `ETH`.
2. No `STATIC_IP`: comes up on a DHCP lease (find it in the router or with `arp -a`); failover simulation moves to wlan0 (its own lease) and back.
3. `PASSWORD` missing and `STATIC_IP=nope`: Pi boots on DHCP with hostname `DietPi`; `dxberry-ERROR.txt` on the boot partition names both problems; `ssh root@<dhcp-address>` with password `dietpi` works.

- [ ] **Step 6: Graywolf install failure path (§14.7)**

Flash once more with `GRAYWOLF_VERSION=v0.0.1`. Expected: Pi reaches the static address; `dxberry-status.txt` lists `graywolf: could not download checksums.txt`. Then edit `/boot/firmware/dxberry.txt` over SSH to remove that line, run `dxberry-provision` (the config has `<applied>` passwords, so set `WEBUI_PASSWORD` again for this run), and confirm Graywolf installs and seeds.

- [ ] **Step 7: Fix, retest, commit**

For each failure found: write the unit test that would have caught it (where the logic is testable), fix, `tests/run.sh`, rebuild, re-flash the affected scenario. Commit each fix separately with a message naming the acceptance step it satisfies. Update `docs/design/2026-09-07-base-image.md` §16 to record what the first flash confirmed.

---

### Task 13: Publish v0.1.0

Requires explicit approval from the repository owner before each of the three actions below (they are visible to others and not reversible in the ordinary sense).

- [ ] **Step 1: Create the GitHub repository and push**

```bash
gh repo create Dustpan95/DXBerry-Pi --public --source=/home/dustin/DXBerry-Pi --description "Flash-and-go Raspberry Pi image for amateur radio digital modes: Graywolf APRS, static IP with WiFi failover, one config file" --push
```

Expected: repository exists, `main` pushed, CI workflow runs green.

- [ ] **Step 2: Tag and release**

```bash
git tag -a v0.1.0 -m "DXBerry-Pi 0.1.0: base image with Graywolf, static IP and WiFi failover"
git push origin v0.1.0
gh run watch --exit-status
gh release view v0.1.0
```

Expected: the release workflow builds and attaches `DXBerry-Pi-0.1.0-rpi234-arm64.img.xz` and `.sha256`.

- [ ] **Step 3: Verify the published artifact**

Download the release asset, compare its sha256 to the attached `.sha256`, flash it, and repeat Task 12 Step 2 once. Then the base image is done and sub-project 2 (radio plumbing) can be specified.
