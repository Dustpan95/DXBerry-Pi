#!/usr/bin/env bash
# Fake sysfs trees for the radio scanner. Layout mirrors a Pi 4:
#   devices/platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb1/1-1/<dev>/<dev>:1.N/...
# shellcheck disable=SC2034
FX_USB_BASE=devices/platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb1/1-1

# fx_usb_device ROOT DEV VID PID SERIAL PRODUCT   e.g. fx_usb_device "$S" 1-1.3 0d8c 013c "" "USB Audio Device"
fx_usb_device() {
  local root=$1 dev=$2 d="$1/$FX_USB_BASE/$2"
  mkdir -p "$d"
  printf '%s\n' "$3" > "$d/idVendor"; printf '%s\n' "$4" > "$d/idProduct"
  [[ -z $5 ]] || printf '%s\n' "$5" > "$d/serial"
  printf '%s\n' "$6" > "$d/product"
}
# fx_usb_function ROOT DEV IFNUM KIND KERNEL: KIND audio|serial|hid; KERNEL card1|ttyUSB0|ttyACM0|hidraw1
fx_usb_function() {
  local root=$1 dev=$2 ifn=$3 kind=$4 kernel=$5 base="$1/$FX_USB_BASE/$2/$2:1.$3" leaf cls
  case $kind in
    audio) leaf="$base/sound/$kernel"; cls="sound" ;;
    serial) leaf="$base/$kernel/tty/$kernel"; cls="tty" ;;
    hid) leaf="$base/0003:0D8C:013C.0002/hidraw/$kernel"; cls="hidraw" ;;
  esac
  mkdir -p "$leaf" "$root/class/$cls"
  # a sound card carries the ALSA id udev's ATTR{id}= rewrites; "Device" is what the kernel
  # gives a USB codec before any rename, so a test can prove apply reports the pending rename
  [[ $kind != audio ]] || printf 'Device\n' > "$leaf/id"
  ln -sfn "$(realpath --relative-to="$root/class/$cls" "$leaf")" "$root/class/$cls/$kernel"
}
# fx_onboard_card ROOT KERNEL NAME: a non-USB sound card (vc4-hdmi, bcm2835)
fx_onboard_card() {
  local d="$1/devices/platform/soc/$3/sound/$2"
  mkdir -p "$d" "$1/class/sound"
  ln -sfn "$(realpath --relative-to="$1/class/sound" "$d")" "$1/class/sound/$2"
}
# fx_systemctl ARGS...: a systemctl stand-in shared by the radio tests. Records every call in
# $TEST_TMP/calls and keeps an "active units" list in $TEST_TMP/active. is-active prints the
# state (unless --quiet) and returns 0/3 like the real command. is-enabled U prints the content
# of $TEST_TMP/enabled-U ("disabled" when there is none) and returns 0 only for "enabled".
# FX_SYSTEMCTL_FAIL_START=U makes "start U" fail (exit 1, the unit stays inactive).
fx_systemctl() {
  echo "systemctl $*" >> "$TEST_TMP/calls"
  while [[ ${1:-} == --no-block || ${1:-} == --quiet ]]; do shift; done   # leading flags are recorded above, not matched
  local u=${*: -1} st
  case $1 in
    is-active)
      if grep -qx "$u" "$TEST_TMP/active" 2> /dev/null; then [[ $2 == --quiet ]] || echo active; return 0
      else [[ $2 == --quiet ]] || echo inactive; return 3; fi ;;
    is-failed) if grep -qx "$u" "$TEST_TMP/failed" 2> /dev/null; then return 0; else return 1; fi ;;
    is-enabled)
      st=disabled; [[ -f $TEST_TMP/enabled-$u ]] && read -r st < "$TEST_TMP/enabled-$u"
      echo "$st"; [[ $st == enabled ]]; return $? ;;
    start|restart)
      [[ $1 == start && -n ${FX_SYSTEMCTL_FAIL_START:-} && $u == "$FX_SYSTEMCTL_FAIL_START" ]] && return 1
      grep -qx "$u" "$TEST_TMP/active" 2> /dev/null || echo "$u" >> "$TEST_TMP/active" ;;
    stop) grep -vx "$u" "$TEST_TMP/active" > "$TEST_TMP/active.n" 2> /dev/null; mv -f "$TEST_TMP/active.n" "$TEST_TMP/active" ;;
  esac
  return 0
}

# fx_unplug ROOT WHAT / fx_replug ROOT WHAT: take one function of the two-digirigs scene off the
# bus and put it back, by moving its class/ link aside (the device tree stays). WHAT names it the
# way test_radio.sh's two_radios pins it: r1-audio (card1 on 1-1.3), r1-cat (ttyUSB0 on 1-1.4),
# r2-audio (card2 on 1-1.1), r2-cat (ttyUSB1 on 1-1.2).
_fx_plug_link() {
  case $1 in
    r1-audio) echo class/sound/card1 ;;
    r1-cat) echo class/tty/ttyUSB0 ;;
    r2-audio) echo class/sound/card2 ;;
    r2-cat) echo class/tty/ttyUSB1 ;;
    *) echo "fx: unknown function $1" >&2; return 1 ;;
  esac
}
fx_unplug() {
  local l; l=$(_fx_plug_link "$2") || return 1
  mkdir -p "$1/.unplugged"; mv "$1/$l" "$1/.unplugged/$2"
}
fx_replug() {
  local l; l=$(_fx_plug_link "$2") || return 1
  mv "$1/.unplugged/$2" "$1/$l"
}

# fx_scene ROOT NAME: digirig | ic705 | split | two-digirigs | none
fx_scene() {
  local r=$1
  mkdir -p "$r/class/sound" "$r/class/tty" "$r/class/hidraw"
  fx_onboard_card "$r" card0 fef00700.hdmi
  case $2 in
    digirig)
      fx_usb_device "$r" 1-1.3 0d8c 013c "" "USB Audio Device"
      fx_usb_function "$r" 1-1.3 0 audio card1
      fx_usb_function "$r" 1-1.3 3 hid hidraw1
      fx_usb_device "$r" 1-1.4 10c4 ea60 0001 "CP2102 USB to UART Bridge Controller"
      fx_usb_function "$r" 1-1.4 0 serial ttyUSB0 ;;
    ic705)
      fx_usb_device "$r" 1-1.2 0c26 0036 IC-705_12345678 "IC-705"
      fx_usb_function "$r" 1-1.2 0 audio card1
      fx_usb_function "$r" 1-1.2 2 serial ttyUSB0
      fx_usb_function "$r" 1-1.2 4 serial ttyUSB1 ;;
    split)
      fx_usb_device "$r" 1-1.1 08bb 29b6 "" "USB Audio CODEC"
      fx_usb_function "$r" 1-1.1 0 audio card1
      fx_usb_device "$r" 1-1.2 0403 6001 FTA1B2C3 "FT232R USB UART"
      fx_usb_function "$r" 1-1.2 0 serial ttyUSB0 ;;
    two-digirigs)
      fx_scene "$r" digirig
      fx_usb_device "$r" 1-1.1 0d8c 013c "" "USB Audio Device"
      fx_usb_function "$r" 1-1.1 0 audio card2
      fx_usb_function "$r" 1-1.1 3 hid hidraw2
      fx_usb_device "$r" 1-1.2 10c4 ea60 0001 "CP2102 USB to UART Bridge Controller"
      fx_usb_function "$r" 1-1.2 0 serial ttyUSB1 ;;
    none) ;;
  esac
}
