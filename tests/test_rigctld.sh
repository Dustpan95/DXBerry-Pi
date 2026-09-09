#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

rig_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld \
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
  assert_eq "$(dxb_rigctld_env radio1 "$R_DIGIRIG")" $'MODEL=1\nPORT=4532\nRIG_ARGS=-r /dev/dxberry/radio1-cat -s 57600\nPTT_ARGS=-P RTS -p /dev/dxberry/radio1-cat'
  assert_eq "$(dxb_rigctld_env hf "$R_IC7300")" $'MODEL=3073\nPORT=4534\nRIG_ARGS=-r /dev/dxberry/hf-cat -s 115200\nPTT_ARGS=-P RIG'
  assert_eq "$(dxb_rigctld_env ht "$R_NOCAT")" $'MODEL=1\nPORT=4536\nRIG_ARGS=\nPTT_ARGS='
  assert_eq "$(dxb_rigctld_env sp "$R_SPLITPTT")" $'MODEL=1\nPORT=4538\nRIG_ARGS=-r /dev/dxberry/sp-cat -s 38400\nPTT_ARGS=-P DTR -p /dev/dxberry/sp-ptt'
}

test_rigctld_sync_starts_when_present_stops_when_absent() {
  rig_env
  assert_ok dxb_rigctld_sync radio1 "$R_DIGIRIG" 1
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/radio1.env" "PORT=4532"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
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
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart rigctld@radio1"
}

test_rigctld_query_and_failure() {
  rig_env
  assert_eq "$(dxb_rigctld_query 4532)" "145390000 FM"
  assert_contains "$(cat "$TEST_TMP/calls")" "rigctl -m 2 -r 127.0.0.1:4532 f m"
  # shellcheck disable=SC2317
  fake_rigctl() { return 2; }
  assert_eq "$(dxb_rigctld_query 4532)" "? ?"
}

test_rigctld_install_units_and_stop_stale() {
  rig_env
  assert_ok dxb_rigctld_install_units
  # shellcheck disable=SC2016  # checking for the literal, unexpanded $MODEL in the unit file
  assert_file_contains "$DXB_SYSTEMD_DIR/rigctld@.service" 'ExecStart=/usr/bin/rigctld -m $MODEL'
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-hotplug.service" "dxberry-radio hotplug"
  assert_file_contains "$DXB_TMPFILES_DIR/dxberry-radio.conf" "/run/dxberry/rigctld"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl daemon-reload"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-radio-hotplug.service"
  dxb_rigctld_install_units; assert_eq "$?" "1"
  dxb_rigctld_sync old "$R_NOCAT" 1 > /dev/null; dxb_rigctld_sync keep "$R_DIGIRIG" 1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_rigctld_stop_all_except keep
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@old"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "rigctld@keep"
  [[ -f $DXB_RIGCTLD_RUN_DIR/old.env ]] && _fail "stale env file not removed"
}
