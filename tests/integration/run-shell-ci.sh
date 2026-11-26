#!/bin/bash
# Root-only fixture metadata belongs in a disposable container, never its host.
set -euo pipefail
[[ $EUID == 0 ]]
git -c safe.directory=/source -C /source archive HEAD | tar -x -C /repo
cp -R /source/.git /repo/.git
cd /repo
if git ls-files | grep -qxE 'boot/dxberry\.txt|boot/dietpi-wifi\.txt'; then exit 1; fi
if git grep -qIiE 'co-?auth[o]red-?by|cla[u]de|anthr[o]pic|cod[e]x|op[e]nai|subag[e]nt|ag[e]ntic' -- .; then exit 1; fi
mapfile -t bins < <(grep -lE '^#!/bin/(ba)?sh' provision/bin/*)
files=("${bins[@]}" provision/lib/*.sh provision/lib/apps/*.sh build/*.sh boot/*.sh tests/*.sh tests/fixtures/*.sh tests/integration/*.sh tests/integration/systemd/init tests/integration/systemd/*.sh tests/integration/pat-manager/init tests/integration/pat-manager/*.sh tests/integration/wsjtx-manager/init tests/integration/wsjtx-manager/*.sh tests/integration/wsjtx-screen/*.sh tests/integration/wsjtx-routing/*.sh tests/integration/wsjtx-cli/*.sh tests/integration/wsjtx-application/*.sh tests/integration/wsjtx-crash/*.sh tests/integration/wsjtx-daemon/*.sh)
for file in "${files[@]}"; do bash -n "$file"; done
shellcheck -x "${files[@]}"
# Empty placeholders exercise packaging only; this image builds no deployable binary.
for binary in dxberryd dxberry-preset dxberry-program-state dxberry-wsjtx-settings; do install -m 755 /dev/null "provision/bin/$binary"; done
tests/run.sh
build/build-image.sh --check
