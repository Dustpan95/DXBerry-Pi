#!/bin/bash
# shellcheck shell=bash disable=SC2016
# One-key settings writers: change a single key in WSJT-X / JS8Call (Qt INI), fldigi (one tag per
# line XML) and flrig (FLTK prefs) files and leave every other line exactly as it was.
# Every writer returns 0 written, 1 unchanged, 6 could not write (checked by reading back).
# Values reach awk through the environment only, never through the program text.
# Needs common.sh (dxb_write_if_changed).

_dxb_awk() { "${DXB_AWK:-awk}" "$@"; }

# Section/key lookup shared by the INI and FLTK prefs files. SEP is the key/value separator.
# Env: _DXB_S section name, _DXB_K key, _DXB_SEP separator. Prints the raw value; exit 1 if absent.
_DXB_KV_GET_AWK='
BEGIN { hdr = "[" ENVIRON["_DXB_S"] "]"; pre = ENVIRON["_DXB_K"] ENVIRON["_DXB_SEP"]; n = length(pre); insec = 0 }
/^\[/ { insec = ($0 == hdr); next }
insec && substr($0, 1, n) == pre { print substr($0, n + 1); found = 1; exit }
END { exit(found ? 0 : 1) }'

# Env as above plus _DXB_V (the raw value). Replaces the first KEY line of the section, or adds it
# after the section's last non-blank line, or adds the whole section at the end of the file.
_DXB_KV_SET_AWK='
BEGIN { hdr = "[" ENVIRON["_DXB_S"] "]"; pre = ENVIRON["_DXB_K"] ENVIRON["_DXB_SEP"]; n = length(pre); line = pre ENVIRON["_DXB_V"] }
{ l[NR] = $0 }
END {
  start = 0; stop = 0; hit = 0
  for (i = 1; i <= NR; i++) {
    if (!start) { if (l[i] == hdr) { start = i; stop = i } continue }
    if (substr(l[i], 1, 1) == "[") break
    if (l[i] != "") stop = i
    if (!hit && substr(l[i], 1, n) == pre) hit = i
  }
  for (i = 1; i <= NR; i++) {
    if (i == hit) print line; else print l[i]
    if (!hit && start && i == stop) print line
  }
  if (!start) {
    if (NR && l[NR] != "") print ""
    print hdr
    print line
  }
}'

# _dxb_kv_get FILE SECTION KEY SEP
_dxb_kv_get() {
  [[ -f $1 ]] || return 1
  _DXB_S=$2 _DXB_K=$3 _DXB_SEP=$4 _dxb_awk "$_DXB_KV_GET_AWK" "$1"
}

# _dxb_commit FILE CONTENT: dxb_write_if_changed, 0 written / 1 unchanged / 6 failed.
# The caller verifies by reading back; this only maps a vanished or unreadable result to 6.
_dxb_commit() {
  local file=$1 content=$2 rc
  dxb_write_if_changed "$file" "$content" 2> /dev/null
  rc=$?
  [[ -f $file && $(< "$file") == "$content" ]] || return 6
  return "$rc"
}

# _dxb_kv_set FILE SECTION KEY VALUE SEP
_dxb_kv_set() {
  local file=$1 sec=$2 key=$3 val=$4 sep=$5 src=/dev/null out
  if [[ $val == *$'\n'* || $key == *$'\n'* ]]; then return 6; fi
  [[ -f $file ]] && src=$file
  out=$(_DXB_S=$sec _DXB_K=$key _DXB_V=$val _DXB_SEP=$sep _dxb_awk "$_DXB_KV_SET_AWK" "$src") || return 6
  _dxb_commit "$file" "$out" || return $?
}

dxb_ini_get() { _dxb_kv_get "$1" "$2" "$3" '='; }
dxb_ini_set() {
  local rc
  _dxb_kv_set "$1" "$2" "$3" "$4" '='
  rc=$?
  (( rc == 6 )) && return 6
  [[ $(dxb_ini_get "$1" "$2" "$3") == "$4" ]] || return 6
  return "$rc"
}

dxb_prefs_get() { _dxb_kv_get "$1" . "$2" ':'; }
dxb_prefs_set() {
  local rc
  _dxb_kv_set "$1" . "$2" "$3" ':'
  rc=$?
  (( rc == 6 )) && return 6
  [[ $(dxb_prefs_get "$1" "$2") == "$3" ]] || return 6
  return "$rc"
}

# dxb_ini_qstring TEXT: TEXT as QSettings writes a string.
dxb_ini_qstring() {
  local t=$1
  if [[ $t == *[,\;\"=]* || $t == ' '* || $t == *' ' ]]; then
    t=${t//\\/\\\\}
    t=${t//\"/\\\"}
    printf '"%s"' "$t"
  else
    printf '%s' "$t"
  fi
}

# _dxb_qesc LENGTH STRING: the length byte then STRING, escaped the way Qt writes a QSettings
# byte string: \0 \a \b \t \n \v \f \r \" \\ by name, other non-printable bytes as \xNN with the
# fewest hex digits, and a printable byte that follows a \x escape and is itself a hex digit is
# escaped too (otherwise the reader would swallow it into the number).
_dxb_qesc() {
  local LC_ALL=C bytes=("$1") s=$2 i out='' b hexrun=0 c
  for ((i = 0; i < ${#s}; i++)); do
    c=${s:i:1}
    printf -v b '%d' "'$c"
    bytes+=("$b")
  done
  for b in "${bytes[@]}"; do
    case $b in
      0) out+='\0'; hexrun=0 ;;
      7) out+='\a'; hexrun=0 ;;
      8) out+='\b'; hexrun=0 ;;
      9) out+='\t'; hexrun=0 ;;
      10) out+='\n'; hexrun=0 ;;
      11) out+='\v'; hexrun=0 ;;
      12) out+='\f'; hexrun=0 ;;
      13) out+='\r'; hexrun=0 ;;
      34) out+='\"'; hexrun=0 ;;
      92) out+=\\\\; hexrun=0 ;;
      *)
        printf -v c '%b' "\\0$(printf '%o' "$b")"
        if (( b < 32 || b > 126 )) || { (( hexrun )) && [[ $c == [0-9a-fA-F] ]]; }; then
          printf -v c '\\x%x' "$b"
          hexrun=1
        else
          hexrun=0
        fi
        out+=$c
        ;;
    esac
  done
  printf '%s' "$out"
}

# dxb_ini_qenum TYPE VALUE: a Qt enum stored as a QVariant user type (name length bytes < 256).
dxb_ini_qenum() {
  local type=$1 val=$2
  printf '@Variant(\\0\\0\\0\\x7f\\0\\0\\0%s\\0\\0\\0\\0%s\\0)' \
    "$(_dxb_qesc $((${#type} + 1)) "$type")" "$(_dxb_qesc $((${#val} + 1)) "$val")"
}

# fldigi: one <TAG>text</TAG> per line inside <FLDIGI_DEFS>. Env: _DXB_K tag, _DXB_V escaped text.
_DXB_XML_GET_AWK='
BEGIN { o = "<" ENVIRON["_DXB_K"] ">"; c = "</" ENVIRON["_DXB_K"] ">" }
substr($0, 1, length(o)) == o && length($0) >= length(o) + length(c) && substr($0, length($0) - length(c) + 1) == c {
  print substr($0, length(o) + 1, length($0) - length(o) - length(c)); found = 1; exit
}
END { exit(found ? 0 : 1) }'
_DXB_XML_SET_AWK='
BEGIN { o = "<" ENVIRON["_DXB_K"] ">"; c = "</" ENVIRON["_DXB_K"] ">"; line = o ENVIRON["_DXB_V"] c }
{ l[NR] = $0 }
END {
  hit = 0; close_at = 0
  for (i = 1; i <= NR; i++) {
    if (!hit && substr(l[i], 1, length(o)) == o && length(l[i]) >= length(o) + length(c) && substr(l[i], length(l[i]) - length(c) + 1) == c) hit = i
    if (l[i] == "</FLDIGI_DEFS>") close_at = i
  }
  if (!hit && !close_at) exit 3
  for (i = 1; i <= NR; i++) {
    if (i == close_at && !hit) print line
    if (i == hit) print line; else print l[i]
  }
}'

dxb_xml_get() {
  local v
  [[ -f $1 ]] || return 1
  v=$(_DXB_K=$2 _dxb_awk "$_DXB_XML_GET_AWK" "$1") || return 1
  v=${v//'&lt;'/<}
  v=${v//'&gt;'/>}
  v=${v//'&quot;'/\"}
  v=${v//'&apos;'/\'}
  printf '%s\n' "${v//'&amp;'/'&'}"
}

dxb_xml_set() {
  local file=$1 tag=$2 text=$3 src out rc esc
  if [[ $text == *$'\n'* ]]; then return 6; fi
  esc=${text//&/'&amp;'}
  esc=${esc//</'&lt;'}
  esc=${esc//>/'&gt;'}
  if [[ -f $file ]]; then
    src=$(< "$file")
  else
    src=$'<?xml version="1.0"?>\n<FLDIGI_DEFS>\n</FLDIGI_DEFS>'
  fi
  out=$(printf '%s\n' "$src" | _DXB_K=$tag _DXB_V=$esc _dxb_awk "$_DXB_XML_SET_AWK") || return 6
  _dxb_commit "$file" "$out"
  rc=$?
  (( rc == 6 )) && return 6
  [[ $(dxb_xml_get "$file" "$tag") == "$text" ]] || return 6
  return "$rc"
}
