#!/usr/bin/env bash
# Runs every test_* function defined in tests/test_*.sh. Needs only bash 5, awk and jq.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
export DXB_ROOT=$PWD
export DXB_LIB=$DXB_ROOT/provision/lib
export DXB_TEMPLATES=$DXB_ROOT/provision/templates
# shellcheck source=tests/lib.sh
source "$DXB_ROOT/tests/lib.sh"
for f in "$DXB_ROOT"/tests/test_*.sh; do
  # shellcheck disable=SC1090
  source "$f"
done
for t in $(declare -F | awk '{print $3}' | grep '^test_' | sort); do
  TESTS_RUN=$((TESTS_RUN + 1))
  TEST_TMP=$(mktemp -d)
  export TEST_TMP
  before=$TESTS_FAILED
  "$t"
  rm -rf "$TEST_TMP"
  if (( TESTS_FAILED == before )); then echo "ok   $t"; else echo "FAIL $t"; fi
done
echo "$TESTS_RUN tests, $TESTS_FAILED failures"
(( TESTS_FAILED == 0 ))
