#!/usr/bin/env bash
export DXB_NW_DEBOUNCE=0 DXB_NW_WIFI_GRACE=0 DXB_NW_RETRY=0 DXB_NW_POLL=1
# shellcheck disable=SC1091
source "$DXB_ROOT/provision/bin/dxberry-netwatch"

nw_env() {
  export DXB_SYS_NET=$TEST_TMP/sys DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_NW_SIM_FILE=$TEST_TMP/sim DXB_NW_STATE_FILE=$TEST_TMP/state
  SYS_NET=$DXB_SYS_NET; IFACES_DIR=$DXB_IFACES_DIR; SIM_FILE=$DXB_NW_SIM_FILE; STATE_FILE=$DXB_NW_STATE_FILE
  mkdir -p "$SYS_NET/eth0" "$SYS_NET/wlan0" "$IFACES_DIR"
  echo 1 > "$SYS_NET/eth0/carrier"; echo 1 > "$SYS_NET/wlan0/carrier"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/ifstate"
  ifup() { echo "ifup $1" >> "$TEST_TMP/calls"; echo "$1" >> "$TEST_TMP/ifstate"; }
  ifdown() { echo "ifdown $1" >> "$TEST_TMP/calls"; grep -vx "$1" "$TEST_TMP/ifstate" > "$TEST_TMP/ifstate.n"; mv "$TEST_TMP/ifstate.n" "$TEST_TMP/ifstate"; }
  ifquery() { grep -qx "$2" "$TEST_TMP/ifstate"; }
  ip() { echo "ip $*" >> "$TEST_TMP/calls"; }
  sleep() { :; }
  NW_STATE=NONE; NW_WIFI_SINCE=0; NW_LAST_RETRY=0
}
calls() { tr '\n' ';' < "$TEST_TMP/calls"; }

test_decide_truth_table() {
  assert_eq "$(nw_decide ETH 1 1 1)" "ETH"
  assert_eq "$(nw_decide ETH 0 1 0)" "WIFI down_eth up_wifi"
  assert_eq "$(nw_decide ETH 0 0 0)" "NONE down_eth"
  assert_eq "$(nw_decide WIFI 1 1 1)" "ETH down_wifi up_eth"
  assert_eq "$(nw_decide WIFI 0 1 1)" "WIFI"
  assert_eq "$(nw_decide WIFI 0 1 0)" "NONE down_wifi"
  assert_eq "$(nw_decide NONE 1 0 0)" "ETH up_eth"
  assert_eq "$(nw_decide NONE 0 1 0)" "WIFI up_wifi"
  assert_eq "$(nw_decide NONE 0 0 0)" "NONE"
}

test_carrier_reads_sysfs_and_simulation() {
  nw_env
  assert_eq "$(nw_carrier eth0)" "1"
  echo 0 > "$SYS_NET/eth0/carrier"; assert_eq "$(nw_carrier eth0)" "0"
  rm "$SYS_NET/eth0/carrier"; assert_eq "$(nw_carrier eth0)" "0"
  echo 1 > "$SIM_FILE"; assert_eq "$(nw_carrier eth0)" "1"
  echo 0 > "$SIM_FILE"; assert_eq "$(nw_carrier wlan0)" "1"
}

test_failover_sequence_never_overlaps() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  nw_startup
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ip link set eth0 up;ifup eth0;"
  echo 0 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" "ifdown eth0;ip link set eth0 up;ifup wlan0;"
  echo 1 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ifdown wlan0;ifup eth0;"
  assert_eq "$(cat "$STATE_FILE")" "ETH"
}

test_no_wifi_goes_to_none_and_back() {
  nw_env
  nw_startup
  echo 0 > "$SYS_NET/eth0/carrier"; nw_tick 0
  assert_eq "$NW_STATE" "NONE"
  echo 1 > "$SYS_NET/eth0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ifup eth0;"
}

test_startup_adopts_existing_state_without_cycling() {
  nw_env
  echo eth0 >> "$TEST_TMP/ifstate"
  nw_startup
  assert_eq "$NW_STATE" "ETH"
  assert_eq "$(calls)" "ip link set eth0 up;"
}

test_wifi_association_loss_returns_to_none_then_retries() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  echo 0 > "$SYS_NET/eth0/carrier"
  nw_startup
  assert_eq "$NW_STATE" "WIFI"
  echo 0 > "$SYS_NET/wlan0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "NONE"
  assert_eq "$(calls)" "ifdown wlan0;"
  echo 1 > "$SYS_NET/wlan0/carrier"; : > "$TEST_TMP/calls"; nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" "ifup wlan0;"
}

test_simulate_writes_and_clears_file() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  nw_simulate eth0-down > /dev/null; assert_eq "$(cat "$SIM_FILE")" "0"
  nw_simulate eth0-up > /dev/null; assert_eq "$(cat "$SIM_FILE")" "1"
  nw_simulate off > /dev/null; [[ -f $SIM_FILE ]] && _fail "sim file should be removed"
  assert_fails nw_simulate bogus 2> /dev/null
}

# On an Ethernet-only Pi, "simulate the cable coming out" takes the address away with nothing to
# fail over to - a guaranteed lockout, so it has to be asked for explicitly.
test_simulate_eth0_down_refuses_without_wifi_unless_forced() {
  nw_env
  assert_fails nw_simulate eth0-down 2> /dev/null
  [[ -f $SIM_FILE ]] && _fail "a refused simulation must not write the sim file"
  local out; out=$(nw_simulate eth0-down 2>&1)
  assert_contains "$out" "refusing"
  assert_contains "$out" "would drop this Pi's only address"
  assert_not_contains "$out" "simulating:"
  # --force is the documented escape hatch for someone at the console, in either argument order.
  out=$(nw_simulate eth0-down --force)
  assert_contains "$out" "simulating: eth0 carrier lost"
  assert_eq "$(cat "$SIM_FILE")" "0"
  rm -f "$SIM_FILE"
  out=$(nw_simulate --force eth0-down)
  assert_eq "$(cat "$SIM_FILE")" "0"
  # eth0-up never needs --force: it only ever restores the primary interface.
  rm -f "$SIM_FILE"
  assert_ok nw_simulate eth0-up > /dev/null
  assert_eq "$(cat "$SIM_FILE")" "1"
}

# An unwritable sim file means the daemon will keep reading the real carrier: say so, and do not
# print a "simulating:" line the operator would then trust.
test_simulate_reports_a_failed_write() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  export DXB_NW_SIM_FILE=$TEST_TMP/no-such-dir/sim
  SIM_FILE=$DXB_NW_SIM_FILE
  local out rc
  out=$(nw_simulate eth0-down 2> /dev/null); rc=$?
  assert_eq "$rc" "1"
  assert_not_contains "$out" "simulating:"
  assert_contains "$(nw_simulate eth0-down 2>&1 > /dev/null)" "could not write $SIM_FILE"
  out=$(nw_simulate eth0-up 2> /dev/null); rc=$?
  assert_eq "$rc" "1"
  assert_not_contains "$out" "simulating:"
}

# A failed ifup eth0 (e.g. a DHCP timeout while carrier is still present) must not wedge
# the daemon in a false ETH state, and must not be retried hotter than DXB_NW_RETRY.
test_failed_ifup_eth_does_not_wedge_or_hot_retry() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  RETRY=5
  ifup() {
    echo "ifup $1" >> "$TEST_TMP/calls"
    if [[ $1 == "$ETH" ]]; then return 1; fi
    echo "$1" >> "$TEST_TMP/ifstate"
  }
  nw_startup
  assert_eq "$NW_STATE" "NONE"
  # Fake the clock forward so the retry gate is armed and blocks an immediate re-attempt.
  NW_LAST_RETRY=9999999999
  : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "NONE"
  assert_eq "$(calls)" ""
  # Fake the clock past the retry window; eth0's cable is also now gone, so failover to WIFI.
  NW_LAST_RETRY=0
  echo 0 > "$SYS_NET/eth0/carrier"
  nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" "ifup wlan0;"
  RETRY=0
}

# A one-tick wlan0 carrier blip that resolves itself during the debounce window must not
# tear wlan0 down.
test_wifi_debounce_absorbs_a_brief_carrier_blip() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  echo 0 > "$SYS_NET/eth0/carrier"
  nw_startup
  assert_eq "$NW_STATE" "WIFI"
  echo 0 > "$SYS_NET/wlan0/carrier"
  sleep() { echo 1 > "$SYS_NET/wlan0/carrier"; }
  : > "$TEST_TMP/calls"
  nw_tick 0
  assert_eq "$NW_STATE" "WIFI"
  assert_eq "$(calls)" ""
}

# ifupdown reporting both interfaces configured at startup (a stale prior run, a manual
# ifup) must be healed back to the single-interface invariant, not just adopted as ETH.
test_startup_with_both_configured_heals_to_eth_only() {
  nw_env
  touch "$IFACES_DIR/wlan0.conf"
  echo eth0 >> "$TEST_TMP/ifstate"
  echo wlan0 >> "$TEST_TMP/ifstate"
  nw_startup
  assert_eq "$NW_STATE" "ETH"
  assert_contains "$(calls)" "ifdown wlan0"
}
