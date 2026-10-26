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
  assert_eq "$got" "/app.js /dxberry.js /settings.js /updates.js /dashboard.js /programs.js /audio.js /vfo.js /radiosettings.js "
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
  # rc2 has no SWR latch and no PTT timer: the hints say so instead of promising a guard
  assert_eq "$(jq -r '.swrHint' <<< "$j")" "Used by the transmit guards in the next release; this release does not watch SWR yet."
  assert_eq "$(jq -r '.pttHint' <<< "$j")" "Used by the transmit guards in the next release; this release does not time transmissions yet."
  assert_eq "$(jq -r '.swr + " " + .ptt' <<< "$j")" "2.5 90"
  assert_eq "$(jq -r '[.swrMin, .swrMax, .swrStep, .pttMin, .pttMax] | join(" ")' <<< "$j")" "1.5 10 0.1 30 600"
  assert_eq "$(jq -r '.getCall.method + " " + .getCall.url + " " + .getCall.credentials' <<< "$j")" "GET /api/radio/radio1/settings same-origin"
  # Save posts both numbers as numbers
  assert_eq "$(jq -r '.saveCall.method + " " + .saveCall.url' <<< "$j")" "POST /api/radio/radio1/settings"
  assert_eq "$(jq -c '.saveCall.body | fromjson' <<< "$j")" '{"swr_limit":4.2,"ptt_timeout_s":120}'
  assert_eq "$(jq -r '.savedMsg' <<< "$j")" "Station settings saved."
  # a refusal shows the reason and the fix
  assert_contains "$(jq -r '.refusedMsg' <<< "$j")" "between 1.5 and 10"
  # Tab leaves the fields alone; Enter saves the station settings and never submits the radio form
  assert_eq "$(jq -r '[.tabSaved, .enterSaved, .enterPrevented] | join(" ")' <<< "$j")" "0 1 true"
  # an expired session names the login problem instead of a network failure
  assert_contains "$(jq -r '.expiredMsg' <<< "$j")" "session"
  assert_contains "$(jq -r '.refusedMsg' <<< "$j")" "enter a value in that range"
  assert_eq "$(jq -r '.refusedClass' <<< "$j")" "error"
  # Re-probe posts to /reprobe and reports both outcomes
  assert_eq "$(jq -r '.probeCall.method + " " + .probeCall.url' <<< "$j")" "POST /api/radio/radio1/reprobe"
  assert_contains "$(jq -r '.probeRefused' <<< "$j")" "wait a few seconds"
  assert_eq "$(jq -r '.probeOk' <<< "$j")" "Capabilities re-read."
}

# ---- the VFO page (Task 6) ---------------------------------------------------------------------
# svf_run: the pure logic of vfo.js, answered as JSON
svf_run() { node "$DXB_ROOT/tests/station_vfo.js" "${SVF_JS:-$SP_WEB/vfo.js}"; }

test_vfo_format_freq() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  assert_eq "$(jq -c '.fmt' <<< "$j")" '["14.074.000","7.074.000","500","0","1.296.100.000","144.174.000","—","—"]'
  # the keypad: MHz with a point, kHz without, Hz when long, grouped Hz; nonsense is refused
  assert_eq "$(jq -c '.parse' <<< "$j")" '[14074000,14074000,14074000,14074000,null,null,null,144000,1296000]'
}

test_vfo_step_math() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  assert_eq "$(jq -c '.steps' <<< "$j")" '[10,100,1000,10000,100000,1000000]'
  assert_eq "$(jq -r '.stepUp' <<< "$j")" "14075000"
  assert_eq "$(jq -r '.stepDown' <<< "$j")" "14073900"
  # clamped to the end of the range it is in; a gap between ranges goes to the nearest edge
  assert_eq "$(jq -r '.stepBig' <<< "$j")" "14350000"
  assert_eq "$(jq -r '.stepTop' <<< "$j")" "14350000"
  assert_eq "$(jq -r '.stepBottom' <<< "$j")" "14000000"
  assert_eq "$(jq -r '.stepGap' <<< "$j")" "14000000"
  assert_eq "$(jq -r '.stepNoRanges' <<< "$j")" "0"
}

test_vfo_bands_from_ranges() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  assert_eq "$(jq -r '.bands | join(",")' <<< "$j")" "160 m,80 m,60 m,40 m,30 m,20 m,17 m,15 m,12 m,10 m,6 m"
  assert_eq "$(jq -r '.bandsNormalized' <<< "$j")" "11"
  assert_eq "$(jq -r '.bandsLower' <<< "$j")" "11"
  # a range inside a band: only that band, tuning from the range's own start
  assert_eq "$(jq -c '.bandsPartial' <<< "$j")" '[["20 m",14100000]]'
  assert_eq "$(jq -c '.bandOf' <<< "$j")" '["20 m",null]'
  # modes: only those the radio has, in the page's order
  assert_eq "$(jq -r '.modes | join(",")' <<< "$j")" "USB,LSB,CW,CWR,AM,FM,RTTY,PKTUSB,PKTLSB"
  assert_eq "$(jq -r '.modesNone | join(",")' <<< "$j")" "FM"
}

test_vfo_no_cat_layout() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  # nothing can be claimed, taken or released on a radio with no rig control
  assert_eq "$(jq -c '.actionsNoCat' <<< "$j")" '[[],[],[],[]]'
  assert_eq "$(jq -c '.layoutDummy' <<< "$j")" '["nocat","stop","owner"]'
  assert_eq "$(jq -c '.layoutNoCatRecord' <<< "$j")" '["nocat","stop","owner","program"]'
  assert_eq "$(jq -c '.layoutNoCapsYet' <<< "$j")" '["connecting","stop","owner"]'
  # a radio with CAT: dial, readout, S-meter, STOP, and what is hidden is listed under why
  assert_eq "$(jq -c '.layoutIC' <<< "$j")" '["readout","dial","bands","modes","vfo","filter","levels","smeter","atu","tune","stop","owner","why"]'
  assert_eq "$(jq -c '.layoutLower' <<< "$j")" '["readout","dial","bands","modes","vfo","filter","smeter","tune","stop","owner","why"]'
  assert_eq "$(jq -c '.hiddenMeterGone' <<< "$j")" '["readout","dial","modes","filter","tune","stop","owner","why"]'
}

test_vfo_owner_line_texts() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  assert_eq "$(jq -r '.owners.mine' <<< "$j")" "You have the transmitter"
  assert_eq "$(jq -r '.owners.other' <<< "$j")" "iPad has the transmitter"
  assert_eq "$(jq -r '.owners.program' <<< "$j")" "WSJT-X has this radio"
  assert_eq "$(jq -r '.owners.none' <<< "$j")" "Nobody has the transmitter"
  assert_eq "$(jq -c '.actions' <<< "$j")" '[["claim"],["release"],["takeover"],["open"]]'
}

test_vfo_meters_and_paths() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_run) || { _fail "station_vfo.js failed"; return; }
  assert_eq "$(jq -c '.s' <<< "$j")" '["S0","S8","S9","S9+20","S9+60"]'
  assert_eq "$(jq -c '.sUnits' <<< "$j")" '[0,9,15,15]'
  assert_eq "$(jq -c '.filters' <<< "$j")" '[[1800,2400,3000],[200,500,1000,1800],[]]'
  assert_eq "$(jq -r '.pathOk' <<< "$j")" "ic7300"
  assert_eq "$(jq -c '.pathBad' <<< "$j")" '[null,null,null,null,null]'
  assert_eq "$(jq -c '.percent' <<< "$j")" '[50,100,0]'
}

# svf_api [PATH]: the radio pages' layer in app.js (tab token, dxb.api, events extras) as JSON
svf_api() { node "$DXB_ROOT/tests/station_vfo_api.js" "$SP_WEB/app.js" "$@"; }

test_vfo_api_sends_the_tab_token_and_shapes_failures() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_api) || { _fail "station_vfo_api.js failed"; return; }
  assert_eq "$(jq -r '.tab' <<< "$j")" "Browser#uuid-1"
  assert_eq "$(jq -r '.tabStable' <<< "$j")" "true"
  assert_eq "$(jq -c '.calls[0]' <<< "$j")" '{"url":"/api/radio/ic7300/freq","method":"POST","tab":"Browser#uuid-1","type":"application/json","body":"{\"hz\":99}"}'
  assert_eq "$(jq -r '.calls[1].tab' <<< "$j")" "Browser#uuid-1"
  assert_eq "$(jq -r '.otherHasTab' <<< "$j")" "false"
  assert_eq "$(jq -c '.refused' <<< "$j")" '{"ok":false,"reason":"99 is outside this radio'"'"'s range","fix":"this radio tunes 30000-74800000 Hz"}'
  assert_eq "$(jq -r '.accepted.ok' <<< "$j")" "true"
  assert_eq "$(jq -r '(.missing.ok|tostring) + " " + .missing.reason' <<< "$j")" "false no such radio"
  assert_eq "$(jq -r '.garbled.ok' <<< "$j")" "false"
  assert_eq "$(jq -r '.down.reason' <<< "$j")" "The station could not be reached."
}

test_vfo_events_carry_the_tab_and_deliver_rig_messages() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_api) || { _fail "station_vfo_api.js failed"; return; }
  assert_eq "$(jq -r '.socketUrl' <<< "$j")" "wss://pi.local/api/events?tab=Browser%23uuid-1"
  assert_eq "$(jq -r '.sendBeforeOpen' <<< "$j")" "false"
  assert_eq "$(jq -r '(.opened|tostring) + " " + (.sendOpen|tostring)' <<< "$j")" "1 true"
  assert_eq "$(jq -r '.sent[0]' <<< "$j")" '{"type":"watch","radio":"ic7300","level":"page"}'
  assert_eq "$(jq -c '.rig' <<< "$j")" '[{"type":"rig","radio":"ic7300","data":{"freq":14074000}}]'
  assert_eq "$(jq -r '.closed' <<< "$j")" "1"
  # off a radio page the address carries no token
  j=$(svf_api /) || { _fail "station_vfo_api.js failed"; return; }
  assert_eq "$(jq -r '.socketUrl' <<< "$j")" "wss://pi.local/api/events"
}

test_vfo_page_files_are_plain() {
  local f
  for f in vfo.js dashboard.js app.js; do assert_file_not_contains "$SP_WEB/$f" "innerHTML"; done
  assert_file_not_contains "$SP_WEB/vfo.js" "cockpit"
  assert_file_not_contains "$SP_WEB/vfo.js" "setAttribute(\"style\""
  assert_file_not_contains "$SP_WEB/vfo.js" "fetch("
  assert_file_not_contains "$SP_WEB/vfo.css" "http"
  assert_file_contains "$SP_WEB/index.html" '<link rel="stylesheet" href="/vfo.css">'
  assert_file_contains "$SP_WEB/vfo.js" 'anchor.parentNode.insertBefore(root'
  # the dashboard card links to the page
  # shellcheck disable=SC2016  # the literal is JavaScript, not a shell expansion
  assert_file_contains "$SP_WEB/dashboard.js" 'href: `/radio/${n}`'
  command -v node > /dev/null 2>&1 || return 0
  node --check "$SP_WEB/vfo.js" 2> "$TEST_TMP/node.err" || _fail "vfo.js does not parse: $(cat "$TEST_TMP/node.err")"
}

# svf_page SCENARIO: vfo.js run as the page /radio/<name> over a fake DOM and fetch, as JSON
svf_page() { node "$DXB_ROOT/tests/station_vfo_page.js" "$SP_WEB" "$1"; }

test_vfo_page_draws_a_radio_with_cat() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_page ic7300 2> "$TEST_TMP/p.err") || { _fail "station_vfo_page.js failed: $(cat "$TEST_TMP/p.err")"; return; }
  # the tab token is "<device>#<nonce>" on the socket (the # encoded) and on every radio request
  assert_eq "$(jq -r '.socketUrl' <<< "$j")" "wss://pi.local/api/events?tab=iPhone%23tab-1"
  assert_eq "$(jq -r '.watch[0]' <<< "$j")" '{"type":"watch","radio":"ic7300","level":"page"}'
  assert_eq "$(jq -r '.getTabs | join(",")' <<< "$j")" "iPhone#tab-1"
  assert_eq "$(jq -r '.readout' <<< "$j")" "14.074.000 Hz"
  assert_eq "$(jq -r '.readoutAfterEvent' <<< "$j")" "7.074.000 Hz"
  assert_eq "$(jq -r '.hasDial' <<< "$j")" "true"
  assert_eq "$(jq -r '.dialRange' <<< "$j")" "30000-74800000"
  assert_eq "$(jq -r '.sectionTitles | join(",")' <<< "$j")" "Band,Mode,VFO,Filter width,Levels,Signal,Antenna tuner,Tune"
  assert_eq "$(jq -r '.bandButtons' <<< "$j")" "160 m,80 m,60 m,40 m,30 m,20 m,17 m,15 m,12 m,10 m,6 m"
  assert_eq "$(jq -r '.modeButtons' <<< "$j")" "USB,LSB,CW,AM,FM"
  assert_eq "$(jq -r '.ownerText' <<< "$j")" "Nobody has the transmitterTake the transmitter"
  assert_contains "$(jq -r '.why' <<< "$j")" "RF gain: this radio cannot set RF over CAT"
  # before any drop only Tune and ATU are disabled (they wait for the transmitter), with a visible line
  assert_eq "$(jq -r '.chipHiddenBefore' <<< "$j")" "true"
  assert_eq "$(jq -c '.disabledBefore' <<< "$j")" '["ATU","Tune"]'
  assert_eq "$(jq -r '.needsHint' <<< "$j")" "2"
  # Tune keys a carrier only in FM or AM, and its hint says so
  assert_eq "$(jq -r '.tuneHint' <<< "$j")" "Sends a carrier for up to 10 seconds (FM or AM)."
  # a tapped mode shows at once and goes to the station with the tab token ...
  assert_eq "$(jq -r '.cwPressedAtOnce' <<< "$j")" "true"
  assert_eq "$(jq -r '.modeCall[0]' <<< "$j")" '/api/radio/ic7300/mode {"mode":"CW"} iPhone#tab-1'
  # ... stays while the station still marks the field optimistic (even with the old value), clears after
  assert_eq "$(jq -r '.cwKeptWhileOptimistic + " " + .cwKeptWhileStillOptimistic' <<< "$j")" "true true"
  assert_eq "$(jq -r '.cwPressedAfter + " " + .usbPressedAfter' <<< "$j")" "false true"
  # the rig link down: the real chip shows, controls are disabled, STOP is not; recovery undoes it
  assert_eq "$(jq -r '.chipHiddenAfter' <<< "$j")" "false"
  assert_eq "$(jq -r '.chipTextDown' <<< "$j")" "Reconnecting…"
  assert_eq "$(jq -r '.rootDown' <<< "$j")" "true"
  assert_eq "$(jq -r '.disabledAfterCount > 10' <<< "$j")" "true"
  assert_eq "$(jq -r '.stopCount' <<< "$j")" "1"
  assert_eq "$(jq -r '.stopDimmable' <<< "$j")" "0"
  assert_eq "$(jq -r '.stopDisabled' <<< "$j")" "0"
  assert_eq "$(jq -r '.chipHiddenRecovered' <<< "$j")" "true"
  assert_eq "$(jq -c '.disabledRecovered' <<< "$j")" '["ATU","Tune"]'
  # a radio that does not answer behind rigctld: the chip says so, controls dim, STOP stays
  assert_eq "$(jq -r '.chipHiddenQuiet' <<< "$j")" "false"
  assert_eq "$(jq -r '.chipTextQuiet' <<< "$j")" "the radio is not answering"
  assert_eq "$(jq -r '.rootDownQuiet' <<< "$j")" "true"
  assert_eq "$(jq -r '.disabledQuietCount > 10' <<< "$j")" "true"
  assert_eq "$(jq -r '.stopDisabledQuiet' <<< "$j")" "0"
  assert_eq "$(jq -r '.chipHiddenAnswering' <<< "$j")" "true"
  assert_eq "$(jq -c '.disabledAnswering' <<< "$j")" '["ATU","Tune"]'
  # the dashboard card follows a rig event in place
  assert_eq "$(jq -r '.dashFreq' <<< "$j")" "14.074.000 Hz USB"
  assert_eq "$(jq -r '.dashFreqQuiet' <<< "$j")" "not answering"
}

test_vfo_page_shows_another_device_by_name() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_page other 2> "$TEST_TMP/p.err") || { _fail "station_vfo_page.js failed: $(cat "$TEST_TMP/p.err")"; return; }
  assert_eq "$(jq -r '.ownerText' <<< "$j")" "iPhone has the transmitterTake over"
}

test_vfo_page_refusal_shows_reason_and_fix_and_rolls_back() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_page refuse 2> "$TEST_TMP/p.err") || { _fail "station_vfo_page.js failed: $(cat "$TEST_TMP/p.err")"; return; }
  assert_eq "$(jq -r '.cwPressedAtOnce' <<< "$j")" "true"
  assert_eq "$(jq -r '.refusedPressed' <<< "$j")" "false"
  # the reason and the fix are two sentences
  assert_eq "$(jq -r '.refusedNotice[0]' <<< "$j")" "bad: this radio has no mode CW. Modes: USB LSB."
}

test_vfo_page_no_cat_and_unknown_radio() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(svf_page dummy 2> "$TEST_TMP/p.err") || { _fail "station_vfo_page.js failed: $(cat "$TEST_TMP/p.err")"; return; }
  assert_eq "$(jq -r '.nocat' <<< "$j")" "true"
  assert_eq "$(jq -r '.hasDial' <<< "$j")" "false"
  assert_eq "$(jq -r '.sectionTitles | length' <<< "$j")" "0"
  assert_eq "$(jq -r '.stopCount' <<< "$j")" "1"
  # nothing to claim, and STOP says why the radio refuses (the station's 409)
  assert_eq "$(jq -c '.ownerButtons' <<< "$j")" "[]"
  assert_eq "$(jq -r '.stopNotice[0]' <<< "$j")" "bad: this radio has no rig control. Give the radio a rig control port in its settings."
  j=$(svf_page missing 2> "$TEST_TMP/p.err") || { _fail "station_vfo_page.js failed: $(cat "$TEST_TMP/p.err")"; return; }
  assert_eq "$(jq -r '.missing' <<< "$j")" "true"
  assert_eq "$(jq -r '.stopCount' <<< "$j")" "0"
}

# ---- browser audio: audio.js over a fake RTCPeerConnection, WebSocket and getUserMedia ----
# sau_run: what audio.js did in tests/station_audio.js's scenario, as JSON
sau_run() { node "$DXB_ROOT/tests/station_audio.js" "$SP_WEB/audio.js"; }

test_station_audio_signalling_messages() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.api' <<< "$j")" '["listen","openMic","setTX","stop"]'
  assert_eq "$(jq -r '.socketUrl' <<< "$j")" "wss://pi.local/api/audio/ic7300/signal?tab=iPhone%23tab-1"
  # no ICE servers; one audio transceiver both ways, so the mic joins later without renegotiating
  assert_eq "$(jq -c '.pcConfig' <<< "$j")" '{"iceServers":[]}'
  assert_eq "$(jq -c '.transceiver' <<< "$j")" '[["audio","sendrecv"]]'
  # the offer first; a candidate found before it waits, one after goes at once, the end marker not at all
  assert_eq "$(jq -r '.sentBeforeOpen' <<< "$j")" "0"
  assert_eq "$(jq -c '.sent' <<< "$j")" '[{"type":"offer","sdp":"OFFER-0"},{"type":"ice","candidate":{"candidate":"candidate:1 1 udp 1 10.0.0.9 5000 typ host","sdpMid":"0","sdpMLineIndex":0}},{"type":"ice","candidate":{"candidate":"candidate:2 1 udp 1 10.0.0.9 5001 typ host","sdpMid":"0","sdpMLineIndex":0}}]'
  assert_eq "$(jq -c '.remote' <<< "$j")" '{"type":"answer","sdp":"ANSWER"}'
  assert_eq "$(jq -c '.addedIce' <<< "$j")" '[{"candidate":"candidate:9 1 udp 1 10.0.0.2 50001 typ host","sdpMid":"0","sdpMLineIndex":0}]'
  assert_eq "$(jq -c '.listened' <<< "$j")" '{"ok":true}'
  # the element is started inside the tap and plays the radio's track when it arrives
  assert_eq "$(jq -r '.playedInTap' <<< "$j")" "1"
  assert_eq "$(jq -r '.audioAttached' <<< "$j")" "true"
  assert_eq "$(jq -c '.playing' <<< "$j")" '["radio"]'
  assert_eq "$(jq -c '.stopped.close' <<< "$j")" '{"type":"close"}'
}

test_station_audio_mic_only_on_first_press_and_duck() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.gumBeforeMic' <<< "$j")" "0"
  assert_eq "$(jq -c '.gum' <<< "$j")" '[{"audio":{"echoCancellation":false,"noiseSuppression":false,"autoGainControl":false}}]'
  assert_eq "$(jq -c '.mic' <<< "$j")" '{"ok":true}'
  assert_eq "$(jq -c '.micReplaced' <<< "$j")" '["mic"]'
  assert_eq "$(jq -r '.micIdle' <<< "$j")" "false"
  # 20 dB down is a tenth of the amplitude
  assert_eq "$(jq -c '.duck' <<< "$j")" '{"db":20,"gain":0.1}'
  assert_eq "$(jq -c '.tx' <<< "$j")" '{"mic":true,"volume":0.1}'
  assert_eq "$(jq -c '.rx' <<< "$j")" '{"mic":false,"volume":1}'
  assert_eq "$(jq -r '.volTXNoMic' <<< "$j")" "0.1"
  assert_eq "$(jq -r '.stopped.mic' <<< "$j")" "true"
}

test_station_audio_lost_chip_reconnects() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.lost' <<< "$j")" '{"chipShown":true,"chipText":"audio lost, tap to reconnect","chipTag":"button","chipType":"button","pcClosed":1,"closeSent":{"type":"close"},"socketClosed":1,"elRemoved":true}'
  assert_eq "$(jq -r '.socketsAfterLost' <<< "$j")" "1"
  assert_eq "$(jq -c '.reconnect' <<< "$j")" '{"chipHidden":true,"sockets":2,"url":"wss://pi.local/api/audio/ic7300/signal?tab=iPhone%23tab-1","mic":["mic"]}'
}

test_station_audio_refusals() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.refused' <<< "$j")" '{"ok":false,"reason":"no sound server for ic7300: WSJT-X has the sound card","fix":"start the radio'"'"'s sound server or stop the program that holds the sound card"}'
  assert_eq "$(jq -r '.refusedPC' <<< "$j")" "1"
  assert_eq "$(jq -r '.refusedChip' <<< "$j")" "true"
  assert_eq "$(jq -r '.closedEarly.reason' <<< "$j")" "The station could not be reached."
  assert_eq "$(jq -r '.bad.reason' <<< "$j")" "no such radio"
}

test_station_audio_file_is_plain() {
  local f=$SP_WEB/audio.js
  assert_file_not_contains "$f" "innerHTML"
  assert_file_not_contains "$f" "style"
  assert_file_not_contains "$f" "fetch("
  assert_file_not_contains "$f" "cockpit"
  assert_file_contains "$f" "createTextNode(LOST_TEXT)"
  assert_file_contains "$SP_WEB/index.html" '<script src="/audio.js"></script>'
  command -v node > /dev/null 2>&1 || return 0
  node --check "$f" 2> "$TEST_TMP/node.err" || _fail "audio.js does not parse: $(cat "$TEST_TMP/node.err")"
}
