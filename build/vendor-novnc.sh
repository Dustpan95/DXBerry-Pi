#!/bin/bash
# vendor-novnc.sh [DEST]: vendor noVNC's newest release (core/, vendor/pako/, LICENSE.txt) into DEST
# (default provision/cockpit/dxberry/novnc) for the console's program tab. Re-running it is how
# noVNC is updated. Writes DEST/VERSION (the release tag) and DEST/README. Fails, leaving DEST
# untouched, when the repository's attribution grep matches any copied file.
set -uo pipefail
die() { echo "vendor-novnc: $*" >&2; exit 1; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd) || die "cannot find the repository"
dest=${1:-$root/provision/cockpit/dxberry/novnc}
api=${DXB_NOVNC_API:-https://api.github.com/repos/novnc/noVNC/releases/latest}
work=$(mktemp -d) || die "no temporary directory"
trap 'rm -rf "$work"' EXIT
curl -fsSL "$api" -o "$work/release.json" || die "cannot read $api"
tag=$(jq -r '.tag_name // empty' "$work/release.json") || die "unreadable release answer"
url=$(jq -r '.tarball_url // empty' "$work/release.json")
[[ $tag =~ ^v?[0-9][0-9A-Za-z.+-]*$ ]] || die "unexpected release tag: $tag"
[[ -n $url ]] || die "the release has no source tarball"
curl -fsSL "$url" -o "$work/src.tar.gz" || die "cannot download $url"
mkdir "$work/src" || die "mkdir failed"
tar -xzf "$work/src.tar.gz" -C "$work/src" --strip-components=1 || die "cannot unpack the tarball"
for p in core vendor/pako LICENSE.txt; do [[ -e $work/src/$p ]] || die "the release has no $p"; done
mkdir -p "$work/out/vendor" || die "mkdir failed"
cp -r "$work/src/core" "$work/out/core" || die "copy failed"
cp -r "$work/src/vendor/pako" "$work/out/vendor/pako" || die "copy failed"
cp "$work/src/LICENSE.txt" "$work/out/LICENSE.txt" || die "copy failed"
printf '%s\n' "$tag" > "$work/out/VERSION"
cat > "$work/out/README" <<'TXT'
This is noVNC, an HTML5 VNC client (https://github.com/novnc/noVNC), vendored unmodified: core/, vendor/pako/ and LICENSE.txt from the release named in VERSION. It is licensed under the MPL-2.0 (see LICENSE.txt; file headers are kept). The program tab (../screen.js) imports core/rfb.js. To update, run build/vendor-novnc.sh from the repository root: it fetches the newest release and replaces this directory.
TXT
if grep -rIlE -i 'co-?auth[o]red-?by|cla[u]de|anthr[o]pic' "$work/out" > "$work/hits"; then
  die "attribution grep matched: $(tr '\n' ' ' < "$work/hits")"
fi
rm -rf "$dest" || die "cannot clear $dest"
mkdir -p "$(dirname "$dest")" || die "mkdir failed"
cp -r "$work/out" "$dest" || die "cannot write $dest"
echo "vendored noVNC $tag into $dest"
