#!/bin/bash
set -Eeuo pipefail
state=/run/dxb-ci
helper=/opt/dxberry/bin/dxberry-radio-event
service=dxb-ci-reconcile.service
path=dxb-ci-reconcile.path
timer=dxb-ci-reconcile.timer
export DXB_RUN_DIR=$state
finish() {
  local result=$?
  trap - EXIT
  journalctl --no-pager -u "$service" -u "$path" -u "$timer" || true
  systemctl show "$service" "$path" "$timer" -p Id -p ActiveState -p Result || true
  if (( result == 0 )); then
    printf '\nDXB_MANAGER_RESULT=PASS\n' > /dev/ttyS0
  else
    printf '\nDXB_MANAGER_RESULT=FAIL:%s\n' "$result" > /dev/ttyS0
  fi
  systemctl poweroff --no-block || true
  exit "$result"
}
trap finish EXIT
trap 'echo "Failed at line $LINENO: $BASH_COMMAND" >&2' ERR
wait_for() {
  local seconds=$1; shift
  local deadline=$((SECONDS+seconds))
  until "$@"; do
    (( SECONDS < deadline )) || { echo "Timed out: $*" >&2; return 1; }
    sleep .1
  done
}
empty_queue() { [[ ! -e $state/radio-events/marks/pending && ! -e $state/radio-events/marks/processing ]]; }
attempts() { wc -l < "$state/attempts"; }
started() { [[ -f $state/started ]]; }
limited() { [[ $(systemctl show "$path" -p Result --value) == unit-start-limit-hit && $(systemctl show "$service" -p ActiveState --value) == failed ]]; }
child_gone() { [[ ! -e /proc/$(cat "$state/child.pid") ]]; }
service_done() { [[ $(systemctl show "$service" -p ActiveState --value) == inactive ]]; }

[[ $(< /proc/1/comm) == systemd ]]
[[ $(stat -fc '%T' /sys/fs/cgroup) == cgroup2fs ]]
[[ $(systemctl show -p Version --value) != '' ]]
printf 'Guest manager: %s; kernel: %s\n' "$(systemctl show -p Version --value)" "$(uname -r)"
[[ $(cat "$state/boot-order") == $'hotplug\ngraywolf' ]]
sha256sum "$helper" /opt/dxberry/lib/common.sh /fixture/templates/* > "$state/source.before"
: > "$state/attempts"
echo ok > "$state/behavior"
for ((i=0;i<12;i++)); do "$helper" enqueue; done
systemctl start "$path" "$timer"
wait_for 20 empty_queue
wait_for 10 service_done
[[ $(attempts) == 1 ]]
echo 'PASS burst coalescing and initial boot ordering'

echo block > "$state/behavior"
"$helper" enqueue
wait_for 20 started
for ((i=0;i<4;i++)); do "$helper" enqueue; done
[[ -d $state/radio-events/marks/processing && -d $state/radio-events/marks/pending ]]
echo ok > "$state/behavior"
touch "$state/release"
wait_for 20 empty_queue
wait_for 10 service_done
[[ $(attempts) == 3 ]]
rm "$state/started" "$state/release"
echo 'PASS arrivals during apply retained and processed separately'

# Reset only before inducing the failure window. Recovery below must be automatic.
systemctl reset-failed "$service"
echo fail > "$state/behavior"
"$helper" enqueue
wait_for 80 limited
[[ -d $state/radio-events/marks/processing ]]
echo 'PASS actual service start limiting retains work'
echo ok > "$state/behavior"
wait_for 110 empty_queue
wait_for 10 service_done
[[ $(systemctl show "$service" -p Result --value) == success ]]
echo 'PASS independent calendar timer recovers start-limited work without reset'

# A failed path is explicitly reactivated, as an operator would do. This does
# not assert automatic path recovery or consumer retirement during tree swaps.
systemctl reset-failed "$path" "$service"
systemctl stop "$path" "$timer"
systemctl start "$path" "$timer"
previous=$(attempts)
"$helper" enqueue
wait_for 20 empty_queue
wait_for 10 service_done
[[ $(attempts) == $((previous+1)) ]]
echo 'PASS explicit path and timer reactivation'

systemctl stop "$path" "$timer"
echo child > "$state/behavior"
"$helper" enqueue
systemctl start --no-block "$service"
wait_for 20 started
[[ -f $state/child.pid ]]
child=$(cat "$state/child.pid")
[[ -r /proc/$child/cgroup ]]
grep -q '/dxb-ci-reconcile.service$' "/proc/$child/cgroup"
systemctl stop "$service"
wait_for 10 child_gone
[[ -d $state/radio-events/marks/processing ]]
rm "$state/started" "$state/child.pid" "$state/child.ready"
echo ok > "$state/behavior"
systemctl start "$path" "$timer"
wait_for 20 empty_queue
wait_for 10 service_done
echo 'PASS stop kills descendants, retains processing and permits retry'

systemctl stop "$path" "$timer"
echo orphan > "$state/behavior"
"$helper" enqueue
systemctl start "$service"
wait_for 10 child_gone
empty_queue
sha256sum -c "$state/source.before"
echo 'PASS successful oneshot removes surviving child and preserves production inputs'
bash /usr/local/libexec/dxb-update-roundtrip
echo 'NOT ACCEPTED: physical hardware, actual udev delivery or release swaps with an enabled queue'
