#!/usr/bin/env bash
# The manager model is explicit: this is not a real systemd acceptance test.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

test_radio_lock_order_claim_leaves_hotplug_prerequisite_available() (
  export DXB_STATE_DIR=$TEST_TMP/state DXB_RUN_DIR=$TEST_TMP/run DXB_LOG_FILE=$TEST_TMP/log
  export DXB_FLOCK=flock DXB_RADIO_APPLY_LOCK_WAIT=0 DXB_SESSION_WAIT_S=0
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR"
  (
    source "$DXB_ROOT/provision/bin/dxberry-radio"
    dxb_require_root() { :; }
    load_config_if_any() { :; }
    dxb_radio_load() { :; }
    dxb_radio_scan_cache() { :; }
    # Reach the real session-start wait through the actual parent CLI boundary.
    dxb_radio_claim() { dxb_app_session_start "$2" "$1"; }
    # dxberry-screen@ is ordered After=dxberry-radio-hotplug.service. Model
    # activation only after its prerequisite can finish the real apply wrapper;
    # derived device/service work is a marker, so no hardware is accessed.
    systemctl() {
      case $1 in
        is-active)
          (
            _dxb_radio_apply() { : > "$TEST_TMP/hotplug-finished"; }
            dxb_radio_apply hotplug
          ) ;;
        *) return 0 ;;
      esac
    }
    main claim radio1 alpha
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 0
  [[ -e $TEST_TMP/hotplug-finished ]] || _fail 'claim blocked the hotplug prerequisite of its session'
  assert_ok command flock -n "$DXB_RUN_DIR/apply.lock" true
)
