#!/usr/bin/env bash
# Stage the pinned upstream application package; never install or execute it.
set -euo pipefail
readonly asset=pat_1.0.1_linux_amd64.deb
readonly digest=21b5c7cbf96375d10da977a014c83d5bd11b2119d72b303db75b4ff168e163c0
readonly size=4940336
readonly url=https://github.com/la5nta/pat/releases/download/v1.0.1/pat_1.0.1_linux_amd64.deb

verify() {
  local file=$1 actual
  [[ -f $file && ! -L $file ]] || { echo 'Expected a regular Pat package file' >&2; return 1; }
  [[ $(stat -c %s -- "$file") == "$size" ]] || { echo 'Pat package size mismatch' >&2; return 1; }
  actual=$(sha256sum < "$file") || return 1
  [[ ${actual%% *} == "$digest" ]] || { echo 'Pat package checksum mismatch' >&2; return 1; }
}

if [[ ${1:-} == --verify-only && $# == 2 ]]; then
  verify "$2"
  printf 'Verified Pat 1.0.1 amd64 package\n'
  exit 0
fi
if [[ $# != 1 || ${1:-} == -* ]]; then
  echo 'Usage: download-pat.sh DESTINATION_DIRECTORY | --verify-only PACKAGE' >&2
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
partial=$(mktemp -- "$dest/.pat-download.XXXXXXXX")
trap 'rm -f -- "$partial"' EXIT
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 10 --max-time 180 --max-filesize "$size" --output "$partial" "$url"
verify "$partial"
ln -- "$partial" "$target"
printf 'Verified and staged %s\n' "$target"
