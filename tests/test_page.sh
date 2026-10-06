#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
PG_DIR=$DXB_ROOT/provision/cockpit/dxberry

# pg_fields: the dxberry-status --json paths dxberry.js declares it reads (between its markers).
pg_fields() { sed -n '/fields-begin/,/fields-end/p' "$PG_DIR/dxberry.js" | grep -oE '"[a-z0-9_.*]+"' | tr -d '"'; }
# pg_has_path FILE PATH: PATH (dot-separated; * = the first element or entry) exists in FILE's JSON.
pg_has_path() {
  jq -e --arg p "$2" '
    def has_path($t):
      if ($t | length) == 0 then true
      elif $t[0] == "*" then
        (if type == "object" and length > 0 then (to_entries[0].value | has_path($t[1:]))
         elif type == "array" and length > 0 then (.[0] | has_path($t[1:]))
         else false end)
      elif type == "object" and has($t[0]) then (.[$t[0]] | has_path($t[1:]))
      else false end;
    has_path($p | split("."))' "$1" > /dev/null
}
# a receiver with a 3D fix, as gpspipe -w prints it
pg_gpspipe() {
  printf '%s\n' '{"class":"DEVICES","devices":[{"path":"/dev/ttyACM0"}]}' \
    '{"class":"TPV","mode":3,"lat":38.9,"lon":-94.6,"altHAE":300,"speed":0,"time":"2026-10-06T12:00:00Z"}' \
    '{"class":"SKY","uSat":8,"nSat":12}'
}

test_page_manifest_makes_dxberry_the_first_menu_entry() {
  local m=$PG_DIR/manifest.json
  assert_eq "$(jq -r '.menu.index.label' "$m")" "DXBerry"
  # Cockpit opens the first entry of its ordered menu after login; its own Overview is order 10
  assert_eq "$(jq -r '.menu.index.order < 10' "$m")" "true"
  assert_eq "$(jq -r '.requires.cockpit | type' "$m")" "string"
}

# Cockpit's page policy allows this package's own files only: no inline script, handler or style.
test_page_html_loads_only_its_own_files() {
  local html=$PG_DIR/index.html
  assert_eq "$(grep -oE 'src="[^"]*"' "$html" | sort | tr '\n' ' ')" 'src="../base1/cockpit.js" src="dxberry.js" '
  assert_eq "$(grep -oE 'href="[^"]*"' "$html" | tr '\n' ' ')" 'href="dxberry.css" '
  assert_file_not_contains "$html" "http"
  assert_file_not_contains "$html" "style="
  if grep -qE '<script>|[[:space:]]on[a-z]+=' "$html"; then _fail "index.html has an inline script or event handler"; fi
}

test_page_runs_everything_through_superuser_commands() {
  local js=$PG_DIR/dxberry.js
  assert_file_contains "$js" 'superuser: "require"'
  assert_file_contains "$js" '"/opt/dxberry/bin/dxberry-status"'
  assert_file_contains "$js" '"/system/logs/#/?prio=debug&service="'
  assert_file_contains "$js" '"reboot.target"'
  assert_file_contains "$js" '"poweroff.target"'
  assert_file_not_contains "$js" "innerHTML"
  assert_file_not_contains "$js" "cockpit.file"
}

test_page_script_parses() {
  command -v node > /dev/null 2>&1 || return 0   # node is optional locally; CI's runner has it
  node --check "$PG_DIR/dxberry.js" 2> "$TEST_TMP/node.err" || _fail "dxberry.js does not parse: $(cat "$TEST_TMP/node.err")"
}

# Every field the page reads must exist in the real command's output, so a change to dxberry-status
# that would leave a card blank fails here. The radios part is real dxberry-radio output.
test_page_reads_only_fields_that_dxberry_status_produces() {
  local DXB_GPSPIPE=pg_gpspipe f n=0
  cli_env
  cli add radio1 --audio 1 --cat 2 --label TM-V71 > /dev/null 2>&1
  cli status --json || _fail "dxberry-radio status --json failed: $(cat "$TEST_TMP/err")"
  cp "$TEST_TMP/out" "$TEST_TMP/radio.json"
  st_env
  st_cli --json || _fail "dxberry-status --json failed: $(cat "$TEST_TMP/err")"
  for f in $(pg_fields); do
    n=$(( n + 1 ))
    pg_has_path "$TEST_TMP/out" "$f" || _fail "dxberry.js reads $f, which dxberry-status --json does not produce"
  done
  (( n >= 40 )) || _fail "only $n fields found between the markers in dxberry.js"
}
