#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

rig_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_RUN_DIR=$TEST_TMP/run DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld \
    DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles DXB_RIGCTL=fake_rigctl
  mkdir -p "$DXB_STATE_DIR"; : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  # shellcheck disable=SC2317  # invoked indirectly through the sourced lib
  systemctl() { fx_systemctl "$@"; }
  # shellcheck disable=SC2317
  systemd-tmpfiles() { echo "systemd-tmpfiles $*" >> "$TEST_TMP/calls"; }
  # shellcheck disable=SC2317
  fake_rigctl() { echo "rigctl $*" >> "$TEST_TMP/calls"; printf '145390000\nFM\n15000\n'; }
  # shellcheck disable=SC2317
  timeout() { shift; "$@"; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
R_DIGIRIG='{"cat":{"path":"usb-0:1.4:1.0"},"ptt_serial":null,"rig":{"model":1,"baud":57600,"ptt_type":"RTS"},"rigctld_port":4532}'
R_IC7300='{"cat":{"path":"usb-0:1.2:1.2"},"ptt_serial":null,"rig":{"model":3073,"baud":115200,"ptt_type":"RIG"},"rigctld_port":4534}'
R_NOCAT='{"cat":null,"ptt_serial":null,"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4536}'
R_SPLITPTT='{"cat":{"path":"usb-0:1.2:1.0"},"ptt_serial":{"path":"usb-0:1.4:1.0"},"rig":{"model":1,"baud":38400,"ptt_type":"DTR"},"rigctld_port":4538}'

test_rigctld_env_shapes() {
  rig_env
  # Command-line assertions exclude inert binding metadata, checked separately.
  assert_eq "$(dxb_rigctld_env radio1 "$R_DIGIRIG" | sed '/^RIG_PINS_SHA256=/d')" $'MODEL=1\nPORT=4532\nRIG_ARGS=-r /dev/dxberry/radio1-cat -s 57600\nPTT_ARGS=-P RTS -p /dev/dxberry/radio1-cat'
  assert_eq "$(dxb_rigctld_env hf "$R_IC7300" | sed '/^RIG_PINS_SHA256=/d')" $'MODEL=3073\nPORT=4534\nRIG_ARGS=-r /dev/dxberry/hf-cat -s 115200\nPTT_ARGS=-P RIG'
  assert_eq "$(dxb_rigctld_env ht "$R_NOCAT" | sed '/^RIG_PINS_SHA256=/d')" $'MODEL=1\nPORT=4536\nRIG_ARGS=\nPTT_ARGS='
  assert_contains "$(dxb_rigctld_env ht "$R_NOCAT")" "MODEL=1"   # a cat-less radio runs the dummy model (spec 7.3)
  assert_eq "$(dxb_rigctld_env sp "$R_SPLITPTT" | sed '/^RIG_PINS_SHA256=/d')" $'MODEL=1\nPORT=4538\nRIG_ARGS=-r /dev/dxberry/sp-cat -s 38400\nPTT_ARGS=-P DTR -p /dev/dxberry/sp-ptt'
}

test_rigctld_sync_starts_when_present_stops_when_absent() {
  rig_env
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 1
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/radio1.env" "PORT=4532"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start rigctld@radio1"
  assert_eq "$(dxb_rigctld_state radio1)" "active"
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"      # already active, env unchanged
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 0
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_eq "$(dxb_rigctld_state radio1)" "inactive"
}

test_rigctld_sync_restarts_on_env_change() {
  rig_env
  dxb_rigctld_sync radio1 "$R_DIGIRIG" 1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$(jq -c '.rig.baud = 9600' <<< "$R_DIGIRIG")" 1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block restart rigctld@radio1"
}

test_rigctld_sync_restarts_when_a_used_pin_changes_under_the_same_stable_name() {
  rig_env
  local original changed kind
  for kind in cat ptt hid; do
    case $kind in
      cat) original=$R_IC7300; changed=$(jq -c '.cat.path = "usb-0:1.5:1.0"' <<< "$original") ;;
      ptt) original=$R_SPLITPTT; changed=$(jq -c '.ptt_serial.path = "usb-0:1.6:1.0"' <<< "$original") ;;
      hid)
        original=$(jq -c '.ptt.method = "cm108" | .hid = {path:"usb-0:1.7:1.3"}' <<< "$R_NOCAT")
        changed=$(jq -c '.hid.path = "usb-0:1.8:1.3"' <<< "$original") ;;
    esac
    assert_ok dxb_rigctld_sync "r$kind" "$original" 1 session
    : > "$TEST_TMP/calls"
    assert_ok dxb_rigctld_sync "r$kind" "$changed" 1 session
    assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block restart rigctld@r$kind.service"
    : > "$TEST_TMP/calls"
    assert_ok dxb_rigctld_sync "r$kind" "$changed" 1 session
    assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
  done
}

test_rigctld_sync_retries_a_rejected_restart_with_the_same_desired_env() {
  rig_env
  local changed
  assert_ok dxb_rigctld_sync radio1 "$R_IC7300" 1
  changed=$(jq -c '.rig.baud = 9600' <<< "$R_IC7300")
  : > "$TEST_TMP/reject-restart"
  # shellcheck disable=SC2317
  systemctl() {
    if [[ $* == '--no-block restart rigctld@radio1.service' && -f $TEST_TMP/reject-restart ]]; then
      echo "systemctl $*" >> "$TEST_TMP/calls"
      return 1
    fi
    fx_systemctl "$@"
  }
  dxb_rigctld_sync radio1 "$changed" 1 > /dev/null 2>&1
  assert_eq "$?" "6"
  [[ -f $DXB_RIGCTLD_RUN_DIR/radio1.restart-pending ]] || _fail "rejected restart lost its intent"
  rm -f "$TEST_TMP/reject-restart"
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$changed" 1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block restart rigctld@radio1.service"
  [[ ! -e $DXB_RIGCTLD_RUN_DIR/radio1.restart-pending ]] || _fail "accepted request retained its intent"
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$changed" 1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
}

test_rigctld_binding_ignores_unused_metadata_and_canonicalizes_pin_keys() {
  rig_env
  local r hash
  assert_ok dxb_rigctld_sync radio1 "$R_IC7300" 1
  hash=$(sed -n 's/^RIG_PINS_SHA256=//p' "$DXB_RIGCTLD_RUN_DIR/radio1.env")
  [[ $hash =~ ^[0-9a-f]{64}$ ]] || _fail "missing pin binding digest"
  r=$(jq -cS '.label="renamed" | .owner="wsjtx" | .audio={path:"usb-0:1.9:1.0"} |
    .cat={serial:null,path:.cat.path,vidpid:null} | .hid={path:"unused"} | .ptt_serial={path:"unused"}' <<< "$R_IC7300")
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$r" 1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
  assert_eq "$(sed -n 's/^RIG_PINS_SHA256=//p' "$DXB_RIGCTLD_RUN_DIR/radio1.env")" "$hash"
  r=$(jq -c '.cat.serial="replacement"' <<< "$r")
  : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_sync radio1 "$r" 1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block restart rigctld@radio1.service"
}

test_rigctld_binding_ignores_the_cat_pin_owned_by_flrig() {
  rig_env
  local r
  r=$(jq -c '.rig.control="flrig"' <<< "$R_IC7300")
  assert_ok dxb_rigctld_sync radio1 "$r" 1
  : > "$TEST_TMP/calls"
  r=$(jq -c '.cat.path="usb-0:1.9:1.0"' <<< "$r")
  assert_ok dxb_rigctld_sync radio1 "$r" 1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
}

test_rigctld_env_publication_failure_keeps_the_old_env_and_retry_intent() {
  rig_env
  local old changed
  assert_ok dxb_rigctld_sync radio1 "$R_IC7300" 1
  old=$(< "$DXB_RIGCTLD_RUN_DIR/radio1.env")
  changed=$(jq -c '.rig.baud=9600' <<< "$R_IC7300")
  mkdir "$DXB_RIGCTLD_RUN_DIR/radio1.env.dxbtmp.$$"
  : > "$TEST_TMP/calls"
  dxb_rigctld_sync radio1 "$changed" 1 > /dev/null 2>&1
  assert_eq "$?" "6"
  assert_eq "$(< "$DXB_RIGCTLD_RUN_DIR/radio1.env")" "$old"
  [[ -f $DXB_RIGCTLD_RUN_DIR/radio1.restart-pending ]] || _fail "publication failure lost retry intent"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
  rm -rf "$DXB_RIGCTLD_RUN_DIR/radio1.env.dxbtmp.$$"
  assert_ok dxb_rigctld_sync radio1 "$changed" 1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block restart rigctld@radio1.service"
}

test_rigctld_failed_binding_derivation_does_not_publish_or_control_services() {
  rig_env
  local old
  assert_ok dxb_rigctld_sync radio1 "$R_IC7300" 1
  old=$(< "$DXB_RIGCTLD_RUN_DIR/radio1.env")
  : > "$TEST_TMP/calls"
  if (
    # shellcheck disable=SC2317
    jq() { return 1; }
    dxb_rigctld_sync radio1 "$R_IC7300" 1
  ) > /dev/null 2>&1; then _fail "unavailable binding derivation succeeded"; fi
  assert_eq "$(< "$DXB_RIGCTLD_RUN_DIR/radio1.env")" "$old"
  assert_eq "$(cat "$TEST_TMP/calls")" ""
  [[ ! -e $DXB_RIGCTLD_RUN_DIR/radio1.restart-pending ]] || _fail "failed derivation published an intent"
}

test_rigctld_pending_clear_refuses_a_different_digest() {
  rig_env
  mkdir -p "$DXB_RIGCTLD_RUN_DIR"
  printf '%064d\n' 1 > "$DXB_RIGCTLD_RUN_DIR/radio1.restart-pending"
  _dxb_rigctld_pending_done "$DXB_RIGCTLD_RUN_DIR/radio1.restart-pending" "$(printf '%064d' 2)" > /dev/null 2>&1
  assert_eq "$?" "6"
  assert_eq "$(< "$DXB_RIGCTLD_RUN_DIR/radio1.restart-pending")" "$(printf '%064d' 1)"
}

test_rigctld_query_and_failure() {
  rig_env
  assert_eq "$(dxb_rigctld_query 4532)" "145390000 FM"
  assert_contains "$(cat "$TEST_TMP/calls")" "rigctl -m 2 -r 127.0.0.1:4532 f m"
  # shellcheck disable=SC2317
  fake_rigctl() { return 2; }
  assert_eq "$(dxb_rigctld_query 4532)" "? ?"
}

test_rigctld_stands_alone_with_templates_default() {
  rig_env
  local out
  # shellcheck disable=SC2016  # single-quoted on purpose: expands inside the child bash -c, not here
  out=$(env -u DXB_TEMPLATES bash -c 'set -u; source "$1/common.sh"; source "$1/rigctld.sh"; echo "$DXB_TEMPLATES"' _ "$DXB_LIB")
  assert_eq "$out" "/opt/dxberry/templates"
}

test_rigctld_install_units_and_stop_stale() {
  rig_env
  assert_ok dxb_rigctld_install_units
  # shellcheck disable=SC2016  # checking for the literal, unexpanded $MODEL in the unit file
  assert_file_contains "$DXB_SYSTEMD_DIR/rigctld@.service" 'ExecStart=/usr/bin/rigctld -m $MODEL'
  assert_file_contains "$DXB_SYSTEMD_DIR/rigctld@.service" 'Before=dxberryd.service'
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-hotplug.service" "dxberry-radio hotplug"
  assert_file_contains "$DXB_TMPFILES_DIR/dxberry-radio.conf" "/run/dxberry/rigctld"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl daemon-reload"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  dxb_rigctld_install_units; assert_eq "$?" "1"
  dxb_rigctld_sync old "$R_NOCAT" 1 > /dev/null; dxb_rigctld_sync keep "$R_DIGIRIG" 1 > /dev/null; : > "$TEST_TMP/calls"
  printf pending > "$DXB_RIGCTLD_RUN_DIR/old.restart-pending"
  printf pending > "$DXB_RIGCTLD_RUN_DIR/orphan.restart-pending"
  assert_ok dxb_rigctld_stop_all_except keep
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@old"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "rigctld@keep"
  [[ -f $DXB_RIGCTLD_RUN_DIR/old.env ]] && _fail "stale env file not removed"
  [[ ! -e $DXB_RIGCTLD_RUN_DIR/old.restart-pending && ! -e $DXB_RIGCTLD_RUN_DIR/orphan.restart-pending ]] || _fail "stale restart intent not removed"
}

test_rigctld_install_units_fails_when_the_tmpfiles_conf_cannot_be_written() {
  rig_env
  : > "$TEST_TMP/blocker"
  DXB_TMPFILES_DIR=$TEST_TMP/blocker                 # the parent is a file: the write cannot land
  dxb_rigctld_install_units 2> /dev/null; assert_eq "$?" "6"
  [[ -f $DXB_TMPFILES_DIR/dxberry-radio.conf ]] && _fail "the tmpfiles conf cannot exist here"
}

test_rigctld_env_keys_cm108_and_gpio_for_session_owners_only() {
  local r='{"cat":null,"ptt_serial":null,"hid":{"path":"usb-0:1.3:1.3"},"ptt":{"method":"cm108","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4534}'
  assert_contains "$(dxb_rigctld_env r2 "$r" session)" 'PTT_ARGS=-P CM108 -p /dev/dxberry/r2-hid'
  assert_contains "$(dxb_rigctld_env r2 "$r" graywolf)"$'\n' 'PTT_ARGS='$'\n'
  assert_eq "$(dxb_rigctld_env r2 "$r")" "$(dxb_rigctld_env r2 "$r" graywolf)"
  r=$(jq -c '.ptt = {method: "gpio", gpio_line: 17}' <<< "$r")
  assert_contains "$(dxb_rigctld_env r2 "$r" session)" 'PTT_ARGS=-P GPIO -p 17'
}

# rigctld keys only a pin it was told: a GPIO radio with no line set never keys GPIO 0, a CM108
# radio with no HID pinned (no /dev/dxberry/NAME-hid) is never keyed through it; both warn
test_rigctld_env_never_keys_a_ptt_pin_it_was_not_given() {
  rig_env
  local r='{"cat":null,"ptt_serial":null,"hid":null,"ptt":{"method":"cm108","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4534}'
  assert_eq "$(dxb_rigctld_env r2 "$r" session 2> "$TEST_TMP/err" | grep '^PTT_ARGS=')" "PTT_ARGS="
  assert_contains "$(cat "$TEST_TMP/err")" "radio r2: its PTT is the sound card's CM108 pin, but no HID is pinned for it; PTT is left off"
  r=$(jq -c '.ptt = {method: "gpio", gpio_line: null}' <<< "$r")
  assert_eq "$(dxb_rigctld_env r2 "$r" session 2> "$TEST_TMP/err" | grep '^PTT_ARGS=')" "PTT_ARGS="
  assert_contains "$(cat "$TEST_TMP/err")" "radio r2: its PTT is a Pi GPIO pin, but no GPIO line is set; PTT is left off"
  r=$(jq -c '.ptt.gpio_line = 0' <<< "$r")
  assert_eq "$(dxb_rigctld_env r2 "$r" session 2> /dev/null | grep '^PTT_ARGS=')" "PTT_ARGS=-P GPIO -p 0"
}
