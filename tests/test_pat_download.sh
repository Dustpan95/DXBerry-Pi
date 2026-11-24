#!/usr/bin/env bash

pat_download_env() {
  mkdir -p "$TEST_TMP/bin" "$TEST_TMP/packages"
  cat > "$TEST_TMP/bin/curl" <<'CURL'
#!/bin/bash
printf 'called\n' >> "$TEST_TMP/curl.calls"
out=''
while (( $# )); do
  if [[ $1 == --output ]]; then out=$2; shift; fi
  shift
done
printf 'partial fixture download' > "$out"
exit 28
CURL
  chmod 755 "$TEST_TMP/bin/curl"
}

test_pat_download_refuses_invalid_arguments() {
  local out rc
  out=$(bash "$DXB_ROOT/tests/integration/download-pat.sh" --unknown 2>&1); rc=$?
  assert_eq "$rc" 2
  assert_contains "$out" 'Usage:'
}

test_pat_download_preserves_existing_invalid_package_without_fetching() {
  pat_download_env
  local target=$TEST_TMP/packages/pat_1.0.1_linux_amd64.deb out rc
  printf 'retain original' > "$target"
  out=$(PATH="$TEST_TMP/bin:$PATH" bash "$DXB_ROOT/tests/integration/download-pat.sh" "$TEST_TMP/packages" 2>&1); rc=$?
  assert_eq "$rc" 1
  assert_contains "$out" 'size mismatch'
  assert_eq "$(cat "$target")" 'retain original'
  [[ ! -e $TEST_TMP/curl.calls ]] || _fail 'existing package triggered a download'
}

test_pat_download_checks_digest_even_at_expected_size() {
  pat_download_env
  local target=$TEST_TMP/packages/pat_1.0.1_linux_amd64.deb out rc before
  truncate -s 4940336 "$target"
  before=$(sha256sum < "$target")
  out=$(PATH="$TEST_TMP/bin:$PATH" bash "$DXB_ROOT/tests/integration/download-pat.sh" "$TEST_TMP/packages" 2>&1); rc=$?
  assert_eq "$rc" 1
  assert_contains "$out" 'checksum mismatch'
  assert_eq "$(sha256sum < "$target")" "$before"
  [[ ! -e $TEST_TMP/curl.calls ]] || _fail 'existing package triggered a download'
}

test_pat_download_refuses_symlink_without_following_it() {
  local out rc
  printf 'untouched' > "$TEST_TMP/original"
  ln -s "$TEST_TMP/original" "$TEST_TMP/link.deb"
  out=$(bash "$DXB_ROOT/tests/integration/download-pat.sh" --verify-only "$TEST_TMP/link.deb" 2>&1); rc=$?
  assert_eq "$rc" 1
  assert_contains "$out" 'regular Pat package'
  assert_eq "$(cat "$TEST_TMP/original")" 'untouched'
}

test_pat_download_cleans_partial_transfer_without_publishing() {
  pat_download_env
  local rc
  PATH="$TEST_TMP/bin:$PATH" bash "$DXB_ROOT/tests/integration/download-pat.sh" "$TEST_TMP/packages" > /dev/null 2>&1; rc=$?
  assert_eq "$rc" 28
  assert_eq "$(cat "$TEST_TMP/curl.calls")" called
  [[ ! -e $TEST_TMP/packages/pat_1.0.1_linux_amd64.deb ]] || _fail 'partial download was published'
  [[ -z $(find "$TEST_TMP/packages" -mindepth 1 -print -quit) ]] || _fail 'temporary download was retained'
}
