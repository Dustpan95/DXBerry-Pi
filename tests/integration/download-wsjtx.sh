#!/usr/bin/env bash
# Stage the pinned upstream compatibility package; never install or execute it.
set -euo pipefail
readonly asset=wsjtx-3.0.2-linux-x86_64.deb
readonly digest=a0a00ebb74f2159b45930be1b2694b22f1a83a802622cd1d2edacb7c11e80e15
readonly size=46499354
readonly url=https://github.com/WSJTX/wsjtx/releases/download/v3.0.2/wsjtx-3.0.2-linux-x86_64.deb

verify() {
  local file=$1
  [[ -f $file && ! -L $file ]] || { echo 'Expected a regular WSJT-X package file' >&2; return 1; }
  [[ $(stat -c %s -- "$file") == "$size" ]] || { echo 'WSJT-X package size mismatch' >&2; return 1; }
  # Hash through stdin so an arbitrary caller-supplied filename is never parsed
  # as part of a checksum record (spaces and backslashes remain ordinary names).
  local actual
  actual=$(sha256sum < "$file") || return 1
  [[ ${actual%% *} == "$digest" ]] || { echo 'WSJT-X package checksum mismatch' >&2; return 1; }
}

if [[ ${1:-} == --verify-only && $# == 2 ]]; then
  verify "$2"
  printf 'Verified WSJT-X 3.0.2 x86_64 package\n'
  exit 0
fi
if [[ $# != 1 || ${1:-} == -* ]]; then
  echo 'Usage: download-wsjtx.sh DESTINATION_DIRECTORY | --verify-only PACKAGE' >&2
  exit 2
fi

mkdir -p -- "$1"
dest=$(cd -- "$1" && pwd)
target=$dest/$asset
if [[ -e $target || -L $target ]]; then
  verify "$target"
  printf 'Verified existing %s\n' "$target"
  exit 0
fi

partial=$(mktemp -- "$dest/.wsjtx-download.XXXXXXXX")
trap 'rm -f -- "$partial"' EXIT
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 10 --max-time 180 --max-filesize "$size" --output "$partial" "$url"
verify "$partial"
# Publish without replacing anything another invocation/caller created while
# the network request was running. A same-directory hard link is atomic.
ln -- "$partial" "$target"
printf 'Verified and staged %s\n' "$target"
