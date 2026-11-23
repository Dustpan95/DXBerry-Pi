#!/usr/bin/env bash
# Scanner metadata and services are fixtures; no physical device is used.
# shellcheck disable=SC2030,SC2031,SC2317

test_radio_pin_identity_rejects_replacement_at_the_same_usb_port() (
  local field value base=$TEST_TMP
  for field in idVendor idProduct serial; do
    TEST_TMP=$base/$field; export TEST_TMP
    radio_env; fake_apps; two_radios
    value=ffff; [[ $field != serial ]] || value=replacement
    printf '%s\n' "$value" > "$DXB_SYSFS_ROOT/$FX_USB_BASE/1-1.4/$field"
    dxb_radio_scan_cache
    assert_eq "$(jq -r .cat <<< "$(dxb_radio_kernel_names r1)")" null
    dxb_radio_present r1; assert_eq "$?" 4
    : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
    dxb_radio_claim r1 alpha > "$TEST_TMP/out" 2>&1; assert_eq "$?" 4
    assert_eq "$(jq -r '.radios.r1.owner' <<< "$DXB_RADIOS")" ''
    assert_eq "$(cat "$TEST_TMP/appcalls")" ''
    assert_not_contains "$(cat "$TEST_TMP/calls")" 'start alpha.service'
  done
)

test_radio_pin_identity_known_empty_serial_is_not_a_wildcard() (
  radio_env; fake_apps; two_radios
  assert_eq "$(jq -r '.radios.r1.audio.serial' <<< "$DXB_RADIOS")" ''
  printf 'new-serial\n' > "$DXB_SYSFS_ROOT/$FX_USB_BASE/1-1.3/serial"
  dxb_radio_scan_cache
  assert_eq "$(jq -r .audio <<< "$(dxb_radio_kernel_names r1)")" null
  dxb_radio_present r1; assert_eq "$?" 4
  # Do not capture the replacement's ALSA name as the original for restoration.
  printf 'Replacement\n' > "$DXB_SYSFS_ROOT/class/sound/card1/id"
  local r
  r=$(jq -c '.radios.r1 | .audio.card_id="Recorded"' <<< "$DXB_RADIOS")
  assert_eq "$(jq -r .audio.card_id <<< "$(_dxb_radio_record_card_id r1 "$r")")" Recorded
)

test_radio_pin_identity_malformed_recorded_fields_do_not_match_unknown_metadata() (
  radio_env; fake_apps; two_radios
  local before=$DXB_RADIOS
  DXB_RADIOS=$(jq -c '.radios.r1.cat.serial=null' <<< "$before")
  DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(del(.serial)))' <<< "$DXB_RADIO_SCAN")
  dxb_radio_present r1; assert_eq "$?" 4
  DXB_RADIOS=$(jq -c '.radios.r1.cat.vidpid=":"' <<< "$before")
  DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(.vidpid=":"))' <<< "$DXB_RADIO_SCAN")
  dxb_radio_present r1; assert_eq "$?" 4
)

test_radio_pin_identity_survives_kernel_renumbering_and_keeps_legacy_path_pins() (
  radio_env; fake_apps; two_radios
  # The hardware identity and port stay fixed while the kernel name changes.
  fx_unplug "$DXB_SYSFS_ROOT" r1-cat
  fx_usb_function "$DXB_SYSFS_ROOT" 1-1.4 0 serial ttyUSB9
  dxb_radio_scan_cache
  assert_ok dxb_radio_present r1
  assert_eq "$(jq -r .cat <<< "$(dxb_radio_kernel_names r1)")" ttyUSB9
  # Older path-only records remain operational; they are not trusted preset evidence.
  DXB_RADIOS=$(jq -c 'del(.radios.r1.cat.vidpid,.radios.r1.cat.serial)' <<< "$DXB_RADIOS")
  assert_ok dxb_radio_present r1
  DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(if .kind=="serial" then del(.serial) else . end))' <<< "$DXB_RADIO_SCAN")
  assert_ok dxb_radio_present r1
)

test_radio_pin_identity_rejects_unknown_or_ambiguous_scanner_metadata() (
  radio_env; fake_apps; two_radios
  local before=$DXB_RADIO_SCAN bad
  for bad in missing-id missing-serial missing-kernel invalid-kernel duplicate; do
    case $bad in
      missing-id) DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(del(.vidpid)))' <<< "$before") ;;
      missing-serial) DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(del(.serial)))' <<< "$before") ;;
      missing-kernel) DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(del(.kernel)))' <<< "$before") ;;
      invalid-kernel) DXB_RADIO_SCAN=$(jq -c 'map(.functions |= map(.kernel=0))' <<< "$before") ;;
      duplicate) DXB_RADIO_SCAN=$(jq -c 'map(.functions += .functions)' <<< "$before") ;;
    esac
    dxb_radio_present r1; assert_eq "$?" 4
    assert_eq "$(jq -r .cat <<< "$(dxb_radio_kernel_names r1)")" null
  done
)
