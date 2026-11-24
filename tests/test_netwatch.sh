#!/usr/bin/env bash
export DXB_NW_DEBOUNCE=0 DXB_NW_WIFI_GRACE=0 DXB_NW_RETRY=0 DXB_NW_POLL=1
# shellcheck disable=SC1091
source "$DXB_ROOT/provision/bin/dxberry-netwatch"

nw_fx_up() {
  local iface=${*: -1}
  echo "ifup $*" >> "$TEST_TMP/calls"
  # Real ifup does nothing for a stale configured entry unless --force is supplied.
  if [[ $1 != --force ]] && grep -qx "$iface" "$TEST_TMP/ifstate"; then return 0; fi
  # A static address left by a failed setup must be removed before it can be added again.
  if grep -qx "$iface" "$TEST_TMP/addresses"; then return 1; fi
  grep -qx "$iface" "$TEST_TMP/ifstate" || echo "$iface" >> "$TEST_TMP/ifstate"
  grep -qx "$iface" "$TEST_TMP/addresses" || echo "$iface" >> "$TEST_TMP/addresses"
}
nw_fx_down() {
  local iface=${*: -1} file
  echo "ifdown $*" >> "$TEST_TMP/calls"
  for file in ifstate addresses; do
    grep -vx "$iface" "$TEST_TMP/$file" > "$TEST_TMP/$file.n"
    mv "$TEST_TMP/$file.n" "$TEST_TMP/$file"
  done
}
nw_fx_ip() {
  local iface
  if [[ $* == '-o -4 addr show '* ]]; then
    while read -r iface; do
      if [[ $* == '-o -4 addr show up' || ${*: -1} == "$iface" ]]; then
        echo "2: $iface inet 10.0.0.90/24 scope global $iface"
      fi
    done < "$TEST_TMP/addresses"
  else echo "ip $*" >> "$TEST_TMP/calls"; fi
}

nw_env() {
  export DXB_SYS_NET=$TEST_TMP/sys DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_NW_SIM_FILE=$TEST_TMP/sim DXB_NW_STATE_FILE=$TEST_TMP/state
  SYS_NET=$DXB_SYS_NET; IFACES_DIR=$DXB_IFACES_DIR; SIM_FILE=$DXB_NW_SIM_FILE; STATE_FILE=$DXB_NW_STATE_FILE
  mkdir -p "$SYS_NET/eth0" "$SYS_NET/wlan0" "$IFACES_DIR"
  echo 1 > "$SYS_NET/eth0/carrier"; echo 1 > "$SYS_NET/wlan0/carrier"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/ifstate"; : > "$TEST_TMP/addresses"
  ifup() { nw_fx_up "$@"; }
  ifdown() { nw_fx_down "$@"; }
  ifquery() { grep -qx "$2" "$TEST_TMP/ifstate"; }
  ip() { nw_fx_ip "$@"; }
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

# rm -f fails (EISDIR here) when SIM_FILE is a directory: report the failure and exit non-zero
# rather than printing "simulation off" while the real carrier state is still being ignored.
test_simulate_off_reports_failure_when_sim_file_cannot_be_removed() {
  nw_env
  mkdir -p "$SIM_FILE"
  local out rc
  out=$(nw_simulate off 2> /dev/null); rc=$?
  assert_eq "$rc" "1"
  assert_not_contains "$out" "simulation off"
  assert_contains "$(nw_simulate off 2>&1 > /dev/null)" "could not remove $SIM_FILE"
  [[ -d $SIM_FILE ]] || _fail "the directory must still be there; rm -f must not have partially succeeded"
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

test_status_reports_live_holder() {
  nw_env
  echo WIFI > "$STATE_FILE"
  ip() { echo '2: eth0    inet 10.0.0.90/24 brd 10.0.0.255 scope global eth0'; }
  assert_eq "$(nw_status)" "WIFI (eth0 holds the address)"
}

test_status_without_holder_prints_state_only() {
  nw_env
  echo NONE > "$STATE_FILE"
  ip() { :; }
  assert_eq "$(nw_status)" "NONE"
}

test_startup_waits_for_ethernet_without_starting_wifi_first() (
  nw_env
  local ETH_GRACE=5 waited=0
  touch "$IFACES_DIR/wlan0.conf"
  echo 0 > "$SYS_NET/eth0/carrier"
  sleep() {
    [[ $1 == 1 ]] || _fail 'startup should wait in one-second intervals'
    waited=$((waited + 1))
    if (( waited == 2 )); then echo 1 > "$SYS_NET/eth0/carrier"; fi
  }
  nw_startup
  assert_eq "$waited" 2
  assert_eq "$NW_STATE" ETH
  assert_eq "$(calls)" 'ip link set eth0 up;ifup eth0;'
  assert_eq "$(< "$TEST_TMP/ifstate")" eth0
)

test_startup_ethernet_wait_is_bounded_and_can_be_skipped() (
  local ETH_GRACE=3 waited=0 scenario
  for scenario in unplugged missing disabled; do
    nw_env; waited=0; ETH_GRACE=3
    touch "$IFACES_DIR/wlan0.conf"
    echo 0 > "$SYS_NET/eth0/carrier"
    if [[ $scenario == missing ]]; then rm -rf "$SYS_NET/eth0"; fi
    if [[ $scenario == disabled ]]; then ETH_GRACE=0; fi
    sleep() { waited=$((waited + 1)); }
    nw_startup
    if [[ $scenario == unplugged ]]; then assert_eq "$waited" 3
    else assert_eq "$waited" 0; fi
    assert_eq "$NW_STATE" WIFI
    assert_eq "$(calls)" 'ip link set eth0 up;ifup wlan0;'
    assert_eq "$(< "$TEST_TMP/ifstate")" wlan0
  done
)

test_startup_adopts_each_existing_connection_without_waiting_or_cycling() (
  local ETH_GRACE=5 waited=0 iface expected
  for iface in eth0 wlan0; do
    nw_env; waited=0
    touch "$IFACES_DIR/wlan0.conf"
    echo "$iface" > "$TEST_TMP/ifstate"
    if [[ $iface == wlan0 ]]; then echo 0 > "$SYS_NET/eth0/carrier"; expected=WIFI
    else expected=ETH; fi
    sleep() { waited=$((waited + 1)); }
    nw_startup
    assert_eq "$waited" 0
    assert_eq "$NW_STATE" "$expected"
    assert_eq "$(calls)" 'ip link set eth0 up;'
    assert_eq "$(< "$TEST_TMP/ifstate")" "$iface"
  done
)

test_failover_blocked_teardown_retries_in_both_directions() (
  local old target old_state target_state blocked=1
  for old in eth0 wlan0; do
    nw_env; touch "$IFACES_DIR/wlan0.conf"; blocked=1
    if [[ $old == eth0 ]]; then
      target=wlan0; old_state=ETH; target_state=WIFI; echo 0 > "$SYS_NET/eth0/carrier"
    else target=eth0; old_state=WIFI; target_state=ETH; fi
    echo "$old" > "$TEST_TMP/ifstate"; echo "$old" > "$TEST_TMP/addresses"
    nw_set_state "$old_state"
    ifdown() {
      if (( blocked )); then echo "failed ifdown $*" >> "$TEST_TMP/calls"; return 1; fi
      nw_fx_down "$@"
    }
    nw_tick 1
    assert_eq "$NW_STATE" "$old_state"
    assert_eq "$(< "$STATE_FILE")" "$old_state"
    assert_eq "$(< "$TEST_TMP/addresses")" "$old"
    assert_file_not_contains "$TEST_TMP/calls" "ifup $target"
    blocked=0; : > "$TEST_TMP/calls"
    nw_tick 1
    assert_eq "$NW_STATE" "$target_state"
    assert_eq "$(< "$TEST_TMP/addresses")" "$target"
    assert_file_contains "$TEST_TMP/calls" "ifdown $old"
    assert_file_contains "$TEST_TMP/calls" "ifup $target"
  done
)

test_failover_forces_teardown_after_error_or_incomplete_success() (
  local mode
  for mode in error unchanged address-left; do
    nw_env; touch "$IFACES_DIR/wlan0.conf"; echo 0 > "$SYS_NET/eth0/carrier"
    echo eth0 > "$TEST_TMP/ifstate"; echo eth0 > "$TEST_TMP/addresses"; nw_set_state ETH
    ifdown() {
      if [[ $1 == --force ]]; then nw_fx_down "$@"; return; fi
      echo "ifdown $*" >> "$TEST_TMP/calls"
      case $mode in error) return 1 ;; address-left) : > "$TEST_TMP/ifstate" ;; esac
      return 0
    }
    nw_tick 1
    assert_eq "$NW_STATE" WIFI
    assert_eq "$(< "$TEST_TMP/addresses")" wlan0
    assert_file_contains "$TEST_TMP/calls" 'ifdown --force eth0'
  done
)

test_failover_unknown_address_state_blocks_setup_until_readback_recovers() (
  nw_env; touch "$IFACES_DIR/wlan0.conf"
  local unreadable=1
  echo wlan0 > "$TEST_TMP/ifstate"; echo wlan0 > "$TEST_TMP/addresses"; nw_set_state WIFI
  ip() {
    if [[ $* == '-o -4 addr show dev wlan0' ]] && (( unreadable )); then return 1; fi
    nw_fx_ip "$@"
  }
  nw_tick 1
  assert_eq "$NW_STATE" WIFI
  assert_file_not_contains "$TEST_TMP/calls" 'ifup eth0'
  unreadable=0
  nw_tick 1
  assert_eq "$NW_STATE" ETH
  assert_eq "$(< "$TEST_TMP/addresses")" eth0
)

test_failover_cleans_partial_setup_before_retry_or_another_interface() (
  local scenario attempts=0 RETRY=0
  for scenario in same other; do
    nw_env; touch "$IFACES_DIR/wlan0.conf"; echo 0 > "$SYS_NET/eth0/carrier"; attempts=0
    echo eth0 > "$TEST_TMP/ifstate"; echo eth0 > "$TEST_TMP/addresses"; nw_set_state ETH
    ifup() {
      attempts=$((attempts + 1))
      if (( attempts == 1 )); then
        echo "partial ifup $*" >> "$TEST_TMP/calls"
        echo wlan0 > "$TEST_TMP/addresses"
        if [[ $scenario == other ]]; then echo wlan0 > "$TEST_TMP/ifstate"; fi
        return 1
      fi
      nw_fx_up "$@"
    }
    nw_tick 1
    assert_eq "$NW_STATE" NONE
    : > "$TEST_TMP/calls"
    if [[ $scenario == other ]]; then echo 1 > "$SYS_NET/eth0/carrier"; fi
    nw_tick 1
    assert_file_contains "$TEST_TMP/calls" 'ifdown wlan0'
    if [[ $scenario == other ]]; then
      assert_eq "$NW_STATE" ETH; assert_eq "$(< "$TEST_TMP/addresses")" eth0
    else assert_eq "$NW_STATE" WIFI; assert_eq "$(< "$TEST_TMP/addresses")" wlan0; fi
  done
)

test_failover_missing_adapter_allows_fallback_and_stale_state_recovery() (
  nw_env; touch "$IFACES_DIR/wlan0.conf"
  echo eth0 > "$TEST_TMP/ifstate"; nw_set_state ETH
  rm -rf "$SYS_NET/eth0"
  ifdown() { if [[ ${*: -1} == eth0 ]]; then return 1; fi; nw_fx_down "$@"; }
  nw_tick 1
  assert_eq "$NW_STATE" WIFI
  assert_eq "$(< "$TEST_TMP/addresses")" wlan0
  mkdir -p "$SYS_NET/eth0"; echo 1 > "$SYS_NET/eth0/carrier"
  nw_tick 1
  assert_eq "$NW_STATE" ETH
  assert_eq "$(< "$TEST_TMP/addresses")" eth0
  assert_file_contains "$TEST_TMP/calls" 'ifup --force eth0'
)

test_startup_overlap_is_visible_and_repair_retries_without_cycling_the_keeper() (
  local carrier keep discard blocked=1
  for carrier in 0 1; do
    nw_env; touch "$IFACES_DIR/wlan0.conf"; blocked=1
    echo "$carrier" > "$SYS_NET/eth0/carrier"
    printf 'eth0\nwlan0\n' > "$TEST_TMP/ifstate"; cp "$TEST_TMP/ifstate" "$TEST_TMP/addresses"
    if (( carrier )); then keep=eth0; discard=wlan0; else keep=wlan0; discard=eth0; fi
    ifdown() {
      if (( blocked )); then echo "failed ifdown $*" >> "$TEST_TMP/calls"; return 1; fi
      nw_fx_down "$@"
    }
    nw_startup
    assert_contains "$(nw_status)" 'eth0, wlan0 hold addresses'
    assert_file_not_contains "$TEST_TMP/calls" 'ifup '
    blocked=0; : > "$TEST_TMP/calls"
    nw_tick 1
    assert_eq "$(< "$TEST_TMP/addresses")" "$keep"
    assert_file_contains "$TEST_TMP/calls" "ifdown $discard"
    assert_file_not_contains "$TEST_TMP/calls" 'ifup '
    if (( carrier )); then assert_eq "$NW_STATE" ETH; else assert_eq "$NW_STATE" WIFI; fi
  done
)

test_status_lists_overlapping_holders_once_even_with_multiple_addresses() (
  nw_env; nw_set_state ETH
  ip() {
    printf '%s\n' '2: eth0 inet 10.0.0.90/24' '2: eth0 inet 10.0.0.91/24' '3: wlan0 inet 10.0.0.90/24'
  }
  assert_eq "$(nw_status)" 'ETH (eth0, wlan0 hold addresses)'
)

test_watcher_term_cleans_its_monitor_and_preserves_network_helpers() (
  local watcher_pid='' monitor_pid='' helper_pid='' pid attempt
  # shellcheck disable=SC2317  # invoked by the exit trap
  cleanup() {
    for pid in "$watcher_pid" "$monitor_pid" "$helper_pid"; do
      [[ -n $pid ]] || continue
      kill "$pid" 2>/dev/null || true
    done
  }
  trap cleanup EXIT
  mkdir -p "$TEST_TMP/bin" "$TEST_TMP/sys/eth0" "$TEST_TMP/sys/wlan0" "$TEST_TMP/ifaces"
  echo 0 > "$TEST_TMP/sys/eth0/carrier"; echo 1 > "$TEST_TMP/sys/wlan0/carrier"
  touch "$TEST_TMP/ifaces/wlan0.conf"
  cat > "$TEST_TMP/bin/ip" <<'SH'
#!/bin/bash
if [[ $* == 'monitor link' ]]; then
  echo "$$" > "$DXB_FIXTURE_DIR/monitor.pid"
  exec sleep 600
fi
exit 0
SH
  cat > "$TEST_TMP/bin/ifquery" <<'SH'
#!/bin/bash
[[ $2 == wlan0 ]]
SH
  cat > "$TEST_TMP/bin/systemd-notify" <<'SH'
#!/bin/bash
# A daemon launched while configuring a link has an independent lifecycle.
sleep 600 >/dev/null 2>&1 &
echo "$!" > "$DXB_FIXTURE_DIR/helper.pid"
SH
  for cmd in ifup ifdown; do
    # shellcheck disable=SC2016  # the fixture expands this when it runs
    printf '#!/bin/bash\necho unexpected >> "$DXB_FIXTURE_DIR/actions"\nexit 1\n' > "$TEST_TMP/bin/$cmd"
  done
  chmod +x "$TEST_TMP/bin/"*
  env -i PATH="$TEST_TMP/bin:$PATH" DXB_FIXTURE_DIR="$TEST_TMP" \
    DXB_SYS_NET="$TEST_TMP/sys" DXB_IFACES_DIR="$TEST_TMP/ifaces" \
    DXB_NW_STATE_FILE="$TEST_TMP/state" DXB_NW_SIM_FILE="$TEST_TMP/sim" DXB_NW_POLL=30 \
    bash "$DXB_ROOT/provision/bin/dxberry-netwatch" run > "$TEST_TMP/watcher.log" 2>&1 &
  watcher_pid=$!
  for ((attempt=0; attempt<100; attempt++)); do
    [[ ! -s $TEST_TMP/monitor.pid || ! -s $TEST_TMP/helper.pid ]] || break
    command sleep 0.05
  done
  if [[ ! -s $TEST_TMP/monitor.pid || ! -s $TEST_TMP/helper.pid ]]; then _fail 'watcher did not become ready'; exit; fi
  monitor_pid=$(< "$TEST_TMP/monitor.pid"); helper_pid=$(< "$TEST_TMP/helper.pid")
  kill -TERM "$watcher_pid"
  for ((attempt=0; attempt<100; attempt++)); do
    kill -0 "$watcher_pid" 2>/dev/null || break
    command sleep 0.05
  done
  if kill -0 "$watcher_pid" 2>/dev/null; then _fail 'watcher did not stop after TERM'; kill -KILL "$watcher_pid"; fi
  wait "$watcher_pid"; watcher_pid=''
  if kill -0 "$monitor_pid" 2>/dev/null; then _fail 'link monitor leaked after watcher exit'; fi
  assert_ok kill -0 "$helper_pid"
  assert_eq "$(< "$TEST_TMP/state")" WIFI
  [[ ! -e $TEST_TMP/actions ]] || _fail 'adoption attempted an interface action'
)

test_monitor_cleanup_rejects_a_reused_process_identity() (
  local child
  command sleep 30 & child=$!
  trap 'kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true' EXIT
  NW_MONITOR_PID=$child
  NW_MONITOR_IDENTITY="$BASHPID incorrect-starttime"
  nw_cleanup_monitor
  assert_ok kill -0 "$child"
  assert_eq "$NW_MONITOR_PID" ''
)
