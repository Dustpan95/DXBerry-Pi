#!/usr/bin/env bash
# Program sessions (screen.sh) and the dxberry-app command.
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

# sc_env: fx_apps_env for the real dxberry-app, run as its own process: every stand-in is a script
# under $TEST_TMP/bin (a function does not reach another process), both radios plugged in (the
# two-digirigs scene), a fake session program "gamma" in $TEST_TMP/apps and in the catalog. gamma
# records firstfill, wire and command in $TEST_TMP/appcalls; GAMMA_AUDIO picks its audio (alsa);
# GAMMA_FAIL=firstfill|wire makes that step fail (exported, so the command sees it).
# Every test file is sourced into one shell, and the brief's test_app_* names sort among other
# files' tests (test_radio's test_apply_*, whose radio_env never resets DXB_SCREENS_DIR), so sc_env
# leaves nothing behind: what it and fx_apps_env set is saved first and put back when the test
# that called it returns (a RETURN trap fires when sc_env returns and when its caller does;
# functions the test calls do not inherit it).
SC_NAMES='DXB_STATE_DIR DXB_LOG_FILE DXB_RUN_DIR DXB_RADIOS_FILE DXB_WIRED_FILE DXB_SCREENS_DIR DXB_APPS_DIR
  DXB_APPS_TSV DXB_APPS_OPT DXB_APPS_HOME DXB_APPS_STATE DXB_APPS_WORK DXB_GH_API DXB_APPS_CURL DXB_APT_GET DXB_DPKG_QUERY
  DXB_APT_CACHE DXB_USERADD DXB_GROUPADD DXB_USERMOD DXB_CHOWN DXB_GETENT DXB_SYSTEMCTL DXB_RUNUSER DXB_GPSPIPE
  DXB_APPS_EXTRACT_BASE DXB_CONSOLE_USER DXB_SHARE DXB_SYSFS_ROOT DXB_RADIOS_STATE DXB_RIGCTLD_RUN_DIR DXB_SYSTEMD_RUN
  DXB_AMIXER DXB_ALSACTL DXB_UPDATE_JOB_FILE DXB_UPDATE_RESULT DXB_UPDATE_WORK DXB_UPDATE_CACHE DXB_CONFIG_JOB_FILE
  DXB_NETSAFE_DIR DXB_UPDATE_CMD DXB_BOOT_DIR DXB_RADIO_LOG DXB_FAKE_EUID DXB_RIGCTLD_WAIT_S DXB_SESSION_WAIT_S
  DXB_UPDATE_LOCK_WAIT DXB_CONFIG_LOCK_WAIT DXB_XVNC DXB_OPENBOX DXB_PULSEAUDIO DXB_SCREEN_STOP_WAIT_S
  GAMMA_CMD GAMMA_FAIL GAMMA_AUDIO'
SC_SAVED=''
SC_ARMED=0
sc_save() {
  local n d
  SC_SAVED=''
  for n in $SC_NAMES; do
    if [[ -v $n ]]; then d=$(declare -p "$n"); SC_SAVED+="declare -g ${d#declare }"$'\n'; else SC_SAVED+="unset $n"$'\n'; fi
  done
}
_sc_restore() {
  [[ ${FUNCNAME[1]:-} == test_* ]] || return 0
  trap - RETURN
  SC_ARMED=0
  eval "$SC_SAVED"
}
sc_env() {
  if (( ! SC_ARMED )); then sc_save; SC_ARMED=1; trap _sc_restore RETURN; fi
  fx_apps_env
  # sc_fakes' stand-ins are exported into this shared shell: never into the next test
  unset GAMMA_FAIL GAMMA_CMD
  export DXB_XVNC=Xvnc DXB_OPENBOX=openbox DXB_PULSEAUDIO=pulseaudio DXB_SCREEN_STOP_WAIT_S=10
  GAMMA_AUDIO=''
  export DXB_APPS_DIR=$TEST_TMP/apps DXB_APPS_TSV=$TEST_TMP/apps.tsv DXB_SHARE=$DXB_ROOT/provision/share \
    DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld \
    DXB_SYSTEMCTL=$TEST_TMP/bin/systemctl DXB_CHOWN=$TEST_TMP/bin/chown DXB_SYSTEMD_RUN=$TEST_TMP/bin/systemd-run \
    DXB_DPKG_QUERY=$TEST_TMP/bin/dpkg-query DXB_AMIXER=$TEST_TMP/bin/none DXB_ALSACTL=$TEST_TMP/bin/none \
    DXB_UPDATE_JOB_FILE=$TEST_TMP/run/update-job.json DXB_UPDATE_RESULT=$TEST_TMP/run/update-result.json \
    DXB_UPDATE_WORK=$TEST_TMP/state/update-work DXB_UPDATE_CACHE=$TEST_TMP/state/update-check.json \
    DXB_CONFIG_JOB_FILE=$TEST_TMP/run/config-job.json DXB_NETSAFE_DIR=$TEST_TMP/state/network-snapshot \
    DXB_UPDATE_CMD=/opt/dxberry/bin/dxberry-update DXB_BOOT_DIR=$TEST_TMP/boot DXB_RADIO_LOG=$TEST_TMP/state/radio.log \
    DXB_FAKE_EUID=0 DXB_RIGCTLD_WAIT_S=0 DXB_SESSION_WAIT_S=0 DXB_UPDATE_LOCK_WAIT=1 DXB_CONFIG_LOCK_WAIT=1
  mkdir -p "$DXB_APPS_DIR" "$DXB_BOOT_DIR" "$DXB_SCREENS_DIR"
  : > "$TEST_TMP/appcalls"
  printf '#!/bin/bash\nsource "$DXB_ROOT/tests/fixtures/sysfs.sh"\nfx_systemctl "$@"\n' > "$TEST_TMP/bin/systemctl"
  printf '#!/bin/bash\necho "chown $*" >> "$TEST_TMP/calls"\n' > "$TEST_TMP/bin/chown"
  printf '#!/bin/bash\necho "systemd-run $*" >> "$TEST_TMP/calls"\n[[ ! -f $TEST_TMP/systemd-run-fails ]]\n' > "$TEST_TMP/bin/systemd-run"
  # gamma's own package is installed (1.0); nothing else is
  printf '#!/bin/bash\nif [[ ${*: -1} == gamma ]]; then echo "installed 1.0"; exit 0; fi\nexit 1\n' > "$TEST_TMP/bin/dpkg-query"
  chmod 755 "$TEST_TMP/bin/systemctl" "$TEST_TMP/bin/chown" "$TEST_TMP/bin/systemd-run" "$TEST_TMP/bin/dpkg-query"
  cat > "$DXB_APPS_DIR/gamma.sh" <<'EOF'
app_gamma_kind() { echo radio; }
app_gamma_session() { echo yes; }
app_gamma_installed() { return 0; }
app_gamma_label() { echo Gamma; }
app_gamma_unit() { dxb_app_session_unit gamma "$1"; }
app_gamma_audio() { echo "${GAMMA_AUDIO:-alsa}"; }
app_gamma_settings_file() { echo "$DXB_APPS_HOME/.config/gamma/$1.ini"; }
app_gamma_firstfill() { echo "gamma firstfill $1" >> "$TEST_TMP/appcalls"; [[ ${GAMMA_FAIL:-} != firstfill ]]; }
app_gamma_wire() { echo "gamma wire $1" >> "$TEST_TMP/appcalls"; [[ ${GAMMA_FAIL:-} != wire ]]; }
app_gamma_unwire() { :; }
app_gamma_needs_service_restart() { echo no; }
app_gamma_command() { echo "gamma command $1 $2" >> "$TEST_TMP/appcalls"; printf '%s\n--rig-name=%s\n' "${GAMMA_CMD:-gamma}" "$1"; }
EOF
  { cat "$DXB_ROOT/provision/share/apps.tsv"; printf 'gamma\tGamma\tradio\tapt:gamma\t-\tA program for the tests\n'; } > "$DXB_APPS_TSV"
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  # radio2's CAT is the second digirig's serial port in this scene
  DXB_RADIOS=$(jq -c '.radios.radio2.cat.path = "usb-0:1.2:1.0"' <<< "$DXB_RADIOS")
  printf '%s\n' "$DXB_RADIOS" > "$DXB_RADIOS_FILE"
}
# sc_owned RADIO APP: APP owns RADIO, and RADIO's rigctld is running.
sc_owned() {
  DXB_RADIOS=$(jq -c --arg n "$1" --arg a "$2" '.radios[$n].owner = $a' <<< "$DXB_RADIOS")
  printf '%s\n' "$DXB_RADIOS" > "$DXB_RADIOS_FILE"
  echo "rigctld@$1.service" >> "$TEST_TMP/active"
}
# sc_set_ptt RADIO METHOD: change RADIO's PTT method in the record (its wiring hash changes).
sc_set_ptt() {
  DXB_RADIOS=$(jq -c --arg n "$1" --arg m "$2" '.radios[$n].ptt.method = $m' <<< "$DXB_RADIOS")
  printf '%s\n' "$DXB_RADIOS" > "$DXB_RADIOS_FILE"
}
sc_unplug() { fx_unplug "$DXB_SYSFS_ROOT" "${1/radio/r}-audio"; }
sc_replug() { fx_replug "$DXB_SYSFS_ROOT" "${1/radio/r}-audio"; }
sc_active() { echo "$1" >> "$TEST_TMP/active"; }
# sc_job_running UNIT: an update job UNIT is running.
sc_job_running() {
  mkdir -p "$DXB_RUN_DIR"
  printf '{"unit":"%s","kind":"system","name":null,"started":1}\n' "$1" > "$DXB_UPDATE_JOB_FILE"
  sc_active "$1"
}
sc_app() { "$DXB_ROOT/provision/bin/dxberry-app" "$@"; }

test_screen_env_names_only_its_own_card() {
  sc_env; sc_owned radio2 gamma
  local e; e=$(dxb_screen_env gamma-radio2)
  assert_contains "$e" "DEVICE=plughw:CARD=RADIO2\\,DEV=0"
  assert_not_contains "$e" "RADIO1"
  assert_not_contains "$e" "CODEC"
}
test_screen_pulse_loads_only_the_session_card() {
  sc_env; sc_owned radio2 gamma; GAMMA_AUDIO=pulse
  dxb_screen_session_argv gamma-radio2 pulse > "$TEST_TMP/argv"
  assert_eq "$(grep -c 'module-alsa-' "$TEST_TMP/argv")" "2"
  assert_eq "$(grep -c 'device=plughw:CARD=RADIO2,DEV=0' "$TEST_TMP/argv")" "2"
  assert_not_contains "$(cat "$TEST_TMP/argv")" "module-udev-detect"
}
test_prepare_fills_once_and_rewires_on_hash_change() {
  sc_env; sc_owned radio2 gamma
  assert_ok "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(tr '\n' ';' < "$TEST_TMP/appcalls")" "gamma firstfill radio2;gamma wire radio2;"
  : > "$TEST_TMP/appcalls"; "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(cat "$TEST_TMP/appcalls")" ""                        # nothing changed: neither again
  sc_set_ptt radio2 vox; "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(tr '\n' ';' < "$TEST_TMP/appcalls")" "gamma wire radio2;"
}
test_present_skips_start_for_unplugged_radio() {
  sc_env; sc_owned radio2 gamma; sc_unplug radio2
  "$DXB_ROOT/provision/bin/dxberry-app" present gamma-radio2; assert_eq "$?" "1"
}
test_app_remove_refused_while_a_radio_runs_it() {
  sc_env; sc_owned radio2 gamma
  "$DXB_ROOT/provision/bin/dxberry-app" remove gamma 2> "$TEST_TMP/err"; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma runs on radio2; switch radio2 to another program first"
}
test_app_install_is_refused_while_an_update_runs() {
  sc_env; sc_job_running dxberry-job-update-system-1
  "$DXB_ROOT/provision/bin/dxberry-app" install wsjtx 2> /dev/null; assert_eq "$?" "5"
}
test_app_screen_answers_without_root() {
  sc_env; sc_owned radio2 gamma; sc_active dxberry-screen@gamma-radio2.service
  assert_eq "$(DXB_FAKE_EUID=1000 "$DXB_ROOT/provision/bin/dxberry-app" screen gamma-radio2 --json | jq -c '{state, socket}')" \
    "{\"state\":\"running\",\"socket\":\"$DXB_SCREENS_DIR/gamma-radio2.sock\"}"
}
test_session_unit_template_runs_prepare_as_root_and_program_as_dxbapps() {
  local u=$DXB_ROOT/provision/templates/dxberry-screen@.service
  assert_file_contains "$u" "User=dxbapps"
  assert_file_contains "$u" "ExecStartPre=+/opt/dxberry/bin/dxberry-app prepare %i"
  assert_file_contains "$u" "ExecCondition=+/opt/dxberry/bin/dxberry-app present %i"
  assert_file_not_contains "$u" "Restart="
}

# ---- beyond the brief's eight ------------------------------------------------------------

# The unit file is exactly the brief's, plus the ExecStopPost line of the stopped/waiting ruling.
test_screen_unit_template_and_tmpfiles_are_exact() {
  local u=$DXB_ROOT/provision/templates/dxberry-screen@.service
  assert_file_contains "$u" "ExecStopPost=+/opt/dxberry/bin/dxberry-app stopped %i"
  assert_file_contains "$u" "SupplementaryGroups=audio dialout dxbscreen"
  assert_file_contains "$u" "KillMode=mixed"
  assert_eq "$(grep -c . "$u")" "20"
  assert_eq "$(cat "$DXB_ROOT/provision/templates/dxberry-apps.tmpfiles")" "d /run/dxberry/screens 2770 root dxbscreen -"
}

# present skips an unplugged radio's start and leaves the session waiting for it (the hotplug
# apply starts it once the radio is back); stopped does the same for a program that ended while
# its radio was gone, and nothing when the radio is there (quit from its menu, or Stop).
test_screen_present_and_stopped_leave_a_session_waiting_for_its_radio() {
  sc_env; sc_owned radio2 gamma
  local m=$DXB_SCREENS_DIR/gamma-radio2.waiting
  assert_ok sc_app present gamma-radio2
  [[ -e $m ]] && _fail "a plugged-in radio leaves no marker"
  sc_unplug radio2
  sc_app present gamma-radio2; assert_eq "$?" "1"
  [[ -f $m ]] || _fail "present must leave the session waiting for its radio"
  rm -f "$m"
  assert_ok sc_app stopped gamma-radio2
  [[ -f $m ]] || _fail "stopped with the radio gone must leave the session waiting"
  rm -f "$m"; sc_replug radio2
  assert_ok sc_app stopped gamma-radio2
  [[ -e $m ]] && _fail "stopped with the radio plugged in must not mark it waiting"
  # a planted symlink is replaced, never written through
  sc_unplug radio2; echo keep > "$TEST_TMP/victim"; ln -s "$TEST_TMP/victim" "$m"
  sc_app stopped gamma-radio2
  assert_eq "$(cat "$TEST_TMP/victim")" "keep"
  [[ -f $m && ! -L $m ]] || _fail "the marker must be a file of its own"
  sc_app stopped nonsense; assert_eq "$?" "0"
}

# prepare writes the session's environment (0640 root:dxbscreen), a fresh private directory
# (0700 dxbapps, no sound server may spawn) and hands the program's settings to dxbapps.
test_screen_prepare_writes_the_env_file_and_private_dir() {
  sc_env; sc_owned radio2 gamma
  mkdir -p "$DXB_APPS_HOME/.config/gamma"; : > "$DXB_SCREENS_DIR/gamma-radio2.sock"
  assert_ok sc_app prepare gamma-radio2
  local f=$DXB_SCREENS_DIR/gamma-radio2.env d=$DXB_SCREENS_DIR/gamma-radio2.d
  assert_eq "$(stat -c %a "$f")" "640"
  assert_eq "$(cat "$f")" "$(dxb_screen_env gamma-radio2)"
  assert_contains "$(cat "$f")" "AUDIO=alsa"
  assert_contains "$(cat "$f")" "SCREEN=yes"
  assert_contains "$(cat "$f")" "GEOMETRY=1280x800"
  assert_contains "$(cat "$f")" "RIGCTLD=127.0.0.1:4534"
  assert_eq "$(stat -c %a "$d")" "700"
  assert_eq "$(cat "$d/client.conf")" "autospawn = no"
  [[ -e $DXB_SCREENS_DIR/gamma-radio2.sock ]] && _fail "a stale screen socket is removed"
  # owned while it is still a temp file, then renamed into place
  assert_contains "$(cat "$TEST_TMP/calls")" "chown root:dxbscreen $DXB_SCREENS_DIR/.dxbtmp."
  assert_contains "$(cat "$TEST_TMP/calls")" "chown -h dxbapps:dxbapps $d"
  assert_contains "$(cat "$TEST_TMP/calls")" "chown -R dxbapps:dxbapps $DXB_APPS_HOME/.config/gamma"
  assert_contains "$(cat "$TEST_TMP/calls")" "chown -h dxbapps:dxbapps $DXB_APPS_HOME/.config"
  [[ -f $DXB_APPS_STATE/firstfill/gamma-radio2 ]] || _fail "the first fill is marked"
}

test_screen_prepare_refusals() {
  sc_env; sc_owned radio2 gamma
  sc_app prepare gamma-radio1 2> /dev/null; assert_eq "$?" "3"     # radio1 is not gamma's
  sc_app prepare gamma-radio9 2> /dev/null; assert_eq "$?" "3"
  sc_app prepare 'gamma-../x' 2> /dev/null; assert_eq "$?" "2"
  export GAMMA_FAIL=firstfill
  sc_app prepare gamma-radio2 2> /dev/null; assert_eq "$?" "7"
  [[ -e $DXB_APPS_STATE/firstfill/gamma-radio2 ]] && _fail "a failed first fill is not marked"
  export GAMMA_FAIL=wire; : > "$TEST_TMP/appcalls"
  sc_app prepare gamma-radio2 2> /dev/null; assert_eq "$?" "7"
  sc_app prepare gamma-radio2 2> /dev/null; assert_eq "$?" "7"     # a failed wiring is tried again
  assert_eq "$(grep -c 'gamma wire' "$TEST_TMP/appcalls")" "2"
  unset GAMMA_FAIL
  sc_unplug radio2
  sc_app prepare gamma-radio2 2> /dev/null; assert_eq "$?" "4"
}

# prepare waits for rigctld when the program talks to the radio through it, and starts the
# program anyway (with a warning) when rigctld never comes up.
test_screen_prepare_waits_for_rigctld() {
  sc_env; sc_owned radio2 gamma
  grep -vx 'rigctld@radio2.service' "$TEST_TMP/active" > "$TEST_TMP/active.n"; mv "$TEST_TMP/active.n" "$TEST_TMP/active"
  sc_app prepare gamma-radio2 2> "$TEST_TMP/err"; assert_eq "$?" "0"
  assert_contains "$(cat "$TEST_TMP/err")" "rigctld@radio2.service is not active"
}

test_screen_xvnc_and_openbox_argv() {
  sc_env; sc_owned radio2 gamma
  dxb_screen_session_argv gamma-radio2 xvnc > "$TEST_TMP/argv"
  assert_eq "$(tr '\n' ' ' < "$TEST_TMP/argv")" \
    "Xvnc -displayfd 3 -rfbunixpath $DXB_SCREENS_DIR/gamma-radio2.sock -rfbunixmode 0660 -rfbport -1 -SecurityTypes None -geometry 1280x800 -nolisten tcp -desktop Gamma · IC-7300 "
  assert_eq "$(dxb_screen_session_argv gamma-radio2 openbox | tr '\n' ' ')" "openbox --sm-disable "
  assert_eq "$(dxb_screen_session_argv gamma-radio2 pulse | tail -1)" \
    "module-native-protocol-unix socket=$DXB_SCREENS_DIR/gamma-radio2.d/pulse.sock"
  # the runner reads the env file prepare wrote, never the radio record
  sc_app prepare gamma-radio2
  DXB_RADIOS='' DXB_RADIOS_FILE=$TEST_TMP/none dxb_screen_session_argv gamma-radio2 xvnc "$DXB_SCREENS_DIR/gamma-radio2.env" > "$TEST_TMP/argv2"
  assert_eq "$(cat "$TEST_TMP/argv2")" "$(cat "$TEST_TMP/argv")"
}

# the screen answer reads its labels from the env file as data: a file that tries to run code
# is never run (the program user can replace files in the screens directory)
test_screen_answer_states_and_never_runs_the_env_file() {
  sc_env; sc_owned radio2 gamma
  sc_app screen gamma-radio2 --json > /dev/null 2>&1; assert_eq "$?" "3"     # never prepared, not running
  sc_app prepare gamma-radio2
  assert_eq "$(DXB_FAKE_EUID=1000 sc_app screen gamma-radio2 --json)" \
    '{"session":"gamma-radio2","app":"gamma","radio":"radio2","label":"Gamma","radio_label":"IC-7300","state":"stopped","socket":null,"open":{"kind":"screen"}}'
  echo dxberry-screen@gamma-radio2.service > "$TEST_TMP/failed"
  assert_eq "$(sc_app screen gamma-radio2 --json | jq -r .state)" "failed"
  printf 'LABEL=$(touch %s/ran)\nRADIO_LABEL=`touch %s/ran`\n' "$TEST_TMP" "$TEST_TMP" > "$DXB_SCREENS_DIR/gamma-radio2.env"
  assert_eq "$(sc_app screen gamma-radio2 --json | jq -r .label)" '$(touch '"$TEST_TMP"'/ran)'
  [[ -e $TEST_TMP/ran ]] && _fail "the env file must never be run"
  sc_app screen ../etc --json > /dev/null 2>&1; assert_eq "$?" "2"
}

test_screen_start_and_stop_a_session() {
  sc_env; sc_owned radio2 gamma
  assert_ok sc_app start radio2 --json > "$TEST_TMP/out"
  assert_eq "$(cat "$TEST_TMP/out")" '{"ok":true,"session":"gamma-radio2","state":"running"}'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-screen@gamma-radio2.service"
  assert_contains "$(cat "$TEST_TMP/active")" "dxberry-screen@gamma-radio2.service"
  assert_ok sc_app stop radio2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-radio2.service"
  assert_eq "$(jq -r .radios.radio2.owner "$DXB_RADIOS_FILE")" "gamma"      # the radio stays owned
  sc_app start radio1 2> /dev/null; assert_eq "$?" "3"                      # nothing owns radio1
  sc_app start radio9 2> /dev/null; assert_eq "$?" "3"
  sc_owned radio1 graywolf
  printf 'app_graywolf_kind() { echo radio; }\napp_graywolf_unit() { echo graywolf.service; }\n' > "$DXB_APPS_DIR/graywolf.sh"
  sc_app stop radio1 2> /dev/null; assert_eq "$?" "3"                       # not a session program
  sc_unplug radio2
  sc_app start radio2 2> /dev/null; assert_eq "$?" "4"
}

test_screen_list_json() {
  sc_env; sc_owned radio2 gamma; sc_active dxberry-screen@gamma-radio2.service
  assert_ok sc_app list --json > "$TEST_TMP/out"
  assert_eq "$(jq -c '.[] | select(.name == "gamma")' "$TEST_TMP/out")" \
    '{"name":"gamma","label":"Gamma","kind":"radio","description":"A program for the tests","source":"debian","installed":true,"version":"1.0","sessions":[{"radio":"radio2","state":"running"}],"single_copy":false,"open":{"kind":"screen"}}'
  assert_eq "$(jq -c '.[] | select(.name == "wsjtx") | [.source, .installed, .version, .sessions]' "$TEST_TMP/out")" '["upstream",false,null,[]]'
  assert_eq "$(jq -r '.[] | select(.name == "graywolf") | .source' "$TEST_TMP/out")" "builtin"
  assert_eq "$(jq -r 'map(.name) | join(" ")' "$TEST_TMP/out")" "graywolf wsjtx js8call fldigi qsstv pat flrig wavelog gamma"
}

# install and remove are update jobs: the shared job file says which program
test_screen_install_and_remove_start_update_jobs() {
  sc_env
  assert_ok sc_app install wsjtx --json > "$TEST_TMP/out"
  assert_eq "$(jq -r '.kind, .name' "$TEST_TMP/out" | tr '\n' ' ')" "app-install wsjtx "
  assert_eq "$(jq -r '.kind, .name' "$DXB_UPDATE_JOB_FILE" | tr '\n' ' ')" "app-install wsjtx "
  assert_contains "$(cat "$TEST_TMP/calls")" "--description=DXBerry update (app-install wsjtx) /opt/dxberry/bin/dxberry-update apply app-install wsjtx"
  assert_contains "$(jq -r .unit "$DXB_UPDATE_JOB_FILE")" "dxberry-job-update-app-install-wsjtx-"
  assert_ok sc_app remove gamma > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "/opt/dxberry/bin/dxberry-update apply app-remove gamma"
  sc_app install nosuch 2> /dev/null; assert_eq "$?" "3"
  sc_app install graywolf 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "part of DXBerry"
  touch "$TEST_TMP/systemd-run-fails"
  sc_app install wsjtx 2> /dev/null; assert_eq "$?" "6"
  sc_app install 2> /dev/null; assert_eq "$?" "2"
  sc_app frobnicate 2> /dev/null; assert_eq "$?" "2"
}

# dxberry-update runs the job bodies: apply app-install NAME / app-remove NAME
test_screen_update_apply_runs_the_program_jobs() {
  sc_env
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_apps_install() { echo "install $*" >> "$TEST_TMP/appcalls"; return 8; }
    dxb_apps_remove() { echo "remove $*" >> "$TEST_TMP/appcalls"; return 0; }
    main apply app-install wsjtx; echo "rc $?" >> "$TEST_TMP/appcalls"
    main apply app-remove wsjtx; echo "rc $?" >> "$TEST_TMP/appcalls"
    main apply app-install; echo "rc $?" >> "$TEST_TMP/appcalls"
    main apply app-install '../x'; echo "rc $?" >> "$TEST_TMP/appcalls"
  ) > /dev/null 2>&1
  assert_eq "$(tr '\n' ';' < "$TEST_TMP/appcalls")" "install wsjtx;rc 8;remove wsjtx;rc 0;rc 2;rc 2;"
  assert_eq "$(jq -r .exit "$DXB_UPDATE_RESULT")" "0"
}

test_screen_session_refuses_root_and_a_missing_env() {
  sc_env; sc_owned radio2 gamma
  sc_app session gamma-radio2 2> "$TEST_TMP/err"; assert_eq "$?" "1"
  DXB_FAKE_EUID=900 sc_app session gamma-radio2 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "prepare"
}

test_screen_usage_lines() {
  local h; h=$(sc_app -h)
  assert_contains "$h" "screen SESSION"
  assert_contains "$h" "Exit:"
  assert_eq "$(sed -n '2,14p' "$DXB_ROOT/provision/bin/dxberry-app" | grep -vc '^#')" "0"
  assert_eq "$(sed -n '15p' "$DXB_ROOT/provision/bin/dxberry-app" | grep -c '^#')" "0"
}

# sc_fakes: stand-ins for Xvnc (answers display 7 on fd 3), openbox and the program. The program
# writes its environment to $TEST_TMP/prog-env, then by PROG_MODE: exit (ends 3), wait (ends 0 on
# TERM) or stubborn (ignores TERM). Xvnc and openbox log "NAME ended" in $TEST_TMP/helpers.
sc_fakes() {
  local b=$TEST_TMP/bin
  printf '#!/bin/bash\ntrap '\''echo "Xvnc ended" >> "$TEST_TMP/helpers"; exit 0'\'' TERM\necho 7 >&3\nexec 3>&-\nwhile :; do sleep 0.1; done\n' > "$b/Xvnc"
  printf '#!/bin/bash\ntrap '\''echo "openbox ended" >> "$TEST_TMP/helpers"; exit 0'\'' TERM\nwhile :; do sleep 0.1; done\n' > "$b/openbox"
  cat > "$b/prog" <<'PROG'
#!/bin/bash
env | grep -E '^(DISPLAY|HOME|XDG_RUNTIME_DIR|PULSE_SERVER|PULSE_CLIENTCONFIG|DEVICE|RADIO|RIGCTLD)=' | sort > "$TEST_TMP/prog-env"
echo "args $*" >> "$TEST_TMP/prog-env"
case $PROG_MODE in
  exit) exit 3 ;;
  wait) trap 'echo TERM >> "$TEST_TMP/prog-log"; exit 0' TERM ;;
  stubborn) trap 'echo TERM >> "$TEST_TMP/prog-log"' TERM ;;
esac
: > "$TEST_TMP/prog-started"
while :; do sleep 0.1; done
PROG
  chmod 755 "$b/Xvnc" "$b/openbox" "$b/prog"
  export DXB_XVNC=$b/Xvnc DXB_OPENBOX=$b/openbox GAMMA_CMD=$b/prog DXB_SCREEN_STOP_WAIT_S=1
}
sc_wait_for() { local i; for (( i = 0; i < 100; i++ )); do [[ -e $1 ]] && return 0; command sleep 0.05; done; return 1; }

test_screen_session_runs_the_program_and_ends_with_it() {
  sc_env; sc_owned radio2 gamma; sc_app prepare gamma-radio2; sc_fakes
  PROG_MODE=exit DXB_FAKE_EUID=900 timeout 20 "$DXB_ROOT/provision/bin/dxberry-app" session gamma-radio2 2> "$TEST_TMP/err"
  assert_eq "$?" "3"
  local d=$DXB_SCREENS_DIR/gamma-radio2.d
  assert_eq "$(cat "$TEST_TMP/prog-env")" "$(printf '%s\n' "DEVICE=plughw:CARD=RADIO2,DEV=0" "DISPLAY=:7" "HOME=$DXB_APPS_HOME" \
    "PULSE_CLIENTCONFIG=$d/client.conf" "PULSE_SERVER=unix:$d/pulse.sock" "RADIO=radio2" "RIGCTLD=127.0.0.1:4534" \
    "XDG_RUNTIME_DIR=$d" "args --rig-name=radio2")"
  assert_eq "$(LC_ALL=C sort "$TEST_TMP/helpers" | tr '\n' ';')" "Xvnc ended;openbox ended;"
}

# ruling 9: a stop sends the program TERM; one that ignores it is killed after the wait. Either
# way the session ends 0 (a stop is not a failure) and the helpers end with it.
test_screen_session_forwards_term_then_kills() {
  local pid mode guard i
  sc_env; sc_owned radio2 gamma; sc_app prepare gamma-radio2; sc_fakes
  for mode in wait stubborn; do
    rm -f "$TEST_TMP/prog-started" "$TEST_TMP/prog-log" "$TEST_TMP/helpers"
    PROG_MODE=$mode DXB_FAKE_EUID=900 "$DXB_ROOT/provision/bin/dxberry-app" session gamma-radio2 2> "$TEST_TMP/err-$mode" &
    pid=$!
    # a runner that hangs must not hang the tests
    ( for (( i = 0; i < 200; i++ )); do command sleep 0.1; kill -0 "$pid" 2> /dev/null || exit 0; done; kill -KILL "$pid" ) &
    guard=$!
    sc_wait_for "$TEST_TMP/prog-started" || _fail "the program never started ($mode)"
    kill -TERM "$pid"; wait "$pid"
    assert_eq "$?" "0"
    wait "$guard" 2> /dev/null
    assert_eq "$(cat "$TEST_TMP/prog-log" 2> /dev/null)" "TERM"
    assert_eq "$(LC_ALL=C sort "$TEST_TMP/helpers" | tr '\n' ';')" "Xvnc ended;openbox ended;"
  done
  assert_contains "$(cat "$TEST_TMP/err-stubborn")" "did not end within 1s of TERM; killing it"
  assert_not_contains "$(cat "$TEST_TMP/err-wait")" "killing it"
}
