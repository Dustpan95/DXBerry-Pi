#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/graywolf.sh"
source "$DXB_LIB/settings.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/inifile.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_LIB/screen.sh"
source "$DXB_LIB/webproxy.sh"
source "$DXB_LIB/logbook.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

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
    DXB_JOURNALD_DROPIN=$TEST_TMP/journald.d/dxberry.conf DXB_JOURNAL_DIR=$TEST_TMP/state/journal DXB_JOURNAL_LINK=$TEST_TMP/journal-link \
    DXB_GW_DROPIN=$TEST_TMP/graywolf.service.d/dxberry-history.conf \
    DXB_GW_HISTORY_MOUNT=$TEST_TMP/units/run-graywolf.mount \
    DXB_SYSFS_ROOT=$TEST_TMP/radio-sys DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules \
    DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=fake_udevadm \
    DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles \
    DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RADIOS_FILE=$TEST_TMP/state/radios.json \
    DXB_GPSD_DEFAULT=$TEST_TMP/etc/default/gpsd DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony/conf.d/dxberry.conf \
    DXB_RPI_CONFIG_TXT=$TEST_TMP/bootfs/config.txt DXB_GPSPIPE=fake_gpspipe DXB_RUN_DIR=$TEST_TMP/run \
    DXB_STATION_BIN=$TEST_TMP/opt/bin/dxberryd DXB_STATION_TLS=$TEST_TMP/etc/dxberry/tls DXB_PAM_DIR=$TEST_TMP/etc/pam.d \
    DXB_POLICY_RC=$TEST_TMP/usr/sbin/policy-rc.d DXB_NGINX=pv_nginx DXB_STATION_WAIT_S=0 \
    DXB_STATION_CURL=true DXB_SS=pv_ss DXB_STATION_API_WAIT_S=0 DXB_STATION_LISTEN_WAIT_S=0 \
    DXB_COCKPIT_LINK=$TEST_TMP/usr-share-cockpit/dxberry DXB_COCKPIT_DROPIN_DIR=$TEST_TMP/cockpit.socket.d \
    DXB_REBOOT_FLAG=$TEST_TMP/run/reboot-required \
    DXB_APPS_DIR=$DXB_ROOT/provision/lib/apps DXB_APPS_TSV=$DXB_ROOT/provision/share/apps.tsv \
    DXB_APPS_OPT=$TEST_TMP/opt/dxberry-apps DXB_APPS_HOME=$TEST_TMP/home/dxbapps DXB_NGINX_DIR=$TEST_TMP/nginx \
    DXB_SYSTEMCTL=systemctl DXB_DPKG_QUERY=dpkg-query DXB_APT_GET=apt-get DXB_APPS_STATE=$TEST_TMP/state/apps DXB_GETENT=pv_account DXB_USERADD=pv_account DXB_GROUPADD=pv_account DXB_USERMOD=pv_account DXB_CHOWN=pv_account
  mkdir -p "$DXB_BOOT_DIR" "$DXB_SYSTEMD_DIR" "$DXB_ZONEINFO_DIR" "$DXB_SYS_NET/eth0" "$DXB_SYS_NET/wlan0" \
    "$DXB_SYSFS_ROOT" "$TEST_TMP/etc" "$DXB_RIGCTLD_RUN_DIR" "$DXB_TMPFILES_DIR" "$TEST_TMP/opt/bin" "$DXB_STATION_TLS"
  # the station app is in place and has made its certificate; a stand-in binary
  printf '#!/bin/sh\necho 0.5.0-rc1\n' > "$DXB_STATION_BIN"; chmod 755 "$DXB_STATION_BIN"
  echo cert > "$DXB_STATION_TLS/station.crt"; echo key > "$DXB_STATION_TLS/station.key"
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
  printf '[Service]\nExecStart=/usr/bin/graywolf -history-db /var/lib/graywolf/graywolf-history.db\n' > "$TEST_TMP/graywolf.service"

  # shellcheck disable=SC2317
  {
    reboot() { echo "reboot" >> "$TEST_TMP/calls"; }
    sync() { echo "sync" >> "$TEST_TMP/calls"; }
    # "is-enabled" answers no while $TEST_TMP/netwatch-not-enabled exists, so a test can drive
    # the pre-reboot gate from outside the subshell run_driver uses.
    # "show" reports the packaged graywolf.service fixture below as the unit's fragment.
    systemctl() {
      echo "systemctl $*" >> "$TEST_TMP/calls"
      if [[ $1 == is-enabled && -f $TEST_TMP/netwatch-not-enabled ]]; then return 1; fi
      [[ $1 == show ]] && echo "$TEST_TMP/graywolf.service"
      return 0
    }
    systemd-run() { echo "systemd-run $*" >> "$TEST_TMP/calls"; }
    chpasswd() { echo "chpasswd" >> "$TEST_TMP/calls"; cat > /dev/null; }
    hostname() { echo "hostname $*" >> "$TEST_TMP/calls"; }
    swapon() { echo "NAME"; echo "/dev/zram0"; }
    apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
    # Graywolf is not installed; nginx and PAM are, so the station step does not try to install them
    dpkg-query() { case ${*: -1} in nginx|libpam-runtime) printf 'installed 1.0' ;; *) return 1 ;; esac; }
    fake_udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
    systemd-tmpfiles() { echo "systemd-tmpfiles $*" >> "$TEST_TMP/calls"; }
    rigctl() { printf '145390000\nFM\n'; }
    fake_gpspipe() { :; }
    timeout() { shift; "$@"; }
    # the accounts the programs run as already exist (dxb_apps_user_ensure only records its calls)
    pv_account() { echo "account $*" >> "$TEST_TMP/calls"; }
  }
  : > "$TEST_TMP/calls"
  # DXB_MODE is a plain (non-local) global in other test files (e.g. test_network.sh); reset it
  # here too so a call to provision_radio outside run_driver's subshell (which sets its own via
  # main()) never inherits a stale "first-boot" from an earlier test in this shared process.
  DXB_MODE=run
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_RADIO_REBOOT_NEEDED=0
  ln -s "$DXB_JOURNAL_DIR" "$DXB_JOURNAL_LINK" # dxberry-journal-link.service, stubbed out here
}

# provision_cfg LINE...: write dxberry.txt under DXB_BOOT_DIR and load it into DXB_CFG, the way
# net_cfg (test_network.sh) and gps_cfg (test_gps.sh) do for their own modules.
provision_cfg() {
  printf '%s\n' "$@" > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_validate
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
  # provision_network and the gate both scan; the stanza itself is listed once, not twice.
  assert_eq "$(grep -c 'allow-hotplug eth0' "$DXB_BOOT_DIR/dxberry-status.txt")" "1"
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
  # netwatch adopts an interface that is already up, so the restart does not apply a new address:
  # the operator is told to restart the Pi, never to expect the new address now
  assert_file_contains "$DXB_LOG_FILE" "An interface that is already up keeps its old settings until it is cycled or the Pi restarts: run sudo reboot to apply the change."
  assert_file_not_contains "$DXB_LOG_FILE" "reconnect at the new address"
  # Run mode means "first boot already happened"; it must never write the marker itself, gate or
  # no gate - only a --first-boot run whose gate passed does that.
  [[ -f $DXB_STATE_DIR/provisioned ]] && _fail "run mode must never write the provisioned marker"
}

# A log path that is not a regular file (a device node, here stood in by a FIFO so the test can
# stat it without root and without harming anything real) must never be chmod'd - that already
# happened for real against /dev/null on a Pi, and chmod 600 /dev/null breaks every other program
# that opens it until a reboot.
test_run_mode_never_chmods_a_log_path_that_is_not_a_regular_file() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  mkfifo "$TEST_TMP/notafile"
  ( while true; do cat "$TEST_TMP/notafile" > /dev/null; done ) &
  local drain=$! before after rc
  before=$(stat -c %a "$TEST_TMP/notafile")
  DXB_LOG_FILE=$TEST_TMP/notafile
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) 2> /dev/null
  rc=$?
  after=$(stat -c %a "$TEST_TMP/notafile")
  kill "$drain" 2> /dev/null; wait "$drain" 2> /dev/null
  assert_eq "$rc" "0"
  assert_eq "$after" "$before"
}

# The finding this guards against: a blocked gate's own status text ("Fix the cause, then run:
# sudo dxberry-provision --first-boot") could be followed literally in RUN mode - which used to
# write the marker anyway (gate_ok started at 1 and only run mode never touched it), permanently
# disarming the gate for the next --first-boot. Walk the real remediation loop end to end.
test_remediation_loop_survives_a_run_mode_call_between_a_blocked_and_passing_gate() {
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
  [[ -f $DXB_STATE_DIR/provisioned ]] && _fail "marker must not exist after a blocked gate"

  # Someone follows the status file's generic tail instead of the gate's own instruction and
  # runs plain "sudo dxberry-provision" - must still not arm the marker.
  : > "$TEST_TMP/calls"
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
  [[ -f $DXB_STATE_DIR/provisioned ]] && _fail "a run-mode call must never write the marker"

  # Now actually fix the cause and re-run --first-boot: the gate passes, the marker is written,
  # and the reboot the gate exists to gatekeep finally happens.
  rm -f "$TEST_TMP/netwatch-not-enabled"
  : > "$TEST_TMP/calls"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main --first-boot
  ) 2> /dev/null
  rc=$?
  assert_eq "$rc" "0"
  [[ -f $DXB_STATE_DIR/provisioned ]] || _fail "marker must be written once --first-boot's gate passes"
  assert_eq "$(tail -2 "$TEST_TMP/calls")" $'sync\nreboot'
}

test_provision_radio_installs_and_reports() {
  full_env; provision_cfg 'PASSWORD=examplepass' 'GPS_DEVICE=uart'
  fx_scene "$DXB_SYSFS_ROOT" none
  provision_radio
  # install-only: a setup run never upgrades them (system upgrades are the Updates card's job)
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-upgrade libhamlib-utils gpsd gpsd-clients chrony alsa-utils psmisc"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now systemd-timesyncd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl mask systemd-timesyncd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable chrony"
  assert_file_contains "$DXB_DIETPI_TXT" "CONFIG_NTP_MODE=0"     # the time hand-off, after chrony is up
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-wire.service"
  assert_file_contains "$DXB_UDEV_RULES_FILE" "IMPORT{builtin}=\"path_id\""
  assert_file_contains "$DXB_MODPROBE_FILE" "snd_usb_audio"
  assert_file_contains "$DXB_GPSD_DEFAULT" 'DEVICES="/dev/ttyAMA0"'
  assert_contains "${DXB_STATUS_LINES[*]}" "radio: 0 radios, 0 present"
  assert_contains "${DXB_STATUS_LINES[*]}" "time: chrony"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  # GPS_DEVICE=uart wrote enable_uart=1/dtoverlay=disable-bt to config.txt; in run mode (what
  # full_env sets DXB_MODE to, since this test calls provision_radio directly rather than through
  # main() --first-boot) that means a reboot is needed, the same way the driver warns for wlan0.
  assert_eq "$DXB_RADIO_REBOOT_NEEDED" "1"
}

test_provision_radio_apt_failure_is_a_failed_step_not_fatal() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  fx_scene "$DXB_SYSFS_ROOT" none
  # shellcheck disable=SC2317  # invoked indirectly as the external apt-get command
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; return 100; }
  provision_radio
  assert_contains "${DXB_FAILED_STEPS[*]}" "radio: could not install"
  assert_file_contains "$DXB_MODPROBE_FILE" "snd_usb_audio"
  # Restore the working stub so a later test that shares this process never inherits a
  # permanently failing apt-get from this one.
  # shellcheck disable=SC2317
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
}

test_provision_driver_runs_radio_step_after_graywolf() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  fx_scene "$DXB_SYSFS_ROOT" none
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) 2> /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  local gw radio
  gw=$(grep -n 'enable --now graywolf' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  radio=$(grep -n 'dxberry-radio-hotplug' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  (( gw < radio )) || _fail "radio step must run after graywolf"
}

# The seed switches the position log on only once Graywolf reports its history in RAM, so the
# drop-in has to be in place before graywolf is started and seeded.
test_provision_driver_moves_graywolf_history_to_ram_before_seeding() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  fx_scene "$DXB_SYSFS_ROOT" none
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_history_in_ram() { echo "history-in-ram" >> "$TEST_TMP/calls"; }
    dxb_gw_seed() { echo "seed" >> "$TEST_TMP/calls"; }
    main
  ) 2> /dev/null
  local hist gw seed
  hist=$(grep -n '^history-in-ram$' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  gw=$(grep -n 'enable --now graywolf' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  seed=$(grep -n '^seed$' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  if [[ -z $hist || -z $gw || -z $seed ]]; then
    _fail "expected the history drop-in, graywolf start and seed to run (got '$hist' '$gw' '$seed')"
  elif ! (( hist < gw && gw < seed )); then
    _fail "the history drop-in must come before graywolf is started and seeded"
  fi
}

# An offline re-run fails the release check but must still rebuild the drop-in for the Graywolf
# already installed (after a hand upgrade it carries the old command line until then).
test_provision_driver_rebuilds_the_history_dropin_when_the_release_check_fails() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  fx_scene "$DXB_SYSFS_ROOT" none
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { dxb_step_failed graywolf "simulated offline release check"; return 1; }
    dxb_gw_installed_version() { echo 0.14.13; }
    dxb_gw_history_in_ram() { echo "history-in-ram" >> "$TEST_TMP/calls"; }
    dxb_gw_seed() { echo "seed" >> "$TEST_TMP/calls"; }
    main
  ) 2> /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "history-in-ram"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "enable --now graywolf"
  assert_fails grep -qx seed "$TEST_TMP/calls"
}

test_provision_driver_runs_the_console_step_after_radio() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  fx_scene "$DXB_SYSFS_ROOT" none
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) 2> /dev/null
  local radio console
  radio=$(grep -n 'dxberry-radio-hotplug' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  console=$(grep -n 'enable --now dxberryd.service' "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  if [[ -z $radio || -z $console ]] || (( radio > console )); then
    _fail "the station step must run after the radio step (radio '$radio', console '$console')"
  fi
  assert_file_contains "$DXB_BOOT_DIR/dxberry-status.txt" "station: https://<this-pi>/ (log in as dietpi)"
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

# A run that needs a reboot (GPS overlay, onboard WiFi re-enabled) says so where Debian and the
# console look: /run/reboot-required.
test_run_mode_writes_the_reboot_flag_when_a_reboot_is_needed() {
  full_env
  export DXB_REBOOT_FLAG=$TEST_TMP/run/reboot-required
  mkdir -p "$TEST_TMP/run"
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    # shellcheck disable=SC2030
    provision_radio() { DXB_RADIO_REBOOT_NEEDED=1; }
    main
  ) > /dev/null 2>&1
  assert_file_contains "$DXB_REBOOT_FLAG" "System restart required"
  rm -f "$DXB_REBOOT_FLAG"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    main
  ) > /dev/null 2>&1
  [[ -e $DXB_REBOOT_FLAG ]] && _fail "a run that needs no reboot must not write $DXB_REBOOT_FLAG"
  unset DXB_REBOOT_FLAG
  return 0
}

# A changed network file takes effect only at the next restart (netwatch adopts an interface that
# is already up), so a run that changed one says so where Debian and the console look, besides
# restarting netwatch and its own warning.
test_run_mode_flags_a_restart_when_the_network_files_change() {
  full_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    # shellcheck disable=SC2030
    provision_network() { DXB_NET_CHANGED=1; }
    main
  ) > /dev/null 2>&1
  assert_file_contains "$DXB_REBOOT_FLAG" "System restart required"
  assert_eq "$(tail -1 "$TEST_TMP/calls")" "systemd-run --quiet --on-active=3 systemctl restart dxberry-netwatch"
  assert_file_contains "$DXB_LOG_FILE" "run sudo reboot to apply the change"
  # first boot reboots anyway: no flag there
  rm -f "$DXB_REBOOT_FLAG"
  (
    # shellcheck disable=SC1091
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    dxb_gw_install() { return 0; }
    dxb_gw_seed() { return 0; }
    provision_network() { DXB_NET_CHANGED=1; }
    main --first-boot
  ) > /dev/null 2>&1
  [[ -e $DXB_REBOOT_FLAG ]] && _fail "first boot writes no restart flag (it reboots)"
  return 0
}

# ---- the apps step (Task 15) ---------------------------------------------------------------

# the driver's calls in order, comments and blank lines left out
pv_driver_steps() { grep -v '^[[:space:]]*#' "$DXB_ROOT/provision/bin/dxberry-provision" | grep -o '^[[:space:]]*provision_[a-z_]*' | tr -d ' \t' | tr '\n' ' '; }

test_provision_apps_runs_right_after_radio_and_absorbs_the_logbook_step() {
  local steps; steps=$(pv_driver_steps)
  assert_contains "$steps" "provision_radio provision_apps provision_station"
  assert_not_contains "$steps" "provision_logbook"
}

test_provision_apps_installs_every_template_once_and_installs_no_program() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  provision_apps
  local t
  for t in dxberry-screen@.service dxberry-ardop@.service dxberry-logbridge.service dxberry-logbook-flush.service dxberry-logbook-flush.timer; do
    assert_eq "$(cat "$DXB_SYSTEMD_DIR/$t")" "$(cat "$DXB_ROOT/provision/templates/$t")"
  done
  assert_eq "$(cat "$DXB_TMPFILES_DIR/dxberry-apps.conf")" "$(cat "$DXB_ROOT/provision/templates/dxberry-apps.tmpfiles")"
  assert_file_contains "$TEST_TMP/calls" "account -aG"                                # dxb_apps_user_ensure ran first
  assert_eq "$(grep -c 'systemctl daemon-reload' "$TEST_TMP/calls")" "1"
  assert_file_contains "$TEST_TMP/calls" "systemd-tmpfiles --create $DXB_TMPFILES_DIR/dxberry-apps.conf"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable --now dxberry-logbook-flush.timer"
  assert_file_not_contains "$TEST_TMP/calls" "apt-get"                                 # no program is installed or upgraded
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  assert_contains "${DXB_STATUS_LINES[*]}" "apps: 0 programs installed (manage them on the DXBerry page)"
  assert_contains "${DXB_STATUS_LINES[*]}" "logbook: not set"
}

test_provision_apps_second_run_changes_nothing() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  provision_apps
  : > "$TEST_TMP/calls"; DXB_STATUS_LINES=()
  provision_apps
  assert_file_not_contains "$TEST_TMP/calls" "daemon-reload"
  assert_file_not_contains "$TEST_TMP/calls" "systemd-tmpfiles"
  assert_file_not_contains "$TEST_TMP/calls" "apt-get"
  assert_eq "$(find "$DXB_SYSTEMD_DIR" "$DXB_TMPFILES_DIR" -name '*.dxbtmp*' | wc -l)" "0"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
}

test_provision_apps_a_failure_is_a_failed_step_named_apps() (
  full_env; provision_cfg 'PASSWORD=examplepass'
  DXB_TEMPLATES=$TEST_TMP/no-templates
  provision_apps 2> /dev/null
  assert_eq "${#DXB_FAILED_STEPS[@]}" "1"
  assert_contains "${DXB_FAILED_STEPS[0]}" "apps: "
)

# pv_nginx_scene: Pat installed behind a working nginx site (the station certificate under $TEST_TMP/certs);
# nginx and its checks are recorded in calls, and the check fails while $TEST_TMP/nginx-bad exists.
# shellcheck disable=SC2317  # called through the DXB_NGINX override and by provision_apps
pv_ss() { echo 'LISTEN 0 511 0.0.0.0:443 0.0.0.0:*'; }
pv_nginx() { echo "nginx $*" >> "$TEST_TMP/calls"; [[ ! -f $TEST_TMP/nginx-bad ]]; }
pv_nginx_scene() {
  full_env; provision_cfg 'PASSWORD=examplepass'
  export DXB_NGINX=pv_nginx DXB_STATION_TLS=$TEST_TMP/certs
  mkdir -p "$DXB_STATION_TLS"
  echo cert > "$DXB_STATION_TLS/station.crt"; echo key > "$DXB_STATION_TLS/station.key"
  dxb_webproxy_site pat > /dev/null 2>&1
  touch -d '1 hour ago' "$DXB_STATION_TLS"/station.* 
  : > "$TEST_TMP/calls"
}
# shellcheck disable=SC2317
pv_pat_nginx_dpkg() { dpkg-query() { case ${*: -1} in nginx|pat) printf 'installed 1.26' ;; libpam-runtime) printf 'installed 1.0' ;; *) return 1 ;; esac; }; }

test_provision_apps_leaves_a_current_site_alone() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  provision_apps 2> /dev/null
  assert_file_not_contains "$TEST_TMP/calls" "nginx -t"
  assert_file_not_contains "$TEST_TMP/calls" "reload nginx"
  assert_file_not_contains "$TEST_TMP/calls" "apt-get"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
)

test_provision_apps_picks_up_a_renewed_certificate_once() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  touch "$DXB_STATION_TLS/station.crt"
  provision_apps 2> /dev/null
  assert_eq "$(grep -c 'reload nginx' "$TEST_TMP/calls")" "1"
  : > "$TEST_TMP/calls"
  provision_apps 2> /dev/null
  assert_file_not_contains "$TEST_TMP/calls" "reload nginx"
)

# an update from a release that has no station certificate yet: provision_station renders the
# sites once the app has made one, so provision_apps neither warns nor touches nginx
test_provision_apps_stays_quiet_about_sites_while_there_is_no_station_certificate() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  rm -f "$DXB_STATION_TLS"/station.crt "$DXB_STATION_TLS"/station.key
  provision_apps 2> "$TEST_TMP/stderr"
  assert_not_contains "$(cat "$TEST_TMP/stderr")" "nginx site could not be rendered"
  assert_file_not_contains "$TEST_TMP/calls" "nginx"
)

test_provision_apps_rerenders_a_site_whose_template_changed() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  echo '# old' >> "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf"
  provision_apps 2> /dev/null
  assert_eq "$(grep -c 'reload nginx' "$TEST_TMP/calls")" "1"
  assert_file_not_contains "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf" "# old"
)

test_provision_apps_a_rejected_refresh_keeps_the_old_site_and_only_warns() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  local avail=$DXB_NGINX_DIR/sites-available/dxberry-pat.conf before
  echo '# old' >> "$avail"; before=$(< "$avail")
  : > "$TEST_TMP/nginx-bad"
  provision_apps 2> /dev/null
  assert_eq "$(< "$avail")" "$before"
  [[ -e $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "the site link was removed"
  assert_file_not_contains "$TEST_TMP/calls" "reload nginx"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  # the helper itself says 6
  dxb_webproxy_site_refresh pat 2> /dev/null; assert_eq "$?" "6"
  [[ -e $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "the site link was removed by the helper"
)

# shellcheck disable=SC2317
test_provision_apps_skips_a_disabled_site_and_missing_nginx() (
  pv_nginx_scene; pv_pat_nginx_dpkg
  echo '# old' >> "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf"
  rm "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf"
  provision_apps 2> /dev/null
  assert_file_not_contains "$TEST_TMP/calls" "nginx"
  # nginx not installed, Pat still is
  ln -s "$DXB_NGINX_DIR/sites-available/dxberry-pat.conf" "$DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf"
  dpkg-query() { case ${*: -1} in pat) printf 'installed 1.26' ;; libpam-runtime) printf 'installed 1.0' ;; *) return 1 ;; esac; }
  : > "$TEST_TMP/calls"
  provision_apps 2> /dev/null
  assert_file_not_contains "$TEST_TMP/calls" "nginx"
  assert_file_not_contains "$TEST_TMP/calls" "apt-get"
)
