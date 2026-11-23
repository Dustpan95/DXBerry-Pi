#!/bin/bash
# Read-only prerequisites, never queue acceptance or permission to activate it.
# Exit 0: prerequisites observed; 77: unavailable; 2: invalid invocation.
set -uo pipefail
if (( $# )); then echo 'usage: probe-radio-event-manager.sh' >&2; exit 2; fi
reasons=()
(( EUID == 0 )) || reasons+=('the root system-manager checks require root')
for tool in systemctl stat findmnt timeout; do
  command -v "$tool" >/dev/null 2>&1 || reasons+=("required tool is missing: $tool")
done
if (( ${#reasons[@]} == 0 )); then
  [[ -r /proc/1/comm && $(< /proc/1/comm) == systemd ]] \
    || reasons+=('PID 1 is not systemd')
  if [[ $(stat -fc '%T' /sys/fs/cgroup 2>/dev/null) != cgroup2fs ]]; then
    reasons+=('a unified cgroup2 view is unavailable')
  else
    options=$(findmnt -n -o VFS-OPTIONS -T /sys/fs/cgroup 2>/dev/null)
    case ,$options, in
      *,rw,*) [[ -w /sys/fs/cgroup ]] || reasons+=('the cgroup2 directory is not writable') ;;
      *) reasons+=('the cgroup2 mount is read-only or its options are unavailable') ;;
    esac
  fi
  version=$(command timeout --kill-after=1 3 systemctl --system show --no-pager --property=Version 2>/dev/null)
  result=$?
  if (( result != 0 )) || [[ ! $version =~ ^Version=.+$ || $version == *$'\n'* ]]; then
    reasons+=('the system manager did not return an unambiguous version')
  fi
fi
if (( ${#reasons[@]} )); then
  printf 'SKIP: %s\n' "${reasons[@]}"
  echo 'No units or configuration were changed. Manager acceptance remains open.'
  exit 77
fi
echo 'System-manager prerequisites observed; delegation and queue behavior are not yet verified.'
echo 'Run behavioral checks only in an explicitly isolated Docker test environment.'
