#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/update.sh"

release_source() {
  local version=$1
  up_tree "$DXB_OPT" "$version"
  mkdir -p "$DXB_OPT/share"
  printf '1\n' > "$DXB_OPT/share/wsjtx-native-protocol"
  export DXB_PROGRAM_RUN=$DXB_RUN_DIR/station-programs
  cat > "$DXB_OPT/bin/dxberryd" <<'SH'
#!/bin/bash
printf '%s\n' "$@" >> "$TEST_TMP/release-args"
cat "$DXB_OPT/VERSION" > "$TEST_TMP/release-source-version"
if flock -n "$DXB_RUN_DIR/native-release.lock" -c true; then echo missing-lock >&2; exit 9; fi
touch "$TEST_TMP/release-lock-held"
[[ ! -e $TEST_TMP/release-refuse ]] || exit 7
target=${*: -1}
if [[ ! -f $target/share/wsjtx-native-protocol || $(cat "$target/share/wsjtx-native-protocol") != 1 ]]; then
  mkdir -p "$DXB_STATE_DIR/station/wsjtx-release"
  echo paused > "$DXB_STATE_DIR/station/wsjtx-release/fixture"
fi
SH
  chmod 755 "$DXB_OPT/bin/dxberryd"
}

release_legacy_archive() {
  local version=$1 name=dxberry-pi-$1.tar.gz
  up_tree "$TEST_TMP/legacy/dxberry" "$version"
  tar -czf "$TEST_TMP/http/$name" -C "$TEST_TMP/legacy" dxberry
  ( cd "$TEST_TMP/http" && sha256sum "$name" > "$name.sha256" )
}

test_native_release_refusal_keeps_rollback_trees_in_place() (
  up_env; up_provision_stub; release_source 0.3.0-rc4
  up_tree "$DXB_OPT.prev" 0.3.0-rc3
  touch "$TEST_TMP/release-refuse"
  dxb_update_apply_rollback > "$TEST_TMP/out" 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc4
  assert_eq "$(cat "$DXB_OPT.prev/VERSION")" 0.3.0-rc3
  assert_eq "$(cat "$TEST_TMP/release-source-version")" 0.3.0-rc4
  assert_ok test -f "$TEST_TMP/release-lock-held"
  assert_ok test ! -e "$TEST_TMP/provision-ran"
  assert_ok flock -n "$DXB_RUN_DIR/native-release.lock" -c true
)

test_native_release_rollback_preserves_pause_outside_replaced_tree() (
  up_env; up_provision_stub; release_source 0.3.0-rc4
  up_tree "$DXB_OPT.prev" 0.3.0-rc3
  assert_ok dxb_update_apply_rollback > "$TEST_TMP/out" 2>&1
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc3
  assert_file_contains "$DXB_STATE_DIR/station/wsjtx-release/fixture" paused
  assert_file_contains "$TEST_TMP/release-args" "$DXB_OPT.prev"
  assert_file_contains "$TEST_TMP/release-args" -program-release-run
  assert_not_contains "$(cat "$TEST_TMP/release-args")" resume
  assert_ok flock -n "$DXB_RUN_DIR/native-release.lock" -c true
)

test_native_release_failed_rollback_keeps_pause_for_reconciliation() (
  up_env; up_provision_stub; release_source 0.3.0-rc4
  up_tree "$DXB_OPT.prev" 0.3.0-rc3
  mv() { [[ $* == "-T $DXB_OPT.prev $DXB_OPT" ]] && return 1; command mv "$@"; }
  dxb_update_apply_rollback > "$TEST_TMP/out" 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc4
  assert_file_contains "$DXB_STATE_DIR/station/wsjtx-release/fixture" paused
  assert_not_contains "$(cat "$TEST_TMP/release-args")" resume
)

test_native_release_failed_setup_keeps_pause_after_rollback_swap() (
  up_env; up_provision_stub; release_source 0.3.0-rc4
  up_tree "$DXB_OPT.prev" 0.3.0-rc3
  echo 1 > "$TEST_TMP/provision-rc"
  dxb_update_apply_rollback > "$TEST_TMP/out" 2>&1; assert_eq "$?" 8
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc3
  assert_file_contains "$DXB_STATE_DIR/station/wsjtx-release/fixture" paused
  assert_ok flock -n "$DXB_RUN_DIR/native-release.lock" -c true
)

test_native_release_refusal_precedes_update_swap_and_backup_rotation() (
  up_env; up_provision_stub; release_source 0.3.0-rc3
  up_tree "$DXB_OPT.prev" 0.3.0-rc2
  dxb_update_set_prereleases on
  release_legacy_archive 0.3.0-rc4
  touch "$TEST_TMP/release-refuse"
  dxb_update_apply_dxberry > "$TEST_TMP/out" 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc3
  assert_eq "$(cat "$DXB_OPT.prev/VERSION")" 0.3.0-rc2
  assert_ok test ! -e "$DXB_OPT.new"
  assert_ok test ! -e "$TEST_TMP/provision-ran"
)

test_native_release_compatible_update_holds_lock_through_setup() (
  up_env; up_provision_stub; release_source 0.3.0-rc3
  dxb_update_set_prereleases on
  up_release_file 0.3.0-rc4 native
  cat > "$DXB_UPDATE_PROVISION" <<'SH'
#!/bin/bash
if flock -n "$DXB_RUN_DIR/native-release.lock" -c true; then exit 9; fi
touch "$TEST_TMP/setup-lock-held"
SH
  assert_ok dxb_update_apply_dxberry > "$TEST_TMP/out" 2>&1
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc4
  assert_ok test -f "$TEST_TMP/release-lock-held"
  assert_ok test -f "$TEST_TMP/setup-lock-held"
  assert_ok test ! -e "$DXB_STATE_DIR/station/wsjtx-release"
  assert_ok flock -n "$DXB_RUN_DIR/native-release.lock" -c true
)

test_native_release_protocol_marker_is_required_in_image_tree() (
  local required
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" provision/share/wsjtx-native-protocol
  assert_eq "$(cat "$DXB_ROOT/provision/share/wsjtx-native-protocol")" 1
)
