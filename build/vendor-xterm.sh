#!/bin/bash
# vendor-xterm.sh [DEST]: vendor xterm.js (@xterm/xterm) and its fit addon (@xterm/addon-fit), at the
# version the npm registry marks latest for each, into DEST (default station/web/vendor/xterm) for the
# console's terminal page. Re-running it is how xterm.js is updated. Writes DEST/VERSION and DEST/README.
# Fails, leaving DEST untouched, when the repository's attribution grep matches any copied file.
set -uo pipefail
die() { echo "vendor-xterm: $*" >&2; exit 1; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd) || die "cannot find the repository"
dest=${1:-$root/station/web/vendor/xterm}
registry=${DXB_NPM_REGISTRY:-https://registry.npmjs.org}
work=$(mktemp -d) || die "no temporary directory"
trap 'rm -rf "$work"' EXIT

# fetch PACKAGE NAME: unpack the registry's latest tarball of PACKAGE into $work/NAME, version in $work/NAME.version
fetch() {
  local pkg=$1 name=$2 ver url
  curl -fsSL "$registry/$pkg" -o "$work/$name.json" || die "cannot read $registry/$pkg"
  ver=$(jq -r '."dist-tags".latest // empty' "$work/$name.json") || die "unreadable registry answer for $pkg"
  [[ $ver =~ ^[0-9][0-9A-Za-z.+-]*$ ]] || die "unexpected version for $pkg: $ver"
  url=$(jq -r --arg v "$ver" '.versions[$v].dist.tarball // empty' "$work/$name.json")
  [[ -n $url ]] || die "$pkg $ver has no tarball"
  curl -fsSL "$url" -o "$work/$name.tgz" || die "cannot download $url"
  mkdir "$work/$name" || die "mkdir failed"
  tar -xzf "$work/$name.tgz" -C "$work/$name" --strip-components=1 || die "cannot unpack $pkg"
  printf '%s\n' "$ver" > "$work/$name.version"
}
fetch @xterm/xterm xterm
fetch @xterm/addon-fit fit
for p in xterm/lib/xterm.js xterm/css/xterm.css xterm/LICENSE fit/lib/addon-fit.js fit/LICENSE; do
  [[ -e $work/$p ]] || die "the package has no $p"
done
mkdir -p "$work/out" || die "mkdir failed"
cp "$work/xterm/lib/xterm.js" "$work/xterm/css/xterm.css" "$work/out/" || die "copy failed"
cp "$work/xterm/LICENSE" "$work/out/LICENSE" || die "copy failed"
cp "$work/fit/lib/addon-fit.js" "$work/out/addon-fit.js" || die "copy failed"
cp "$work/fit/LICENSE" "$work/out/LICENSE.addon-fit" || die "copy failed"
{
  printf '@xterm/xterm %s\n' "$(cat "$work/xterm.version")"
  printf '@xterm/addon-fit %s\n' "$(cat "$work/fit.version")"
} > "$work/out/VERSION"
cat > "$work/out/README" <<'TXT'
This is xterm.js (https://github.com/xtermjs/xterm.js), a terminal for the browser, and its fit addon, vendored unmodified from the npm packages @xterm/xterm and @xterm/addon-fit at the versions named in VERSION: xterm.js, xterm.css and addon-fit.js, with their MIT licenses (LICENSE, LICENSE.addon-fit). The terminal page (../../terminal.js) loads them. To update, run build/vendor-xterm.sh from the repository root: it fetches the newest releases and replaces this directory.
TXT
if grep -rIlE -i 'co-?auth[o]red-?by|cla[u]de|anthr[o]pic' "$work/out" > "$work/hits"; then
  die "attribution grep matched: $(tr '\n' ' ' < "$work/hits")"
fi
rm -rf "$dest" || die "cannot clear $dest"
mkdir -p "$(dirname "$dest")" || die "mkdir failed"
cp -r "$work/out" "$dest" || die "cannot write $dest"
echo "vendored xterm.js $(head -n1 "$dest/VERSION") into $dest"
