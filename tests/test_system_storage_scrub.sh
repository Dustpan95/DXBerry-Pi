#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/system.sh"
source "$DXB_LIB/storage.sh"
source "$DXB_LIB/scrub.sh"

sys_env() {
  export DXB_HOSTNAME_FILE=$TEST_TMP/hostname DXB_HOSTS_FILE=$TEST_TMP/hosts DXB_LOCALTIME=$TEST_TMP/localtime \
    DXB_TIMEZONE_FILE=$TEST_TMP/timezone DXB_ZONEINFO_DIR=$TEST_TMP/zi DXB_DIETPI_FUNC=$TEST_TMP/nofunc \
    DXB_DIETPI_TXT=$TEST_TMP/dietpi.txt DXB_JOURNALD_DROPIN=$TEST_TMP/journald.d/dxberry.conf \
    DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_LOG_FILE=$TEST_TMP/log DXB_MODE=run
  mkdir -p "$DXB_ZONEINFO_DIR/America" "$DXB_BOOT_DIR"; : > "$DXB_ZONEINFO_DIR/America/Chicago"; : > "$DXB_ZONEINFO_DIR/UTC"
  echo DietPi > "$DXB_HOSTNAME_FILE"; printf '127.0.0.1 localhost\n127.0.1.1 DietPi\n' > "$DXB_HOSTS_FILE"
  printf 'CONFIG_SERIAL_CONSOLE_ENABLE=0\nAUTO_SETUP_GLOBAL_PASSWORD=dietpi\n' > "$DXB_DIETPI_TXT"
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  chpasswd() { cat >> "$TEST_TMP/chpasswd"; }
  swapon() { echo "NAME"; echo "/dev/zram0"; }
  hostname() { :; }
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_CONSUMED_SECRETS=''
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
  assert_contains "$DXB_CONSUMED_SECRETS" "PASSWORD"
  provision_system
  assert_eq "$(grep -c ssh-ed25519 "$TEST_TMP/root/.ssh/authorized_keys")" "1"
  assert_eq "$(stat -c %a "$TEST_TMP/root/.ssh/authorized_keys")" "600"
}

test_system_skips_password_when_applied_or_first_boot() {
  sys_env
  sys_cfg 'PASSWORD=<applied>'
  provision_system
  [[ -f $TEST_TMP/chpasswd ]] && _fail "chpasswd must not run for <applied>"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "PASSWORD"
  sys_cfg 'PASSWORD=secretpass'
  DXB_MODE=first-boot provision_system
  [[ -f $TEST_TMP/chpasswd ]] && _fail "chpasswd must not run on first boot (DietPi applied it)"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "PASSWORD"
}

test_system_password_failure_is_reported_and_not_consumed() {
  sys_env
  sys_cfg 'PASSWORD=secretpass'
  chpasswd() { return 1; }
  provision_system
  assert_contains "${DXB_FAILED_STEPS[*]}" "chpasswd failed"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "PASSWORD"
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
  # Sub-case: missing template fails cleanly
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  local saved_templates=$DXB_TEMPLATES
  DXB_TEMPLATES=$TEST_TMP/empty-templates
  mkdir -p "$DXB_TEMPLATES"
  provision_storage 2> /dev/null
  assert_file_contains "$DXB_JOURNALD_DROPIN" "Storage=volatile"
  assert_fails test -e "$DXB_JOURNALD_DROPIN.dxbtmp"
  assert_contains "${DXB_FAILED_STEPS[*]}" "journald template missing"
  DXB_TEMPLATES=$saved_templates
}

test_scrub_replaces_secrets_in_place_and_dietpi_password() {
  sys_env
  printf 'HOSTNAME=x\r\nPASSWORD = "secretpass"\r\nWIFI_SSID=Home\r\nWIFI_PASSWORD=wifipass1\r\nWEBUI_PASSWORD=<applied>\r\n' > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"; DXB_CFG[WIFI_COUNTRY]=US; dxb_config_validate
  DXB_CONSUMED_SECRETS='PASSWORD WIFI_PASSWORD'
  provision_scrub
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=<applied>"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_PASSWORD=<applied>"
  assert_file_not_contains "$DXB_BOOT_DIR/dxberry.txt" "secretpass"
  assert_file_not_contains "$DXB_BOOT_DIR/dxberry.txt" "wifipass1"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "HOSTNAME=x"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD="
  assert_file_not_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=dietpi"
  # Sub-case: write failure is detected and not marked applied
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  printf 'PASSWORD=newsecret\nWIFI_PASSWORD=newwifi\n' > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"; DXB_CFG[WIFI_COUNTRY]=US; dxb_config_validate
  DXB_CONSUMED_SECRETS='PASSWORD WIFI_PASSWORD'
  mkdir "$DXB_BOOT_DIR/dxberry.txt.dxbtmp"
  provision_scrub 2> /dev/null
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=newsecret"
  [[ "${DXB_CFG[PASSWORD]}" == "$DXB_APPLIED" ]] && _fail "PASSWORD should not be marked applied when scrub fails"
  assert_contains "${DXB_FAILED_STEPS[*]}" "scrub"
  assert_file_not_contains "$DXB_BOOT_DIR/dxberry.txt" "<applied>"
}

test_scrub_leaves_unconsumed_secrets_alone() {
  sys_env
  printf 'PASSWORD=secretpass\nWIFI_SSID=Home\nWIFI_PASSWORD=wifipass1\n' > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_config_load "$DXB_BOOT_DIR/dxberry.txt"; DXB_CFG[WIFI_COUNTRY]=US; dxb_config_validate
  printf "aWIFI_SSID[0]='Home'\naWIFI_KEY[0]='wifipass1'\naWIFI_KEYMGR[0]='WPA-PSK'\n" > "$DXB_DIETPI_WIFI"
  # Neither secret was actually consumed this run (e.g. Graywolf install failed before install
  # ever reached it, or the WiFi import never ran): scrub must leave both alone so a re-run can
  # still use them, rather than replacing them with <applied> and losing them for good.
  DXB_CONSUMED_SECRETS=''
  provision_scrub
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=secretpass"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_PASSWORD=wifipass1"
  [[ "${DXB_CFG[PASSWORD]}" == "$DXB_APPLIED" ]] && _fail "PASSWORD must not be marked applied when it was never consumed"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_KEY[0]='wifipass1'"
  # Once actually consumed, scrub proceeds as usual (including removing the WiFi key file).
  DXB_CONSUMED_SECRETS='PASSWORD WIFI_PASSWORD'
  provision_scrub
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=<applied>"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_PASSWORD=<applied>"
  [[ -f $DXB_DIETPI_WIFI ]] && _fail "dietpi-wifi.txt should be removed once WIFI_PASSWORD was consumed"
}
