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
# state (unless --quiet) and returns 0/3 like the real command.
fx_systemctl() {
  echo "systemctl $*" >> "$TEST_TMP/calls"
  local u=${*: -1}
  case $1 in
    is-active)
      if grep -qx "$u" "$TEST_TMP/active" 2> /dev/null; then [[ $2 == --quiet ]] || echo active; return 0
      else [[ $2 == --quiet ]] || echo inactive; return 3; fi ;;
    start|restart) grep -qx "$u" "$TEST_TMP/active" 2> /dev/null || echo "$u" >> "$TEST_TMP/active" ;;
    stop) grep -vx "$u" "$TEST_TMP/active" > "$TEST_TMP/active.n" 2> /dev/null; mv -f "$TEST_TMP/active.n" "$TEST_TMP/active" ;;
  esac
  return 0
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
