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
    provision/templates/dxberry-radio-hotplug.service provision/templates/dxberry-radio.tmpfiles \
    provision/templates/70-dxberry-radio.rules.head provision/templates/dxberry-audio.conf \
    provision/templates/chrony-dxberry.conf provision/templates/gpsd-default.tmpl; do
    assert_contains "$required" "$f"
  done
  assert_contains "$executable" "provision/bin/dxberry-radio"
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
  for b in dxberry-provision dxberry-netwatch dxberry-radio; do
    grep -qF "ln -sf /opt/dxberry/bin/$b " "$DXB_ROOT/build/build-image.sh" \
      || _fail "build-image.sh has no /usr/local/sbin symlink line for $b"
  done
}
