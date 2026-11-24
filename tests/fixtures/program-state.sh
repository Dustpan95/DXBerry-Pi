#!/usr/bin/env bash
# Metadata-only helper stand-in for shell unit fixtures. Actual helper/file
# ordering is checked separately in the hardware-free process integration.
set -uo pipefail
[[ $# == 3 && $1 == --state && $2 == /*/station ]] || exit 2
case $3 in advance|prepare-advance) ;; *) exit 2 ;; esac
printf '%s\n' '{"ok":true,"protocol":1}'
