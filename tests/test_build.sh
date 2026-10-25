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
  for b in dxberry-provision dxberry-netwatch dxberry-radio dxberry-status dxberry-config dxberry-update; do
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

test_build_ships_the_station_files() {
  local required f
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  for f in provision/lib/station.sh provision/templates/dxberryd.service provision/templates/nginx-dxberry.conf \
    provision/templates/pam-dxberry provision/bin/dxberryd station/web/index.html station/web/dxberry.js station/web/dxberry.css; do
    assert_contains "$required" "$f"
  done
  # nothing of the Cockpit console is shipped any more
  assert_not_contains "$required" "cockpit"
  assert_not_contains "$required" "console.sh"
}

# bt_hide_bin / bt_restore_bin: the built service binary is git-ignored; a test controls whether one is there.
bt_hide_bin() { local b=$DXB_ROOT/provision/bin/dxberryd; if [[ -e $b ]]; then mv -f "$b" "$b.bt-keep"; fi; }
bt_restore_bin() { local b=$DXB_ROOT/provision/bin/dxberryd; rm -f "$b"; if [[ -e $b.bt-keep ]]; then mv -f "$b.bt-keep" "$b"; fi; }

test_build_tree_check_lists_station_files() {
  local b=$DXB_ROOT/provision/bin/dxberryd out
  bt_hide_bin
  out=$("$DXB_ROOT/build/build-image.sh" --check 2>&1); local rc=$?
  assert_eq "$rc" "1"
  assert_contains "$out" "missing provision/bin/dxberryd"
  : > "$b"; chmod 755 "$b"
  out=$("$DXB_ROOT/build/build-image.sh" --check 2>&1); rc=$?
  bt_restore_bin
  assert_eq "$out" "tree ok"
  assert_eq "$rc" "0"
}

test_build_update_file_needs_the_service_binary() {
  local out=$TEST_TMP/out msg
  bt_hide_bin
  msg=$("$DXB_ROOT/build/make-update-tarball.sh" v0.5.0-rc1 "$out" 2>&1); local rc=$?
  bt_restore_bin
  assert_eq "$rc" "1"
  assert_contains "$msg" "provision/bin/dxberryd is missing"
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
  assert_contains "$required" "station/web/settings.js"
}

test_build_ships_the_updates_page_script() {
  local required
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "station/web/updates.js"
}

test_build_ships_the_update_command() {
  local required executable
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  executable=$(sed -n '/^EXECUTABLE_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" "provision/bin/dxberry-update"
  assert_contains "$required" "provision/lib/update.sh"
  assert_contains "$executable" "provision/bin/dxberry-update"
}

# sudo build-image.sh creates out/ as root; the update file is made by the runner user, so it must
# come first (the runner then owns out/, and root can still write the image into it) - and the
# image build hands out/ itself back to the invoking user as well.
test_build_release_makes_the_update_file_before_the_image() {
  local wf=$DXB_ROOT/.github/workflows/release.yml upd img
  upd=$(grep -n -- '- name: DXBerry update file$' "$wf" | cut -d: -f1)
  img=$(grep -n -- '- name: Build image$' "$wf" | cut -d: -f1)
  if [[ -z $upd || -z $img ]]; then
    _fail "release.yml must have both a 'DXBerry update file' and a 'Build image' step (got '$upd' '$img')"
  elif (( upd > img )); then
    _fail "release.yml makes the update file (line $upd) after the image build (line $img), which leaves out/ owned by root"
  fi
  # shellcheck disable=SC2016  # the literal line, unexpanded
  assert_file_contains "$DXB_ROOT/build/build-image.sh" 'chown "${SUDO_UID}:${SUDO_GID:-0}" "$OUT"'
}

test_build_makes_the_update_file() {
  local out=$TEST_TMP/out name list b=$DXB_ROOT/provision/bin/dxberryd
  bt_hide_bin; : > "$b"
  name=$( umask 002; "$DXB_ROOT/build/make-update-tarball.sh" v0.3.0-rc4 "$out" ); local rc=$?
  bt_restore_bin
  (( rc == 0 )) || _fail "make-update-tarball.sh failed"
  assert_eq "$name" "$out/dxberry-pi-0.3.0-rc4.tar.gz"
  ( cd "$out" && sha256sum -c --quiet dxberry-pi-0.3.0-rc4.tar.gz.sha256 ) || _fail "the .sha256 does not match"
  list=$(tar -tvzf "$name")
  assert_contains "$list" "dxberry/bin/dxberry-provision"
  assert_contains "$list" "dxberry/lib/settings.sh"
  assert_contains "$list" "dxberry/bin/dxberryd"
  assert_contains "$list" "dxberry/lib/station.sh"
  assert_not_contains "$list" "cockpit"
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

test_build_ships_every_program_file() {
  local required f d
  required=" $(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh" | tr -s ' \n' '  ') "
  for f in "$DXB_ROOT"/provision/lib/apps/*.sh "$DXB_ROOT"/provision/templates/dxberry-*; do
    [[ -f $f ]] || continue
    f=${f#"$DXB_ROOT"/}
    [[ $required == *" $f "* ]] || _fail "$f is not in REQUIRED_FILES"
  done
  d=station/web/vendor/novnc
  [[ -f $DXB_ROOT/$d/core/rfb.js ]] || _fail "$d/core/rfb.js is missing from the tree"
  grep -qF "$d/core/rfb.js" "$DXB_ROOT/build/build-image.sh" || _fail "build-image.sh does not check $d/core/rfb.js"
  assert_contains "$required" "provision/bin/dxberry-app"
  grep -qF "ln -sf /opt/dxberry/bin/dxberry-app " "$DXB_ROOT/build/build-image.sh" || _fail "no /usr/local/sbin link for dxberry-app"
}

test_build_check_fails_on_a_missing_or_non_executable_dxberry_app() {
  mkdir -p "$TEST_TMP/build"
  cp -r "$DXB_ROOT/boot" "$DXB_ROOT/provision" "$TEST_TMP/"
  cp "$DXB_ROOT/build/build-image.sh" "$TEST_TMP/build/"
  chmod -x "$TEST_TMP/provision/bin/dxberry-app"
  local out; out=$("$TEST_TMP/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "not executable: provision/bin/dxberry-app"
  rm "$TEST_TMP/provision/bin/dxberry-app"
  out=$("$TEST_TMP/build/build-image.sh" --check 2>&1)
  assert_eq "$?" "1"
  assert_contains "$out" "missing provision/bin/dxberry-app"
}
