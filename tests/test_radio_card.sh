#!/usr/bin/env bash
# The sound card's ALSA id: recorded at add, put back at remove (the rc1 hardware check: the card
# stayed CHK1 after "remove chk1" and Graywolf's plughw:CARD=CODEC could not open).
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

# rc_env: the digirig scene (its card is card1 on USB device 1-1.3, id "Device") with every
# sysfs write, fuser and sleep a recorder in $TEST_TMP/calls
rc_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_RADIOS_FILE=$TEST_TMP/state/radios.json \
    DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=rc_udevadm \
    DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RUN_DIR=$TEST_TMP/run DXB_WIRED_FILE=$TEST_TMP/state/wired.json \
    DXB_SYS_WRITE=rc_sys_write DXB_FUSER=rc_fuser DXB_PS=rc_ps DXB_SLEEP=rc_sleep DXB_DEV_SND=$TEST_TMP/dev/snd \
    DXB_USB_DRIVER_DIR=$TEST_TMP/sys/bus/usb/drivers/usb
  unset DXB_SYS_SOUND
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc" "$DXB_DEV_SND" "$DXB_USB_DRIVER_DIR"; : > "$TEST_TMP/calls"
  rm -f "$TEST_TMP"/{id-write-fails,card-busy,fuser-missing,bind-fails,card-returns}
  : > "$DXB_DEV_SND/pcmC1D0c"; : > "$DXB_DEV_SND/pcmC1D0p"
  fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
rc_udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
rc_sleep() { echo "sleep $*" >> "$TEST_TMP/calls"; }
# fuser: 0 and a pid when $TEST_TMP/card-busy, 127 when $TEST_TMP/fuser-missing, else 1 (nobody)
rc_fuser() {
  echo "fuser $*" >> "$TEST_TMP/calls"
  [[ -e $TEST_TMP/fuser-missing ]] && return 127
  [[ -e $TEST_TMP/card-busy ]] && { echo " 4242"; return 0; }
  return 1
}
rc_ps() { echo "ps $*" >> "$TEST_TMP/calls"; echo graywolf; }
# a sysfs write: recorded, then really made unless $TEST_TMP/id-write-fails and it is an id file
rc_sys_write() {
  echo "write ${1#"$TEST_TMP"/} $2" >> "$TEST_TMP/calls"
  if [[ $1 == */id && -e $TEST_TMP/id-write-fails ]]; then return 22; fi
  if [[ $1 == */bind && -e $TEST_TMP/bind-fails ]]; then return 19; fi
  # the card re-enumerates under its own id when the device is bound again
  if [[ $1 == */bind && -e $TEST_TMP/card-returns ]]; then printf 'Device\n' > "$DXB_SYSFS_ROOT/class/sound/card1/id"; return 0; fi
  printf '%s\n' "$2" > "$1"
}
# rc_added NAME: the radio added on the digirig, then its udev rename applied to the card
rc_added() {
  dxb_radio_add "$1" '{"audio":"1","cat":"2"}' > /dev/null
  printf '%s\n' "${1^^}" > "$DXB_SYSFS_ROOT/class/sound/card1/id"
}
rc_card_id() { cat "$DXB_SYSFS_ROOT/class/sound/card1/id"; }

test_radio_add_records_the_card_id() {
  rc_env
  dxb_radio_add chk1 '{"audio":"1","cat":"2"}' > /dev/null
  assert_eq "$(jq -r '.radios.chk1.audio.card_id' <<< "$DXB_RADIOS")" "Device"
  assert_eq "$(jq -r '.radios.chk1.audio.card_id' "$DXB_RADIOS_FILE")" "Device"
  # no readable id: nothing stored, and the log says so
  rm -f "$DXB_SYSFS_ROOT/class/sound/card1/id" "$DXB_SYSFS_ROOT"/devices/*/*/*/*/*/*/*/1-1.3/1-1.3:1.0/sound/card1/id
  local err; err=$(dxb_radio_add chk2 '{"audio":"1"}' 2>&1)
  assert_eq "$(jq -r '.radios.chk2.audio | has("card_id")' <<< "$DXB_RADIOS")" "false"
  assert_contains "$err" "could not read the sound card's id"
}

test_radio_remove_restores_the_card_id_by_sysfs() {
  rc_env; rc_added chk1
  local rec; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  _dxb_radio_card_restore chk1 "$rec" > /dev/null 2>&1
  assert_eq "$(rc_card_id)" "Device"
  assert_contains "$(cat "$TEST_TMP/calls")" "write sys/class/sound/card1/id Device"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "unbind"
  assert_contains "$(cat "$TEST_TMP/calls")" "fuser $DXB_DEV_SND/pcmC1D0c $DXB_DEV_SND/pcmC1D0p"
}

test_radio_remove_rebinds_usb_when_the_id_write_fails() {
  rc_env; rc_added chk1
  : > "$TEST_TMP/id-write-fails"; : > "$TEST_TMP/card-returns"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_eq "$(grep -E '^(write .*(unbind|bind)|sleep)' "$TEST_TMP/calls" | tr '\n' '|')" \
    "write sys/bus/usb/drivers/usb/unbind 1-1.3|sleep 3|write sys/bus/usb/drivers/usb/bind 1-1.3|sleep 1|"
  assert_contains "$(cat "$TEST_TMP/calls")" "fuser $DXB_DEV_SND/pcmC1D0c $DXB_DEV_SND/pcmC1D0p"
  assert_contains "$err" "re-enumerated to restore the sound card id Device"
  assert_eq "$(rc_card_id)" "Device"
}

test_radio_remove_rebind_whose_bind_fails_warns_without_success() {
  rc_env; rc_added chk1
  : > "$TEST_TMP/id-write-fails"; : > "$TEST_TMP/bind-fails"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_contains "$err" "could not bind USB device 1-1.3 again; replug it"
  assert_not_contains "$err" "re-enumerated"
}

test_radio_remove_rebind_whose_card_never_returns_warns() {
  rc_env; rc_added chk1
  : > "$TEST_TMP/id-write-fails"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_contains "$err" "the sound card did not come back as Device after re-enumeration; replug it or restart the Pi"
  assert_not_contains "$err" "re-enumerated"
  assert_eq "$(grep -c '^sleep 1' "$TEST_TMP/calls")" "5"
}

test_radio_remove_leaves_the_card_when_another_holds_the_wanted_id() {
  rc_env; rc_added chk1
  fx_onboard_card "$DXB_SYSFS_ROOT" card0 bcm2835; printf 'Device\n' > "$DXB_SYSFS_ROOT/class/sound/card0/id"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null; : > "$TEST_TMP/calls"
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_contains "$err" "another sound card already has the id Device"
  assert_eq "$(rc_card_id)" "CHK1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "write "
}

# fuser exit 1 means nobody holds the card; any other failure is not an answer
test_radio_remove_does_nothing_when_fuser_cannot_answer() {
  rc_env; rc_added chk1
  : > "$TEST_TMP/fuser-missing"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_contains "$err" "could not check whether the sound card is in use (fuser): leaving its name CHK1"
  assert_eq "$(rc_card_id)" "CHK1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "write "
}

# a card an earlier busy remove left named CHK1 is not recorded as the next radio's original id
test_radio_add_does_not_record_a_renamed_id() {
  rc_env
  printf 'CHK1\n' > "$DXB_SYSFS_ROOT/class/sound/card1/id"
  dxb_radio_load; DXB_RADIOS=$(jq -c '.radios.chk1 = {"audio":null}' <<< "$DXB_RADIOS")
  local err; err=$(dxb_radio_add other '{"audio":"1"}' 2>&1)
  assert_eq "$(jq -r '.radios.other.audio | has("card_id")' <<< "$DXB_RADIOS")" "false"
  assert_contains "$err" "original id is unknown"
  # nor the new radio's own name
  printf 'THIRD\n' > "$DXB_SYSFS_ROOT/class/sound/card1/id"
  err=$(dxb_radio_add third '{"audio":"1"}' 2>&1)
  assert_eq "$(jq -r '.radios.third.audio | has("card_id")' <<< "$DXB_RADIOS")" "false"
}

test_radio_remove_leaves_a_busy_card_and_warns() {
  rc_env; rc_added chk1
  : > "$TEST_TMP/card-busy"
  local rec err; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  err=$(_dxb_radio_card_restore chk1 "$rec" 2>&1)
  assert_contains "$err" "the sound card keeps the name CHK1 until it is replugged or the Pi restarts (in use by graywolf)"
  assert_eq "$(rc_card_id)" "CHK1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "write "
}

test_radio_remove_without_a_recorded_id_changes_nothing() {
  rc_env; rc_added chk1
  dxb_radio_load; DXB_RADIOS=$(jq -c 'del(.radios.chk1.audio.card_id)' <<< "$DXB_RADIOS")
  local rec; rec=$(dxb_radio_get chk1)
  dxb_radio_remove chk1 > /dev/null
  : > "$TEST_TMP/calls"
  _dxb_radio_card_restore chk1 "$rec" > /dev/null 2>&1; assert_eq "$?" "0"
  assert_eq "$(rc_card_id)" "CHK1"
  assert_eq "$(cat "$TEST_TMP/calls")" ""
}

# a card that is not named after the radio (already replugged, or never renamed) is not touched
test_radio_remove_leaves_a_card_that_is_not_named_after_the_radio() {
  rc_env; dxb_radio_add chk1 '{"audio":"1"}' > /dev/null
  local rec; rec=$(dxb_radio_get chk1)
  : > "$TEST_TMP/calls"
  _dxb_radio_card_restore chk1 "$rec" > /dev/null 2>&1
  assert_eq "$(cat "$TEST_TMP/calls")" ""
}
