#!/bin/bash
# make-update-tarball.sh VERSION OUTDIR: the DXBerry update file for a release (console spec 11.3).
# OUTDIR/dxberry-pi-VERSION.tar.gz holds dxberry/ = the provision/ tree as the image installs it
# (directories 755, files 644, bin/* 755, owned by root) with VERSION and RELEASE written, and
# OUTDIR/dxberry-pi-VERSION.tar.gz.sha256 its checksum. Prints the archive's path.
set -uo pipefail
die() { echo "make-update-tarball: $*" >&2; exit 1; }
[[ $# -eq 2 ]] || die "usage: make-update-tarball.sh VERSION OUTDIR"
version=${1#v}; out=$2
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]] || die "not a version: $1"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd) || die "cannot find the repository"
stage=$(mktemp -d) || die "no temporary directory"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/dxberry" "$out" || die "cannot create $out"
cp -r "$root/provision/." "$stage/dxberry/" || die "could not copy provision/"
find "$stage/dxberry" -type d -exec chmod 755 {} + || die "chmod failed"
find "$stage/dxberry" -type f -exec chmod 644 {} + || die "chmod failed"
chmod 755 "$stage/dxberry/bin/"* || die "chmod failed"
printf '%s\n' "$version" > "$stage/dxberry/VERSION"
printf 'DXBERRY_VERSION=%s\nDXBERRY_COMMIT=%s\nDXBERRY_BUILD_DATE=%s\n' "$version" \
  "$(git -C "$root" rev-parse --short HEAD 2> /dev/null || echo unknown)" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" > "$stage/dxberry/RELEASE"
name="dxberry-pi-$version.tar.gz"
tar --owner=0 --group=0 --numeric-owner -czf "$out/$name" -C "$stage" dxberry || die "tar failed"
( cd "$out" && sha256sum "$name" > "$name.sha256" ) || die "sha256sum failed"
printf '%s\n' "$out/$name"
