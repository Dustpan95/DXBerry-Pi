#!/bin/bash
# shellcheck shell=bash
# Metadata only: no sound device, settings, service action or raw log export.
# Sourced only for dxberry-status --json --capture [--previous-boot].

dxb_status_capture() (
  set -o pipefail
  umask 077
  local selection=${1:-current} proc=${DXB_PROC:-/proc} journal=${DXB_CAPTURE_JOURNALCTL:-journalctl}
  [[ $selection == current || $selection == previous ]] || return 2
  local timeout_cmd=${DXB_CAPTURE_TIMEOUT:-timeout} work current='' selected='' scope=$selection
  local f state owner tgid name format rate period buffer entry count=0 streams_clipped=false
  work=$(mktemp -d) || return 1
  trap 'rm -rf -- "$work"' EXIT
  : > "$work/streams"
  [[ ! -r $proc/sys/kernel/random/boot_id ]] || IFS= read -r current < "$proc/sys/kernel/random/boot_id"
  current=${current//-/}
  [[ $current =~ ^[0-9a-f]{32}$ ]] || current=''
  selected=$current
  if [[ $selection == previous ]]; then
    selected=''
    # Do not choose by wall time: a clock correction can overlap adjacent boots.
    if "$timeout_cmd" 5 "$journal" --list-boots --no-pager 2>/dev/null | head -c 131073 > "$work/boots" &&
      (( $(wc -c < "$work/boots") <= 131072 )); then
      selected=$(awk '$1 == "-1" {print $2; exit}' "$work/boots")
    fi
    [[ $selected =~ ^[0-9a-f]{32}$ ]] || selected=''
  fi

  for f in "$proc"/asound/card[0-9]*/pcm[0-9]*[cp]/sub[0-9]*/status; do
    [[ -f $f ]] || continue
    if (( count >= 32 )); then streams_clipped=true; break; fi
    count=$((count+1))
    state='' owner='' tgid='' name='' format='' rate='' period='' buffer=''
    while read -r entry f1 f2 _; do
      case $entry in state:) state=$f1 ;; owner_pid) owner=$f2 ;; closed) state=closed ;; esac
    done < "$f"
    if [[ $owner =~ ^[1-9][0-9]{0,9}$ && -r $proc/$owner/status ]]; then
      # ALSA owner_pid can identify an opening task, not its process leader.
      while IFS=: read -r entry f1; do
        case $entry in Tgid) tgid=${f1//[[:space:]]/} ;; Name) name=${f1#$'\t'} ;; esac
      done < "$proc/$owner/status"
    fi
    [[ $owner =~ ^[1-9][0-9]{0,9}$ ]] || owner=null
    [[ $tgid =~ ^[1-9][0-9]{0,9}$ ]] || tgid=null
    if [[ -r ${f%/status}/hw_params ]]; then
      while read -r entry f1 _; do
        case $entry in format:) format=$f1 ;; rate:) rate=$f1 ;; period_size:) period=$f1 ;; buffer_size:) buffer=$f1 ;; esac
      done < "${f%/status}/hw_params"
    fi
    [[ $rate =~ ^[1-9][0-9]{0,9}$ ]] || rate=null
    [[ $period =~ ^[1-9][0-9]{0,9}$ ]] || period=null
    [[ $buffer =~ ^[1-9][0-9]{0,9}$ ]] || buffer=null
    jq -cn --arg pcm "${f#"$proc"/asound/}" --arg state "${state:-unknown}" --arg task "$name" --arg format "$format" \
      --argjson owner "$owner" --argjson tgid "$tgid" --argjson rate "$rate" --argjson period "$period" --argjson buffer "$buffer" \
      '{pcm:($pcm|sub("/status$";"")),state:$state,owner_task_id:$owner,owner_process_id:$tgid,owner_task_name:(if $task=="" then null else $task end),format:(if $format=="" then null else $format end),sample_rate:$rate,period_frames:$period,buffer_frames:$buffer}' >> "$work/streams" || return 1
  done

  local reason='' rc=0 bytes=0
  if [[ -z $current || -z $selected ]]; then
    reason=boot_unavailable
  else
    # Read newest first. The final (oldest) record only detects truncation.
    if "$timeout_cmd" 5 "$journal" --boot="$selected" --unit=graywolf.service --grep=POLLERR --reverse --no-pager --output=json -n 1001 2>/dev/null |
      head -c 2097153 > "$work/journal"; then rc=0; else rc=${PIPESTATUS[0]}; fi
    bytes=$(wc -c < "$work/journal")
    if (( bytes > 2097152 )); then reason=output_limit
    elif (( rc == 124 )); then reason=deadline
    elif (( rc != 0 )); then reason=journal_unavailable
    fi
  fi
  if [[ -z $reason ]]; then
    if ! jq -sc --arg boot "$selected" '
      def stamp: if type=="string" and test("^[0-9]{1,16}$") then tonumber else null end;
      if any(.[]; type!="object" or (._BOOT_ID|type)!="string" or (._BOOT_ID|test("^[0-9a-f]{32}$")|not) or (.MESSAGE|type)!="string") then error("record") else . end |
      . as $all | (length>1000) as $clipped | .[:1000] as $recent |
      [$recent[] | select(._BOOT_ID==$boot and (.MESSAGE|type)=="string" and (.MESSAGE|contains("POLLERR")))] as $events |
      [$events[] | .__MONOTONIC_TIMESTAMP | stamp | select(.!=null)] as $mono |
      [$events[] | .__REALTIME_TIMESTAMP | stamp | select(.!=null)] as $real |
      {available:true,record_limit:1000,truncated:$clipped,records_returned:($all|length),records_considered:($events|length),
       rejected_other_boot_records:([$recent[]|select(._BOOT_ID!=$boot)]|length),
       input:([$events[]|select(.MESSAGE|contains("input"))]|length),
       output:([$events[]|select(.MESSAGE|contains("output"))]|length),
       unclassified:([$events[]|select((.MESSAGE|contains("input") or contains("output"))|not)]|length),
       monotonic_timestamps_complete:(($mono|length)==($events|length)),
       monotonic_span_us:(if ($events|length)>0 and ($mono|length)==($events|length) then ($mono|max)-($mono|min) else null end),
       realtime_bounds_us:{minimum:($real|min),maximum:($real|max)},logging_rate_limit_applies:true,counts_are_audio_gaps:false}
    ' "$work/journal" > "$work/summary" 2>/dev/null; then reason=invalid_journal; fi
  fi
  if [[ -n $reason ]]; then
    jq -cn --arg reason "$reason" '{available:false,reason:$reason}' > "$work/summary" || return 1
  fi
  jq -cn --arg current "$current" --arg boot "$selected" --arg scope "$scope" --argjson clipped "$streams_clipped" \
    --slurpfile streams "$work/streams" --slurpfile journal "$work/summary" \
    '{version:1,kind:"capture-diagnostics",selected_boot:$scope,selected_boot_id:(if $boot=="" then null else $boot end),
      live:{boot_id:(if $current=="" then null else $current end),streams:$streams,truncated:$clipped},journal:$journal[0],
      reads_audio:false,changes_settings:false}'
)
