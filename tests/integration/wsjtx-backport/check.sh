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

export XDG_CONFIG_HOME="$home/config" XDG_DATA_HOME="$home/data" XDG_CACHE_HOME="$home/cache"
export XDG_RUNTIME_DIR="$home/runtime" XDG_STATE_HOME="$home/state"
mkdir -m 700 "$XDG_CONFIG_HOME" "$XDG_RUNTIME_DIR"
export PULSE_SERVER="unix:$XDG_RUNTIME_DIR/pulse.sock"
cat > "$home/pulse.pa" <<PULSE
load-module module-native-protocol-unix socket=$XDG_RUNTIME_DIR/pulse.sock auth-anonymous=1
load-module module-null-sink sink_name=fixture_null sink_properties=device.description=DXBerryFixture
set-default-sink fixture_null
set-default-source fixture_null.monitor
PULSE
pulseaudio --daemonize=yes --exit-idle-time=-1 -n --file="$home/pulse.pa"
[[ $(pactl get-default-sink) == fixture_null && $(pactl get-default-source) == fixture_null.monitor ]]
export DXB_BACKPORT_WAV="$home/260101_120000.wav"
timeout 120 xvfb-run -a -s '-screen 0 1280x900x24 -nolisten tcp' bash -ec '
  openbox > "$HOME/openbox.log" 2>&1 &
  exec /fixture/profile.test -test.v -test.timeout=110s -test.run="^TestBackportApplicationProfile$"
'
