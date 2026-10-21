#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_LIB/screen.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/webproxy.sh"
source "$DXB_LIB/apps/pat.sh"

# pt_env: the two-radio program environment, radio2 on CAT PTT, call W0BTE, grid EM28qv, and a
# stand-in pat that writes its real default config (what Pat 1.0.0 does when the file is missing)
pt_env() {
  fx_apps_env
  DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]=38.88 [LONGITUDE]=-94.6)
  export DXB_PAT_CMD=pt_pat
}
pt_pat() {
  echo "pat $*" >> "$TEST_TMP/calls"
  [[ ! -e $TEST_TMP/pat-fail ]] || return 1
  if [[ $1 == --config ]]; then ( umask 077; cp "$DXB_ROOT/tests/fixtures/apps/pat_config.json" "$2" ); fi
  return 1   # like the real one: "Missing mycall"
}
pt_set_ptt() { DXB_RADIOS=$(jq -c --arg n "$1" --arg m "$2" '.radios[$n].ptt.method = $m' <<< "$DXB_RADIOS"); }
pt_get() { jq -r "$1" "$(app_pat_settings_file)"; }

test_pat_firstfill_then_wire_into_pats_own_defaults() {
  pt_env
  assert_ok app_pat_firstfill radio2
  assert_ok app_pat_wire radio2
  local f; f=$(app_pat_settings_file)
  assert_eq "$f" "$DXB_APPS_HOME/.config/pat/config.json"
  assert_eq "$(pt_get .mycall)" "W0BTE"
  assert_eq "$(pt_get .locator)" "EM28qv"
  assert_eq "$(pt_get .http_addr)" "127.0.0.1:8180"
  assert_eq "$(pt_get .ardop.addr)" "localhost:8515"
  assert_eq "$(pt_get .ardop.rig)" "RADIO"
  assert_eq "$(pt_get .ardop.ptt_ctrl)" "true"
  assert_eq "$(pt_get '.hamlib_rigs.RADIO | [.address, .network] | join(" ")')" "127.0.0.1:4534 tcp"
  # Pat's own defaults are all still there
  assert_eq "$(pt_get .connect_aliases.telnet)" "telnet://{mycall}:CMSTelnet@cms.winlink.org:8772/wl2k"
  assert_eq "$(pt_get .ardop.connect_requests)" "10"
  assert_eq "$(stat -c %a "$f")" "600"
  assert_eq "$(find "${f%/*}" -type f | wc -l)" "1"
}
test_pat_firstfill_without_a_grid_leaves_the_locator_alone() {
  pt_env; DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]='' [LONGITUDE]='')
  assert_ok app_pat_firstfill radio2
  assert_eq "$(pt_get .locator)" ""
  assert_eq "$(pt_get .mycall)" "W0BTE"
}
test_pat_rewire_keeps_operator_keys() {
  pt_env; app_pat_firstfill radio2; app_pat_wire radio2
  local f; f=$(app_pat_settings_file)
  jq -c '.secure_login_password = "s3cret-pw" | .connect_aliases.vhf = "ardop:///K0ABC" | .mycall = "K0XYZ" | .motd = ["hi"]
    | .hamlib_rigs.RADIO.VFO = "VFOB"' "$f" > "$TEST_TMP/edited" && cp "$TEST_TMP/edited" "$f"
  assert_ok app_pat_wire radio2
  assert_eq "$(pt_get .secure_login_password)" "s3cret-pw"
  assert_eq "$(pt_get .connect_aliases.vhf)" "ardop:///K0ABC"
  assert_eq "$(pt_get .mycall)" "K0XYZ"
  assert_eq "$(pt_get '.motd[0]')" "hi"
  assert_eq "$(pt_get .hamlib_rigs.RADIO.VFO)" "VFOB"
  assert_eq "$(stat -c %a "$f")" "600"
}
test_pat_vox_radio_has_no_ardop_rig_and_no_ptt_ctrl() {
  pt_env; pt_set_ptt radio2 vox
  app_pat_firstfill radio2; app_pat_wire radio2
  assert_eq "$(pt_get '.ardop | has("rig")')" "false"
  assert_eq "$(pt_get .ardop.ptt_ctrl)" "false"
  assert_eq "$(pt_get .hamlib_rigs.RADIO.address)" "127.0.0.1:4534"
  # CAT again: the rig comes back
  pt_set_ptt radio2 rigctld; app_pat_wire radio2
  assert_eq "$(pt_get .ardop.rig)" "RADIO"
  assert_eq "$(pt_get .ardop.ptt_ctrl)" "true"
}
test_pat_missing_file_asks_pat_for_its_defaults_once() {
  pt_env
  app_pat_firstfill radio2; app_pat_wire radio2
  assert_eq "$(grep -c '^pat --config .* mps list' "$TEST_TMP/calls")" "1"
}
test_pat_failing_pat_starts_from_an_empty_object() {
  pt_env; : > "$TEST_TMP/pat-fail"
  assert_ok app_pat_firstfill radio2 2> /dev/null
  assert_ok app_pat_wire radio2
  assert_eq "$(pt_get .mycall)" "W0BTE"
  assert_eq "$(pt_get .ardop.addr)" "localhost:8515"
  assert_eq "$(stat -c %a "$(app_pat_settings_file)")" "600"
  app_pat_firstfill radio2 2> "$TEST_TMP/err2"   # the file exists now: no more warnings
  assert_eq "$(wc -c < "$TEST_TMP/err2")" "0"
}
test_pat_failing_pat_warns_once_on_stderr() {
  pt_env; : > "$TEST_TMP/pat-fail"
  app_pat_firstfill radio2 2> "$TEST_TMP/err"
  assert_eq "$(grep -c . "$TEST_TMP/err")" "1"
  assert_contains "$(< "$TEST_TMP/err")" "defaults"
}
test_pat_unreadable_config_is_left_alone_and_fails() {
  pt_env
  local f; f=$(app_pat_settings_file); mkdir -p "${f%/*}"; echo 'not json' > "$f"
  assert_fails app_pat_wire radio2 2> /dev/null
  assert_eq "$(< "$f")" "not json"
}
test_pat_module_facts() {
  pt_env
  assert_eq "$(app_pat_label)" "Pat (Winlink)"
  assert_eq "$(app_pat_audio)" "alsa"
  assert_eq "$(app_pat_screen)" "no"
  assert_eq "$(app_pat_multi)" "no"
  assert_eq "$(app_pat_open)" '{"kind":"url","port":8081,"path":"/"}'
}
test_pat_open_passes_through_the_screen_step_intact() {
  pt_env
  assert_eq "$(dxb_screen_open pat)" '{"kind":"url","port":8081,"path":"/"}'
}
test_pat_command_is_a_fixed_script_with_the_device_from_the_environment() {
  pt_env
  local out; out=$(app_pat_command radio2 1)
  assert_eq "$(sed -n 1,2p <<< "$out" | tr '\n' ' ')" "bash -c "
  assert_eq "$(wc -l <<< "$out")" "3"
  # shellcheck disable=SC2016  # the script text is matched literally
  assert_contains "$out" '"$DXB_APPS_OPT/ardop/ardopcf" 8515 "$DEVICE" "$DEVICE" --logdir'
  assert_contains "$out" 'trap '
  assert_contains "$out" 'pat http'
  assert_not_contains "$out" 'plughw'
  assert_not_contains "$out" 'radio2'
}
test_pat_command_runs_ardop_beside_pat_and_stops_it_with_pat() {
  pt_env
  mkdir -p "$TEST_TMP/opt/dxberry-apps/ardop" "$TEST_TMP/stubs" "$TEST_TMP/home"
  printf '#!/bin/bash\necho "$*" > "%s/ardop-args"\necho $$ > "%s/ardop-pid"\nexec sleep 30\n' "$TEST_TMP" "$TEST_TMP" > "$DXB_APPS_OPT/ardop/ardopcf"
  printf '#!/bin/bash\necho "$*" > "%s/pat-args"\nsleep 0.5\nexit 0\n' "$TEST_TMP" > "$TEST_TMP/stubs/pat"
  chmod 755 "$DXB_APPS_OPT/ardop/ardopcf" "$TEST_TMP/stubs/pat"
  local -a argv
  mapfile -t argv < <(app_pat_command radio2 1)
  env PATH="$TEST_TMP/stubs:$PATH" HOME="$TEST_TMP/home" DEVICE='plughw:CARD=RADIO2,DEV=0' DXB_APPS_OPT="$DXB_APPS_OPT" "${argv[@]}"
  assert_eq "$?" "0"
  assert_eq "$(< "$TEST_TMP/ardop-args")" "8515 plughw:CARD=RADIO2,DEV=0 plughw:CARD=RADIO2,DEV=0 --logdir $TEST_TMP/home/.local/share/ardop"
  assert_eq "$(< "$TEST_TMP/pat-args")" "http"
  [[ -d $TEST_TMP/home/.local/share/ardop ]] || _fail "the ARDOP log directory was not created"
  command sleep 0.3
  if kill -0 "$(< "$TEST_TMP/ardop-pid")" 2> /dev/null; then _fail "ARDOP still running after Pat ended"; fi
  return 0
}
test_pat_command_term_stops_both() {
  pt_env
  mkdir -p "$TEST_TMP/opt/dxberry-apps/ardop" "$TEST_TMP/stubs" "$TEST_TMP/home"
  printf '#!/bin/bash\necho $$ > "%s/ardop-pid"\nexec sleep 30\n' "$TEST_TMP" > "$DXB_APPS_OPT/ardop/ardopcf"
  printf '#!/bin/bash\necho $$ > "%s/pat-pid"\nexec sleep 30\n' "$TEST_TMP" > "$TEST_TMP/stubs/pat"
  chmod 755 "$DXB_APPS_OPT/ardop/ardopcf" "$TEST_TMP/stubs/pat"
  local -a argv
  mapfile -t argv < <(app_pat_command radio2 1)
  env PATH="$TEST_TMP/stubs:$PATH" HOME="$TEST_TMP/home" DEVICE=x DXB_APPS_OPT="$DXB_APPS_OPT" "${argv[@]}" &
  local runner=$! i
  for i in {1..30}; do
    [[ -s $TEST_TMP/ardop-pid && -s $TEST_TMP/pat-pid ]] && break
    command sleep 0.1
  done
  command sleep 0.2
  kill -TERM "$runner"
  wait "$runner" 2> /dev/null
  command sleep 0.3
  if kill -0 "$(< "$TEST_TMP/ardop-pid")" 2> /dev/null; then _fail "ARDOP survived SIGTERM"; fi
  if kill -0 "$(< "$TEST_TMP/pat-pid")" 2> /dev/null; then _fail "Pat survived SIGTERM"; fi
  return 0
}
test_pat_install_extra_places_ardop_and_sets_up_the_proxy() {
  pt_env
  export DXB_NGINX_DIR=$TEST_TMP/etc/nginx DXB_POLICY_RC=$TEST_TMP/usr/sbin/policy-rc.d DXB_NGINX=pt_nginx \
    DXB_COCKPIT_CERTS=$TEST_TMP/etc/cockpit/ws-certs.d
  mkdir -p "$DXB_NGINX_DIR/sites-enabled" "$DXB_NGINX_DIR/sites-available" "${DXB_POLICY_RC%/*}" "$DXB_COCKPIT_CERTS"
  : > "$DXB_COCKPIT_CERTS/0-self-signed.cert"; : > "$DXB_COCKPIT_CERTS/0-self-signed.key"
  pt_nginx() { echo "nginx $*" >> "$TEST_TMP/calls"; }
  printf 'ardop-bytes' > "$TEST_TMP/http/ardopcf_arm_Linux_64"
  ap_release pflarue/ardop v1.0.4.1.3 ardopcf_arm_Linux_64
  assert_ok app_pat_install_extra
  assert_eq "$(< "$DXB_APPS_OPT/ardop/ardopcf")" "ardop-bytes"
  [[ -x $DXB_APPS_OPT/ardop/ardopcf ]] || _fail "ardopcf is not executable"
  assert_eq "$(< "$DXB_APPS_OPT/ardop/VERSION")" "1.0.4.1.3"
  [[ -L $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "the Pat site is not enabled"
  assert_ok app_pat_remove_extra
  [[ ! -e $DXB_APPS_OPT/ardop && ! -L $DXB_NGINX_DIR/sites-enabled/dxberry-pat.conf ]] || _fail "remove left files"
}
