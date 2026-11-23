#!/usr/bin/env bash
# Fetch the same latest Graywolf release used by an unpinned first boot.
# Extract only: never install its package or start a system service.
set -euo pipefail
dest=${1:?destination directory required}
mkdir -p "$dest"
dest=$(cd "$dest" && pwd)
base=https://github.com/chrissnell/graywolf/releases/latest/download
curl -fsSL --connect-timeout 10 --max-time 60 "$base/checksums.txt" >"$dest/checksums.txt"
line=$(awk '$2 ~ /^graywolf_[0-9.]+_amd64\.deb$/ { print $1, $2; exit }' "$dest/checksums.txt")
read -r expected asset <<<"$line"
[[ $expected =~ ^[0-9a-f]{64}$ && $asset =~ ^graywolf_[0-9.]+_amd64\.deb$ ]] || {
  echo 'No valid amd64 Graywolf package checksum found' >&2; exit 1;
}
curl -fsSL --connect-timeout 10 --max-time 180 -o "$dest/$asset" "$base/$asset"
printf '%s  %s\n' "$expected" "$dest/$asset" | sha256sum --check --status
dpkg-deb -x "$dest/$asset" "$dest/extracted"
printf 'Verified and extracted %s\n' "$asset"
