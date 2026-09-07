#!/bin/bash
# shellcheck shell=bash
# Replace applied secrets with <applied> so plaintext never lingers on the FAT partition.

: "${DXB_DIETPI_TXT:=/boot/dietpi.txt}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"

# dxb_scrub_key FILE KEY: rewrite every "KEY = value" line as "KEY=<applied>".
dxb_scrub_key() {
  local file=$1
  if k="$2" awk '{ if ($0 ~ ("^[[:blank:]]*" ENVIRON["k"] "[[:blank:]]*=")) print ENVIRON["k"] "=<applied>"; else print }' "$file" > "$file.dxbtmp"; then
    mv "$file.dxbtmp" "$file"
  else
    rm -f "$file.dxbtmp"
    return 1
  fi
}

provision_scrub() {
  local cfg k v scrub_ok=1
  cfg="$(dxb_boot_dir)/dxberry.txt"
  for k in $DXB_SECRET_KEYS; do
    v=${DXB_CFG[$k]:-}
    [[ -n $v && $v != "$DXB_APPLIED" ]] || continue
    if dxb_scrub_key "$cfg" "$k"; then
      # shellcheck disable=SC2004
      DXB_CFG[$k]=$DXB_APPLIED
    else
      dxb_step_failed scrub "could not scrub $k from dxberry.txt"
      scrub_ok=0
    fi
  done
  if [[ -f $DXB_DIETPI_TXT ]]; then
    v=$(sed -n 's/^[[:blank:]]*AUTO_SETUP_GLOBAL_PASSWORD=//p' "$DXB_DIETPI_TXT" | head -1)
    if [[ -n $v ]]; then
      if ! dxb_set_kv "$DXB_DIETPI_TXT" AUTO_SETUP_GLOBAL_PASSWORD ''; then
        dxb_step_failed scrub "could not clear AUTO_SETUP_GLOBAL_PASSWORD from dietpi.txt"
        scrub_ok=0
      fi
    fi
  fi
  if [[ -f $DXB_DIETPI_WIFI ]] && grep -qE "^aWIFI_KEY\[[0-9]\]='.+'" "$DXB_DIETPI_WIFI"; then
    dxb_warn "$DXB_DIETPI_WIFI still held a WiFi key; removed it"
    rm -f "$DXB_DIETPI_WIFI"
  fi
  if (( scrub_ok )); then
    dxb_status_add "secrets: scrubbed from dxberry.txt"
  fi
  return 0
}
