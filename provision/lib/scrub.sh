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
  local cfg k v scrubbed=() remaining=()
  cfg="$(dxb_boot_dir)/dxberry.txt"
  for k in $DXB_SECRET_KEYS; do
    # DXB_CFG_LINES only holds keys that really appear in dxberry.txt: a key that was defaulted
    # (WEBUI_PASSWORD falling back to PASSWORD) has no line to rewrite and no plaintext of its own.
    [[ -n ${DXB_CFG_LINES[$k]:-} ]] || continue
    v=${DXB_CFG[$k]:-}
    [[ -n $v && $v != "$DXB_APPLIED" ]] || continue
    if ! dxb_secret_was_consumed "$k"; then
      remaining+=("$k")
      continue
    fi
    if dxb_scrub_key "$cfg" "$k"; then
      # shellcheck disable=SC2004
      DXB_CFG[$k]=$DXB_APPLIED
      scrubbed+=("$k")
    else
      dxb_step_failed scrub "could not scrub $k from dxberry.txt"
      remaining+=("$k")
    fi
  done
  if [[ -f $DXB_DIETPI_TXT ]]; then
    v=$(sed -n 's/^[[:blank:]]*AUTO_SETUP_GLOBAL_PASSWORD=//p' "$DXB_DIETPI_TXT" | head -1)
    if [[ -n $v ]]; then
      if ! dxb_set_kv "$DXB_DIETPI_TXT" AUTO_SETUP_GLOBAL_PASSWORD ''; then
        dxb_step_failed scrub "could not clear AUTO_SETUP_GLOBAL_PASSWORD from dietpi.txt"
      fi
    fi
  fi
  if dxb_secret_was_consumed WIFI_PASSWORD && [[ -f $DXB_DIETPI_WIFI ]] && grep -qE "^aWIFI_KEY\[[0-9]\]='.+'" "$DXB_DIETPI_WIFI"; then
    dxb_warn "$DXB_DIETPI_WIFI still held a WiFi key; removed it"
    rm -f "$DXB_DIETPI_WIFI"
  fi
  # Only claim what actually happened: nothing to scrub means no line at all.
  if (( ${#scrubbed[@]} )); then
    dxb_status_add "secrets: scrubbed ${scrubbed[*]}"
  fi
  if (( ${#remaining[@]} )); then
    dxb_status_add "secrets: ${remaining[*]} left in dxberry.txt (the step that needed it did not complete; fix and re-run sudo dxberry-provision)"
  fi
  return 0
}
