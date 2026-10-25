#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
# The station app's page scripts: the compatibility layer (app.js) and dxberry.js's text helpers.
SP_WEB=$DXB_ROOT/station/web

# sp_run SCENARIO: what dxb.run did against the fake fetch, as JSON
sp_run() { node "$DXB_ROOT/tests/station_page.js" "$SP_WEB/app.js" "$1"; }

test_dxb_run_posts_the_command_and_resolves_with_stdout() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sp_run ok)
  assert_eq "$(jq -r '.resolved' <<< "$j")" "out"
  assert_eq "$(jq -r '.calls[0].url' <<< "$j")" "/api/cli/dxberry-status"
  assert_eq "$(jq -r '.calls[0].method + " " + .calls[0].credentials' <<< "$j")" "POST same-origin"
  assert_eq "$(jq -c '.calls[0].body | fromjson' <<< "$j")" '{"args":["--json"],"stdin":"secret"}'
}

test_dxb_run_rejects_like_cockpit() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sp_run exit)
  assert_eq "$(jq -r '.rejected.exit_status' <<< "$j")" "6"
  assert_contains "$(jq -r '.rejected.message' <<< "$j")" "bad"
  assert_contains "$(jq -r '.rejected.text' <<< "$j")" "bad"
  assert_eq "$(jq -r '.rejected.problem' <<< "$j")" "null"
}

test_dxb_run_names_a_refused_command() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sp_run refused)
  assert_eq "$(jq -r '.rejected.problem' <<< "$j")" "refused"
  assert_eq "$(jq -r '.rejected.message' <<< "$j")" "command not allowed"
}

test_dxb_run_401_redirects() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sp_run 401)
  assert_eq "$(jq -r '.assigned[0]' <<< "$j")" "/login?next=%2Fradios%3Fx%3D1%23s"
  assert_eq "$(jq -r '.settled' <<< "$j")" "false"
}

test_dxb_run_403_and_network_failure_are_unreachable() {
  command -v node > /dev/null 2>&1 || return 0
  local s j
  for s in 403 net; do
    j=$(sp_run "$s")
    assert_eq "$(jq -r '.rejected.problem' <<< "$j")" "unreachable"
    assert_eq "$(jq -r '.rejected.exit_status' <<< "$j")" "null"
  done
}

test_dxb_ws_follows_the_page_scheme() {
  command -v node > /dev/null 2>&1 || return 0
  assert_eq "$(jq -r '.ws' <<< "$(sp_run ok)")" "wss://pi.local/api/x"
}

test_station_page_files_are_plain() {
  local f
  for f in app.js dxberry.js index.html; do
    assert_file_not_contains "$SP_WEB/$f" "innerHTML"
  done
  assert_file_not_contains "$SP_WEB/app.js" "cockpit"
  assert_file_not_contains "$SP_WEB/index.html" "cockpit"
  assert_file_not_contains "$SP_WEB/index.html" "style="
  if grep -qE '<script>|[[:space:]]on[a-z]+=' "$SP_WEB/index.html"; then _fail "index.html has an inline script or event handler"; fi
  assert_file_contains "$SP_WEB/index.html" '<meta name="viewport" content="width=device-width, initial-scale=1">'
  assert_file_contains "$SP_WEB/index.html" '<link rel="manifest" href="/manifest.webmanifest">'
  # app.js first, then dxberry.js; Task 5 adds the other page scripts
  assert_eq "$(grep -oE 'src="[^"]*"' "$SP_WEB/index.html" | head -2 | tr '\n' ' ')" 'src="/app.js" src="/dxberry.js" '
}

test_station_page_manifest_and_icons() {
  local m=$SP_WEB/manifest.webmanifest i
  assert_eq "$(jq -r '[.name, .short_name, .start_url, .display] | join(",")' "$m")" "DXBerry,DXBerry,/,standalone"
  for i in 192 512; do
    assert_eq "$(jq -r --arg s "${i}x${i}" '[.icons[] | select(.sizes == $s) | .src] | first' "$m")" "/icons/icon-$i.png"
    assert_eq "$(head -c 8 "$SP_WEB/icons/icon-$i.png" | od -An -tx1 | tr -d ' \n')" "89504e470d0a1a0a"
  done
}

# Same assertions as test_page.sh's, on the moved dxberry.js.
test_station_page_error_text_keeps_the_problem_and_drops_progress() {
  command -v node > /dev/null 2>&1 || return 0
  local js=$SP_WEB/dxberry.js et pt stderr got
  et=$(sed -n '/^function errorText(/,/^}/p' "$js")
  pt=$(sed -n '/^function problemText(/,/^}/p' "$js")
  stderr=$(printf '%s\n' '2026-10-06 12:00:00 [INFO] radio radio1 now owned by graywolf' \
    '2026-10-06 12:00:01 [ERROR] invalid radio record:' '  radio1: bad label' 'unknown option: --x')
  got=$(node -e "$et
$pt
console.log(errorText(process.argv[1]));
console.log(problemText({exit_status: 4, message: process.argv[2]}, {4: 'the device is not plugged in'}));
console.log(problemText({problem: 'not-found'}));
console.log(problemText({problem: 'refused', message: 'command not allowed'}));" "$stderr" '2026-10-06 12:00:00 [ERROR] radio radio1 is not plugged in')
  assert_eq "$(sed -n 1p <<< "$got")" "invalid radio record: radio1: bad label unknown option: --x"
  assert_eq "$(sed -n 2p <<< "$got")" "Exit 4: the device is not plugged in. radio radio1 is not plugged in"
  assert_eq "$(sed -n 3p <<< "$got")" "not-found"
}

test_station_page_dxberry_js_uses_the_layer() {
  local js=$SP_WEB/dxberry.js
  assert_file_contains "$js" "dxb.run("
  assert_file_contains "$js" "dxb.openLog("
  assert_file_contains "$js" "dxb.hidden"
  assert_file_contains "$js" "dxb.onVisible("
  assert_file_not_contains "$js" "const LOGS"
  if grep -qE 'cockpit\.(spawn|jump|hidden|addEventListener|file|transport)' "$js"; then _fail "dxberry.js still calls the Cockpit API"; fi
  command -v node > /dev/null 2>&1 || return 0
  node --check "$js" 2> "$TEST_TMP/node.err" || _fail "dxberry.js does not parse: $(cat "$TEST_TMP/node.err")"
  node --check "$SP_WEB/app.js" 2> "$TEST_TMP/node.err" || _fail "app.js does not parse: $(cat "$TEST_TMP/node.err")"
}

# ---- the four ported pages (dashboard, settings, updates, programs) ----------------------------
SP_PAGES="settings updates dashboard programs"

test_station_pages_live_in_the_web_tree() {
  local f
  for f in $SP_PAGES; do
    [[ -f $SP_WEB/$f.js ]] || _fail "station/web/$f.js is missing"
    [[ ! -e $DXB_ROOT/provision/cockpit/dxberry/$f.js ]] || _fail "provision/cockpit/dxberry/$f.js is still there"
  done
}

test_station_pages_have_no_cockpit_left() {
  local f
  for f in $SP_PAGES; do
    [[ -f $SP_WEB/$f.js ]] || _fail "station/web/$f.js is missing"
    assert_file_not_contains "$SP_WEB/$f.js" "cockpit"
    assert_file_not_contains "$SP_WEB/$f.js" "cockpit.spawn"
  done
}

# dxberry.js no longer defines a global run(); the pages must go through dxb.run.
test_station_pages_run_through_the_layer() {
  local f bare
  for f in $SP_PAGES; do
    [[ -f $SP_WEB/$f.js ]] || _fail "station/web/$f.js is missing"
    bare=$(grep -nE '(^|[^.A-Za-z0-9_])run\(\[|(^|[^.A-Za-z0-9_])run\(args' "$SP_WEB/$f.js" || true)
    assert_eq "$bare" ""
  done
  assert_file_contains "$SP_WEB/settings.js" 'dxb.run([CONFIG, ...args, "--json"], input)'
}

test_station_dashboard_opens_programs_on_the_screen_page() {
  assert_file_contains "$SP_WEB/dashboard.js" 'const DASH_SCREEN = "/screen#";'
}

test_station_index_loads_the_page_scripts_in_order() {
  local got
  got=$(grep -oE '<script src="[^"]+"' "$SP_WEB/index.html" | grep -oE '/[a-z.]+' | tr '\n' ' ')
  assert_eq "$got" "/app.js /dxberry.js /settings.js /updates.js /dashboard.js /programs.js "
}

test_station_pages_parse_together() {
  command -v node > /dev/null 2>&1 || return 0
  local f
  for f in $SP_PAGES; do [[ -f $SP_WEB/$f.js ]] || _fail "station/web/$f.js is missing"; done
  cat "$SP_WEB/dxberry.js" "$SP_WEB/settings.js" "$SP_WEB/updates.js" "$SP_WEB/dashboard.js" "$SP_WEB/programs.js" > "$TEST_TMP/sp-all.js"
  node --check "$TEST_TMP/sp-all.js" 2> "$TEST_TMP/sp-node.err" || _fail "the page scripts do not parse as one: $(cat "$TEST_TMP/sp-node.err")"
}

# ---- the program window: screen.html, screen.js and the vendored noVNC (moved from test_page.sh) ----
test_station_screen_html_loads_only_its_own_files() {
  local h=$SP_WEB/screen.html
  assert_eq "$(grep -oE 'src="[^"]*"' "$h" | sort | tr '\n' ' ')" 'src="/app.js" src="/screen.js" '
  assert_file_contains "$h" 'type="module"'
  assert_file_not_contains "$h" "style="
  if grep -qE '<script>|[[:space:]]on[a-z]+=' "$h"; then _fail "screen.html has an inline script or handler"; fi
}
test_station_screen_js_uses_the_relay_without_cockpit() {
  local js=$SP_WEB/screen.js
  assert_file_contains "$js" '"/opt/dxberry/bin/dxberry-app"'
  assert_file_contains "$js" 'dxb.ws("/api/vnc/" + scr.session)'
  assert_file_not_contains "$js" 'cockpit'
  assert_file_not_contains "$js" 'superuser'
  assert_file_not_contains "$js" 'innerHTML'
  assert_file_contains "$js" 'from "./vendor/novnc/core/rfb.js"'
}
test_station_novnc_is_vendored_with_its_licence() {
  local d=$SP_WEB/vendor/novnc
  [[ -f $d/core/rfb.js && -f $d/LICENSE.txt && -f $d/VERSION ]] || _fail "noVNC is not vendored"
  assert_file_contains "$d/core/rfb.js" "MPL"
}
# every relative import inside the vendored noVNC tree resolves to a file inside it
test_station_novnc_imports_resolve_inside_the_tree() {
  local d=$SP_WEB/vendor/novnc f dir spec target bad=''
  while IFS= read -r f; do
    dir=$(dirname "$f")
    while IFS= read -r spec; do
      target=$(realpath -m "$dir/$spec")
      [[ $target == "$d/"* && -f $target ]] || bad+=" $f->$spec"
    done < <(grep -hoE "(from|import) +['\"]\.[^'\"]+['\"]" "$f" | grep -oE "\.[^'\"]+")
  done < <(find "$d" -name '*.js')
  assert_eq "$bad" ""
}
test_station_novnc_has_no_attribution_text() {
  if grep -rIqiE 'co-?auth[o]red-?by|cla[u]de|anthr[o]pic' "$SP_WEB/vendor/novnc"; then _fail "vendored noVNC matches the attribution grep"; fi
}
test_station_embed_carries_the_vendored_novnc() {
  assert_file_contains "$SP_WEB/embed.go" 'vendor/novnc'
}
# the program window in the fake browser: stopped, running over the relay, and a bad address
test_station_screen_smoke_drives_the_program_window() {
  command -v node > /dev/null 2>&1 || return 0
  local out
  out=$(node "$DXB_ROOT/tests/station_screen.js" "$SP_WEB/screen.js" 2>&1) || _fail "station_screen.js failed:
$out"
}

# ---- the events stream (Task 8) ----------------------------------------------------------------
test_refresh_uses_events_when_open() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(node "$DXB_ROOT/tests/station_events.js" "$SP_WEB" 2> "$TEST_TMP/ev.err") || { _fail "station_events.js failed: $(cat "$TEST_TMP/ev.err")"; return; }
  assert_eq "$(jq -r '.socketUrl' <<< "$j")" "wss://pi.local/api/events"
  assert_eq "$(jq -r '.pollTimer' <<< "$j")" "true"
  assert_eq "$(jq -r '.initialRuns' <<< "$j")" "1"
  # open: the timer poll runs nothing, a status message renders (callsign and host from the message)
  assert_eq "$(jq -r '.runsWhileOpen' <<< "$j")" "0"
  assert_eq "$(jq -r '.chipHiddenWhileOpen' <<< "$j")" "true"
  assert_eq "$(jq -r '.callsign' <<< "$j")" "W1AW"
  assert_eq "$(jq -r '.hostAfterMessage' <<< "$j")" "pi"
  # a ping, junk and a null status change nothing and never run the command
  assert_eq "$(jq -r '.runsAfterMessages' <<< "$j")" "0"
  # closed: the chip shows, a retry is queued at 1 s (then 2 s) and the poll resumes
  assert_eq "$(jq -r '.chipShownOnClose' <<< "$j")" "true"
  assert_eq "$(jq -r '.retryDelayMs' <<< "$j")" "1000"
  assert_eq "$(jq -r '.runsAfterClose' <<< "$j")" "1"
  assert_eq "$(jq -r '.sockets' <<< "$j")" "2"
  assert_eq "$(jq -r '.secondRetryDelayMs' <<< "$j")" "2000"
  assert_eq "$(jq -r '.chipHiddenAfterReopen' <<< "$j")" "true"
}

test_station_index_has_the_reconnecting_chip() {
  assert_file_contains "$SP_WEB/index.html" '<span id="link" class="chip" hidden>'
}

# ---- per-radio station settings (Task 7) -------------------------------------------------------
test_radio_settings_form_values() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(node "$DXB_ROOT/tests/station_radiosettings.js" "$SP_WEB/radiosettings.js" 2> "$TEST_TMP/rs.err") || { _fail "station_radiosettings.js failed: $(cat "$TEST_TMP/rs.err")"; return; }
  # the defaults show until the station answers, then the saved values
  assert_eq "$(jq -r '.initialSwr + " " + .initialPtt' <<< "$j")" "3 180"
  assert_eq "$(jq -r '.swr + " " + .ptt' <<< "$j")" "2.5 90"
  assert_eq "$(jq -r '[.swrMin, .swrMax, .swrStep, .pttMin, .pttMax] | join(" ")' <<< "$j")" "1.5 10 0.1 30 600"
  assert_eq "$(jq -r '.getCall.method + " " + .getCall.url + " " + .getCall.credentials' <<< "$j")" "GET /api/radio/radio1/settings same-origin"
  # Save posts both numbers as numbers
  assert_eq "$(jq -r '.saveCall.method + " " + .saveCall.url' <<< "$j")" "POST /api/radio/radio1/settings"
  assert_eq "$(jq -c '.saveCall.body | fromjson' <<< "$j")" '{"swr_limit":4.2,"ptt_timeout_s":120}'
  assert_eq "$(jq -r '.savedMsg' <<< "$j")" "Station settings saved."
  # a refusal shows the reason and the fix
  assert_contains "$(jq -r '.refusedMsg' <<< "$j")" "between 1.5 and 10"
  assert_contains "$(jq -r '.refusedMsg' <<< "$j")" "enter a value in that range"
  assert_eq "$(jq -r '.refusedClass' <<< "$j")" "error"
  # Re-probe posts to /reprobe and reports both outcomes
  assert_eq "$(jq -r '.probeCall.method + " " + .probeCall.url' <<< "$j")" "POST /api/radio/radio1/reprobe"
  assert_contains "$(jq -r '.probeRefused' <<< "$j")" "wait a few seconds"
  assert_eq "$(jq -r '.probeOk' <<< "$j")" "Capabilities re-read."
}
