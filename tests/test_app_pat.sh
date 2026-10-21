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
test_pat_command_is_pat_alone_on_loopback() {
  pt_env
  assert_eq "$(app_pat_command radio2 1 | tr '\n' ' ')" "pat http --addr 127.0.0.1:8180 "
}
test_pat_companions_lists_the_ardop_unit() {
  pt_env
  assert_eq "$(app_pat_companions radio2)" "dxberry-ardop@pat-radio2.service"
}
test_pat_ardop_unit_is_loopback_only_and_logs_to_the_journal() {
  local u=$DXB_ROOT/provision/templates/dxberry-ardop@.service
  assert_file_contains "$u" "IPAddressDeny=any"
  assert_file_contains "$u" "IPAddressAllow=localhost"
  assert_file_contains "$u" "ExecStart=/opt/dxberry-apps/ardop/ardopcf -m 8515 \${DEVICE} \${DEVICE}"
  assert_file_contains "$u" "EnvironmentFile=/run/dxberry/sessions/%i.env"
  assert_file_contains "$u" "User=dxbapps"
  assert_file_contains "$u" "SupplementaryGroups=audio"
  assert_file_contains "$u" "Restart=on-failure"
  assert_file_contains "$u" "StartLimitIntervalSec=60"
  assert_file_contains "$u" "StartLimitBurst=3"
  assert_file_contains "$u" "StopPropagatedFrom=dxberry-screen@%i.service"
  assert_file_not_contains "$u" "PartOf="
}
test_pat_failing_jq_leaves_the_config_byte_identical() {
  pt_env
  local f; f=$(app_pat_settings_file); mkdir -p "${f%/*}"; printf '{"a":1} x' > "$f"; cp "$f" "$TEST_TMP/before"
  assert_eq "$(app_pat_wire radio2 > /dev/null 2>&1; echo $?)" "6"
  assert_eq "$(cmp "$TEST_TMP/before" "$f" && echo same)" "same"
  assert_eq "$(find "${f%/*}" -type f | wc -l)" "1"
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

# the optional companions hook in the session steps, shown with gamma (test_screen.sh's fake
# program) given Pat's own hook
pt_companion_env() {
  sc_env; sc_owned radio2 gamma
  # shellcheck disable=SC2016  # the hook text is written literally
  printf 'app_gamma_companions() { printf "dxberry-ardop@gamma-%%s.service\\n" "$1"; }\n' >> "$DXB_APPS_DIR/gamma.sh"
  unset FX_SYSTEMCTL_FAIL_START
}
test_pat_prepare_starts_the_companions_and_stopped_stops_them() {
  pt_companion_env
  assert_ok sc_app prepare gamma-radio2
  assert_file_contains "$TEST_TMP/calls" "systemctl start dxberry-ardop@gamma-radio2.service"
  [[ -s $DXB_SESSIONS_DIR/gamma-radio2.env ]] || _fail "the env file the companion reads was not written"
  assert_ok sc_app stopped gamma-radio2
  assert_file_contains "$TEST_TMP/calls" "systemctl stop dxberry-ardop@gamma-radio2.service"
}
test_pat_a_companion_that_fails_to_start_is_only_a_warning() {
  pt_companion_env; export FX_SYSTEMCTL_FAIL_START=dxberry-ardop@gamma-radio2.service
  assert_ok sc_app prepare gamma-radio2
  unset FX_SYSTEMCTL_FAIL_START
}
test_pat_a_program_without_companions_starts_none() {
  sc_env; sc_owned radio2 gamma
  assert_ok sc_app prepare gamma-radio2
  assert_file_not_contains "$TEST_TMP/calls" "systemctl start dxberry-ardop"
}
