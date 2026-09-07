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
