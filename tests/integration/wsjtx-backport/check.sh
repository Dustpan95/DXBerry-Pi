#!/bin/bash
set -euo pipefail

[[ $(uname -m) == aarch64 && $(dpkg --print-architecture) == arm64 && $(id -u) != 0 ]]
[[ ! -e /dev/snd && ! -e /dev/bus/usb ]]
version=3.0.2+dfsg-2~bpo13+1
for package in wsjtx wsjtx-data; do
    [[ $(dpkg-query -W -f='${Version}' "$package") == "$version" ]]
done
for program in wsjtx jt9 ft8sim; do
    path=$(command -v "$program")
    dpkg-query -S "$path" | grep -Fx "wsjtx: $path"
    dependencies=$(ldd "$path")
    if [[ $dependencies == *'not found'* ]]; then
        printf 'Unresolved library for %s\n' "$path" >&2
        exit 1
    fi
done

home=$(mktemp -d)
trap 'rm -rf -- "$home"' EXIT
export HOME="$home"
cd "$home"
ft8sim 'CQ K1ABC FN42' 1500 0 0 0 1 10
mv 000000_000001.wav 260101_120000.wav
timeout 65 jt9 -8 260101_120000.wav > decode.txt
cat decode.txt
awk '$1 == "120000" && $4 >= 1497 && $4 <= 1503 && $5 == "~" && $6 == "CQ" && $7 == "K1ABC" && $8 == "FN42" { found = 1 }
     END { exit !found }' decode.txt
echo 'Verified native ARM64 backport packages, installed libraries and offline FT8 decode.'
