#!/usr/bin/env bash
# Assertion helpers for tests/run.sh. Sourced, never executed.
# shellcheck disable=SC2034
TESTS_RUN=0
TESTS_FAILED=0

# A failure inside a subshell ( ... ) cannot raise the counter of the shell that runs the test,
# so it is also written to TESTS_FAIL_LOG, a per-test file run.sh checks after every test.
_fail() {
  TESTS_FAILED=$((TESTS_FAILED + 1))
  if [[ -n ${TESTS_FAIL_LOG:-} ]]; then printf '%s\n' "$1" >> "$TESTS_FAIL_LOG"; fi
  printf '    FAIL in %s: %s\n' "${FUNCNAME[2]:-?}" "$1" >&2
}
assert_eq() { [[ "$1" == "$2" ]] || _fail "expected '$2', got '$1'"; }
assert_contains() { [[ "$1" == *"$2"* ]] || _fail "expected to find '$2' in: $1"; }
assert_not_contains() { [[ "$1" != *"$2"* ]] || _fail "did not expect '$2' in: $1"; }
assert_ok() { "$@" || _fail "expected success: $*"; }
assert_fails() { if "$@"; then _fail "expected failure: $*"; fi; }
assert_file_contains() { grep -qF -- "$2" "$1" || _fail "expected $1 to contain '$2'"; }
assert_file_not_contains() { if grep -qF -- "$2" "$1"; then _fail "did not expect $1 to contain '$2'"; fi; }
