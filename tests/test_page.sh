#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
PG_DIR=$DXB_ROOT/provision/cockpit/dxberry

# pg_fields: the dxberry-status --json paths dxberry.js declares it reads (between its markers).
# Anchored on the comment's own `/* fields-begin` so it does not also match `radio-fields-begin`.
pg_fields() { sed -n '/\/\* fields-begin/,/\/\* fields-end/p' "$PG_DIR/dxberry.js" | grep -oE '"[a-z0-9_.*]+"' | tr -d '"'; }
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

# Cockpit keeps a visited page's frame alive but hidden, and document.hidden stays false inside
# it, so the refresh loop must watch cockpit.hidden/cockpit's own visibilitychange, not document's.
# A --no-block power action that fails outright (not just the connection going away) must be
# reported through failure(), not swallowed as a silent, permanent "restarting" state.
test_page_watches_cockpit_visibility_and_reports_power_failures() {
  local js=$PG_DIR/dxberry.js power_body
  assert_file_contains "$js" "cockpit.hidden"
  assert_file_contains "$js" 'cockpit.addEventListener("visibilitychange"'
  assert_file_not_contains "$js" "document.hidden"
  power_body=$(sed -n '/^function power(/,/^}/p' "$js")
  assert_contains "$power_body" "failure("
  assert_contains "$power_body" "disconnected"
  assert_contains "$power_body" "terminated"
}

# A refresh failure's notice must not outlive the refresh: notice()/failure() return the node
# they created, setRefreshFailure() tracks it, and a later good refresh removes it. It must never
# wipe out another notice (e.g. an action's "done") that happens to be showing at the time.
test_page_clears_the_refresh_failure_notice_once_a_refresh_succeeds() {
  local js=$PG_DIR/dxberry.js notice_body failure_body set_body refresh_body
  notice_body=$(sed -n '/^function notice(/,/^}/p' "$js")
  assert_contains "$notice_body" "return n"
  assert_contains "$notice_body" ".append(n)"
  failure_body=$(sed -n '/^function failure(/,/^}/p' "$js")
  assert_contains "$failure_body" "return notice("
  set_body=$(sed -n '/^function setRefreshFailure(/,/^}/p' "$js")
  assert_contains "$set_body" "refreshFailureNotice = "
  assert_contains "$set_body" "could not be read"
  refresh_body=$(sed -n '/^function refresh(/,/^}/p' "$js")
  assert_contains "$refresh_body" "setRefreshFailure("
  assert_contains "$refresh_body" "clearRefreshFailureNotice()"
  assert_file_contains "$js" "refreshFailureNotice.remove()"
}

# An action asks for a refresh while the 10 s one may still be running; that request must run
# when the running one ends, not vanish. And a card that throws must show a notice, not leave the
# page silently stale.
test_page_reruns_a_refresh_asked_for_while_busy_and_survives_a_card_that_throws() {
  local js=$PG_DIR/dxberry.js refresh_body show_body
  refresh_body=$(sed -n '/^function refresh(/,/^}/p' "$js")
  assert_contains "$refresh_body" "state.again = true"
  assert_contains "$refresh_body" "if (state.again)"
  show_body=$(sed -n '/^function show(/,/^}/p' "$js")
  assert_contains "$show_body" "catch (e)"
  assert_contains "$show_body" "setRefreshFailure("
}

# The cards are rebuilt every 10 s; a keyboard user's focus must land back on the same button.
test_page_keeps_keyboard_focus_across_a_refresh() {
  local js=$PG_DIR/dxberry.js render_body
  render_body=$(sed -n '/^function render(/,/^}/p' "$js")
  assert_contains "$render_body" "dataset.key"
  assert_contains "$render_body" ".focus()"
  assert_file_contains "$js" '"data-key": o.key'
  # shellcheck disable=SC2016  # checking for the literal, unexpanded `Stop ${u.unit}` in the JS
  assert_file_contains "$js" 'aria: `Stop ${u.unit}`'
  assert_file_not_contains "$js" 'role: "status"'
  assert_file_contains "$PG_DIR/index.html" 'aria-labelledby="confirm-title"'
}

# A failed DXBerry command's stderr is timestamped log lines; the page shows the WARN/ERROR text
# (and lines that are not log lines) without timestamps, drops INFO progress, and puts the plain
# meaning of the exit code first.
test_page_error_text_keeps_the_problem_and_drops_progress() {
  command -v node > /dev/null 2>&1 || return 0
  local js=$PG_DIR/dxberry.js et pt stderr got
  et=$(sed -n '/^function errorText(/,/^}/p' "$js")
  pt=$(sed -n '/^function problemText(/,/^}/p' "$js")
  stderr=$(printf '%s\n' '2026-10-06 12:00:00 [INFO] radio radio1 now owned by graywolf' \
    '2026-10-06 12:00:01 [ERROR] invalid radio record:' '  radio1: bad label' 'unknown option: --x')
  got=$(node -e "$et
$pt
console.log(errorText(process.argv[1]));
console.log(problemText({exit_status: 4, message: process.argv[2]}, {4: 'the device is not plugged in'}));
console.log(problemText({problem: 'not-found'}));" "$stderr" '2026-10-06 12:00:00 [ERROR] radio radio1 is not plugged in')
  assert_eq "$(sed -n 1p <<< "$got")" "invalid radio record: radio1: bad label unknown option: --x"
  assert_eq "$(sed -n 2p <<< "$got")" "Exit 4: the device is not plugged in. radio radio1 is not plugged in"
  assert_eq "$(sed -n 3p <<< "$got")" "not-found"
}

# Restart of cockpit.socket drops this console's session just like Stop, so it must ask first
# too, with its own warning text (it reconnects on its own, unlike Stop).
test_page_confirms_restarting_cockpit_socket_too() {
  local js=$PG_DIR/dxberry.js unit_action_body
  assert_file_contains "$js" "RESTART_WARNINGS"
  # shellcheck disable=SC2016  # checking for the literal, unexpanded `Restart ${unit}?` in the JS
  assert_file_contains "$js" '`Restart ${unit}?`'
  unit_action_body=$(sed -n '/^function unitAction(/,/^}/p' "$js")
  assert_contains "$unit_action_body" "RESTART_WARNINGS[unit]"
  assert_contains "$unit_action_body" '"Restart"'
  # Stop's own three confirmations must still be there, unchanged.
  assert_file_contains "$js" '"graywolf.service": "APRS, the iGate and the digipeater stop'
  assert_file_contains "$js" '"dxberry-netwatch.service": "Network failover stops.'
  assert_file_contains "$js" '"cockpit.socket": "This console closes and stays unreachable'
}

# tests/page_smoke.js runs the page against a fake DOM and a fake cockpit.spawn: the cards render,
# the radio buttons ask what they should and run the right commands, and the form sends what it should.
test_page_smoke_drives_the_page_in_a_fake_browser() {
  command -v node > /dev/null 2>&1 || return 0
  local out
  out=$(node "$DXB_ROOT/tests/page_smoke.js" "$PG_DIR/dxberry.js" 2>&1) || _fail "tests/page_smoke.js failed:
$out"
}

test_page_script_parses() {
  command -v node > /dev/null 2>&1 || return 0   # node is optional locally; CI's runner has it
  node --check "$PG_DIR/dxberry.js" 2> "$TEST_TMP/node.err" || _fail "dxberry.js does not parse: $(cat "$TEST_TMP/node.err")"
}

# Every field the page reads must exist in the real command's output, so a change to dxberry-status
# that would leave a card blank fails here. The radios part is real dxberry-radio output, with one
# radio pinned and a second DigiRig plugged in but not set up.
test_page_reads_only_fields_that_dxberry_status_produces() {
  local DXB_GPSPIPE=pg_gpspipe f n=0
  cli_env
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  cli add radio1 --audio 1 --cat 2 --label TM-V71 > /dev/null 2>&1
  cli status --json || _fail "dxberry-radio status --json failed: $(cat "$TEST_TMP/err")"
  cp "$TEST_TMP/out" "$TEST_TMP/radio.json"
  st_env
  st_cli --json || _fail "dxberry-status --json failed: $(cat "$TEST_TMP/err")"
  for f in $(pg_fields); do
    n=$(( n + 1 ))
    pg_has_path "$TEST_TMP/out" "$f" || _fail "dxberry.js reads $f, which dxberry-status --json does not produce"
  done
  (( n >= 60 )) || _fail "only $n fields found between the markers in dxberry.js"
}

# pg_radio_fields: "COMMAND:PATH" entries dxberry.js declares it reads from dxberry-radio's own answers.
pg_radio_fields() { sed -n '/radio-fields-begin/,/radio-fields-end/p' "$PG_DIR/dxberry.js" | grep -oE '"[a-z]+:[a-z0-9_.*]+"' | tr -d '"'; }

test_page_reads_only_fields_that_dxberry_radio_produces() {
  local DXB_RIGCTL=cli_rigctl f n=0
  cli_env
  cli models --json || _fail "dxberry-radio models --json failed: $(cat "$TEST_TMP/err")"
  cp "$TEST_TMP/out" "$TEST_TMP/models.json"
  cli add radio1 --audio 1 --cat 2 --json || _fail "dxberry-radio add --json failed: $(cat "$TEST_TMP/err")"
  cp "$TEST_TMP/out" "$TEST_TMP/add.json"
  cli release radio1 --json || _fail "dxberry-radio release --json failed: $(cat "$TEST_TMP/err")"
  cp "$TEST_TMP/out" "$TEST_TMP/release.json"
  for f in $(pg_radio_fields); do
    n=$(( n + 1 ))
    pg_has_path "$TEST_TMP/${f%%:*}.json" "${f#*:}" || _fail "dxberry.js reads ${f#*:} from dxberry-radio ${f%%:*} --json, which it does not produce"
  done
  (( n >= 4 )) || _fail "only $n fields found between the radio-fields markers in dxberry.js"
}

test_page_drives_radios_through_dxberry_radio() {
  local js=$PG_DIR/dxberry.js
  assert_file_contains "$js" 'const RADIO = "/opt/dxberry/bin/dxberry-radio";'
  assert_file_contains "$js" '["claim", name, app.name]'
  assert_file_contains "$js" '["release", name]'
  assert_file_contains "$js" '["remove", name]'
  assert_file_contains "$js" '4: "the device is not plugged in"'
  assert_file_contains "$js" '5: "the application could not take the radio, so it was left released"'
  assert_file_contains "$js" "failure(t.what, ex, RADIO_EXITS)"
}

# A channel Graywolf has that is not named after a DXBerry radio was made by hand (spec 8.3), even
# one named like a member every JavaScript object inherits.
test_page_flags_graywolf_channels_made_by_hand() {
  command -v node > /dev/null 2>&1 || return 0
  local fn got
  fn=$(sed -n '/^function handMadeChannels(/,/^}/p' "$PG_DIR/dxberry.js")
  got=$(node -e "let last = {graywolf: {ok: true, api_ok: true, channels: [{name: 'VHF APRS'}, {name: 'radio1'}, {name: 'constructor'}]}, radios: {radios: {radio1: {}}}};
$fn
console.log(JSON.stringify(handMadeChannels()));
last.graywolf.api_ok = false;
console.log(JSON.stringify(handMadeChannels()));")
  assert_eq "$(sed -n 1p <<< "$got")" '["VHF APRS","constructor"]'
  assert_eq "$(sed -n 2p <<< "$got")" '[]'
}

# Release and Remove stop an application that owns no other radio (plumbing spec 9.2); the page
# must say so before it happens.
test_page_says_when_the_owner_will_stop() {
  command -v node > /dev/null 2>&1 || return 0
  local fn got
  fn=$(sed -n '/^function ownsOthers(/,/^}/p' "$PG_DIR/dxberry.js")
  got=$(node -e "let last = {radios: {radios: {radio1: {owner: 'graywolf'}, radio2: {owner: ''}}}};
$fn
console.log(ownsOthers('graywolf', 'radio1'));
last.radios.radios.radio2.owner = 'graywolf';
console.log(ownsOthers('graywolf', 'radio1'));")
  assert_eq "$got" "$(printf 'false\ntrue')"
}

test_page_has_the_radio_form_dialog() {
  local html=$PG_DIR/index.html js=$PG_DIR/dxberry.js
  assert_file_contains "$html" '<dialog id="radio-dialog" aria-labelledby="radio-dialog-title">'
  assert_file_contains "$html" '<form id="radio-form" novalidate>'
  assert_file_contains "$html" 'id="radio-fields"'
  assert_file_contains "$html" 'id="radio-cancel"'
  assert_file_contains "$js" 'addEventListener("submit", saveRadioForm)'
  assert_file_contains "$js" 'btn("Add", () => openRadioForm(null, c)'
  assert_file_contains "$js" 'btn("Edit", () => openRadioForm(n)'
}

# The form pins by port path, sends add every field, and sends set only what changed.
test_page_radio_form_sends_only_what_changed() {
  command -v node > /dev/null 2>&1 || return 0
  local js=$PG_DIR/dxberry.js fn re got
  fn=$(sed -n '/^function radioFormArgs(/,/^}/p' "$js")
  re=$(grep -m1 '^const NAME_RE' "$js")
  got=$(node -e "$re
const vals = {'rf-label': 'TM-V71', 'rf-audio': '', 'rf-cat': 'none', 'rf-ptt-serial': '', 'rf-hid': '', 'rf-model': '1',
  'rf-baud': '57600', 'rf-ptt': 'rigctld', 'rf-ptt-type': 'RTS', 'rf-gpio': '', 'rf-wiring': 'full'};
const document = { getElementById: id => (id in vals ? { value: vals[id] } : null) };
const x = {label: 'old', rig: {model: 1, baud: 57600, ptt_type: 'RTS'}, ptt: {method: 'rigctld', gpio_line: null}, wiring: 'full'};
let form = {edit: true, name: 'radio1', x};
let last = {radios: {radios: {radio1: x}}};
$fn
console.log(JSON.stringify(radioFormArgs()));
vals['rf-label'] = 'old'; vals['rf-cat'] = '';
console.log(JSON.stringify(radioFormArgs()));
form = {edit: false}; vals['rf-name'] = 'radio2'; vals['rf-audio'] = 'usb-0:1.3:1.0'; vals['rf-cat'] = 'usb-0:1.4:1.0'; vals['rf-ptt-serial'] = 'none';
console.log(JSON.stringify(radioFormArgs()));
vals['rf-name'] = 'radio1';
console.log(JSON.stringify(radioFormArgs()));
vals['rf-name'] = 'radio3'; vals['rf-audio'] = 'none'; vals['rf-cat'] = 'none';
console.log(JSON.stringify(radioFormArgs()));
vals['rf-name'] = 'radio4'; vals['rf-ptt-serial'] = 'usb-0:1.5:1.0';
console.log(JSON.stringify(radioFormArgs()));
vals['rf-name'] = 'radio5'; vals['rf-ptt'] = 'vox';
console.log(JSON.stringify(radioFormArgs()));
vals['rf-name'] = 'constructor';
console.log(JSON.stringify(radioFormArgs()));")
  assert_eq "$(sed -n 1p <<< "$got")" '{"args":["set","radio1","--label","TM-V71","--cat","none"],"name":"radio1"}'
  assert_eq "$(sed -n 2p <<< "$got")" '{"args":null,"name":"radio1"}'
  assert_eq "$(sed -n 3p <<< "$got")" '{"args":["add","radio2","--audio","usb-0:1.3:1.0","--cat","usb-0:1.4:1.0","--model","1","--baud","57600","--ptt","rigctld","--ptt-type","RTS","--wiring","full","--label","old"],"name":"radio2"}'
  assert_contains "$(sed -n 4p <<< "$got")" "already exists"
  assert_contains "$(sed -n 5p <<< "$got")" "at least one"
  # A sound card and a CAT port are not the only way to pin a radio: a PTT serial port (or a HID,
  # not exercised here) counts too, so this must return args, not the "at least one" error.
  assert_eq "$(sed -n 6p <<< "$got")" '{"args":["add","radio4","--audio","none","--cat","none","--ptt-serial","usb-0:1.5:1.0","--model","1","--baud","57600","--ptt","rigctld","--ptt-type","RTS","--wiring","full","--label","old"],"name":"radio4"}'
  # only rigctld keys by a serial line: any other PTT is added with NONE, not the profile's RTS
  assert_eq "$(sed -n 7p <<< "$got")" '{"args":["add","radio5","--audio","none","--cat","none","--ptt-serial","usb-0:1.5:1.0","--model","1","--baud","57600","--ptt","vox","--ptt-type","NONE","--wiring","full","--label","old"],"name":"radio5"}'
  # a valid name that every JavaScript object inherits is not a radio that "already exists"
  assert_contains "$(sed -n 8p <<< "$got")" '"args":["add","constructor",'
}

# A failed Hamlib list fetch must not be cached forever: opening the form retries it, so the
# searchable list still appears once the command works.
test_page_retries_a_failed_model_list_from_the_form() {
  local js=$PG_DIR/dxberry.js load_body cards_body got
  load_body=$(sed -n '/^function loadModels(/,/^}/p' "$js")
  assert_contains "$load_body" "modelsLoading = null"
  cards_body=$(sed -n '/^function radiosCard(/,/^}/p' "$js")
  assert_contains "$cards_body" "modelsTried = true"
  # a listing that does not parse is a failure too: the next ask runs the command again
  command -v node > /dev/null 2>&1 || return 0
  got=$(node -e "const RADIO = 'dxberry-radio';
let models = null, modelsLoading = null;
const answers = ['rigctl: not a listing', '[{\"model\":1}]'];
function run() { return Promise.resolve(answers.shift()); }
$load_body
loadModels().then(() => console.log('parsed garbage'), () => {
  console.log('cleared ' + (modelsLoading === null));
  return loadModels().then(m => console.log('retried ' + JSON.stringify(m)));
}).catch(e => console.log('stuck ' + e));" 2>&1)
  assert_eq "$got" "$(printf 'cleared true\nretried [{"model":1}]')"
}

# A save that is still in flight when the operator cancels and reopens the form must not touch
# the newer form's dialog once it resolves: only the page notice and the refresh happen either
# way; re-enabling Save and closing the dialog are for the form that is still open.
test_page_a_stale_save_leaves_a_newer_form_alone() {
  command -v node > /dev/null 2>&1 || return 0
  local js=$PG_DIR/dxberry.js fn got
  fn=$(sed -n '/^function saveRadioForm(/,/^}/p' "$js")
  got=$(node -e "
const RADIO = '/opt/dxberry/bin/dxberry-radio';
const RADIO_EXITS = {};
const dialog = { open: true, closes: 0, close() { this.open = false; this.closes++; } };
const document = { getElementById: id => (id === 'radio-dialog' ? dialog : null) };
const savingCalls = [];
function setSaving(on) { savingCalls.push(on); }
function setFormError(t) { /* not expected here */ }
const notices = [];
function notice(t) { notices.push(t); }
function failure(what) { notices.push('FAILURE:' + what); }
function warningsOf() { return []; }
let refreshCalls = 0;
function refresh() { refreshCalls++; }
function problemText() { return 'problem'; }
function radioFormArgs() { return { args: ['set', 'radio1', '--label', 'TM-V71'], name: 'radio1' }; }
let resolveRun;
function run() { return new Promise(res => { resolveRun = res; }); }
let form = { edit: true, name: 'radio1', saving: false };
$fn
saveRadioForm({ preventDefault() {} });
form = { edit: true, name: 'radio1', saving: false };   // Cancel, then the form is opened again
resolveRun('{}');
Promise.resolve().then(() => {
  console.log(JSON.stringify({ dialogOpen: dialog.open, closes: dialog.closes, savingCalls, notices, refreshCalls }));
});
")
  assert_eq "$got" '{"dialogOpen":true,"closes":0,"savingCalls":[true],"notices":["radio1 updated."],"refreshCalls":1}'
}
