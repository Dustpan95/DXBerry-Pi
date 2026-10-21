#!/usr/bin/env bash
# The logbook (logbook.sh, dxberry-logbook). (Prefix lb_.)
# The server answers are tests/fixtures/logbook/*, each built from the server's own source (its
# header names the file, tag and lines). wsjtx-contact.adi is the datagram WSJT-X sends to its
# N1MM port: LogBook::QSOToADIF (WSJT-X v3.0.2, commit ccdfaf3, logbook/logbook.cpp lines 77-181)
# followed by " <eor>" (widgets/mainwindow.cpp lines 10643-10653).
# shellcheck disable=SC1091,SC2012,SC2016,SC2034,SC2317,SC2329  # SC2012: the brief's own tests count with ls
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_LIB/screen.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/logbook.sh"

# lb_env: a fresh logbook under $TEST_TMP/lb, run as the real command; LB_CMD is dxberry-logbook.
# Its curl ($DXB_LOGBOOK_CURL) is a stand-in server: it records its own arguments in
# $TEST_TMP/argv (one line per call), the URL in $TEST_TMP/urls, every request body in
# $TEST_TMP/bodies and the ADIF of every contact it accepted in $TEST_TMP/posted, and answers as
# LB_SERVER says: wavelog, cloudlog, none (a web server that is no logbook: 404 everywhere),
# down (nothing answers; curl prints the URL on stderr), dupe (Wavelog refusing every contact as a
# duplicate), cloudlog-dupe (Cloudlog's 201 that imported nothing: a duplicate) and
# cloudlog-noimport (the same for another reason).
lb_env() {
  rm -rf "$TEST_TMP/lb"
  mkdir -p "$TEST_TMP/lb/bin" "$TEST_TMP/lb/state" "$TEST_TMP/lb/run"
  export DXB_STATE_DIR=$TEST_TMP/lb/state DXB_RUN_DIR=$TEST_TMP/lb/run DXB_LOG_FILE=$TEST_TMP/lb/state/provision.log
  export DXB_LOGBOOK_FILE=$DXB_STATE_DIR/logbook.json DXB_LOGBOOK_QUEUE=$DXB_STATE_DIR/logbook-queue
  export DXB_LOGBOOK_CURL=$TEST_TMP/lb/bin/curl DXB_SYSTEMCTL=$TEST_TMP/lb/bin/systemctl DXB_FAKE_EUID=0
  export DXB_RADIOS_FILE=$DXB_STATE_DIR/radios.json DXB_APPS_OPT=$TEST_TMP/lb/opt DXB_APPS_STATE=$DXB_STATE_DIR/apps
  export DXB_APPS_TSV=$DXB_ROOT/provision/share/apps.tsv DXB_APPS_DIR=$DXB_LIB/apps DXB_PI_ADDRESS=192.0.2.10
  export LB_SERVER=wavelog LB_FIX=$DXB_ROOT/tests/fixtures/logbook
  LB_CMD=$DXB_ROOT/provision/bin/dxberry-logbook
  : > "$TEST_TMP/argv"; : > "$TEST_TMP/posted"; : > "$TEST_TMP/urls"; : > "$TEST_TMP/bodies"; : > "$TEST_TMP/calls"
  : > "$DXB_LOG_FILE"
  lb_server
  printf '#!/bin/bash\necho "systemctl $*" >> "$TEST_TMP/calls"\nexit 0\n' > "$TEST_TMP/lb/bin/systemctl"
  chmod 755 "$TEST_TMP/lb/bin/systemctl"
}
# lb_server: the stand-in server alone, $TEST_TMP/lb/bin/curl
lb_server() {
  mkdir -p "$TEST_TMP/lb/bin"
  cat > "$TEST_TMP/lb/bin/curl" << 'CURL'
#!/bin/bash
printf '%s\n' "$*" >> "$TEST_TMP/argv"
cfg=''; prev=''
for a in "$@"; do [[ $prev == -K ]] && cfg=$a; prev=$a; done
[[ -n $cfg ]] || { echo "lb curl: no -K config" >&2; exit 2; }
conf=$(< "$cfg")
url=$(sed -n 's/^url = "\(.*\)"$/\1/p' <<< "$conf")
body=''
if grep -q '^data-binary = "@-"$' <<< "$conf"; then body=$(cat); fi
printf '%s\n' "$url" >> "$TEST_TMP/urls"
[[ -z $body ]] || printf '%s\n' "$body" >> "$TEST_TMP/bodies"
# answer CODE FIXTURE: the fixture without its # header, then the write-out (the HTTP code)
answer() { sed '/^#/d' "$LB_FIX/$2"; printf '\n%s' "$1"; }
case $LB_SERVER in
  down) printf '\n000'; echo "curl: (7) Failed to connect to $url" >&2; exit 7 ;;
esac
case "$LB_SERVER:$url" in
  wavelog:*/index.php/api/version|dupe:*/index.php/api/version) answer 200 wavelog-version.json ;;
  wavelog:*/index.php/api/qso)
    jq -r .string <<< "$body" >> "$TEST_TMP/posted"; answer 201 wavelog-qso.json ;;
  dupe:*/index.php/api/qso) answer 400 wavelog-qso-duplicate.json ;;
  wavelog:*/index.php/api/get_contacts_adif)
    if [[ $(jq -r .fetchfromid <<< "$body") == 0 ]]; then answer 200 wavelog-contacts.json; else answer 200 wavelog-contacts-none.json; fi ;;
  cloudlog*:*/index.php/api/auth/*) answer 200 cloudlog-auth.xml ;;
  cloudlog:*/index.php/api/qso)
    jq -r .string <<< "$body" >> "$TEST_TMP/posted"; answer 201 cloudlog-qso.json ;;
  cloudlog-dupe:*/index.php/api/qso) answer 201 cloudlog-qso-duplicate.json ;;
  cloudlog-noimport:*/index.php/api/qso) answer 201 cloudlog-qso-not-imported.json ;;
  cloudlog*:*/index.php/api/recent_qsos/*) answer 200 cloudlog-recent.json ;;
  *) printf '<html><body>404 Page Not Found</body></html>\n404' ;;
esac
exit 0
CURL
  chmod 755 "$TEST_TMP/lb/bin/curl"
}
# lb_configured TYPE: a stored logbook of that type (as set would have saved it), its server up
lb_configured() {
  if [[ $1 == cloudlog ]]; then
    printf '{"url":"https://c/","type":"cloudlog","key":"k","station_id":"1","slug":"me"}\n' > "$DXB_LOGBOOK_FILE"
  else
    printf '{"url":"https://w/","type":"wavelog","key":"k","station_id":"1","slug":""}\n' > "$DXB_LOGBOOK_FILE"
  fi
  chmod 600 "$DXB_LOGBOOK_FILE"
  export LB_SERVER=$1
}
# lb_shims DIR: a wrapper for each external command the logbook runs that records its arguments
# in $TEST_TMP/argv and runs the real one (the key test puts DIR first on PATH)
lb_shims() {
  local c real
  mkdir -p "$1"
  for c in jq awk sed grep tr head tail cut wc sort cat mktemp mv rm chmod mkdir flock date stat ls iconv sha256sum find; do
    real=$(command -v "$c") || continue
    printf '#!/bin/bash\nprintf "%%s\\n" "%s $*" >> "%s/argv"\nexec %s "$@"\n' "$c" "$TEST_TMP" "$real" > "$1/$c"
    chmod 755 "$1/$c"
  done
}
lb_queue_count() { find "$DXB_LOGBOOK_QUEUE" -maxdepth 1 -name '*.adi' 2> /dev/null | wc -l; }

# ---- the brief's tests -------------------------------------------------------------------

test_logbook_key_never_in_argv_or_output() {
  lb_env   # LB_CURL records its own argv to $TEST_TMP/argv and answers from fixtures/logbook
  printf '{"url":"https://log.example/","key":"SECRETKEY123","station_id":"1"}' | "$LB_CMD" set --json > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$(jq -r .ok "$TEST_TMP/out")" "true"
  assert_file_not_contains "$TEST_TMP/argv" "SECRETKEY123"
  assert_file_not_contains "$TEST_TMP/out" "SECRETKEY123"
  assert_eq "$("$LB_CMD" get --json | jq -r .key)" "set"
  assert_file_not_contains "$DXB_LOG_FILE" "SECRETKEY123"
  assert_file_not_contains "$TEST_TMP/err" "SECRETKEY123"
  # every external command the logbook runs, for every subcommand that reads the key: jq, awk
  # and the rest through shims, curl through its stand-in
  lb_shims "$TEST_TMP/shim"
  : > "$TEST_TMP/argv"
  {
    printf '{"url":"https://log.example/","key":"SECRETKEY123","station_id":"1"}' | PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" set --json
    PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" test --json < /dev/null
    PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" get --json
    PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"
    LB_SERVER=down PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"
    PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" flush
    PATH="$TEST_TMP/shim:$PATH" "$LB_CMD" recent --json
  } > "$TEST_TMP/out2" 2> "$TEST_TMP/err2"
  grep -q '^jq ' "$TEST_TMP/argv" || _fail "the jq shim was never called"
  grep -q '^awk ' "$TEST_TMP/argv" || _fail "the awk shim was never called"
  # the post, the one the down server never answered, and the flush's
  assert_eq "$(grep -c 'index.php/api/qso' "$TEST_TMP/urls")" "3"
  assert_eq "$(lb_queue_count)" "0"
  assert_file_not_contains "$TEST_TMP/argv" "SECRETKEY123"
  assert_file_not_contains "$TEST_TMP/out2" "SECRETKEY123"
  assert_file_not_contains "$TEST_TMP/err2" "SECRETKEY123"
  assert_file_not_contains "$DXB_LOG_FILE" "SECRETKEY123"
  # the key reached the server, in the request bodies only
  assert_file_contains "$TEST_TMP/bodies" '"key":"SECRETKEY123"'
}
test_logbook_detects_wavelog_and_cloudlog() {
  lb_env; LB_SERVER=wavelog
  assert_eq "$(printf '{"url":"https://w/","key":"k","station_id":"1"}' | "$LB_CMD" test --json | jq -r .type)" "wavelog"
  LB_SERVER=cloudlog
  assert_eq "$(printf '{"url":"https://c/","key":"k","station_id":"1","slug":"me"}' | "$LB_CMD" test --json | jq -r .type)" "cloudlog"
  LB_SERVER=none
  printf '{"url":"https://x/","key":"k","station_id":"1"}' | "$LB_CMD" set --json > /dev/null; assert_eq "$?" "6"
  [[ -f $DXB_LOGBOOK_FILE ]] && _fail "a logbook that did not answer was saved"
}
test_logbook_queues_when_unreachable_and_flushes_later() {
  lb_env; lb_configured wavelog; LB_SERVER=down
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"; assert_eq "$?" "0"
  assert_eq "$(ls "$DXB_LOGBOOK_QUEUE" | wc -l)" "1"
  LB_SERVER=wavelog; "$LB_CMD" flush
  assert_eq "$(ls "$DXB_LOGBOOK_QUEUE" | wc -l)" "0"
  assert_contains "$(cat "$TEST_TMP/posted")" "<call:5>K0ABC"
}
test_logbook_queues_when_no_logbook_is_set() {
  lb_env; "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"; assert_eq "$?" "0"
  assert_eq "$(ls "$DXB_LOGBOOK_QUEUE" | wc -l)" "1"
}
test_logbook_recent_is_one_shape_for_both_servers() {
  local w c
  lb_env; lb_configured wavelog; w=$("$LB_CMD" recent --json 3)
  lb_env; lb_configured cloudlog; c=$("$LB_CMD" recent --json 3)
  assert_eq "$(jq -c '.contacts[0] | keys' <<< "$w")" '["band","call","grid","mode","rst_rcvd","rst_sent","time_utc"]'
  assert_eq "$(jq -c '.contacts[0] | keys' <<< "$c")" "$(jq -c '.contacts[0] | keys' <<< "$w")"
  assert_eq "$(jq '.contacts | length' <<< "$w")" "3"
}
test_logbook_recent_is_cached_for_a_minute() {
  lb_env; lb_configured cloudlog; "$LB_CMD" recent --json > /dev/null; : > "$TEST_TMP/argv"
  "$LB_CMD" recent --json > /dev/null
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"
}

# ---- the setting -------------------------------------------------------------------------

test_logbook_set_saves_0600_atomically_and_get_never_shows_the_key() {
  lb_env
  printf '{"url":"https://log.example","key":"k1","station_id":"7","slug":""}' | "$LB_CMD" set --json > "$TEST_TMP/out"; assert_eq "$?" "0"
  assert_eq "$(jq -c . "$TEST_TMP/out")" '{"ok":true,"type":"wavelog","kept":[]}'
  assert_eq "$(stat -c %a "$DXB_LOGBOOK_FILE")" "600"
  assert_eq "$(jq -c . "$DXB_LOGBOOK_FILE")" '{"url":"https://log.example/","type":"wavelog","key":"k1","station_id":"7","slug":""}'
  assert_eq "$(find "$DXB_STATE_DIR" -maxdepth 1 -name '.*' | wc -l)" "0"           # no temp file left
  assert_eq "$("$LB_CMD" get --json | jq -c '{configured, url, type, station_id, slug, key, queue}')" \
    '{"configured":true,"url":"https://log.example/","type":"wavelog","station_id":"7","slug":"","key":"set","queue":0}'
  # a successful set starts a flush of what is queued (without waiting for it)
  assert_file_contains "$TEST_TMP/calls" "systemctl --no-block start dxberry-logbook-flush.service"
}
test_logbook_set_without_a_key_keeps_the_stored_one() {
  lb_env; lb_configured wavelog
  printf '{"url":"https://w/","station_id":"2"}' | "$LB_CMD" set --json > /dev/null; assert_eq "$?" "0"
  assert_eq "$(jq -r '.key + " " + .station_id' "$DXB_LOGBOOK_FILE")" "k 2"
  assert_file_contains "$TEST_TMP/bodies" '"key":"k"'
}
test_logbook_set_clear_forgets_the_logbook() {
  lb_env; lb_configured wavelog
  printf '{"clear":true}' | "$LB_CMD" set --json > "$TEST_TMP/out"; assert_eq "$?" "0"
  assert_eq "$(jq -c '{ok, type}' "$TEST_TMP/out")" '{"ok":true,"type":null}'
  [[ -e $DXB_LOGBOOK_FILE ]] && _fail "a cleared logbook is still stored"
  assert_eq "$("$LB_CMD" get --json | jq -c '{configured, key}')" '{"configured":false,"key":null}'
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"                                      # no request
}
test_logbook_url_must_be_https_or_the_local_wavelog() {
  local u
  lb_env
  for u in http://log.example/ ftp://x/ 'https://x/a b' 'https://x/"' file:///etc/passwd 'https://user:pw@x/' http://127.0.0.1:8084/ http://localhost/ ''; do
    jq -cn --arg u "$u" '{url: $u, key: "k", station_id: "1"}' | "$LB_CMD" set --json > /dev/null 2>&1
    assert_eq "$?" "2"
  done
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"                                      # refused before any request
  [[ -e $DXB_LOGBOOK_FILE ]] && _fail "a refused url was saved"
  printf '{"url":"https://w/","key":"k","station_id":"x"}' | "$LB_CMD" set --json > /dev/null 2>&1; assert_eq "$?" "2"
  printf '{"url":"https://w/","key":"a b","station_id":"1"}' | "$LB_CMD" set --json > /dev/null 2>&1; assert_eq "$?" "2"
  printf '{"url":"https://w/","key":"k","station_id":"1","slug":"../x"}' | "$LB_CMD" set --json > /dev/null 2>&1; assert_eq "$?" "2"
  printf 'nonsense' | "$LB_CMD" set --json > /dev/null 2>&1; assert_eq "$?" "2"
  printf '{"url":"https://w/","station_id":"1"}' | "$LB_CMD" set --json > /dev/null 2>&1; assert_eq "$?" "2"   # no key stored yet
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"
}
test_logbook_https_is_https_only_and_the_local_wavelog_may_use_http() {
  lb_env
  printf '{"url":"https://w/wavelog/index.php","key":"k","station_id":"1"}' | "$LB_CMD" set --json > /dev/null; assert_eq "$?" "0"
  assert_eq "$(jq -r .url "$DXB_LOGBOOK_FILE")" "https://w/wavelog/"
  assert_contains "$(head -1 "$TEST_TMP/argv")" "--proto =https --proto-redir =https"
  assert_eq "$(head -1 "$TEST_TMP/urls")" "https://w/wavelog/index.php/api/version"
  : > "$TEST_TMP/argv"
  printf '{"url":"http://127.0.0.1:8083","key":"k","station_id":"1"}' | "$LB_CMD" set --json > /dev/null; assert_eq "$?" "0"
  assert_eq "$(jq -r .url "$DXB_LOGBOOK_FILE")" "http://127.0.0.1:8083/"
  assert_contains "$(head -1 "$TEST_TMP/argv")" "--proto =https,http --proto-redir =https,http"
  printf '{"url":"http://localhost:8083/","key":"k","station_id":"1"}' | "$LB_CMD" set --json > /dev/null; assert_eq "$?" "0"
  # every request: the brief's timeouts, its config on a pipe, never a URL in the arguments
  assert_contains "$(head -1 "$TEST_TMP/argv")" "-sS --connect-timeout 5 --max-time 15"
  assert_contains "$(head -1 "$TEST_TMP/argv")" "-K /dev/fd/"
  assert_file_not_contains "$TEST_TMP/argv" "http://"
}
test_logbook_test_without_input_uses_the_stored_setting() {
  lb_env; lb_configured cloudlog
  assert_eq "$("$LB_CMD" test --json < /dev/null | jq -c .)" '{"ok":true,"type":"cloudlog","version":null,"error":null}'
  assert_eq "$(head -1 "$TEST_TMP/urls")" "https://c/index.php/api/version"
  assert_eq "$(sed -n 2p "$TEST_TMP/urls")" "https://c/index.php/api/auth/k"
  LB_SERVER=wavelog
  assert_eq "$(printf '{"url":"https://w/","key":"k","station_id":"1"}' | "$LB_CMD" test --json | jq -c .)" '{"ok":true,"type":"wavelog","version":"3.2.4","error":null}'
  LB_SERVER=down
  printf '{"url":"https://w/","key":"k","station_id":"1"}' | "$LB_CMD" test --json > "$TEST_TMP/out"; assert_eq "$?" "6"
  assert_eq "$(jq -c '{ok, type, version}' "$TEST_TMP/out")" '{"ok":false,"type":null,"version":null}'
  assert_contains "$(jq -r .error "$TEST_TMP/out")" "did not answer"
}
test_logbook_get_offers_the_local_wavelog_once_installed() {
  lb_env
  assert_eq "$("$LB_CMD" get --json | jq -c .)" \
    '{"configured":false,"url":null,"type":null,"station_id":null,"slug":null,"key":null,"queue":0,"refused":0,"refused_reason":null,"local":{"installed":false,"url":null,"web":null}}'
  mkdir -p "$DXB_APPS_OPT/wavelog"; echo 3.2.4 > "$DXB_APPS_OPT/wavelog/VERSION"
  assert_eq "$("$LB_CMD" get --json | jq -c .local)" '{"installed":true,"url":"http://127.0.0.1:8083/","web":"https://192.0.2.10:8082/"}'
}
# (test_app_fldigi.sh's fd_env unsets the function in the shared test shell: loaded again here,
# and unset again afterwards)
test_logbook_fldigi_values_are_the_current_logbook() {
  lb_env; source "$DXB_LIB/logbook.sh"
  assert_eq "$(dxb_logbook_fldigi_values)" "{}"
  lb_configured wavelog
  assert_eq "$(dxb_logbook_fldigi_values)" '{"url":"https://w/","key":"k","station_id":"1"}'
  echo 'not json' > "$DXB_LOGBOOK_FILE"
  assert_eq "$(dxb_logbook_fldigi_values 2> /dev/null)" "{}"
  unset -f dxb_logbook_fldigi_values
}

# ---- posting and the queue ---------------------------------------------------------------

test_logbook_submit_posts_the_record_as_the_api_wants_it() {
  lb_env; lb_configured wavelog
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "0"
  assert_eq "$(cat "$TEST_TMP/urls")" "https://w/index.php/api/qso"
  assert_eq "$(jq -c 'del(.string)' "$TEST_TMP/bodies")" '{"key":"k","station_profile_id":"1","type":"adif"}'
  assert_eq "$(jq -r .string "$TEST_TMP/bodies")" "$(cat "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi")"
  lb_configured cloudlog; : > "$TEST_TMP/posted"
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"; assert_eq "$?" "0"
  assert_contains "$(cat "$TEST_TMP/posted")" "<call:5>K0ABC"
}
test_logbook_queue_files_are_private_and_named_by_time() {
  lb_env; lb_configured wavelog; LB_SERVER=down
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null
  assert_eq "$(stat -c %a "$DXB_LOGBOOK_QUEUE")" "700"
  local f
  for f in "$DXB_LOGBOOK_QUEUE"/*; do
    [[ ${f##*/} =~ ^[0-9]{10}-[0-9]+\.adi$ ]] || _fail "queue file name ${f##*/}"
    assert_eq "$(stat -c %a "$f")" "600"
  done
  assert_eq "$(lb_queue_count)" "2"
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE" -mindepth 1 ! -name .lock | wc -l)" "2"     # nothing else: no temp files
}
test_logbook_flush_posts_oldest_first_and_stops_at_the_first_failure() {
  lb_env; lb_configured wavelog
  mkdir -p "$DXB_LOGBOOK_QUEUE"
  printf '<call:4>OLD1 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  printf '<call:4>NEW2 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000005-1.adi"
  printf '<call:4>MID3 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-2.adi"
  LB_SERVER=down; "$LB_CMD" flush 2> /dev/null; assert_eq "$?" "6"
  assert_eq "$(lb_queue_count)" "3"
  assert_eq "$(grep -c . "$TEST_TMP/argv")" "1"                                      # stopped at the first
  LB_SERVER=wavelog; "$LB_CMD" flush; assert_eq "$?" "0"
  assert_eq "$(tr '\n' ' ' < "$TEST_TMP/posted")" "<call:4>OLD1 <eor> <call:4>MID3 <eor> <call:4>NEW2 <eor> "
  "$LB_CMD" flush; assert_eq "$?" "0"                                                # nothing left: nothing to do
}
test_logbook_flush_without_a_logbook_does_nothing() {
  lb_env; mkdir -p "$DXB_LOGBOOK_QUEUE"; printf '<call:4>OLD1 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  "$LB_CMD" flush; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "1"
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"
}
test_logbook_a_second_flush_never_posts_the_same_file() {
  lb_env; lb_configured wavelog
  mkdir -p "$DXB_LOGBOOK_QUEUE"; printf '<call:4>OLD1 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  # another flush holds the flush lock
  exec 7> "$DXB_RUN_DIR/logbook-flush.lock"; flock -n 7 || _fail "could not take the lock for the test"
  "$LB_CMD" flush; assert_eq "$?" "0"
  exec 7>&-
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"
  assert_eq "$(lb_queue_count)" "1"
}
test_logbook_a_record_the_server_refuses_is_set_aside() {
  # Wavelog answers 400 "abort" for a record it read and will not import (a duplicate: a contact
  # posted before whose answer was lost). It must not block the queue for ever.
  lb_env; lb_configured wavelog; LB_SERVER=dupe
  mkdir -p "$DXB_LOGBOOK_QUEUE"
  printf '<call:5>K0ABC <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  printf '<call:4>NEW2 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000005-1.adi"
  "$LB_CMD" flush 2> "$TEST_TMP/err"; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "0"
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE/refused" -name '*.adi' | wc -l)" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "Duplicate"
  assert_eq "$("$LB_CMD" get --json | jq -c '{queue, refused}')" '{"queue":0,"refused":2}'
  # the server's reason is kept beside each refused contact, the newest one shown by get
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE/refused" -name '*.why' | wc -l)" "2"
  assert_contains "$("$LB_CMD" get --json | jq -r .refused_reason)" "Duplicate for W0BTE"
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null; assert_eq "$?" "0"
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE/refused" -name '*.adi' | wc -l)" "3"
  assert_eq "$(lb_queue_count)" "0"
}
test_logbook_submit_refuses_an_oversized_record_and_a_full_queue() {
  lb_env
  { printf '<call:5>K0ABC <comment:70000>'; head -c 70000 /dev/zero | tr '\0' x; printf ' <eor>'; } > "$TEST_TMP/big.adi"
  "$LB_CMD" submit < "$TEST_TMP/big.adi" 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "64 KiB"
  assert_eq "$(lb_queue_count)" "0"
  printf 'hello' | "$LB_CMD" submit 2> /dev/null; assert_eq "$?" "6"                 # no contact in it
  assert_eq "$(lb_queue_count)" "0"
  mkdir -p "$DXB_LOGBOOK_QUEUE"
  local i
  for (( i = 1; i <= 1000; i++ )); do : > "$DXB_LOGBOOK_QUEUE/1700000000-$i.adi"; done
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "1000"
  assert_eq "$(lb_queue_count)" "1000"
  [[ -e $DXB_LOGBOOK_QUEUE/1700000000-1.adi ]] || _fail "the oldest records are kept"
}
test_logbook_submit_reads_a_latin1_record_as_utf8() {
  lb_env; lb_configured wavelog
  printf '<call:5>K0ABC <name:4>Jos\xe9 <eor>' | "$LB_CMD" submit; assert_eq "$?" "0"
  assert_eq "$(cat "$TEST_TMP/posted")" "<call:5>K0ABC <name:4>José <eor>"
}

# ---- recent contacts ---------------------------------------------------------------------

test_logbook_recent_wavelog_is_newest_first_and_fetches_only_new_contacts() {
  lb_env; lb_configured wavelog
  local w; w=$("$LB_CMD" recent --json 3)
  assert_eq "$(jq -c '.contacts[0]' <<< "$w")" \
    '{"time_utc":"2026-10-20T18:01:30Z","call":"JA1AAA","band":"15m","mode":"SSB","rst_sent":"59","rst_rcvd":"57","grid":"PM95"}'
  assert_eq "$(jq -r '[.contacts[] | .call + "/" + .mode] | join(" ")' <<< "$w")" "JA1AAA/SSB VE3XYZ/FT4 K0ABC/FT8"
  assert_eq "$(jq -c '{ok, type, error}' <<< "$w")" '{"ok":true,"type":"wavelog","error":null}'
  assert_eq "$(jq -c 'del(.key)' "$TEST_TMP/bodies")" '{"station_id":"1","fetchfromid":0}'
  # Wavelog sends this id as a string in an export (the fixture's own shape) and as a number when
  # nothing is new: both are read, and it is kept as a number
  assert_eq "$(sed '/^#/d' "$LB_FIX/wavelog-contacts.json" | jq -r '.lastfetchedid | type')" "string"
  assert_eq "$(sed '/^#/d' "$LB_FIX/wavelog-contacts-none.json" | jq -r '.lastfetchedid | type')" "number"
  assert_eq "$(jq -c .lastfetchedid "$DXB_STATE_DIR/logbook-wavelog.json")" "1287"
  assert_eq "$(jq '.contacts | length' "$DXB_STATE_DIR/logbook-wavelog.json")" "4"
  # a minute later: only what is newer than the last id
  rm -f "$DXB_RUN_DIR/logbook-recent.json"; : > "$TEST_TMP/bodies"
  w=$("$LB_CMD" recent --json)
  assert_eq "$(jq -c 'del(.key)' "$TEST_TMP/bodies")" '{"station_id":"1","fetchfromid":1287}'
  assert_eq "$(jq '.contacts | length' <<< "$w")" "4"
  assert_eq "$(jq -r '.contacts[3].call' <<< "$w")" "W1AW"
  assert_eq "$(jq -c .lastfetchedid "$DXB_STATE_DIR/logbook-wavelog.json")" "1287"
}
test_logbook_recent_cloudlog_reads_the_public_slug() {
  lb_env; lb_configured cloudlog
  local c; c=$("$LB_CMD" recent --json 2)
  assert_eq "$(cat "$TEST_TMP/urls")" "https://c/index.php/api/recent_qsos/me/25"
  assert_eq "$(jq -c '.contacts[0]' <<< "$c")" \
    '{"time_utc":"2026-10-20T18:01:00Z","call":"JA1AAA","band":"15m","mode":"SSB","rst_sent":"59","rst_rcvd":"57","grid":"PM95"}'
  assert_eq "$(jq -r '[.contacts[] | .call + "/" + .mode + "/" + .grid] | join(" ")' <<< "$c")" "JA1AAA/SSB/PM95 VE3XYZ/FT4/FN03"
  assert_eq "$(wc -c < "$TEST_TMP/bodies")" "0"                                    # Cloudlog's recent contacts need no key
}
test_logbook_recent_failures_are_cached_as_failures_and_shapes_hold() {
  lb_env
  assert_eq "$("$LB_CMD" recent --json | jq -c .)" '{"ok":false,"type":null,"contacts":[],"error":"no logbook is set"}'
  lb_configured wavelog; LB_SERVER=down
  local r; r=$("$LB_CMD" recent --json 2> /dev/null)
  assert_eq "$(jq -c '{ok, type, contacts}' <<< "$r")" '{"ok":false,"type":"wavelog","contacts":[]}'
  : > "$TEST_TMP/argv"; LB_SERVER=wavelog
  assert_eq "$("$LB_CMD" recent --json | jq -r .ok)" "false"                         # a dead server is not asked again within the minute
  assert_eq "$(wc -c < "$TEST_TMP/argv")" "0"
  [[ -f $DXB_STATE_DIR/logbook-wavelog.json ]] && _fail "a failed fetch must not be stored as contacts"
  "$LB_CMD" recent --json 26 > /dev/null 2>&1; assert_eq "$?" "2"
  "$LB_CMD" recent --json 0 > /dev/null 2>&1; assert_eq "$?" "2"
  # a new setting is a new answer
  printf '{"url":"https://w/","key":"k","station_id":"1"}' | "$LB_CMD" set --json > /dev/null
  assert_eq "$("$LB_CMD" recent --json | jq -r .ok)" "true"
}
test_logbook_recent_cache_is_written_atomically() {
  lb_env; lb_configured cloudlog
  "$LB_CMD" recent --json > /dev/null
  assert_eq "$(jq -r .answer.ok "$DXB_RUN_DIR/logbook-recent.json")" "true"
  assert_eq "$(find "$DXB_RUN_DIR" -name '.*' -type f | wc -l)" "0"
}

# ---- the fldigi follow -------------------------------------------------------------------

# set: a program that follows the logbook (gamma, test_screen.sh's fake, as fldigi) whose
# session is enabled but not running follows it now, through the settings step as dxbapps; its
# "kept" lines come back per radio. A running session follows at its next start.
test_logbook_set_makes_stopped_sessions_follow_now() {
  sc_env; sc_owned radio2 gamma; sc_owned radio1 gamma
  lb_server
  export DXB_LOGBOOK_FILE=$DXB_STATE_DIR/logbook.json DXB_LOGBOOK_QUEUE=$DXB_STATE_DIR/logbook-queue \
    DXB_LOGBOOK_CURL=$TEST_TMP/lb/bin/curl LB_SERVER=wavelog LB_FIX=$DXB_ROOT/tests/fixtures/logbook
  : > "$TEST_TMP/argv"; : > "$TEST_TMP/urls"; : > "$TEST_TMP/bodies"
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-radio2.service"
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-radio1.service"
  sc_active dxberry-screen@gamma-radio1.service                                    # running: next start
  : > "$TEST_TMP/appcalls"
  printf '{"url":"https://w/","key":"SECRET-K","station_id":"1"}' | "$DXB_ROOT/provision/bin/dxberry-logbook" set --json > "$TEST_TMP/out"
  assert_eq "$(jq -c .kept "$TEST_TMP/out")" '["radio2: KEY_A (changed in gamma)"]'
  assert_eq "$(cat "$TEST_TMP/appcalls")" "gamma logbook radio2"
  assert_contains "$(cat "$TEST_TMP/calls")" "runuser -u dxbapps -- env HOME=$DXB_APPS_HOME $DXB_ROOT/provision/bin/dxberry-app settings gamma-radio2"
  assert_eq "$(jq -c .steps "$TEST_TMP/settings-stdin")" '["logbook"]'
  assert_eq "$(jq -r .logbook.key "$TEST_TMP/settings-stdin")" "SECRET-K"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "SECRET-K"
  assert_eq "$(cat "$DXB_APPS_STATE/logbook-kept/gamma-radio2")" "kept KEY_A (changed in gamma)"
}

# prepare: a program that follows the logbook gets the logbook step at every start; root keeps
# its "kept" lines for the Programs row, and removes them when there are none
test_logbook_prepare_follows_at_every_start_and_keeps_the_kept_lines() {
  sc_env; sc_owned radio2 gamma
  assert_ok sc_app prepare gamma-radio2
  assert_eq "$(jq -c .steps "$TEST_TMP/settings-stdin")" '["firstfill","wire","logbook"]'
  local k=$DXB_APPS_STATE/logbook-kept/gamma-radio2
  assert_eq "$(cat "$k")" "kept KEY_A (changed in gamma)"
  assert_eq "$(stat -c %a "$k")" "600"
  : > "$TEST_TMP/settings-stdin"
  assert_ok sc_app prepare gamma-radio2
  assert_eq "$(jq -c .steps "$TEST_TMP/settings-stdin")" '["logbook"]'
  # the module no longer reports a kept key: the file goes
  sed -i 's/echo "kept KEY_A (changed in gamma)"; //' "$DXB_APPS_DIR/gamma.sh"
  assert_ok sc_app prepare gamma-radio2
  [[ -e $k ]] && _fail "no kept lines: the file must go"
  return 0
}
# a logbook follow that fails is a warning: the program still starts
test_logbook_prepare_starts_the_program_when_only_the_follow_fails() {
  sc_env; sc_owned radio2 gamma
  sed -i 's/^app_gamma_logbook_follow() { /app_gamma_logbook_follow() { [[ -z ${GAMMA_LB_FAIL:-} ]] || return 1; /' "$DXB_APPS_DIR/gamma.sh"
  GAMMA_LB_FAIL=1 sc_app prepare gamma-radio2 2> "$TEST_TMP/err"; assert_eq "$?" "0"
  assert_contains "$(cat "$TEST_TMP/err")" "could not follow the logbook"
  [[ -f $DXB_APPS_STATE/firstfill/gamma-radio2 ]] || _fail "the first fill is still marked"
}

# ---- units and provisioning --------------------------------------------------------------

test_logbook_bridge_unit_is_the_briefs_and_sandboxed() {
  local u=$DXB_ROOT/provision/templates/dxberry-logbridge.service l
  assert_file_contains "$u" 'ExecStart=/usr/bin/socat -u UDP4-RECVFROM:2333,bind=127.0.0.1,fork SYSTEM:/opt/dxberry/bin/dxberry-logbook\ submit'
  for l in Restart=on-failure WantedBy=multi-user.target NoNewPrivileges=yes ProtectSystem=strict ProtectHome=yes PrivateTmp=yes \
    ProtectKernelTunables=yes ProtectControlGroups=yes StartLimitIntervalSec=60 StartLimitBurst=3 'ReadWritePaths=/var/lib/dxberry -/run/dxberry'; do
    assert_file_contains "$u" "$l"
  done
  assert_file_not_contains "$u" "User="
  assert_file_not_contains "$u" "IPAddressDeny"
  u=$DXB_ROOT/provision/templates/dxberry-logbook-flush.service
  for l in Type=oneshot 'ExecStart=/opt/dxberry/bin/dxberry-logbook flush' NoNewPrivileges=yes ProtectSystem=strict ProtectHome=yes PrivateTmp=yes \
    ProtectKernelTunables=yes ProtectControlGroups=yes StartLimitIntervalSec=60 StartLimitBurst=3 'ReadWritePaths=/var/lib/dxberry -/run/dxberry'; do
    assert_file_contains "$u" "$l"
  done
  u=$DXB_ROOT/provision/templates/dxberry-logbook-flush.timer
  for l in OnBootSec=2min OnUnitActiveSec=5min WantedBy=timers.target; do assert_file_contains "$u" "$l"; done
}
# (in a subshell: the paths it sets must not reach later tests)
test_logbook_provision_installs_the_units_once_and_enables_them() (
  lb_env
  export DXB_SYSTEMD_DIR=$TEST_TMP/lb/systemd DXB_SOCAT=$TEST_TMP/lb/bin/socat DXB_TEMPLATES=$DXB_ROOT/provision/templates
  printf '#!/bin/sh\n' > "$DXB_SOCAT"; chmod 755 "$DXB_SOCAT"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  ( systemctl() { "$DXB_SYSTEMCTL" "$@"; }; provision_logbook )
  local t
  for t in dxberry-logbridge.service dxberry-logbook-flush.service dxberry-logbook-flush.timer; do
    assert_eq "$(cat "$DXB_SYSTEMD_DIR/$t")" "$(cat "$DXB_ROOT/provision/templates/$t")"
  done
  assert_file_contains "$TEST_TMP/calls" "systemctl daemon-reload"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable --now dxberry-logbook-flush.timer"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable --now dxberry-logbridge.service"
  : > "$TEST_TMP/calls"
  ( systemctl() { "$DXB_SYSTEMCTL" "$@"; }; provision_logbook )
  assert_file_not_contains "$TEST_TMP/calls" "daemon-reload"                         # nothing changed
  # without socat the bridge is enabled for later (its unit waits for socat) but not started now
  rm -f "$DXB_SOCAT"; : > "$TEST_TMP/calls"
  ( systemctl() { "$DXB_SYSTEMCTL" "$@"; }; provision_logbook )
  assert_file_contains "$TEST_TMP/calls" "systemctl enable dxberry-logbridge.service"
  assert_file_not_contains "$TEST_TMP/calls" "enable --now dxberry-logbridge.service"
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-logbridge.service" "ConditionPathExists=/usr/bin/socat"
)
# the bridge waits for socat; the program install that brings socat starts it (when it is enabled)
test_logbook_bridge_starts_when_a_program_brings_socat() {
  fx_apps_env; echo enabled > "$TEST_TMP/enabled-dxberry-logbridge.service"
  assert_ok dxb_apps_install fldigi > /dev/null
  assert_file_contains "$TEST_TMP/calls" "systemctl --no-block start dxberry-logbridge.service"
  fx_apps_env; echo enabled > "$TEST_TMP/enabled-dxberry-logbridge.service"
  AP_INSTALLED='tigervnc-standalone-server openbox socat xauth pulseaudio'
  assert_ok dxb_apps_install fldigi > /dev/null
  assert_file_not_contains "$TEST_TMP/calls" "start dxberry-logbridge.service"
}
test_logbook_ships_in_the_image() {
  local required executable f
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  executable=$(sed -n '/^EXECUTABLE_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  for f in provision/lib/logbook.sh provision/bin/dxberry-logbook provision/lib/apps/wavelog.sh \
    provision/templates/dxberry-logbridge.service provision/templates/dxberry-logbook-flush.service \
    provision/templates/dxberry-logbook-flush.timer provision/templates/nginx-wavelog.conf \
    provision/templates/nginx-dxberry-auth-gate.conf provision/templates/nginx-dxberry-auth-endpoints.conf; do
    assert_contains "$required" "$f"
  done
  assert_contains "$executable" "provision/bin/dxberry-logbook"
  grep -qF "ln -sf /opt/dxberry/bin/dxberry-logbook " "$DXB_ROOT/build/build-image.sh" || _fail "no /usr/local/sbin link for dxberry-logbook"
  grep -q 'provision_logbook' "$DXB_ROOT/provision/bin/dxberry-provision" || _fail "dxberry-provision does not run provision_logbook"
}

# ---- never lose a contact (fix round 1) --------------------------------------------------

# refused/ is never pruned: once it holds DXB_LOGBOOK_MAX_REFUSED contacts, a refused one stays
# queued and the flush stops, so the queue grows where the page shows it
test_logbook_a_full_refused_dir_leaves_the_contact_queued() {
  lb_env; lb_configured wavelog; LB_SERVER=dupe; export DXB_LOGBOOK_MAX_REFUSED=2
  mkdir -p "$DXB_LOGBOOK_QUEUE"
  printf '<call:4>OLD1 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  printf '<call:4>MID2 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000001-1.adi"
  printf '<call:4>NEW3 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000002-1.adi"
  "$LB_CMD" flush 2> "$TEST_TMP/err"; assert_eq "$?" "6"
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE/refused" -name '*.adi' | wc -l)" "2"
  assert_eq "$(lb_queue_count)" "1"
  assert_eq "$(cat "$DXB_LOGBOOK_QUEUE"/*.adi)" "<call:4>NEW3 <eor>"
  assert_contains "$(cat "$TEST_TMP/err")" "stays queued"
  # a submit the server refuses goes to the queue too, and nothing in refused/ was removed
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "2"
  assert_eq "$(grep -l OLD1 "$DXB_LOGBOOK_QUEUE"/refused/*.adi | wc -l)" "1"
  unset DXB_LOGBOOK_MAX_REFUSED
}
# a contact that cannot be set aside (refused/ cannot be made) stays where it was
test_logbook_a_failed_set_aside_keeps_the_contact() {
  lb_env; lb_configured wavelog; LB_SERVER=dupe
  mkdir -p "$DXB_LOGBOOK_QUEUE"; : > "$DXB_LOGBOOK_QUEUE/refused"            # a file where the directory should be
  printf '<call:4>OLD1 <eor>' > "$DXB_LOGBOOK_QUEUE/1700000000-1.adi"
  "$LB_CMD" flush 2> /dev/null; assert_eq "$?" "6"
  assert_eq "$(cat "$DXB_LOGBOOK_QUEUE/1700000000-1.adi")" "<call:4>OLD1 <eor>"
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "2"
}
# a submit killed while it posted leaves its temp file: the next flush adopts and posts it; a
# fresh one (a submit still at work) is left alone
test_logbook_flush_adopts_a_killed_submits_contact() {
  lb_env; lb_configured wavelog
  mkdir -p "$DXB_LOGBOOK_QUEUE"
  printf '<call:5>STALE <eor>' > "$DXB_LOGBOOK_QUEUE/.in.abc123"; touch -d '5 minutes ago' "$DXB_LOGBOOK_QUEUE/.in.abc123"
  printf '<call:5>FRESH <eor>' > "$DXB_LOGBOOK_QUEUE/.in.def456"
  : > "$DXB_LOGBOOK_QUEUE/.in.empty1"; touch -d '5 minutes ago' "$DXB_LOGBOOK_QUEUE/.in.empty1"   # cut off: no contact
  "$LB_CMD" flush; assert_eq "$?" "0"
  assert_eq "$(cat "$TEST_TMP/posted")" "<call:5>STALE <eor>"
  [[ -f $DXB_LOGBOOK_QUEUE/.in.def456 ]] || _fail "a submit's fresh temp file must be left alone"
  [[ -e $DXB_LOGBOOK_QUEUE/.in.abc123 || -e $DXB_LOGBOOK_QUEUE/.in.empty1 ]] && _fail "stale temp files must be adopted or removed"
  assert_eq "$(lb_queue_count)" "0"
}
# Cloudlog answers 201 "created" also when it imported nothing: a duplicate is logged already, any
# other reason is set aside with that reason
test_logbook_cloudlog_imported_nothing_is_not_posted() {
  lb_env; lb_configured cloudlog; LB_SERVER=cloudlog-dupe
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi"; assert_eq "$?" "0"
  assert_eq "$(lb_queue_count)" "0"
  [[ -d $DXB_LOGBOOK_QUEUE/refused ]] && _fail "a duplicate is in the log already: nothing to set aside"
  LB_SERVER=cloudlog-noimport
  "$LB_CMD" submit < "$DXB_ROOT/tests/fixtures/logbook/wsjtx-contact.adi" 2> /dev/null; assert_eq "$?" "0"
  assert_eq "$(find "$DXB_LOGBOOK_QUEUE/refused" -name '*.adi' | wc -l)" "1"
  assert_contains "$("$LB_CMD" get --json | jq -r .refused_reason)" "RX_PWR (5W) is not a number"
}
# curl's own messages are never shown (a Cloudlog URL carries the key); what failed is said in
# DXBerry's words
test_logbook_curls_messages_never_reach_the_output() {
  lb_env; LB_SERVER=down
  printf '{"url":"https://c/","key":"SECRETKEY9","station_id":"1"}' | "$LB_CMD" test --json > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" "6"
  assert_file_not_contains "$TEST_TMP/out" "SECRETKEY9"
  assert_file_not_contains "$TEST_TMP/err" "SECRETKEY9"
  assert_file_not_contains "$TEST_TMP/err" "curl:"
  assert_contains "$(jq -r .error "$TEST_TMP/out")" "nothing answered on its port"
}
# set's follow leaves a session that is starting to its own prepare
test_logbook_set_skips_a_session_that_is_starting() {
  sc_env; sc_owned radio2 gamma
  lb_server
  export DXB_LOGBOOK_FILE=$DXB_STATE_DIR/logbook.json DXB_LOGBOOK_QUEUE=$DXB_STATE_DIR/logbook-queue \
    DXB_LOGBOOK_CURL=$TEST_TMP/lb/bin/curl LB_SERVER=wavelog LB_FIX=$DXB_ROOT/tests/fixtures/logbook
  : > "$TEST_TMP/argv"; : > "$TEST_TMP/urls"; : > "$TEST_TMP/bodies"
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-radio2.service"
  printf '#!/bin/bash\nif [[ $1 == is-active && ${*: -1} == dxberry-screen@gamma-radio2.service ]]; then echo activating; exit 3; fi\nsource "$DXB_ROOT/tests/fixtures/sysfs.sh"\nfx_systemctl "$@"\n' > "$TEST_TMP/bin/systemctl"
  : > "$TEST_TMP/appcalls"
  printf '{"url":"https://w/","key":"k","station_id":"1"}' | "$DXB_ROOT/provision/bin/dxberry-logbook" set --json > "$TEST_TMP/out"
  assert_eq "$(jq -c .kept "$TEST_TMP/out")" '[]'
  assert_eq "$(cat "$TEST_TMP/appcalls")" ""
}
test_logbook_units_restart_slowly_and_a_waiting_queue_is_no_failure() {
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-logbridge.service" "RestartSec=30"
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-logbook-flush.service" "SuccessExitStatus=6"
  # the detection probe's URL carries a Cloudlog-style key: this Pi's Wavelog does not log it
  local f=$DXB_ROOT/provision/templates/nginx-wavelog.conf
  assert_eq "$(awk '/location ~ \^\/index\\.php\/api\/auth\// { getline; print; exit }' "$f" | tr -d ' ')" "access_logoff;"
}
