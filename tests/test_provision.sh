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

# Full-run fixture: every module's state lives under $TEST_TMP. reboot/sync/systemd/password/
# hostname/swap/package tools are harmless shell functions logging to $TEST_TMP/calls; they are
# plain external command names that no library file redefines, so they are safe to set once here
# and let each test's subshell inherit them.
full_env() {
  export DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log \
    DXB_ZONEINFO_DIR=$TEST_TMP/nozone DXB_TEMPLATES=$DXB_ROOT/provision/templates \
    DXB_HOSTNAME_FILE=$TEST_TMP/hostname DXB_HOSTS_FILE=$TEST_TMP/hosts DXB_LOCALTIME=$TEST_TMP/localtime \
    DXB_TIMEZONE_FILE=$TEST_TMP/timezone DXB_DIETPI_FUNC=$TEST_TMP/nofunc DXB_DIETPI_TXT=$TEST_TMP/dietpi.txt \
    DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_RESOLV_CONF=$TEST_TMP/resolv.conf DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_DIETPI_WIFIDB=$TEST_TMP/wifidb DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt \
    DXB_DIETPI_SET_HW=$TEST_TMP/set_hw DXB_SYS_NET=$TEST_TMP/sys DXB_INTERFACES_FILE=$TEST_TMP/interfaces \
    DXB_JOURNALD_DROPIN=$TEST_TMP/journald.d/dxberry.conf
  mkdir -p "$DXB_BOOT_DIR" "$DXB_SYSTEMD_DIR" "$DXB_ZONEINFO_DIR" "$DXB_SYS_NET/eth0" "$DXB_SYS_NET/wlan0"
  : > "$DXB_ZONEINFO_DIR/UTC"
  echo DietPi > "$DXB_HOSTNAME_FILE"
  printf '127.0.0.1 localhost\n127.0.1.1 DietPi\n' > "$DXB_HOSTS_FILE"
  printf 'CONFIG_SERIAL_CONSOLE_ENABLE=0\nAUTO_SETUP_GLOBAL_PASSWORD=dietpi\n' > "$DXB_DIETPI_TXT"
  printf '#!/bin/bash\necho "wifidb $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_WIFIDB"
  chmod +x "$DXB_DIETPI_WIFIDB"
  printf '#!/bin/bash\necho "set_hw $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_SET_HW"
  chmod +x "$DXB_DIETPI_SET_HW"
  printf 'source /etc/network/interfaces.d/*\nauto lo\niface lo inet loopback\n' > "$DXB_INTERFACES_FILE"
  printf "aWIFI_SSID[0]=''\naWIFI_KEY[0]=''\naWIFI_KEYMGR[0]='WPA-PSK'\n" > "$DXB_DIETPI_WIFI"

  # shellcheck disable=SC2317
  {
    reboot() { echo "reboot" >> "$TEST_TMP/calls"; }
    sync() { echo "sync" >> "$TEST_TMP/calls"; }
    # "is-enabled" answers no while $TEST_TMP/netwatch-not-enabled exists, so a test can drive
    # the pre-reboot gate from outside the subshell run_driver uses.
    systemctl() {
      echo "systemctl $*" >> "$TEST_TMP/calls"
      if [[ $1 == is-enabled && -f $TEST_TMP/netwatch-not-enabled ]]; then return 1; fi
      return 0
    }
    systemd-run() { echo "systemd-run $*" >> "$TEST_TMP/calls"; }
    chpasswd() { echo "chpasswd" >> "$TEST_TMP/calls"; cat > /dev/null; }
    hostname() { echo "hostname $*" >> "$TEST_TMP/calls"; }
    swapon() { echo "NAME"; echo "/dev/zram0"; }
    apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
    dpkg-query() { return 1; }
  }
  : > "$TEST_TMP/calls"
}

# run_driver ARGS...: sources dxberry-provision and calls its main in a SUBSHELL, never the test
# process itself. Two reasons: (1) dxberry-preboot (sourced by test_preboot.sh) and
# dxberry-provision both define a function literally named "main" - sourcing the driver at this
# file's top level would permanently clobber test_preboot.sh's "main" for the rest of the suite,
# since every tests/test_*.sh is sourced into one shared process. (2) dxb_require_root and
# dxb_gw_install/dxb_gw_seed must be overridden AFTER the source (which re-defines the real
# common.sh/graywolf.sh versions) but must never leak into later tests - test_graywolf.sh exercises
# the real dxb_gw_install/dxb_gw_seed directly. A subshell makes both a non-issue: its function
# definitions vanish when it exits, while file-based effects ($TEST_TMP/*, dxberry.txt, dxberry-
# ERROR.txt, dxberry-status.txt, $TEST_TMP/calls) and the exit status are still observable.
run_driver() {
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    main "$@"
  )
}

test_first_boot_with_failing_graywolf_install_still_finishes_and_reboots() {
  full_env
  printf 'PASSWORD=secretpass\nCALLSIGN=N0CALL-9\nWEBUI_PASSWORD=webuipass1\n' > "$DXB_BOOT_DIR/dxberry.txt"
  local rc
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { dxb_step_failed graywolf "simulated install failure"; return 1; }
    dxb_gw_seed() { :; }
    main --first-boot
  ) 2> /dev/null
  rc=$?
  assert_eq "$rc" "1"
  # WEBUI_PASSWORD was never consumed (install failed before seeding), so it must survive for a
  # retry; PASSWORD was applied by DietPi itself on first boot, so it is scrubbed regardless.
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WEBUI_PASSWORD=webuipass1"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=<applied>"
  [[ -f $DXB_BOOT_DIR/dxberry-status.txt ]] || _fail "boot status file missing"
  [[ -f $DXB_STATE_DIR/status.txt ]] || _fail "state status file missing"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "graywolf: simulated install failure"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "secrets: WEBUI_PASSWORD left in dxberry.txt"
  # The network is sound (netwatch enabled, no stray stanzas), so the gate lets the reboot happen
  # and the marker is written: a failed Graywolf download is not a reason to strand the boot.
  [[ -f $DXB_STATE_DIR/provisioned ]] || _fail "provisioned marker missing after a clean network gate"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl is-enabled --quiet dxberry-netwatch"
  assert_eq "$(tail -2 "$TEST_TMP/calls")" $'sync\nreboot'
}

# The first-boot reboot comes back on a network only dxberry-netwatch can raise. If the unit is
# not enabled, rebooting strands the Pi - so do not, and leave the still-open session an
# explanation instead.
test_first_boot_does_not_reboot_when_netwatch_is_not_enabled() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  : > "$TEST_TMP/netwatch-not-enabled"
  local rc
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main --first-boot
  ) 2> /dev/null
  rc=$?
  assert_eq "$rc" "1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "reboot"
  [[ -f $DXB_STATE_DIR/provisioned ]] && _fail "the provisioned marker must not be written when the gate blocks"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "not rebooting: dxberry-netwatch is not enabled"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "sudo dxberry-provision --first-boot"
  assert_file_contains "$DXB_STATE_DIR/status.txt" "not rebooting"
}

test_first_boot_does_not_reboot_when_a_stray_stanza_would_fight_netwatch() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  mkdir -p "$DXB_IFACES_DIR"
  printf '# a leftover from dietpi-config\nallow-hotplug eth0\n' > "$DXB_IFACES_DIR/dietpi.conf"
  local rc
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main --first-boot
  ) 2> /dev/null
  rc=$?
  assert_eq "$rc" "1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "reboot"
  [[ -f $DXB_STATE_DIR/provisioned ]] && _fail "the provisioned marker must not be written when the gate blocks"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "stray stanza $DXB_IFACES_DIR/dietpi.conf:2: allow-hotplug eth0"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "not rebooting"
  # The foreign file is reported, never edited.
  assert_file_contains "$DXB_IFACES_DIR/dietpi.conf" "allow-hotplug eth0"
}

test_first_boot_invalid_config_appends_to_existing_error_file_and_reboots() {
  full_env
  printf 'HOSTNAME=Bad\n' > "$DXB_BOOT_DIR/dxberry.txt"
  {
    echo 'DXBerry-Pi: dxberry.txt has errors. Fix them and reboot.'
    echo
    echo 'PASSWORD is required'
    echo 'line 1: HOSTNAME must be lowercase letters, digits and hyphens, 1-63 characters'
    echo
    echo 'This boot used DietPi defaults: DHCP address, hostname dxberry-pi, login root / dietpi.'
  } > "$DXB_BOOT_DIR/dxberry-ERROR.txt"
  local rc
  run_driver --first-boot 2> /dev/null
  rc=$?
  assert_eq "$rc" "1"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "Fix them and reboot."
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "dxberry-provision:"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "PASSWORD is required"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "This boot used DietPi defaults: DHCP address, hostname dxberry-pi, login root / dietpi."
  [[ -f $DXB_BOOT_DIR/dxberry-status.txt ]] || _fail "boot status file missing"
  [[ -f $DXB_STATE_DIR/status.txt ]] || _fail "state status file missing"
  assert_contains "$(cat "$TEST_TMP/calls")" "reboot"
}

test_first_boot_missing_config_writes_error_status_and_reboots() {
  full_env
  rm -f "$DXB_BOOT_DIR/dxberry.txt"
  local rc
  run_driver --first-boot 2> /dev/null
  rc=$?
  assert_eq "$rc" "1"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "no dxberry.txt was found"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "This boot used DietPi defaults: DHCP address, hostname dxberry-pi, login root / dietpi."
  [[ -f $DXB_BOOT_DIR/dxberry-status.txt ]] || _fail "boot status file missing"
  [[ -f $DXB_STATE_DIR/status.txt ]] || _fail "state status file missing"
  assert_contains "$(cat "$TEST_TMP/calls")" "reboot"
}

test_run_mode_restarts_netwatch_last_and_never_reboots() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  local rc
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) 2> /dev/null
  rc=$?
  assert_eq "$rc" "0"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "reboot"
  assert_eq "$(tail -1 "$TEST_TMP/calls")" "systemd-run --quiet --on-active=3 systemctl restart dxberry-netwatch"
}

test_run_mode_wifi_import_failure_leaves_wifi_password_intact() {
  full_env
  printf 'PASSWORD=secretpass\nWIFI_SSID=Home\nWIFI_PASSWORD=wifipass1\nWIFI_COUNTRY=US\n' > "$DXB_BOOT_DIR/dxberry.txt"
  printf '#!/bin/bash\nexit 1\n' > "$DXB_DIETPI_WIFIDB"
  chmod +x "$DXB_DIETPI_WIFIDB"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) 2> /dev/null
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_PASSWORD=wifipass1"
}
