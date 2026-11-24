#!/bin/bash
set -eu
state=/run/dxb-ci
[[ $# == 1 && $1 == hotplug ]]
for fd in /proc/self/fd/*; do
  [[ $(readlink "$fd" 2>/dev/null || true) != "$state/radio-events/worker.lock" ]]
done
if flock -n "$state/radio-events/worker.lock" true; then exit 91; fi
printf '%s\n' "$BASHPID" >> "$state/attempts"
case $(cat "$state/behavior") in
  ok) ;;
  fail) exit 7 ;;
  block) touch "$state/started"; while [[ ! -e $state/release ]]; do sleep .05; done ;;
  child|orphan)
    bash -c 'echo "$BASHPID" > /run/dxb-ci/child.pid; touch /run/dxb-ci/child.ready; while :; do sleep 1; done' &
    until [[ -f $state/child.ready ]]; do sleep .05; done
    [[ $(cat "$state/behavior") == orphan ]] || { touch "$state/started"; while :; do sleep 1; done; }
    ;;
  *) exit 92 ;;
esac
