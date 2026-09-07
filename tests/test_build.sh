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
