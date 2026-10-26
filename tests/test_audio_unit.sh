#!/usr/bin/env bash
# The radio's sound server (dxberry-audio@RADIO.service, provision/bin/dxberry-audio) and how a
# program session yields to it. Helpers here start with au_.
# shellcheck disable=SC1091,SC2016,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/settings.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_LIB/screen.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"

AU_BIN=$DXB_ROOT/provision/bin/dxberry-audio

# au_env: the apps scene (radio1, radio2 plugged in) plus "codec" (no owner) and "gw" (Graywolf's);
# systemctl and pulseaudio are stand-in scripts that record into $TEST_TMP/calls and $TEST_TMP/pulse-argv.
au_env() {
  fx_apps_env
  export DXB_SHARE=$DXB_ROOT/provision/share DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json \
    DXB_AUDIO_DIR=$TEST_TMP/run/audio DXB_AUDIO_WAIT_S=0 DXB_FAKE_EUID=0 DXB_SESSIONS_DIR=$TEST_TMP/run/sessions
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  DXB_RADIOS=$(jq -c '.radios.radio2.cat.path = "usb-0:1.2:1.0"
    | .radios.codec = (.radios.radio1 | .label = "Codec" | .rigctld_port = 4536 | .audio.path = "usb-0:1.9:1.0")
    | .radios.gw = (.radios.radio1 | .label = "Packet" | .rigctld_port = 4538 | .owner = "graywolf")' <<< "$DXB_RADIOS")
  printf '%s\n' "$DXB_RADIOS" > "$DXB_RADIOS_FILE"
  printf '#!/bin/bash\nsource "$DXB_ROOT/tests/fixtures/sysfs.sh"\nfx_systemctl "$@"\n' > "$TEST_TMP/bin/systemctl"
  printf '#!/bin/bash\nprintf "%%s\\n" "$@" > "$TEST_TMP/pulse-argv"\n' > "$TEST_TMP/bin/pulseaudio"
  chmod 755 "$TEST_TMP/bin/systemctl" "$TEST_TMP/bin/pulseaudio"
  export DXB_SYSTEMCTL=$TEST_TMP/bin/systemctl DXB_PULSEAUDIO=$TEST_TMP/bin/pulseaudio
  : > "$TEST_TMP/pulse-argv"
}

test_audio_unit_renders_for_radio() (
  au_env
  assert_ok "$AU_BIN" run codec
  assert_eq "$(tr '\n' '|' < "$TEST_TMP/pulse-argv")" \
    "-n|--daemonize=no|--exit-idle-time=-1|--disallow-exit|--use-pid-file=no|-F|/dev/null|-L|module-alsa-sink device=plughw:CARD=CODEC,DEV=0 sink_name=CODEC|-L|module-alsa-source device=plughw:CARD=CODEC,DEV=0 source_name=CODEC.in|-L|module-null-sink sink_name=CODEC.tx|-L|module-native-protocol-unix socket=$DXB_AUDIO_DIR/codec/native auth-anonymous=1|"
  # no module-loading restriction: the station loads a loopback later
  assert_not_contains "$(cat "$TEST_TMP/pulse-argv")" "disallow-module-loading"
  # the unit file: yields nothing, runs as the program user, owns its runtime directory
  local u=$DXB_ROOT/provision/templates/dxberry-audio@.service
  assert_file_contains "$u" "User=dxbapps"
  assert_file_contains "$u" "Group=dxbscreen"
  assert_file_contains "$u" "RuntimeDirectory=dxberry/audio/%i"
  assert_file_contains "$u" "RuntimeDirectoryMode=0750"
  assert_file_contains "$u" "ExecStart=/opt/dxberry/bin/dxberry-audio run %i"
  assert_file_contains "$u" "Restart=on-failure"
  assert_file_contains "$u" "RestartSec=2"
  assert_file_contains "$u" "After=sound.target"
)

# the production path: as dxbapps the record is unreadable, so the card and device come from the name
test_audio_run_without_a_readable_record() (
  au_env
  local want; "$AU_BIN" run codec; want=$(cat "$TEST_TMP/pulse-argv")
  : > "$TEST_TMP/pulse-argv"
  DXB_RADIOS='' DXB_RADIOS_FILE=$TEST_TMP/none/radios.json "$AU_BIN" run codec
  assert_eq "$(cat "$TEST_TMP/pulse-argv")" "$want"
  assert_contains "$want" "sink_name=CODEC"
)

test_audio_check_refuses_what_cannot_be_served() (
  au_env
  assert_ok "$AU_BIN" check codec
  "$AU_BIN" check nope 2> "$TEST_TMP/err"; assert_eq "$?" "4"
  "$AU_BIN" check gw 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "Graywolf"
  jq -c '.radios.codec.audio = null' "$DXB_RADIOS_FILE" > "$TEST_TMP/r.json" && mv "$TEST_TMP/r.json" "$DXB_RADIOS_FILE"
  "$AU_BIN" check codec 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "no audio device"
  "$AU_BIN" ensure codec 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemctl start"
)

test_audio_unit_yields_to_alsa_program() (
  au_env
  dxb_app_load wsjtx > /dev/null 2>&1
  dxb_radio_scan_cache
  : > "$TEST_TMP/calls"
  dxb_screen_audio_prepare wsjtx radio2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@radio2.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-audio"
  : > "$TEST_TMP/calls"
  dxb_screen_audio_stopped wsjtx radio2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start dxberry-audio@radio2.service"
  # a pulse program leaves the server running when it ends
  dxb_app_load js8call > /dev/null 2>&1
  : > "$TEST_TMP/calls"
  dxb_screen_audio_stopped js8call radio2
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-audio"
)

test_audio_unit_started_for_pulse_program() (
  au_env
  dxb_app_load js8call > /dev/null 2>&1
  : > "$TEST_TMP/calls"
  dxb_screen_audio_prepare js8call radio2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start dxberry-audio@radio2.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "stop dxberry-audio"
)

test_graywolf_card_gets_no_server() (
  au_env
  : > "$TEST_TMP/calls"
  dxb_screen_audio_prepare js8call gw
  dxb_screen_audio_stopped wsjtx gw
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-audio"
  "$AU_BIN" ensure gw 2> "$TEST_TMP/err"
  assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "Graywolf"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemctl start"
)

test_audio_status_json() (
  au_env
  local j
  j=$("$AU_BIN" status codec --json)
  assert_eq "$(jq -c . <<< "$j")" "{\"radio\":\"codec\",\"active\":false,\"socket\":\"$DXB_AUDIO_DIR/codec/native\",\"card\":\"CODEC\",\"present\":false}"
  echo "dxberry-audio@codec.service" >> "$TEST_TMP/active"
  mkdir -p "$DXB_AUDIO_DIR/codec"; : > "$DXB_AUDIO_DIR/codec/native"
  j=$("$AU_BIN" status codec --json)
  assert_eq "$(jq -c '[.active, .present]' <<< "$j")" "[true,true]"
  "$AU_BIN" status nope --json > /dev/null 2>&1; assert_eq "$?" "4"
  "$AU_BIN" status > /dev/null 2>&1; assert_eq "$?" "2"
)

test_audio_ensure_and_stop() (
  au_env
  "$AU_BIN" ensure codec > /dev/null 2>&1       # nothing makes the socket: failed
  assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start dxberry-audio@codec.service"
  mkdir -p "$DXB_AUDIO_DIR/codec"; : > "$DXB_AUDIO_DIR/codec/native"
  assert_ok "$AU_BIN" ensure codec
  assert_eq "$("$AU_BIN" ensure codec --json | jq -r .ok)" "true"
  assert_ok "$AU_BIN" stop codec
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@codec.service"
  "$AU_BIN" ensure nope > /dev/null 2>&1; assert_eq "$?" "4"
  "$AU_BIN" ensure > /dev/null 2>&1; assert_eq "$?" "2"
)

# stop needs only a valid name: the server of a radio already removed from the record can still be
# stopped
test_audio_stop_works_for_a_radio_not_in_the_record() (
  au_env
  assert_ok "$AU_BIN" stop gone
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@gone.service"
  assert_eq "$("$AU_BIN" stop gone --json | jq -r .ok)" "true"
  "$AU_BIN" stop 'Bad!' > /dev/null 2>&1; assert_eq "$?" "2"
)

# au_before A B: call A was made before call B (both made)
au_before() {
  local a b
  a=$(grep -nxF -- "$1" "$TEST_TMP/calls" | head -1 | cut -d: -f1); b=$(grep -nxF -- "$2" "$TEST_TMP/calls" | head -1 | cut -d: -f1)
  if [[ -z $a || -z $b ]] || (( a > b )); then _fail "'$1' (call ${a:-none}) must come before '$2' (call ${b:-none})"; fi
}

# a session's start of the server clears its earlier failures first: a replug, or an update's
# session restarts, can use up the unit's start limit
test_audio_session_start_clears_the_start_limit_first() (
  au_env
  dxb_app_load js8call > /dev/null 2>&1
  : > "$TEST_TMP/calls"
  dxb_screen_audio_prepare js8call radio2
  au_before "systemctl reset-failed dxberry-audio@radio2.service" "systemctl start dxberry-audio@radio2.service"
  dxb_app_load wsjtx > /dev/null 2>&1
  dxb_radio_scan_cache
  : > "$TEST_TMP/calls"
  dxb_screen_audio_stopped wsjtx radio2
  au_before "systemctl reset-failed dxberry-audio@radio2.service" "systemctl start dxberry-audio@radio2.service"
)
