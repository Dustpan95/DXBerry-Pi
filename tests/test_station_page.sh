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
  # rc3 has the SWR latch and the PTT timeout: the hints say what they do (changed in rc3 Task 5)
  assert_eq "$(jq -r '.swrHint' <<< "$j")" "Three SWR readings in a row over this limit unkey the radio and latch until you change band, tune the ATU, or override."
  assert_eq "$(jq -r '.pttHint' <<< "$j")" "Hold-to-talk PTT drops after this long."
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
  # rc3 Task 6: level, micLabel and onChange joined the API (the speaker, mic and lost state on the page)
  assert_eq "$(jq -c '.api' <<< "$j")" '["level","listen","micLabel","onChange","openMic","setTX","stop"]'
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
  # rc3 Task 6 (Ruling 8): the duck is the Web Audio gain's, the element's volume stays at 1
  assert_eq "$(jq -c '.tx' <<< "$j")" '{"mic":true,"gain":0.1,"volume":1}'
  assert_eq "$(jq -c '.rx' <<< "$j")" '{"mic":false,"gain":1,"volume":1}'
  assert_eq "$(jq -r '.gainTXNoMic' <<< "$j")" "0.1"
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

test_station_audio_stop_releases_a_pending_mic() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.raced.stopped' <<< "$j")" "true"
  assert_eq "$(jq -r '.raced.result.ok' <<< "$j")" "false"
  assert_eq "$(jq -r '.askedAgain' <<< "$j")" "3"
  assert_eq "$(jq -c '.againResult' <<< "$j")" '{"ok":true}'
}

# ---- PTT on the VFO page: vfo.js's press-and-hold button over fake timers and a fake dxbAudio ----
# spt_run SCENARIO: what tests/station_ptt.js saw the page send, as JSON
spt_run() { node "$DXB_ROOT/tests/station_ptt.js" "$SP_WEB" "$1"; }

test_ptt_release_events() {
  command -v node > /dev/null 2>&1 || return 0
  local j k; j=$(spt_run release 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '.disabled' <<< "$j")" "false"
  assert_eq "$(jq -r '.label' <<< "$j")" "PTT"
  # each release (and every other release event after it) sends one off, after the on and heartbeats
  for k in pointerup pointercancel blur pagehide; do
    # rc3 fix wave (m12): the event under test alone ends the hold and sends the off
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.labelAtRelease) \(.offAtRelease)"' <<< "$j")" "PTT 1"
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.on) \(.off) \(.order)"' <<< "$j")" "1 1 on,hb,hb,hb,off"
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.label) \(.labelAfter) \(.timeHidden)"' <<< "$j")" "Transmitting PTT true"
  done
  # the held timer shows against the radio's PTT timeout
  assert_eq "$(jq -r '.rounds.pointerup.time' <<< "$j")" "0:00 / 3:00"
  # a release before the microphone is ready sends nothing; the radio's audio started once, inside the first press
  assert_eq "$(jq -r '.quickOrder' <<< "$j")" ""
  assert_eq "$(jq -r '.audioCalls.listen' <<< "$j")" "1"
  assert_eq "$(jq -r '.audioCalls.openMic' <<< "$j")" "5"
  # the microphone goes out only after the station's OK, and stops with every end
  assert_eq "$(jq -c '.audioCalls.tx' <<< "$j")" '[true,false,true,false,true,false,true,false,false]'
  # rc3 Task 6: the same four releases with the real audio.js: held, the radio's audio is 20 dB down
  # (the gain) with the duck shown and the microphone live; released, one off, full gain, mic idle
  j=$(sau_ui ptt 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  for k in pointerup pointercancel blur pagehide; do
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.labelAtRelease) \(.offAtRelease)"' <<< "$j")" "PTT 1"
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.on) \(.off) \(.first) \(.last) \(.label)"' <<< "$j")" "1 1 on off Transmitting"
    assert_eq "$(jq -c --arg k "$k" '.rounds[$k].held' <<< "$j")" '{"gain":0.1,"mic":true,"duck":"Radio audio down 20 dB while you talk"}'
    assert_eq "$(jq -c --arg k "$k" '.rounds[$k].after' <<< "$j")" '{"gain":1,"mic":false,"duckShown":false,"label":"PTT","timeShown":false}'
  done
  # one audio connection and one microphone prompt for all four presses
  assert_eq "$(jq -r '"\(.sockets) \(.gum)"' <<< "$j")" "1 1"
}

test_ptt_heartbeat_every_5s() {
  command -v node > /dev/null 2>&1 || return 0
  local j k; j=$(spt_run release 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  for k in pointerup pointercancel blur pagehide; do
    # one 5 s heartbeat timer per press, a heartbeat each period, stopped by the release
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.heartbeatTimers) \(.hb) \(.hbCleared)"' <<< "$j")" "1 3 true"
  done
  assert_file_contains "$SP_WEB/vfo.js" "PTT_HEARTBEAT: 5000"
  # rc3 Task 6: by a fake clock: 65 s held is 13 heartbeats 5 s apart, the timer counts up beside
  # the radio's configured timeout (120 s here), and nothing more goes after the release
  j=$(sau_ui ptt 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  for k in pointerup pointercancel blur pagehide; do
    assert_eq "$(jq -r --arg k "$k" '.rounds[$k] | "\(.hb) \(.hbEvery) \(.time) \(.hbAfter)"' <<< "$j")" "13 [5000] 1:05 / 2:00 0"
  done
}

test_ptt_disabled_with_the_reason() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(spt_run notowner 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '"\(.disabled) \(.why)"' <<< "$j")" "true Take the transmitter first."
  assert_eq "$(jq -c '[.posts, .audioCalls.listen, .audioCalls.openMic]' <<< "$j")" '[[],0,0]'
  j=$(spt_run noaudio 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '"\(.disabled) \(.why)"' <<< "$j")" "true No sound server for ic7300: WSJT-X has the sound card."
  assert_eq "$(jq -c '[.posts, .audioCalls.openMic]' <<< "$j")" '[[],0]'
}

test_ptt_station_refusal_and_drop_end_the_hold() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  # the station refuses the on (the mic is not open yet): its words, the mic never let out, and
  # one off after the answer (an on was sent: the off is idempotent), none at the release
  j=$(spt_run refused 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off"]'
  assert_eq "$(jq -r '.notices[0]' <<< "$j")" "bad: the microphone is not open yet. Press PTT again once the browser has asked for the microphone."
  assert_eq "$(jq -c '.tx' <<< "$j")" '[false]'
  assert_eq "$(jq -r '.labelAfter' <<< "$j")" "PTT"
  assert_eq "$(jq -c '.postsAfterRelease' <<< "$j")" '["on","off"]'
  # the station drops PTT (its notice for this window): the hold ends, the reason shows, one off
  j=$(spt_run dropped 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '"\(.transmitting) \(.labelAfter) \(.hbCleared)"' <<< "$j")" "Transmitting PTT true"
  assert_eq "$(jq -c '.notices' <<< "$j")" '["warn: PTT off: heartbeat lost"]'
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off"]'
  assert_eq "$(jq -c '.tx' <<< "$j")" '[true,false]'
}

test_ptt_notice_for_another_window_keeps_the_hold() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run stale 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  # the previous press's late "released" leaves this press held; this window's own notice ends it
  assert_eq "$(jq -r '"\(.afterStale) \(.afterOwn)"' <<< "$j")" "Transmitting PTT"
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off","on","off"]'
  assert_eq "$(jq -c '.notices' <<< "$j")" '["warn: PTT off: heartbeat lost"]'
}

test_ptt_refused_heartbeat_sends_off() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run hbrefused 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","hb","off"]'
  assert_eq "$(jq -c '.notices' <<< "$j")" '["bad: PTT is not on. Press PTT again."]'
  assert_eq "$(jq -r '"\(.labelAfter) \(.hbCleared)"' <<< "$j")" "PTT true"
}

test_ptt_rebuilt_section_ends_the_hold() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run rebuild 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '.held' <<< "$j")" "Transmitting"
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off"]'
  assert_eq "$(jq -r '"\(.labelAfter) \(.hbCleared)"' <<< "$j")" "PTT true"
  assert_eq "$(jq -c '.tx' <<< "$j")" '[true,false]'
}

test_ptt_release_while_the_mic_prompt_is_open() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run pendingmic 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '.labelPending' <<< "$j")" "Opening the microphone…"
  # the prompt answered after the release: no on, no off, the mic never let out
  assert_eq "$(jq -c '.posts' <<< "$j")" '[]'
  assert_eq "$(jq -c '.tx' <<< "$j")" '[false]'
  assert_eq "$(jq -r '.labelAfter' <<< "$j")" "PTT"
}

test_ptt_page_wide_release() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run docrelease 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '"\(.otherFinger) \(.afterUp) \(.afterHidden) \(.afterLost)"' <<< "$j")" "Transmitting PTT PTT PTT"
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off","on","off","on","off"]'
}

# rc3 fix wave (m8): audio.js's setTX throwing (on or off) skips neither the heartbeat nor the off
test_ptt_audio_trouble_keeps_the_heartbeat_and_the_off() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(spt_run txthrows 2> "$TEST_TMP/spt.err") || { _fail "station_ptt.js failed: $(cat "$TEST_TMP/spt.err")"; return; }
  assert_eq "$(jq -r '"\(.held) \(.heartbeatTimers)"' <<< "$j")" "Transmitting 1"
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","hb","off"]'
  assert_eq "$(jq -r '"\(.labelAfter) \(.hbCleared)"' <<< "$j")" "PTT true"
  assert_eq "$(jq -c '.tx' <<< "$j")" '[true,false]'
}

test_ptt_page_files_are_plain() {
  assert_file_not_contains "$DXB_ROOT/tests/station_ptt.js" "innerHTML"
  assert_file_not_contains "$SP_WEB/vfo.js" "innerHTML"
  assert_file_not_contains "$SP_WEB/vfo.js" "setAttribute(\"style\""
  assert_file_not_contains "$SP_WEB/vfo.js" "fetch("
  assert_file_contains "$SP_WEB/vfo.js" 'window.addEventListener("pagehide", pttUp)'
  assert_file_contains "$SP_WEB/vfo.js" 'window.addEventListener("blur", pttUp)'
}

# ---- the SWR guard's banner (rc3 Task 5) --------------------------------------------------------
# sgd_run SCENARIO: what tests/station_guard.js saw the page show and send, as JSON
sgd_run() { node "$DXB_ROOT/tests/station_guard.js" "$SP_WEB" "$1"; }

test_guard_banner_texts() {
  command -v node > /dev/null 2>&1 || return 0
  local j k="SWR guard: 5.8:1 against 3.0:1; tune the antenna, change band, or override"
  j=$(sgd_run latched 2> "$TEST_TMP/sgd.err") || { _fail "station_guard.js failed: $(cat "$TEST_TMP/sgd.err")"; return; }
  # the banner shows the station's words, with Run ATU and Override; Tune and PTT are dimmed
  assert_eq "$(jq -r '"\(.start.hidden) \(.start.atuShown) \(.start.overrideShown) \(.start.tuneDisabled) \(.start.pttDisabled)"' <<< "$j")" "false true true true true"
  assert_eq "$(jq -r '.start.text' <<< "$j")" "$k"
  # Override asks first, in the page: Cancel sends nothing
  assert_eq "$(jq -r '.dialogText' <<< "$j")" "Transmit anyway into a bad match? The radio will fold back power."
  assert_eq "$(jq -r '"\(.dialogOpen) \(.postsAfterCancel) \(.dialogOpenAfterCancel)"' <<< "$j")" "true 0 false"
  # Run ATU and the confirmed override go to the station with this tab's token
  assert_eq "$(jq -c '.posts' <<< "$j")" '["atu:iPad#tab-1","override:iPad#tab-1"]'
  # hidden again once the latch is clear, Tune and PTT usable
  assert_eq "$(jq -r '"\(.afterOverride.hidden) \(.afterOverride.tuneDisabled) \(.afterOverride.pttDisabled)"' <<< "$j")" "true false false"
  # a latch notice from the station brings the banner back; a clear notice hides it
  assert_eq "$(jq -r '"\(.relatched.hidden) \(.relatched.text)"' <<< "$j")" "false $k"
  assert_eq "$(jq -r '.cleared.hidden' <<< "$j")" "true"
  assert_eq "$(jq -c '.notices' <<< "$j")" "[\"bad: $k. WSJT-X may key again; Stop WSJT-X to end it.\",\"good: SWR guard cleared: band changed.\"]"
  # rc3 Task 6: the flows with the real audio.js. Latched, PTT says why it is dimmed.
  j=$(sau_ui guard 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.start' <<< "$j")" '{"hidden":false,"pttDisabled":true,"pttWhy":"SWR guard is latched: see the banner above."}'
  # Run ATU refused: the station's words, the banner stays; a good tune's clear notice hides it
  assert_eq "$(jq -c '.atuRefused' <<< "$j")" '{"notice":"bad: the tuner did not answer. Try Run ATU again.","hidden":false}'
  assert_eq "$(jq -c '.atuCleared' <<< "$j")" '{"hidden":true,"pttDisabled":false,"pttWhy":""}'
  assert_eq "$(jq -c '.atuPosts' <<< "$j")" '["iPad#tab-1","iPad#tab-1"]'
  # a trip while transmitting ends the hold at once: one off, the RX gain back, the banner up
  assert_eq "$(jq -c '.tripHeld' <<< "$j")" '{"label":"Transmitting","gain":0.1}'
  assert_eq "$(jq -c '.tripped' <<< "$j")" '{"label":"PTT","gain":1,"posts":["on","off"],"banner":{"hidden":false,"pttDisabled":true,"pttWhy":"SWR guard is latched: see the banner above."}}'
  # Override refused leaves the banner; confirmed and accepted, it goes and PTT is back
  assert_eq "$(jq -c '.overrideRefused' <<< "$j")" '{"notice":"bad: the radio is not answering. Check the radio.","banner":{"hidden":false,"pttDisabled":true,"pttWhy":"SWR guard is latched: see the banner above."}}'
  assert_eq "$(jq -c '.overridden' <<< "$j")" '{"hidden":true,"pttDisabled":false,"pttWhy":""}'
  # both are the owner's: on a radio another tab holds they are disabled, and PTT says why
  assert_eq "$(jq -c '.notOwner' <<< "$j")" '{"hidden":false,"atu":true,"override":true,"pttWhy":"Take the transmitter first."}'
}

test_guard_banner_hidden_unless_latched() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(sgd_run clear 2> "$TEST_TMP/sgd.err") || { _fail "station_guard.js failed: $(cat "$TEST_TMP/sgd.err")"; return; }
  assert_eq "$(jq -r '"\(.start.hidden) \(.start.tuneDisabled) \(.start.pttDisabled)"' <<< "$j")" "true false false"
  j=$(sgd_run noguard 2> "$TEST_TMP/sgd.err") || { _fail "station_guard.js failed: $(cat "$TEST_TMP/sgd.err")"; return; }
  assert_eq "$(jq -r '.start.hidden' <<< "$j")" "true"
  # no tuner: Override only
  j=$(sgd_run noatu 2> "$TEST_TMP/sgd.err") || { _fail "station_guard.js failed: $(cat "$TEST_TMP/sgd.err")"; return; }
  assert_eq "$(jq -r '"\(.start.hidden) \(.start.atuShown) \(.start.overrideShown)"' <<< "$j")" "false false true"
  # a notice about another radio changes nothing here
  j=$(sgd_run notice 2> "$TEST_TMP/sgd.err") || { _fail "station_guard.js failed: $(cat "$TEST_TMP/sgd.err")"; return; }
  assert_eq "$(jq -r '"\(.start.hidden) \(.after.hidden) \(.otherRadio)"' <<< "$j")" "true false false"
}

test_guard_page_files_are_plain() {
  assert_file_not_contains "$DXB_ROOT/tests/station_guard.js" "innerHTML"
  assert_file_not_contains "$SP_WEB/vfo.js" "innerHTML"
  assert_file_not_contains "$SP_WEB/vfo.js" "window.confirm"
  assert_file_not_contains "$SP_WEB/vfo.js" " confirm("
  assert_file_contains "$SP_WEB/vfo.js" "Transmit anyway into a bad match? The radio will fold back power."
  assert_file_contains "$SP_WEB/radiosettings.js" "no SWR guard: this radio does not report SWR over CAT"
}

test_radio_settings_swr_guard_line() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(node "$DXB_ROOT/tests/station_radiosettings.js" "$SP_WEB/radiosettings.js" 2> "$TEST_TMP/rs.err") || { _fail "station_radiosettings.js failed: $(cat "$TEST_TMP/rs.err")"; return; }
  assert_eq "$(jq -r '.guardLineOff' <<< "$j")" "no SWR guard: this radio does not report SWR over CAT"
  assert_eq "$(jq -r '.guardLineOn' <<< "$j")" ""
  assert_eq "$(jq -r '.guardLineUnsaid' <<< "$j")" ""
}

# ---- the page's audio: speaker, mic, notices, the lost chip, #listen (rc3 Task 6) -----------------
# sau_ui SCENARIO: the VFO page with the real app.js, audio.js and vfo.js (tests/station_audio_ui.js)
sau_ui() { node "$DXB_ROOT/tests/station_audio_ui.js" "$SP_WEB" "$1"; }

test_audio_stopped_notice_on_program_start() {
  command -v node > /dev/null 2>&1 || return 0
  local j k='WSJT-X has the sound card; no browser audio while it runs'
  j=$(sau_ui program 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '"\(.listening.listenPressed) \(.listening.rx)"' <<< "$j")" "true Listening"
  # WSJT-X takes the sound card: the audio stops (the station is told), the page says why once,
  # Listen and Mic are disabled with the same words, PTT keeps the station's reason
  assert_eq "$(jq -c '.close' <<< "$j")" '{"type":"close"}'
  assert_eq "$(jq -r '.closedSocket' <<< "$j")" "1"
  assert_eq "$(jq -c '.notices' <<< "$j")" "[\"warn: $k\"]"
  assert_eq "$(jq -r '.noticesLater' <<< "$j")" "1"
  assert_eq "$(jq -r '.stopped | "\(.listenPressed) \(.listenDisabled) \(.micDisabled) \(.rx == "") \(.chip)"' <<< "$j")" "false true true true false"
  assert_eq "$(jq -r '.stopped.why' <<< "$j")" "$k"
  assert_eq "$(jq -r '.stopped.pttWhy' <<< "$j")" "No sound server for ic7300: WSJT-X has the sound card."
  # the program ends: Listen is offered again, with a word that it is
  assert_eq "$(jq -r '"\(.freed.listenDisabled) \(.freed.micDisabled) \(.freed.why == "")"' <<< "$j")" "false false true"
  assert_eq "$(jq -r '.freedNotice' <<< "$j")" "good: The sound card is free again: tap Listen to hear the radio."
  # the sound server stopping under the stream (audio lost) before any poll: the page asks the
  # station at once, and the program's words replace the reconnect chip
  assert_eq "$(jq -r '"\(.chipAtLoss) \(.askedAtOnce) \(.afterLoss.chip) \(.afterLoss.listenPressed)"' <<< "$j")" "true 1 false false"
  assert_eq "$(jq -r '.afterLoss.why' <<< "$j")" "$k"
  assert_eq "$(jq -c '.noticesAfterLoss' <<< "$j")" "[\"warn: $k\"]"
}

test_station_audio_duck_by_gain_node() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_run 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  # the audio context is made inside the tap; the radio's track goes source > analyser > gain > out,
  # and the element (kept to start the remote track) is muted so the gain is what is heard
  assert_eq "$(jq -r '.contextInTap' <<< "$j")" "1"
  # rc3 fix wave (m12): counted the moment listen returns, so a context made later (at the
  # socket's open or the answer, outside the gesture) would fail here
  assert_eq "$(jq -r '.contextAtTap' <<< "$j")" "1"
  assert_eq "$(jq -c '.graph' <<< "$j")" '["analyser>gain","gain>destination","source>analyser"]'
  assert_eq "$(jq -r '.muted' <<< "$j")" "true"
  # 20 dB down is a gain of a tenth while transmitting, 1 again after
  assert_eq "$(jq -r '"\(.gainTXNoMic) \(.gainRX) \(.volTXNoMic)"' <<< "$j")" "0.1 1 1"
  # the receiving level comes from the analyser: nothing before the track, 0 for silence, RMS after
  assert_eq "$(jq -r '"\(.levelBeforeTrack) \(.levelSilent) \(.levelLoud)"' <<< "$j")" "null 0 0.35"
  # rc3 fix wave (m11): the level is read from the float samples, so a quiet noise floor reads as
  # what it is (the byte form floors and would read about 0.0055, -45 dBFS)
  assert_eq "$(jq -r '.levelNoise' <<< "$j")" "0.0007"
  # a lost connection takes its nodes down; the reconnection reuses the one context (still running)
  assert_eq "$(jq -c '.graphAfterLost' <<< "$j")" '["source off","analyser off","gain off"]'
  assert_eq "$(jq -c '.reconnectContext' <<< "$j")" '{"made":1,"resumed":0}'
  # stop puts the context to sleep and resets the duck: the next connection starts at full gain
  assert_eq "$(jq -r '"\(.stoppedContext) \(.gainAfterStop)"' <<< "$j")" "suspended 1"
  # the microphone's device name, once allowed; none after stop
  assert_eq "$(jq -r '"\(.micLabelBefore) \(.micLabel) \(.micLabelStopped)"' <<< "$j")" "null USB Audio CODEC null"
  # the states the page follows, in order; a listen replaced before its answer resolves
  assert_eq "$(jq -c '.states' <<< "$j")" '["connecting","on","lost","connecting","connecting","idle","connecting","idle","connecting","on","idle","connecting","connecting","idle"]'
  assert_eq "$(jq -r '.replaced.reason' <<< "$j")" "Audio was stopped."
  # a browser without Web Audio ducks the element's volume instead
  j=$(node "$DXB_ROOT/tests/station_audio.js" "$SP_WEB/audio.js" noaudioctx 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js noaudioctx failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.tx' <<< "$j")" '{"mic":true,"gain":null,"volume":0.1}'
  assert_eq "$(jq -c '.rx' <<< "$j")" '{"mic":false,"gain":null,"volume":1}'
  assert_eq "$(jq -r '"\(.muted) \(.levelLoud)"' <<< "$j")" "false null"
  # rc3 fix wave (m12): Web Audio that will not take the track: the gain leaves the path, the
  # element is unmuted and plays it, its volume ducks, and there is no level to show
  j=$(node "$DXB_ROOT/tests/station_audio.js" "$SP_WEB/audio.js" nosource 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js nosource failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.graph' <<< "$j")" '["analyser>gain","gain>destination","gain off"]'
  assert_eq "$(jq -r '"\(.muted) \(.playing) \(.levelSilent) \(.levelLoud)"' <<< "$j")" 'false ["radio"] null null'
  assert_eq "$(jq -c '.tx' <<< "$j")" '{"mic":true,"gain":1,"volume":0.1}'
  assert_eq "$(jq -c '.rx' <<< "$j")" '{"mic":false,"gain":1,"volume":1}'
  # an analyser without the float form: the bytes still measure the level
  j=$(node "$DXB_ROOT/tests/station_audio.js" "$SP_WEB/audio.js" bytesonly 2> "$TEST_TMP/sau.err") || { _fail "station_audio.js bytesonly failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '"\(.levelSilent) \(.levelLoud)"' <<< "$j")" "0 0.35"
}

test_vfo_listen_toggle_message_sequence() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_ui listen 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.before | "\(.listenText) \(.listenPressed) \(.listenDisabled) \(.rx == "")"' <<< "$j")" "Listen false false true"
  # tapped: the signalling socket opens with the tab token, the button is pressed, connecting
  assert_eq "$(jq -c '.urls' <<< "$j")" '["wss://pi.local/api/audio/ic7300/signal?tab=iPad%23tab-1"]'
  assert_eq "$(jq -r '"\(.connecting.listenPressed) \(.connecting.rx)"' <<< "$j")" "true Connecting…"
  # rc3 fix wave (m12): the audio context exists the moment the tap's handler returns
  assert_eq "$(jq -r '.contextAtTap' <<< "$j")" "1"
  # answered: one audio context, the graph through the gain, the element muted
  assert_eq "$(jq -c '.context' <<< "$j")" '{"made":1,"state":"running"}'
  assert_eq "$(jq -c '.graph' <<< "$j")" '["analyser>gain","gain>destination","source>analyser"]'
  assert_eq "$(jq -r '.muted' <<< "$j")" "true"
  # the receiving indicator follows the analyser
  assert_eq "$(jq -r '"\(.listening)|\(.silent)|\(.receiving)|\(.meterUp)"' <<< "$j")" "Listening|Listening, no sound|Receiving|true"
  # rc3 fix wave (m11): a quiet noise floor is not "Receiving"; only the speaker's state is
  # announced (a polite live region apart from the level line), not the level's flicker
  assert_eq "$(jq -r '.noise' <<< "$j")" "Listening, no sound"
  assert_eq "$(jq -c '.live' <<< "$j")" '{"connecting":"Connecting…","listening":"Listening","silent":"Listening","receiving":"Listening"}'
  assert_eq "$(jq -c '.ariaLive' <<< "$j")" '{"line":null,"state":"polite"}'
  # tapped again: offer then close on the one socket, socket and peer closed, the context asleep
  assert_eq "$(jq -c '.messages' <<< "$j")" '[["offer","close"]]'
  assert_eq "$(jq -c '.closed' <<< "$j")" '{"socket":1,"pc":1,"context":"suspended"}'
  assert_eq "$(jq -r '"\(.stopped.listenPressed) \(.stopped.rx == "") \(.levelTimers)"' <<< "$j")" "false true 0"
  # once more: a new socket on the same context; the speaker never asked for the microphone
  assert_eq "$(jq -c '.again' <<< "$j")" '{"sockets":2,"made":1,"state":"running","pressed":"true"}'
  assert_eq "$(jq -r '.gum' <<< "$j")" "0"
}

test_vfo_mic_button_shows_the_device() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_ui mic 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.before | "\(.micText)|\(.micDisabled)|\(.micLine)"' <<< "$j")" "Microphone|false|The microphone is asked for once and used only while PTT is held."
  # allowed: the device's name as plain text, and the note that it is used only during PTT
  assert_eq "$(jq -r '.after | "\(.micText)|\(.micDisabled)|\(.micLine)"' <<< "$j")" "Microphone ready|true|Microphone: USB Audio CODEC <1>. It is used only while PTT is held."
  assert_eq "$(jq -r '.gum' <<< "$j")" "1"
  # listening afterwards carries it, idle; stopping the audio releases it and offers it again
  assert_eq "$(jq -c '.onConnection' <<< "$j")" '{"replaced":["mic"],"enabled":false}'
  assert_eq "$(jq -c '.released' <<< "$j")" '{"stopped":true,"text":"Microphone","disabled":false}'
}

test_vfo_audio_disabled_with_the_reason() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_ui reasons 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.pure.program' <<< "$j")" "WSJT-X has the sound card; no browser audio while it runs"
  assert_eq "$(jq -r '.pure.flrig' <<< "$j")" "flrig has the sound card; no browser audio while it runs"
  assert_eq "$(jq -r '.pure.down' <<< "$j")" "No sound server for ic7300: the server is not running."
  assert_eq "$(jq -r '.pure.none' <<< "$j")" "No browser audio on this radio."
  assert_eq "$(jq -r '.pure.nojs' <<< "$j")" "This page has no browser audio."
  assert_eq "$(jq -r '"[\(.pure.ok)] \(.pure.holder)"' <<< "$j")" "[] WSJT-X"
  # rc3 fix wave (m9; changes this test): one unavailable answer with no program named leaves a
  # flowing stream alone, and so does the next good one; the second unavailable answer in a row
  # stops it (a program taking the card still stops it at once: test_audio_stopped_notice_on_program_start)
  assert_eq "$(jq -c '[.blip, .afterBlip, .downOnce]' <<< "$j")" '[{"pressed":"true","closed":0,"notices":0},{"pressed":"true","closed":0,"notices":0},{"pressed":"true","closed":0,"notices":0}]'
  # the sound server stops while listening: the audio stops, Listen and Mic are disabled with the
  # station's reason, and taps do nothing; once it is back the page says so in plain words
  assert_eq "$(jq -r '.down | "\(.listenPressed) \(.listenDisabled) \(.micDisabled) \(.why)"' <<< "$j")" "false true true No sound server for ic7300: the server is not running."
  assert_eq "$(jq -r '"\(.downSockets) \(.downGum)"' <<< "$j")" "1 0"
  assert_eq "$(jq -c '.notices' <<< "$j")" '["warn: No sound server for ic7300: the server is not running.","good: Browser audio is back: tap Listen to hear the radio."]'
  assert_eq "$(jq -r '"\(.back.listenDisabled) \(.back.why == "")"' <<< "$j")" "false true"
  j=$(sau_ui nojs 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '.page | "\(.listenDisabled) \(.micDisabled) \(.why)|\(.pttWhy)"' <<< "$j")" "true true This page has no browser audio.|This page has no browser audio."
}

# rc3 fix wave (m10): the page's own stop is not reported as a failure, and a microphone the Mic
# button opened is let go when a program takes the sound card while the tab is not listening
test_vfo_page_stop_is_quiet_and_lets_the_microphone_go() {
  command -v node > /dev/null 2>&1 || return 0
  local j k='WSJT-X has the sound card; no browser audio while it runs'
  j=$(sau_ui pagestop 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -c '.press' <<< "$j")" '{"posts":[],"notices":[],"label":"PTT"}'
  assert_eq "$(jq -c '.mic' <<< "$j")" '{"notices":[],"stopped":true,"text":"Microphone"}'
  assert_eq "$(jq -r '.micOpen' <<< "$j")" "Microphone ready"
  assert_eq "$(jq -c '.taken' <<< "$j")" "{\"stopped\":true,\"text\":\"Microphone\",\"notices\":[\"warn: $k\"]}"
}

test_vfo_audio_lost_chip_reconnects_the_page() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_ui lost 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '"\(.holding) \(.gainHeld)"' <<< "$j")" "Transmitting 0.1"
  # the connection is lost mid-transmission: the hold ends with one off, the chip and the words show
  assert_eq "$(jq -c '.posts' <<< "$j")" '["on","off"]'
  assert_eq "$(jq -r '"\(.labelAfter) \(.lost.chip) \(.lost.listenPressed)|\(.lost.rx)"' <<< "$j")" "PTT true false|Audio lost. Tap Listen to reconnect."
  # the station's PTT notice gives its reason
  assert_eq "$(jq -c '.notices' <<< "$j")" '["warn: PTT off: audio lost"]'
  # tapping the chip reconnects; the page follows it (pressed, connecting, then listening, full gain)
  assert_eq "$(jq -r '"\(.sockets) \(.reconnecting.chip) \(.reconnecting.listenPressed) \(.reconnecting.rx)"' <<< "$j")" "2 false true Connecting…"
  assert_eq "$(jq -r '"\(.back.rx) \(.gain)"' <<< "$j")" "Listening 1"
}

test_vfo_listen_hash_waits_for_a_gesture() {
  command -v node > /dev/null 2>&1 || return 0
  local j
  j=$(sau_ui hash 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  # opened from the dashboard's Listen: nothing plays before a tap; the page says to tap
  assert_eq "$(jq -r '"\(.socketsBefore) \(.armed)|\(.before.rx)"' <<< "$j")" "0 true|Tap anywhere on the page to start listening."
  # the first tap anywhere starts the audio once, and the hash is dropped
  assert_eq "$(jq -r '"\(.sockets) \(.after.listenPressed) \(.armedAfter) \(.socketsLater)"' <<< "$j")" "1 true false 1"
  assert_eq "$(jq -c '.history' <<< "$j")" '["/radio/ic7300"]'
  # a tap on Listen itself is the button's own: one start, not a start and a stop
  j=$(sau_ui hashbutton 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '"\(.sockets) \(.after.listenPressed) \(.socketsLater)"' <<< "$j")" "1 true 1"
  # no sound server: the tap starts nothing and the reason shows instead of the prompt
  j=$(sau_ui hashnoaudio 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  assert_eq "$(jq -r '"\(.armed) \(.before.rx == "") \(.sockets)"' <<< "$j")" "false true 0"
  assert_eq "$(jq -r '.after.why' <<< "$j")" "WSJT-X has the sound card; no browser audio while it runs"
}

test_dashboard_listen_button() {
  command -v node > /dev/null 2>&1 || return 0
  local j; j=$(sau_ui dashboard 2> "$TEST_TMP/sau.err") || { _fail "station_audio_ui.js failed: $(cat "$TEST_TMP/sau.err")"; return; }
  # a radio with a sound card gets Listen, which opens its page with #listen (with or without CAT)
  assert_eq "$(jq -c '.withAudio' <<< "$j")" '{"href":"/radio/radio2#listen","aria":"Listen to radio2","cls":"btn small"}'
  assert_eq "$(jq -r '.noCat.href' <<< "$j")" "/radio/radio4#listen"
  # none without a sound card, and none for Graywolf (it holds the card itself)
  assert_eq "$(jq -r '"\(.noAudio) \(.graywolf)"' <<< "$j")" "null null"
}

test_station_audio_ui_files_are_plain() {
  local f
  for f in vfo.js audio.js dashboard.js; do
    assert_file_not_contains "$SP_WEB/$f" "innerHTML"
    assert_file_not_contains "$SP_WEB/$f" "insertAdjacentHTML"
    assert_file_not_contains "$SP_WEB/$f" "setAttribute(\"style\""
  done
  assert_file_not_contains "$DXB_ROOT/tests/station_audio_ui.js" "innerHTML"
  assert_file_not_contains "$SP_WEB/vfo.css" "http"
  # the volume of an element is not what ducks any more (iPhones ignore it): the gain is
  assert_file_contains "$SP_WEB/audio.js" "createGain()"
  assert_file_contains "$SP_WEB/audio.js" "createMediaStreamSource("
  assert_file_not_contains "$DXB_ROOT/README.md" "iPhones ignore the page's volume setting"
  command -v node > /dev/null 2>&1 || return 0
  for f in vfo.js audio.js dashboard.js; do
    node --check "$SP_WEB/$f" 2> "$TEST_TMP/node.err" || _fail "$f does not parse: $(cat "$TEST_TMP/node.err")"
  done
  node --check "$DXB_ROOT/tests/station_audio_ui.js" 2> "$TEST_TMP/node.err" || _fail "station_audio_ui.js does not parse: $(cat "$TEST_TMP/node.err")"
}
