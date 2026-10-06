#!/usr/bin/env bash

test_build_check_passes_on_complete_tree() {
  local out; out=$("$DXB_ROOT/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "0"
  assert_contains "$out" "tree ok"
}

test_build_required_files_names_radio_plumbing() {
  local f required executable
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  executable=$(sed -n '/^EXECUTABLE_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  for f in provision/bin/dxberry-radio provision/lib/radio.sh provision/lib/radio_udev.sh \
    provision/lib/rigctld.sh provision/lib/gps.sh provision/lib/apps/graywolf.sh \
    provision/share/radio-profiles.tsv provision/templates/rigctld@.service \
    provision/templates/dxberry-radio-hotplug.service provision/templates/dxberry-radio-wire.service provision/templates/dxberry-radio.tmpfiles \
    provision/templates/70-dxberry-radio.rules.head provision/templates/dxberry-audio.conf \
    provision/templates/chrony-dxberry.conf provision/templates/gpsd-default.tmpl; do
    assert_contains "$required" "$f"
  done
  assert_contains "$executable" "provision/bin/dxberry-radio"
  # the wire unit exists to run after graywolf; the hotplug unit must stay before it
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-radio-wire.service" "After=dxberry-radio-hotplug.service graywolf.service"
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-radio-hotplug.service" "Before=graywolf.service"
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-radio-wire.service" "TimeoutStartSec="
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-radio-hotplug.service" "TimeoutStartSec="
}

test_build_check_fails_on_missing_file() {
  mkdir -p "$TEST_TMP/build"
  cp -r "$DXB_ROOT/boot" "$DXB_ROOT/provision" "$TEST_TMP/"
  cp "$DXB_ROOT/build/build-image.sh" "$TEST_TMP/build/"
  rm "$TEST_TMP/provision/bin/dxberry-netwatch"
  local out; out=$("$TEST_TMP/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "missing provision/bin/dxberry-netwatch"
}

test_build_check_fails_on_non_executable_file() {
  mkdir -p "$TEST_TMP/build"
  cp -r "$DXB_ROOT/boot" "$DXB_ROOT/provision" "$TEST_TMP/"
  cp "$DXB_ROOT/build/build-image.sh" "$TEST_TMP/build/"
  chmod -x "$TEST_TMP/provision/bin/dxberry-preboot"
  local out; out=$("$TEST_TMP/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "not executable: provision/bin/dxberry-preboot"
}

test_build_symlinks_every_command_into_usr_local_sbin() {
  local b
  for b in dxberry-provision dxberry-netwatch dxberry-radio dxberry-status dxberry-config; do
    grep -qF "ln -sf /opt/dxberry/bin/$b " "$DXB_ROOT/build/build-image.sh" \
      || _fail "build-image.sh has no /usr/local/sbin symlink line for $b"
  done
}

test_build_ships_the_status_command() {
  local required executable
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  executable=$(sed -n '/^EXECUTABLE_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "provision/bin/dxberry-status"
  assert_contains "$required" "provision/lib/status.sh"
  assert_contains "$executable" "provision/bin/dxberry-status"
}

test_build_ships_the_console_step() {
  local required
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "provision/lib/console.sh"
  assert_contains "$required" "provision/templates/cockpit-listen.conf"
}

test_build_ships_the_console_page() {
  local required f
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  for f in manifest.json index.html dxberry.js dxberry.css; do
    assert_contains "$required" "provision/cockpit/dxberry/$f"
  done
}

test_build_ships_the_config_command() {
  local required executable
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  executable=$(sed -n '/^EXECUTABLE_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "provision/bin/dxberry-config"
  assert_contains "$required" "provision/lib/settings.sh"
  assert_contains "$required" "provision/templates/dxberry-config-boot.service"
  assert_contains "$executable" "provision/bin/dxberry-config"
}

test_build_ships_the_settings_page_script() {
  local required
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "provision/cockpit/dxberry/settings.js"
}

test_build_makes_the_update_file() {
  local out=$TEST_TMP/out name list
  name=$( umask 002; "$DXB_ROOT/build/make-update-tarball.sh" v0.3.0-rc4 "$out" ) || _fail "make-update-tarball.sh failed"
  assert_eq "$name" "$out/dxberry-pi-0.3.0-rc4.tar.gz"
  ( cd "$out" && sha256sum -c --quiet dxberry-pi-0.3.0-rc4.tar.gz.sha256 ) || _fail "the .sha256 does not match"
  list=$(tar -tvzf "$name")
  assert_contains "$list" "dxberry/bin/dxberry-provision"
  assert_contains "$list" "dxberry/lib/settings.sh"
  assert_contains "$list" "dxberry/cockpit/dxberry/index.html"
  # modes as the image installs them, owned by root
  assert_eq "$(awk '$NF == "dxberry/bin/dxberry-provision" {print $1, $2}' <<< "$list")" "-rwxr-xr-x 0/0"
  assert_eq "$(awk '$NF == "dxberry/lib/common.sh" {print $1}' <<< "$list")" "-rw-r--r--"
  assert_eq "$(awk '$NF == "dxberry/VERSION" {print $1}' <<< "$list")" "-rw-r--r--"
  assert_eq "$(awk '$NF == "dxberry/RELEASE" {print $1}' <<< "$list")" "-rw-r--r--"
  assert_eq "$(tar -xOzf "$name" dxberry/VERSION)" "0.3.0-rc4"
  assert_contains "$(tar -xOzf "$name" dxberry/RELEASE)" "DXBERRY_VERSION=0.3.0-rc4"
  assert_contains "$(tar -xOzf "$name" dxberry/RELEASE)" "DXBERRY_COMMIT="
  "$DXB_ROOT/build/make-update-tarball.sh" 'not a version' "$out" 2> /dev/null && _fail "a bad version must be refused"
  return 0
}
