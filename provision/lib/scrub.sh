#!/bin/bash
# shellcheck shell=bash
# Replace applied secrets with <applied> so plaintext never lingers on the FAT partition.

: "${DXB_DIETPI_TXT:=/boot/dietpi.txt}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"

# dxb_scrub_key FILE KEY: rewrite every "KEY = value" line as "KEY=<applied>".
dxb_scrub_key() {
  local file=$1
  k="$2" awk '{ if ($0 ~ ("^[[:blank:]]*" ENVIRON["k"] "[[:blank:]]*=")) print ENVIRON["k"] "=<applied>"; else print }' "$file" > "$file.dxbtmp" && mv "$file.dxbtmp" "$file"
}

provision_scrub() {
  local cfg k v
  cfg="$(dxb_boot_dir)/dxberry.txt"
  for k in $DXB_SECRET_KEYS; do
    v=${DXB_CFG[$k]:-}
    [[ -n $v && $v != "$DXB_APPLIED" ]] || continue
    dxb_scrub_key "$cfg" "$k"
    # shellcheck disable=SC2004
    DXB_CFG[$k]=$DXB_APPLIED
  done
  if [[ -f $DXB_DIETPI_TXT ]]; then
    v=$(sed -n 's/^[[:blank:]]*AUTO_SETUP_GLOBAL_PASSWORD=//p' "$DXB_DIETPI_TXT" | head -1)
    [[ -z $v ]] || dxb_set_kv "$DXB_DIETPI_TXT" AUTO_SETUP_GLOBAL_PASSWORD ''
  fi
  if [[ -f $DXB_DIETPI_WIFI ]] && grep -qE "^aWIFI_KEY\[[0-9]\]='.+'" "$DXB_DIETPI_WIFI"; then
    dxb_warn "$DXB_DIETPI_WIFI still held a WiFi key; removed it"
    rm -f "$DXB_DIETPI_WIFI"
  fi
  dxb_status_add "secrets: scrubbed from dxberry.txt"
  return 0
}
