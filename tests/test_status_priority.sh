#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034

st_priority_fixture() {
  mkdir -p "$TEST_TMP/lib" "$TEST_TMP/empty-path"
  cat > "$TEST_TMP/lib/common.sh" <<'SH'
/usr/bin/awk '{print $19}' /proc/self/stat >> "$TEST_TMP/source-priorities"
/usr/bin/awk '/Cpus_allowed_list/ {print $2}' /proc/self/status >> "$TEST_TMP/source-cpus"
dxb_require_root() { :; }
SH
  local part
  for part in config graywolf gps apps; do : > "$TEST_TMP/lib/$part.sh"; done
  cat > "$TEST_TMP/lib/status.sh" <<'SH'
dxb_status_json() {
  local priority cpus
  priority=$(/usr/bin/awk '{print $19}' /proc/self/stat)
  cpus=$(/usr/bin/awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  printf '{"nice":%s,"cpus":"%s"}\n' "$priority" "$cpus"
}
SH
}

test_status_priority_applies_before_the_first_library_is_loaded() {
  st_priority_fixture
  local before expected value
  before=$(awk '{print $19}' /proc/self/stat)
  expected=$(( before + 10 )); (( expected <= 19 )) || expected=19
  env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json > /dev/null
  while IFS= read -r value; do assert_eq "$value" "$expected"; done < "$TEST_TMP/source-priorities"
}

test_status_entrypoint_lowers_only_its_reporting_process_priority() {
  st_priority_fixture
  local before expected out after
  before=$(awk '{print $19}' /proc/self/stat)
  expected=$(( before + 10 )); (( expected <= 19 )) || expected=19
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  assert_eq "$(jq -r .nice <<< "$out")" "$expected"
  after=$(awk '{print $19}' /proc/self/stat)
  assert_eq "$after" "$before"
}

test_status_entrypoint_does_not_promote_an_already_niced_report() {
  st_priority_fixture
  local before expected out
  before=$(awk '{print $19}' /proc/self/stat)
  expected=$(( before + 25 )); (( expected <= 19 )) || expected=19
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" nice -n 15 /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  assert_eq "$(jq -r .nice <<< "$out")" "$expected"
}

test_status_entrypoint_still_reports_without_the_priority_utility() {
  st_priority_fixture
  local before out
  before=$(awk '{print $19}' /proc/self/stat)
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" PATH="$TEST_TMP/empty-path" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  assert_eq "$(jq -r .nice <<< "$out")" "$before"
}

test_status_sourcing_does_not_reexecute_or_change_priority() (
  st_priority_fixture
  local before after cpus
  before=$(awk '{print $19}' /proc/self/stat)
  cpus=$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  DXB_LIB=$TEST_TMP/lib
  unset DXB_STATUS_LOW_PRIORITY
  source "$DXB_ROOT/provision/bin/dxberry-status"
  after=$(awk '{print $19}' /proc/self/stat)
  assert_eq "$after" "$before"
  assert_eq "$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)" "$cpus"
  assert_eq "${DXB_STATUS_LOW_PRIORITY:-unset}" unset
)
