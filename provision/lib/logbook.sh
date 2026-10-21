#!/bin/bash
# shellcheck shell=bash
# The logbook (applications spec): one Wavelog or Cloudlog server - anyone's, or this Pi's own
# Wavelog - that every program logs to. The setting lives in logbook.json (0600, root); the
# contacts WSJT-X and JS8Call broadcast reach "dxberry-logbook submit" through the logging bridge
# (dxberry-logbridge.service: socat on 127.0.0.1:2333, one process per datagram) and are posted,
# or queued while the server cannot be reached and posted by "flush" (dxberry-logbook-flush.timer).
# "recent" reads the newest contacts back for the dashboard.
#
# The API key is a secret: it is read from stdin, kept only in logbook.json, handed to curl only
# inside a -K config on a pipe and to jq only on stdin or through a file, and never printed or
# logged (get shows it as "set"). Every process's arguments are readable by any user on the Pi.
#
# The servers' API, read from their source (Wavelog 3.2.4 and Cloudlog 2.8.20,
# application/controllers/Api.php; the test fixtures name the lines):
#   POST index.php/api/version {"key"}                       Wavelog: {"status":"ok","version"}
#   GET  index.php/api/auth/<key>                            both: <status>Valid</status>
#   POST index.php/api/qso {"key","station_profile_id","type":"adif","string"}
#        both: HTTP 201 {"status":"created"}; Wavelog: HTTP 400 {"status":"abort"} for a record it
#        read and will not import (a duplicate among them)
#   POST index.php/api/get_contacts_adif {"key","station_id","fetchfromid"}
#        Wavelog: {"status":"successful","lastfetchedid","adif"} (oldest first, ids > fetchfromid)
#   GET  index.php/api/recent_qsos/<public slug>/<limit>     Cloudlog: {"qsos":[...]} newest first
#
# Concurrency: socat runs one submit per datagram and the timer runs flush, all at once. A flush
# holds logbook-flush.lock (in the run directory) for its whole run, so two flushes never post the
# same file; a second one leaves at once. Only flush removes queued files. Submit never touches a
# queued file: it writes a temp file (.in.*) and renames it to a new name under the queue's own
# .lock, which is held only for that and never across a request. A contact is never deleted
# before the server took it: a submit killed while posting leaves its .in.* file, which the next
# flush adopts into the queue; a contact the server refuses for good is kept in refused/ with the
# server's reason beside it, and once refused/ holds DXB_LOGBOOK_MAX_REFUSED the next one stays
# queued (the queue grows and the page shows why).
# Expects common.sh to be sourced; follow_stopped also screen.sh, radio.sh and apps.sh.

: "${DXB_STATE_DIR:=/var/lib/dxberry}"
: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_LOGBOOK_CURL:=curl}"
: "${DXB_ICONV:=iconv}"
: "${DXB_SOCAT:=/usr/bin/socat}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
# the queue keeps at most this many contacts (the oldest are kept), each at most this many bytes
: "${DXB_LOGBOOK_MAX_QUEUE:=1000}"
: "${DXB_LOGBOOK_MAX_RECORD:=65536}"
# contacts the server refused for good, kept in refused/ (never deleted); when it holds this many,
# a refused contact stays in the queue instead
: "${DXB_LOGBOOK_MAX_REFUSED:=100}"
# a submit's temp file this many minutes old belongs to a submit that was killed: flush adopts it
: "${DXB_LOGBOOK_STALE_MIN:=2}"
: "${DXB_LOGBOOK_CACHE_S:=60}"
# this Pi's own Wavelog, as the bridge reaches it (plain http, loopback only) and as a browser does
: "${DXB_LOGBOOK_LOCAL_URL:=http://127.0.0.1:8083/}"

# paths, worked out when used (a test may set them later)
_dxb_lb_file() { printf '%s\n' "${DXB_LOGBOOK_FILE:-$DXB_STATE_DIR/logbook.json}"; }
_dxb_lb_queue() { printf '%s\n' "${DXB_LOGBOOK_QUEUE:-$DXB_STATE_DIR/logbook-queue}"; }
_dxb_lb_wavelog_state() { printf '%s\n' "${DXB_LOGBOOK_WAVELOG:-$DXB_STATE_DIR/logbook-wavelog.json}"; }
_dxb_lb_recent_cache() { printf '%s\n' "${DXB_LOGBOOK_RECENT:-$DXB_RUN_DIR/logbook-recent.json}"; }

# ---- the setting ---------------------------------------------------------------------------
# dxb_logbook_url URL: URL as it is stored - a trailing slash added, a trailing index.php dropped.
# Only https:// (a host name or address, an optional port and a plain path), or this Pi's own
# Wavelog on its loopback listener (http://127.0.0.1:8083/ or http://localhost:8083/): the key
# must never cross the network in clear. 2 refused.
dxb_logbook_url() {
  local u=$1 port
  [[ -n $u && $u != *[[:space:][:cntrl:]]* ]] || return 2
  u=${u%/}; u=${u%/index.php}
  if [[ $u =~ ^http://(127\.0\.0\.1|localhost):8083$ ]]; then printf '%s/\n' "$u"; return 0; fi
  [[ $u =~ ^https://([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*|\[[0-9A-Fa-f:.]+\])(:([0-9]{1,5}))?(/[A-Za-z0-9._~%-]+)*$ ]] || return 2
  port=${BASH_REMATCH[6]}
  [[ -z $port ]] || (( 10#$port >= 1 && 10#$port <= 65535 )) || return 2
  printf '%s/\n' "$u"
}

# dxb_logbook_stored: the stored setting, one JSON line (it holds the key: never print it), or 1
# when none is set (or the file is not a setting).
dxb_logbook_stored() {
  local f; f=$(_dxb_lb_file)
  [[ -f $f && -r $f ]] || return 1
  jq -ce 'select(type == "object" and (.url | type) == "string" and .url != "" and (.key | type) == "string" and .key != "")' < "$f" 2> /dev/null
}

# dxb_logbook_fldigi_values: {"url","key","station_id"} of the current logbook, or {} when none
# is set (what fldigi's first fill and follow are told: Task 8).
dxb_logbook_fldigi_values() {
  local c
  if c=$(dxb_logbook_stored); then jq -c '{url, key, station_id}' <<< "$c"; else echo '{}'; fi
}

# _dxb_lb_input JSON: a set/test request ({"url","key","station_id","slug"}) checked and merged
# with the stored setting (a missing key keeps the stored one), one JSON line. 2 with the reason
# on stderr.
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_input() {
  local in=$1 old=/dev/null c url key sid slug
  jq -e 'type == "object" and ([.url, .key, .station_id, .slug][] | . == null or type == "string" or type == "number")' <<< "$in" > /dev/null 2>&1 \
    || { echo "the logbook setting is one JSON object: {\"url\",\"key\",\"station_id\",\"slug\"}" >&2; return 2; }
  # a stored file that is not a setting is ignored (set replaces it)
  dxb_logbook_stored > /dev/null && old=$(_dxb_lb_file)
  c=$(jq -c --slurpfile old "$old" '(($old[0] // {}) | if type == "object" then . else {} end) as $o
    | {url: (.url // "" | tostring),
       key: (if (.key // "" | tostring) != "" then (.key | tostring) else ($o.key // "" | tostring) end),
       station_id: (if (.station_id // "" | tostring) != "" then (.station_id | tostring) else "1" end),
       slug: (.slug // "" | tostring)}' <<< "$in") || { echo "the logbook setting could not be read" >&2; return 2; }
  url=$(jq -r .url <<< "$c")
  url=$(dxb_logbook_url "$url") || { echo "the logbook URL must start with https:// (or be this Pi's own Wavelog, http://127.0.0.1:8083/)" >&2; return 2; }
  key=$(jq -r .key <<< "$c")
  [[ -n $key ]] || { echo "the logbook needs an API key" >&2; return 2; }
  [[ $key =~ ^[A-Za-z0-9._~-]{1,128}$ ]] || { echo "that is not an API key (letters, digits and . _ ~ - only)" >&2; return 2; }
  sid=$(jq -r .station_id <<< "$c"); slug=$(jq -r .slug <<< "$c")
  [[ $sid =~ ^[0-9]{1,9}$ ]] || { echo "the station id is a number (the station location's id in the logbook)" >&2; return 2; }
  [[ $slug =~ ^[A-Za-z0-9_-]{0,64}$ ]] || { echo "the public slug has letters, digits, - and _ only" >&2; return 2; }
  jq -c --arg u "$url" '.url = $u' <<< "$c"
}

# _dxb_lb_save JSON TYPE: logbook.json, 0600, written atomically. 0 ok, 6 failed.
_dxb_lb_save() {
  local f tmp
  f=$(_dxb_lb_file)
  ( umask 077; mkdir -p "${f%/*}" ) || return 6
  tmp=$(umask 077; mktemp "${f%/*}/.logbook.json.XXXXXX") || return 6
  if jq -c --arg t "$2" '{url, type: $t, key, station_id, slug}' <<< "$1" > "$tmp" && chmod 600 "$tmp" && mv -f "$tmp" "$f"; then return 0; fi
  rm -f "$tmp"
  return 6
}

# _dxb_lb_fingerprint JSON: what the cached answers are for (the server, type, station, slug; never
# the key), a sha256.
_dxb_lb_fingerprint() { jq -r '[.url, (.type // ""), .station_id, .slug] | join(" ")' <<< "$1" | sha256sum | cut -d' ' -f1; }

# ---- requests ------------------------------------------------------------------------------
# _dxb_lb_request URL MAX_S [post]: one request. The URL, headers and write-out are a curl config
# on a pipe (-K), so nothing in them is in an argument; a post's body is stdin. https:// may only
# be fetched (and redirected) over https; the local Wavelog's http:// over http too. Prints the
# answer, then a last line with the HTTP code (000 when nothing answered); exits with curl's
# status. curl's own messages are dropped, never shown: a Cloudlog URL carries the key.
_dxb_lb_request() {
  local url=$1 max=$2 p='=https'
  [[ $url == http://* ]] && p='=https,http'
  if [[ ${3:-} == post ]]; then
    "$DXB_LOGBOOK_CURL" -sS --connect-timeout 5 --max-time "$max" --proto "$p" --proto-redir "$p" -K <(
      printf 'url = "%s"\nheader = "Content-Type: application/json"\nheader = "Accept: application/json"\ndata-binary = "@-"\nwrite-out = "\\n%%{http_code}"\n' "$url") 2> /dev/null
  else
    "$DXB_LOGBOOK_CURL" -sS --connect-timeout 5 --max-time "$max" --proto "$p" --proto-redir "$p" -K <(
      printf 'url = "%s"\nwrite-out = "\\n%%{http_code}"\n' "$url") < /dev/null 2> /dev/null
  fi
}

# _dxb_lb_call URL MAX_S [post]: _dxb_lb_request into _LB_CODE (the HTTP code, 000 for none) and
# _LB_BODY. Called with its stdin redirected, never at the end of a pipe (it sets globals).
_LB_CODE=000
_LB_BODY=''
_LB_CURL_RC=0
_dxb_lb_call() {
  local out
  out=$(_dxb_lb_request "$@"); _LB_CURL_RC=$?
  if [[ $out == *$'\n'* ]]; then _LB_CODE=${out##*$'\n'}; _LB_BODY=${out%$'\n'*}; else _LB_CODE=$out; _LB_BODY=''; fi
  [[ $_LB_CODE =~ ^[0-9]{3}$ ]] || _LB_CODE=000
  [[ $_LB_CODE != 000 ]]
}

# _dxb_lb_noanswer URL: why nothing answered, from curl's exit status (its own messages are not
# shown: the URL may carry the key).
_dxb_lb_noanswer() {
  local why
  case $_LB_CURL_RC in
    6) why="its name could not be resolved" ;;
    7) why="nothing answered on its port" ;;
    28) why="it did not answer in time" ;;
    35|51|53|54|58|59|60|77|80|83|90|91) why="the TLS connection failed (is its certificate valid?)" ;;
    1) why="it would have needed a protocol other than https" ;;
    *) why="curl exit $_LB_CURL_RC" ;;
  esac
  printf 'the logbook at %s did not answer: %s\n' "$1" "$why"
}

# _dxb_lb_reason: the server's own reason in _LB_BODY ("reason", or Wavelog's "messages"), as
# one line without markup; empty when it gave none.
_dxb_lb_reason() {
  jq -r '[(.reason // empty), ((.messages // [])[] | strings)] | map(select(. != "")) | join("; ")' <<< "$_LB_BODY" 2> /dev/null \
    | sed 's/<[^>]*>/ /g; s/  */ /g; s/^ //; s/ $//' | head -c 300
}

# _dxb_lb_detect JSON: what answers at the setting's URL with its key, as
# {"ok","type","version","error"}: Wavelog answers api/version, Cloudlog (which has no
# api/version) api/auth/<key>. 0 a logbook answered, 6 none did.
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_detect() {
  local c=$1 url key answered=0 err
  url=$(jq -r .url <<< "$c"); key=$(jq -r .key <<< "$c")
  if _dxb_lb_call "${url}index.php/api/version" 15 post < <(jq -c '{key}' <<< "$c"); then
    answered=1
    if [[ $_LB_CODE == 200 ]] && jq -e '.status == "ok"' <<< "$_LB_BODY" > /dev/null 2>&1; then
      jq -c '{ok: true, type: "wavelog", version: (.version // null | if . == null then null else tostring end), error: null}' <<< "$_LB_BODY"
      return 0
    fi
    if [[ $_LB_CODE == 401 ]] && jq -e '.status == "failed"' <<< "$_LB_BODY" > /dev/null 2>&1; then
      jq -cn '{ok: false, type: null, version: null, error: "Wavelog answered but refused the API key"}'
      return 6
    fi
  fi
  if _dxb_lb_call "${url}index.php/api/auth/$key" 15; then
    answered=1
    if [[ $_LB_CODE == 200 && $_LB_BODY == *'<status>Valid</status>'* ]]; then
      jq -cn '{ok: true, type: "cloudlog", version: null, error: null}'
      return 0
    fi
    if [[ $_LB_BODY == *'<auth>'* ]]; then
      jq -cn '{ok: false, type: null, version: null, error: "the logbook answered but refused the API key"}'
      return 6
    fi
  fi
  if (( answered )); then err="no Wavelog or Cloudlog answered at $url"; else err=$(_dxb_lb_noanswer "$url"); fi
  jq -cn --arg e "$err" '{ok: false, type: null, version: null, error: $e}'
  return 6
}

# ---- posting and the queue -----------------------------------------------------------------
# _dxb_lb_post JSON FILE: post the ADIF record in FILE to the logbook. 0 the server took it, 1 it
# read the record and refused it for good (Wavelog's 400 "abort": a duplicate, or a station
# callsign that differs from the station's; Cloudlog's 201 that imported nothing and says why),
# 2 not posted (no answer, the key or station refused, the server failing). The reason goes to
# stderr; a refusal's also into _LB_WHY (one line, the server's words).
_LB_WHY=''
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_post() {
  local c=$1 f=$2 url why
  _LB_WHY=''
  url=$(jq -r .url <<< "$c")
  if ! _dxb_lb_call "${url}index.php/api/qso" 15 post < <(jq -c --rawfile adif "$f" '{key, station_profile_id: (.station_id // "1"), type: "adif", string: $adif}' <<< "$c"); then
    _dxb_lb_noanswer "$url" >&2; return 2
  fi
  why=$(_dxb_lb_reason)
  case $_LB_CODE in
    200|201)
      if jq -e '.status == "created" or .status == "ok"' <<< "$_LB_BODY" > /dev/null 2>&1; then
        # Cloudlog answers 201 "created" also when it imported nothing; a duplicate is in the log already
        jq -e '(.imported_count? // 1) == 0' <<< "$_LB_BODY" > /dev/null 2>&1 || return 0
        [[ ${why,,} != *duplicate* ]] || return 0
        _LB_WHY=${why:-the logbook imported nothing}
        echo "the logbook did not import the contact: $_LB_WHY" >&2; return 1
      fi
      echo "the logbook did not take the contact${why:+: $why}" >&2; return 2 ;;
    400)
      if jq -e '.status == "abort"' <<< "$_LB_BODY" > /dev/null 2>&1; then
        _LB_WHY=${why:-the logbook refused the contact}
        echo "the logbook refused the contact: $_LB_WHY" >&2; return 1
      fi ;;
  esac
  echo "the logbook answered HTTP $_LB_CODE${why:+: $why}" >&2
  return 2
}

# _dxb_lb_queue_dir: the queue directory (0700) exists. 0 ok, 6 not.
_dxb_lb_queue_dir() {
  local q; q=$(_dxb_lb_queue)
  { ( umask 077; mkdir -p "$q" ) && chmod 700 "$q"; } 2> /dev/null || { echo "could not create $q" >&2; return 6; }
}
# _dxb_lb_queue_files DIR: the queued files' names in DIR, oldest first (<epoch>-<n>.adi).
_dxb_lb_queue_files() {
  find "$1" -maxdepth 1 -type f -name '[0-9]*-[0-9]*.adi' -printf '%f\n' 2> /dev/null | sort -t- -k1,1n -k2,2n
}
_dxb_lb_queue_count() { _dxb_lb_queue_files "$1" | wc -l; }

# _dxb_lb_lock_queue / _dxb_lb_unlock_queue: the queue's own .lock (fd 6; in the queue directory,
# which the sandboxed units may always write), held only while a name is chosen and a file
# renamed. 0 locked (also without flock), 6 not (nothing was moved or removed).
_dxb_lb_lock_queue() {
  _dxb_lb_queue_dir || return 6
  exec 6> "$(_dxb_lb_queue)/.lock" || return 6
  command -v flock > /dev/null 2>&1 || return 0
  flock -w 10 6 || { echo "the logbook queue is busy" >&2; return 6; }
}
_dxb_lb_unlock_queue() { exec 6>&-; }

# _dxb_lb_place TMP DIR: rename TMP to DIR/<epoch>-<n>.adi (a name nobody has), 0600. Under the
# queue lock. Prints the name.
_dxb_lb_place() {
  local e i=1
  e=$(date +%s)
  while [[ -e $2/$e-$i.adi ]]; do i=$(( i + 1 )); done
  chmod 600 "$1" && mv -f "$1" "$2/$e-$i.adi" && printf '%s\n' "$e-$i.adi"
}

# _dxb_lb_enqueue TMP: TMP (a checked record, a .in.* file in the queue directory) into the queue,
# unless it holds DXB_LOGBOOK_MAX_QUEUE contacts already (the oldest are kept: TMP is refused and
# removed). 0 queued; 6 not - full, or the lock or rename failed, in which case TMP stays where it
# is and the next flush adopts it.
_dxb_lb_enqueue() {
  local q n name
  q=$(_dxb_lb_queue)
  _dxb_lb_lock_queue || { echo "could not lock the logbook queue; the contact waits in $1 for the next flush" >&2; return 6; }
  n=$(_dxb_lb_queue_count "$q")
  if (( n >= DXB_LOGBOOK_MAX_QUEUE )); then
    _dxb_lb_unlock_queue; rm -f "$1"
    echo "the logbook queue is full ($DXB_LOGBOOK_MAX_QUEUE contacts wait for the server); this contact was not kept" >&2
    return 6
  fi
  name=$(_dxb_lb_place "$1" "$q") || { _dxb_lb_unlock_queue; echo "could not queue the contact; it waits in $1 for the next flush" >&2; return 6; }
  _dxb_lb_unlock_queue
  dxb_info "logbook: contact queued ($name)"
}

# _dxb_lb_set_aside FILE WHY: a contact the server refused for good goes to the queue's refused/
# directory, the server's reason WHY beside it (<name>.why, one line), out of the queue's way.
# Nothing there is ever deleted. 0 set aside; 1 not - refused/ holds DXB_LOGBOOK_MAX_REFUSED
# already, or it could not be made, locked or written - and FILE is left exactly where it was.
_dxb_lb_set_aside() {
  local r name why
  r=$(_dxb_lb_queue)/refused
  why=$(printf '%s' "$2" | tr -s '[:cntrl:]' ' ' | head -c 300)
  { ( umask 077; mkdir -p "$r" ) && chmod 700 "$r"; } 2> /dev/null || { echo "could not create $r" >&2; return 1; }
  _dxb_lb_lock_queue || return 1
  if (( $(_dxb_lb_queue_count "$r") >= DXB_LOGBOOK_MAX_REFUSED )); then
    _dxb_lb_unlock_queue
    echo "$DXB_LOGBOOK_MAX_REFUSED refused contacts are kept in $r already; this one stays queued (check the station callsign in the program and in the logbook)" >&2
    return 1
  fi
  name=$(_dxb_lb_place "$1" "$r") || { _dxb_lb_unlock_queue; echo "could not move the refused contact to $r" >&2; return 1; }
  ( umask 077; printf '%s\n' "$why" > "$r/${name%.adi}.why" ) 2> /dev/null || dxb_warn "logbook: could not write the reason for $r/$name"
  _dxb_lb_unlock_queue
  dxb_warn "logbook: the refused contact is kept as $r/$name"
  return 0
}

# _dxb_lb_adopt: a submit's temp file (.in.*) older than DXB_LOGBOOK_STALE_MIN minutes belongs to
# a submit that was killed while it posted (a bridge restart, a shutdown): into the queue with it.
# One that holds no contact (cut off while it was read) is removed. Under the queue lock. Always 0.
_dxb_lb_adopt() {
  local q f name
  q=$(_dxb_lb_queue)
  [[ -d $q ]] || return 0
  _dxb_lb_lock_queue || return 0
  while IFS= read -r f; do
    if grep -qi '<call:' "$f" 2> /dev/null; then
      name=$(_dxb_lb_place "$f" "$q") && dxb_info "logbook: adopted a contact a stopped submit left ($name)"
    else
      rm -f "$f"
    fi
  done < <(find "$q" -maxdepth 1 -type f -name '.in.*' -mmin "+$DXB_LOGBOOK_STALE_MIN" 2> /dev/null)
  _dxb_lb_unlock_queue
  return 0
}

# dxb_logbook_submit: one ADIF record on stdin (a datagram from WSJT-X or JS8Call). A record that
# is not UTF-8 is read as Latin-1 (WSJT-X sends Latin-1). Posted when a logbook is set; queued
# when none is set or the server does not take it; set aside when the server refuses it for
# good (queued when refused/ is full). 0 posted, queued or set aside; 6 refused (over
# DXB_LOGBOOK_MAX_RECORD bytes, no contact in it, the queue full) or left for the next flush to
# adopt (the queue could not be locked or written).
dxb_logbook_submit() {
  local q tmp u size c rc
  _dxb_lb_queue_dir || return 6
  q=$(_dxb_lb_queue)
  tmp=$(umask 077; mktemp "$q/.in.XXXXXX") || { echo "could not write in $q" >&2; return 6; }
  head -c $(( DXB_LOGBOOK_MAX_RECORD + 1 )) > "$tmp"
  size=$(stat -c %s "$tmp" 2> /dev/null || echo 0)
  if (( size > DXB_LOGBOOK_MAX_RECORD )); then
    rm -f "$tmp"; echo "a contact of more than 64 KiB ($DXB_LOGBOOK_MAX_RECORD bytes) was refused" >&2; return 6
  fi
  if ! grep -qi '<call:' "$tmp"; then
    rm -f "$tmp"; echo "that is not an ADIF contact (no <call:...> field); nothing was logged" >&2; return 6
  fi
  if ! "$DXB_ICONV" -f UTF-8 -t UTF-8 "$tmp" > /dev/null 2>&1; then
    if ! { u=$(umask 077; mktemp "$q/.in.XXXXXX") && "$DXB_ICONV" -f ISO-8859-1 -t UTF-8 "$tmp" > "$u" 2> /dev/null && mv -f "$u" "$tmp"; }; then
      rm -f "${u:-}"
    fi
  fi
  if c=$(dxb_logbook_stored); then
    _dxb_lb_post "$c" "$tmp"; rc=$?
    case $rc in
      0) rm -f "$tmp"; return 0 ;;
      1) _dxb_lb_set_aside "$tmp" "$_LB_WHY" && return 0 ;;
    esac
  fi
  _dxb_lb_enqueue "$tmp"
}

# dxb_logbook_flush: post the queued contacts, oldest first, stopping at the first that is not
# posted; a contact the server refuses for good is set aside and the flush goes on (when it cannot
# be set aside - refused/ full - it stays queued and the flush stops). A killed submit's temp file
# is adopted first. Nothing to do without a logbook, or while another flush runs. 0 the queue is
# empty (or nothing to do), 6 contacts still wait.
dxb_logbook_flush() {
  local c q name n=0 left rc
  c=$(dxb_logbook_stored) || return 0
  q=$(_dxb_lb_queue)
  [[ -d $q ]] || return 0
  mkdir -p "$DXB_RUN_DIR" 2> /dev/null
  exec 5> "$DXB_RUN_DIR/logbook-flush.lock" || { echo "could not open $DXB_RUN_DIR/logbook-flush.lock" >&2; return 6; }
  if command -v flock > /dev/null 2>&1 && ! flock -n 5; then
    dxb_info "logbook: another flush is running"; exec 5>&-; return 0
  fi
  _dxb_lb_adopt
  while IFS= read -r name; do
    [[ -f $q/$name ]] || continue
    _dxb_lb_post "$c" "$q/$name"; rc=$?
    case $rc in
      0) rm -f "${q:?}/$name"; n=$(( n + 1 )) ;;
      1) _dxb_lb_set_aside "$q/$name" "$_LB_WHY" && continue
         left=$(_dxb_lb_queue_count "$q")
         echo "$left queued contacts wait for the logbook" >&2
         exec 5>&-; return 6 ;;
      *) left=$(_dxb_lb_queue_count "$q")
         echo "$left queued contacts wait for the logbook; they are posted again within five minutes" >&2
         exec 5>&-; return 6 ;;
    esac
  done < <(_dxb_lb_queue_files "$q")
  (( n == 0 )) || dxb_info "logbook: $n queued contacts posted"
  exec 5>&-
  return 0
}

# ---- recent contacts -----------------------------------------------------------------------
# _dxb_lb_adif_tsv: ADIF on stdin (any case, a header ending <EOH> or none), one line per contact
# on stdout: time_utc, call, band, mode (the submode when there is one), rst_sent, rst_rcvd and
# grid (the grid square, else the VUCC grids), tab-separated. Each field is taken by its length;
# scanning then goes on at the next "<", so a field whose length counts characters rather than
# bytes (Wavelog's mb_strlen) cannot throw the fields after it.
_dxb_lb_adif_tsv() {
  awk 'BEGIN { RS = "<[Ee][Oo][Rr]>" }
    {
      rec = $0
      if (!seen) { p = index(tolower(rec), "<eoh>"); if (p) rec = substr(rec, p + 5); seen = 1 }
      split("", f)
      while ((p = index(rec, "<")) > 0) {
        rec = substr(rec, p + 1)
        q = index(rec, ">"); if (q == 0) break
        tag = tolower(substr(rec, 1, q - 1)); rec = substr(rec, q + 1)
        if (split(tag, part, ":") < 2 || part[2] !~ /^[0-9]+$/) continue
        len = part[2] + 0
        v = substr(rec, 1, len); rec = substr(rec, len + 1)
        gsub(/[\t\r\n]/, " ", v)
        f[part[1]] = v
      }
      if (f["call"] == "") next
      d = f["qso_date"]; t = f["time_on"]
      if (length(t) == 4) t = t "00"
      iso = ""
      if (length(d) == 8 && d ~ /^[0-9]+$/ && length(t) == 6 && t ~ /^[0-9]+$/)
        iso = substr(d, 1, 4) "-" substr(d, 5, 2) "-" substr(d, 7, 2) "T" substr(t, 1, 2) ":" substr(t, 3, 2) ":" substr(t, 5, 2) "Z"
      m = f["submode"]; if (m == "") m = f["mode"]
      g = f["gridsquare"]; if (g == "") g = f["vucc_grids"]
      printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", iso, toupper(f["call"]), f["band"], m, f["rst_sent"], f["rst_rcvd"], g
    }'
}

# _dxb_lb_answer TYPE ERROR: a failed recent answer.
_dxb_lb_answer() { jq -cn --arg t "$1" --arg e "$2" '{ok: false, type: $t, contacts: [], error: $e}'; }

# _dxb_lb_recent_cloudlog JSON: Cloudlog's newest 25 contacts of the public slug's logbook (no key
# needed), as a recent answer.
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_recent_cloudlog() {
  local c=$1 url slug why
  url=$(jq -r .url <<< "$c"); slug=$(jq -r .slug <<< "$c")
  [[ -n $slug ]] || { _dxb_lb_answer cloudlog "Cloudlog shows recent contacts only for a logbook with a public slug: set it under Logbook"; return 0; }
  _dxb_lb_call "${url}index.php/api/recent_qsos/$slug/25" 15 || { _dxb_lb_answer cloudlog "$(_dxb_lb_noanswer "$url")"; return 0; }
  why=$(_dxb_lb_reason)
  if [[ $_LB_CODE == 404 && ( $why == 'No QSOs found' || $why == 'Empty Logbook' ) ]]; then
    jq -cn '{ok: true, type: "cloudlog", contacts: [], error: null}'; return 0
  fi
  [[ $_LB_CODE == 200 ]] || { _dxb_lb_answer cloudlog "the logbook answered HTTP $_LB_CODE${why:+: $why}"; return 0; }
  jq -c 'def s: if . == null then "" else tostring end;
    {ok: true, type: "cloudlog", error: null,
     contacts: [(.qsos // [])[] | {time_utc: (if (.date // "") != "" and (.time // "") != "" then .date + "T" + .time + ":00Z" else "" end),
       call: (.callsign | s), band: (.band | s), mode: (.mode | s), rst_sent: (.rst_sent | s), rst_rcvd: (.rst_rcvd | s),
       grid: (.gridsquare | s)}][:25]}
    | {ok, type, contacts, error}' <<< "$_LB_BODY" 2> /dev/null || _dxb_lb_answer cloudlog "Cloudlog's answer could not be read"
}

# _dxb_lb_recent_wavelog JSON FINGERPRINT: Wavelog's contacts newer than the last id fetched,
# merged into logbook-wavelog.json (the last id and the newest 25), as a recent answer. A failed
# fetch changes nothing.
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_recent_wavelog() {
  local c=$1 fp=$2 st url last=0 old='[]' merged next why tmp
  st=$(_dxb_lb_wavelog_state); url=$(jq -r .url <<< "$c")
  if [[ -f $st ]] && jq -e --arg fp "$fp" '.for == $fp and (.lastfetchedid | tostring | test("^[0-9]+$")) and (.contacts | type) == "array"' "$st" > /dev/null 2>&1; then
    last=$(jq -r '.lastfetchedid | tostring | tonumber' "$st"); old=$(jq -c '.contacts[:25]' "$st")
  fi
  _dxb_lb_call "${url}index.php/api/get_contacts_adif" 60 post < <(jq -c --argjson f "$last" '{key, station_id, fetchfromid: $f}' <<< "$c") \
    || { _dxb_lb_answer wavelog "$(_dxb_lb_noanswer "$url")"; return 0; }
  why=$(_dxb_lb_reason)
  if [[ $_LB_CODE != 200 ]] || ! jq -e '.status == "successful" and (.lastfetchedid | tostring | test("^[0-9]+$"))' <<< "$_LB_BODY" > /dev/null 2>&1; then
    _dxb_lb_answer wavelog "the logbook answered HTTP $_LB_CODE${why:+: $why}"; return 0
  fi
  # Wavelog's export answer carries the id as a string (max() of the database row's value), its
  # "no new QSOs" answer as the number that was sent: both are read
  next=$(jq -r '.lastfetchedid | tostring | tonumber' <<< "$_LB_BODY")
  merged=$(jq -r '.adif // ""' <<< "$_LB_BODY" | _dxb_lb_adif_tsv | jq -Rnc --argjson old "$old" '
    [inputs | split("\t") | select(length == 7)
      | {time_utc: .[0], call: .[1], band: .[2], mode: .[3], rst_sent: .[4], rst_rcvd: .[5], grid: .[6]}] as $new
    | (($old | reverse) + $new) | to_entries | sort_by(.value.time_utc, .key) | reverse | map(.value) | .[:25]') \
    || { _dxb_lb_answer wavelog "Wavelog's answer could not be read"; return 0; }
  if ! { tmp=$(umask 077; mktemp "${st%/*}/.logbook-wavelog.XXXXXX") \
      && jq -cn --arg fp "$fp" --argjson l "$next" --argjson c "$merged" '{for: $fp, lastfetchedid: $l, contacts: $c}' > "$tmp" \
      && mv -f "$tmp" "$st"; }; then
    rm -f "${tmp:-}"; dxb_warn "logbook: could not write $st; the contacts are fetched again next time"
  fi
  jq -cn --argjson c "$merged" '{ok: true, type: "wavelog", contacts: $c, error: null}'
}

# _dxb_lb_cached CACHE FINGERPRINT: the cached answer when CACHE holds one for FINGERPRINT that is
# younger than DXB_LOGBOOK_CACHE_S. 1 when there is none.
# shellcheck disable=SC2016  # jq filters, not shell text
_dxb_lb_cached() {
  [[ -f $1 ]] || return 1
  jq -ce --arg fp "$2" --argjson now "$(date +%s)" --argjson ttl "$DXB_LOGBOOK_CACHE_S" \
    'select(.for == $fp and (.at | type) == "number" and $now >= .at and $now - .at < $ttl) | .answer | select(type == "object")' "$1" 2> /dev/null
}

# dxb_logbook_recent N: the newest N (1-25) contacts, {"ok","type","contacts":[{time_utc, call,
# band, mode, rst_sent, rst_rcvd, grid}],"error"}, newest first. The answer (also a failed one,
# so a dead server is not asked again and again) is cached DXB_LOGBOOK_CACHE_S seconds in the run
# directory for the setting it was made for; one fetch at a time.
# shellcheck disable=SC2016  # jq filters, not shell text
dxb_logbook_recent() {
  local n=$1 c fp cache ans tmp
  c=$(dxb_logbook_stored) || { jq -cn '{ok: false, type: null, contacts: [], error: "no logbook is set"}'; return 0; }
  fp=$(_dxb_lb_fingerprint "$c"); cache=$(_dxb_lb_recent_cache)
  if ! ans=$(_dxb_lb_cached "$cache" "$fp"); then
    mkdir -p "$DXB_RUN_DIR" 2> /dev/null
    exec 4> "$DXB_RUN_DIR/logbook-recent.lock" && command -v flock > /dev/null 2>&1 && flock -w 75 4
    # another reader may have fetched while this one waited
    if ! ans=$(_dxb_lb_cached "$cache" "$fp"); then
      case $(jq -r '.type // ""' <<< "$c") in
        cloudlog) ans=$(_dxb_lb_recent_cloudlog "$c") ;;
        *) ans=$(_dxb_lb_recent_wavelog "$c" "$fp") ;;
      esac
      if ! { tmp=$(umask 077; mktemp "${cache%/*}/.logbook-recent.XXXXXX") \
          && jq -cn --arg fp "$fp" --argjson at "$(date +%s)" --argjson a "$ans" '{at: $at, for: $fp, answer: $a}' > "$tmp" \
          && mv -f "$tmp" "$cache"; }; then
        rm -f "${tmp:-}"
      fi
    fi
    exec 4>&-
  fi
  jq -c --argjson n "$n" '.contacts |= .[:$n]' <<< "$ans"
}

# ---- the program side ----------------------------------------------------------------------
# dxb_logbook_fldigi_follow_stopped: every session program that follows the logbook
# (app_<a>_logbook_follow: fldigi) and whose session is enabled but not running follows it now,
# through the same settings step as dxbapps that its prepare runs (dxb_screen_logbook_follow); a
# running (or starting, or stopping) one follows at its next start. Prints
# {"kept": ["<radio>: <KEY> (changed in <program>)", ...], "following_later": ["<radio>", ...]}.
# Always 0 (a failure is a warning).
dxb_logbook_fldigi_follow_stopped() {
  local r a u l
  local -a kept=() later=()
  if [[ -f $DXB_RADIOS_FILE ]] && dxb_radio_load > /dev/null 2>&1; then
    while IFS=$'\t' read -r r a; do
      [[ -n $a ]] || continue
      ( dxb_app_load "$a" && declare -F "app_${a}_logbook_follow" ) > /dev/null 2>&1 || continue
      u=$(dxb_app_session_unit "$a" "$r")
      [[ $("$DXB_SYSTEMCTL" is-enabled "$u" 2> /dev/null) == enabled ]] || continue
      case $("$DXB_SYSTEMCTL" is-active "$u" 2> /dev/null) in active|activating|reloading|deactivating) later+=("$r"); continue ;; esac
      while IFS= read -r l; do
        [[ $l == "kept "* ]] && kept+=("$r: ${l#kept }")
      done < <(dxb_screen_logbook_follow "$a-$r")
    done < <(jq -r '.radios | to_entries[] | [.key, (.value.owner // "")] | @tsv' <<< "$DXB_RADIOS")
  fi
  jq -cn --argjson k "$(jq -cn '$ARGS.positional' --args "${kept[@]}")" '{kept: $k, following_later: $ARGS.positional}' --args "${later[@]}"
}

# ---- provisioning --------------------------------------------------------------------------
# provision_logbook: the logging bridge, the flush service and its timer installed (daemon-reload
# only when one changed); the timer enabled and started; the bridge enabled, and started now when
# socat is installed (it comes with the first program that has a screen session; the unit waits
# for it: ConditionPathExists). A failure is a failed step named apps (provision_apps runs this).
# _dxb_logbook_install_units: the three units written when they differ; DXB_LB_UNITS_CHANGED=1 when
# one was. The one place they are installed. 0 ok, 1 failed (the step is marked).
_dxb_logbook_install_units() {
  local t content dest
  DXB_LB_UNITS_CHANGED=0
  mkdir -p "$DXB_SYSTEMD_DIR" 2> /dev/null
  for t in dxberry-logbridge.service dxberry-logbook-flush.service dxberry-logbook-flush.timer; do
    content=$(< "$DXB_TEMPLATES/$t") || { dxb_step_failed apps "the $t template is missing"; return 1; }
    dest=$DXB_SYSTEMD_DIR/$t
    if dxb_write_if_changed "$dest" "$content" 644; then
      [[ $(< "$dest") == "$content" ]] || { dxb_step_failed apps "could not write $dest"; return 1; }
      DXB_LB_UNITS_CHANGED=1
    fi
  done
  return 0
}

# (provision_apps passes --installed after it wrote the units and reloaded systemd itself)
provision_logbook() {
  local c
  if [[ ${1:-} != --installed ]]; then
    _dxb_logbook_install_units || return 1
    if (( DXB_LB_UNITS_CHANGED )); then systemctl daemon-reload || { dxb_step_failed apps "systemctl daemon-reload failed"; return 1; }; fi
  fi
  systemctl enable --now dxberry-logbook-flush.timer > /dev/null 2>&1 || dxb_step_failed apps "could not enable dxberry-logbook-flush.timer"
  if [[ -x $DXB_SOCAT ]]; then
    systemctl enable --now dxberry-logbridge.service > /dev/null 2>&1 || dxb_step_failed apps "could not start dxberry-logbridge.service"
  else
    systemctl enable dxberry-logbridge.service > /dev/null 2>&1 || dxb_step_failed apps "could not enable dxberry-logbridge.service"
  fi
  if c=$(dxb_logbook_stored); then
    dxb_status_add "logbook: $(jq -r '.url + " (" + (.type // "?") + ")"' <<< "$c"), $(_dxb_lb_queue_count "$(_dxb_lb_queue)") contacts queued"
  else
    dxb_status_add "logbook: not set (choose one on the DXBerry page)"
  fi
  return 0
}
