#!/usr/bin/env bash
# Uses st_priority_fixture from test_status_priority.sh; run.sh loads all test files first.
# shellcheck disable=SC2034

test_status_reporting_uses_one_cpu_before_loading_libraries() {
  st_priority_fixture
  local before out cpus value
  before=$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  cpus=$(jq -r .cpus <<< "$out")
  [[ $cpus =~ ^[0-9]+$ ]] || _fail "report was not confined to one CPU: $cpus"
  while IFS= read -r value; do assert_eq "$value" "$cpus"; done < "$TEST_TMP/source-cpus"
  assert_eq "$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)" "$before"
}

test_status_reporting_never_expands_an_existing_cpu_restriction() {
  st_priority_fixture
  local before first out
  before=$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  first=${before%%,*}; first=${first%%-*}
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" taskset -c "$first" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  assert_eq "$(jq -r .cpus <<< "$out")" "$first"
}

test_status_reporting_survives_an_affinity_permission_failure() {
  st_priority_fixture
  local before out
  before=$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  mkdir -p "$TEST_TMP/deny"
  cat > "$TEST_TMP/deny/taskset" <<'SH'
#!/bin/sh
: > "$TEST_TMP/taskset-attempted"
exit 1
SH
  chmod 755 "$TEST_TMP/deny/taskset"
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" PATH="$TEST_TMP/deny:$PATH" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  [[ -f $TEST_TMP/taskset-attempted ]] || _fail "affinity was not attempted"
  assert_eq "$(jq -r .cpus <<< "$out")" "$before"
}

test_status_reporting_survives_a_missing_affinity_utility() {
  st_priority_fixture
  local before out
  before=$(awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
  ln -s "$(command -v nice)" "$TEST_TMP/empty-path/nice"
  out=$(env -u DXB_STATUS_LOW_PRIORITY DXB_LIB="$TEST_TMP/lib" PATH="$TEST_TMP/empty-path" /bin/bash "$DXB_ROOT/provision/bin/dxberry-status" --json)
  assert_eq "$(jq -r .cpus <<< "$out")" "$before"
}
