#!/bin/bash
set -eu
# A real owned child makes the mixed-stop refusal case deterministic. It is
# fixture workload, not a substitute application or process metadata source.
if [[ ${DXB_WSJTX_RETAIN_CHILD:-0} == 1 ]]; then
  sleep 300 &
  printf '%s\n' "$!" > "$XDG_RUNTIME_DIR/held-child.pid"
fi
exec /usr/bin/wsjtx --rig-name=fixture1
