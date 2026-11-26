#!/bin/bash
set -euo pipefail
# Each independent diagnostic has fresh process, IPC and temporary state.
for case_number in {1..20}; do
  printf 'Fresh WSJT-X debugger diagnostic %d/20\n' "$case_number"
  docker run --rm --network none --ipc=private --cap-drop ALL --read-only \
    --security-opt=no-new-privileges --pids-limit=512 --memory=3g \
    --user 65534:65534 --shm-size=128m \
    --tmpfs /tmp:rw,exec,nosuid,nodev,size=512m,mode=1777 \
    --env DXB_WSJTX_DEBUGGER=1 dxberry-wsjtx-debug \
    -test.v -test.timeout=150s -test.count=1 -test.run '^TestWSJTXApplicationCompatibility$'
done
