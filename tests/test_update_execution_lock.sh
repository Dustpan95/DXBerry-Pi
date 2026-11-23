#!/usr/bin/env bash
# Fixture variables and callbacks stay in isolated subshells; effects use files.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

update_execution_wait_file() {
  local deadline=$((SECONDS + 5))
  while [[ ! -e $1 ]]; do
    (( SECONDS < deadline )) || { _fail "timed out waiting for $1"; return 1; }
    command sleep 0.01
  done
}

update_execution_stub_bodies() {
  dxb_update_apply_graywolf() { update_execution_effect; }
  dxb_update_apply_dxberry() { update_execution_effect; }
  dxb_update_apply_rollback() { update_execution_effect; }
  dxb_update_apply_system() { update_execution_effect; }
  dxb_update_apply_app() { update_execution_effect; }
  dxb_apps_install() { update_execution_effect; }
  dxb_apps_remove() { update_execution_effect; }
}

test_update_execution_lock_refuses_every_kind_before_body_and_result() (
  up_cli_env
  ( umask 077; : > "$DXB_RUN_DIR/update-apply.lock" )
  local holder kind
  exec {holder}< "$DXB_RUN_DIR/update-apply.lock"
  command flock -s "$holder"
  printf 'retained-cache\n' > "$DXB_UPDATE_CACHE"
  printf '{"exit":47,"finished":123}\n' > "$DXB_UPDATE_RESULT"
  for kind in graywolf dxberry rollback system app app-install app-remove; do
    (
      source "$DXB_ROOT/provision/bin/dxberry-update"
      dxb_require_root() { :; }
      update_execution_effect() { : > "$TEST_TMP/live-effect"; return 8; }
      update_execution_stub_bodies
      DXB_UPDATE_APPLY_LOCK_WAIT=0 main apply "$kind" pat
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/live-effect ]] || _fail "$kind entered body through held execution lock"
    assert_eq "$(cat "$DXB_UPDATE_CACHE" 2>/dev/null)" retained-cache
    assert_eq "$(cat "$DXB_UPDATE_RESULT")" '{"exit":47,"finished":123}'
  done
  exec {holder}>&-
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    update_execution_effect() { : > "$TEST_TMP/live-effect"; return 8; }
    update_execution_stub_bodies
    DXB_UPDATE_APPLY_LOCK_WAIT=0 main apply system
  ) > /dev/null 2>&1
  assert_eq "$?" 8
  [[ -e $TEST_TMP/live-effect ]] || _fail 'released execution lock did not allow retry'
  [[ ! -e $DXB_UPDATE_CACHE ]] || _fail 'completed retry retained stale cache'
  assert_eq "$(jq -r .exit "$DXB_UPDATE_RESULT")" 8
  assert_ok command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_update_execution_lock_covers_body_without_taking_admission_lock() (
  up_cli_env
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    dxb_update_apply_system() {
      command flock -s -n "$DXB_RUN_DIR/update.lock" true || { _fail 'execution took legacy admission lock'; return 71; }
      : > "$TEST_TMP/entered"
      update_execution_wait_file "$TEST_TMP/release" || return 72
      return 8
    }
    main apply system
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err" &
  local worker=$!
  trap 'touch "$TEST_TMP/release"; kill "$worker" 2>/dev/null; wait "$worker" 2>/dev/null || true' EXIT
  update_execution_wait_file "$TEST_TMP/entered" || return
  if command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true; then _fail 'reader entered unfinished update body'; fi
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    main job --json
  ) > "$TEST_TMP/job-status" 2> "$TEST_TMP/status-err"
  assert_eq "$?" 0
  assert_eq "$(jq -r .job "$TEST_TMP/job-status")" null
  : > "$TEST_TMP/release"
  wait "$worker"; assert_eq "$?" 8
  assert_eq "$(jq -r .exit "$DXB_UPDATE_RESULT")" 8
  assert_ok command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_update_execution_lock_covers_result_publication() (
  up_cli_env
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    dxb_update_apply_system() { return 8; }
    jq() {
      if [[ $1 == -cn && ${2:-} == --argjson && ${3:-} == e ]]; then
        : > "$TEST_TMP/result-entered"
        update_execution_wait_file "$TEST_TMP/release" || return 72
      fi
      command jq "$@"
    }
    main apply system
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err" &
  local worker=$!
  trap 'touch "$TEST_TMP/release"; kill "$worker" 2>/dev/null; wait "$worker" 2>/dev/null || true' EXIT
  update_execution_wait_file "$TEST_TMP/result-entered" || return
  if command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true; then _fail 'reader entered unfinished result publication'; fi
  : > "$TEST_TMP/release"
  wait "$worker"; assert_eq "$?" 8
  assert_eq "$(jq -r .exit "$DXB_UPDATE_RESULT")" 8
  assert_ok command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_update_execution_lock_keeps_exit_cleanup_protected_and_closes() (
  up_cli_env
  ( umask 077; : > "$DXB_RUN_DIR/update-apply.lock" )
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    dxb_update_apply_system() {
      trap 'if command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true; then : > "$TEST_TMP/cleanup-escaped"; else : > "$TEST_TMP/cleanup-protected"; fi' EXIT
      exit 73
    }
    main apply system
    echo "$?" > "$TEST_TMP/returned"
  ) > /dev/null 2>&1
  [[ -f $TEST_TMP/returned ]] || _fail 'body exit escaped command subshell'
  assert_eq "$(cat "$TEST_TMP/returned" 2>/dev/null)" 73
  [[ -f $TEST_TMP/cleanup-protected && ! -e $TEST_TMP/cleanup-escaped ]] || _fail 'exit cleanup ran outside execution lease'
  assert_ok command flock -s -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_update_execution_lock_refuses_unsafe_or_unavailable_state() (
  local base=$TEST_TMP fault
  for fault in missing-flock failed-flock bad-directory symlink-directory symlink fifo hardlink mode content replaced-file replaced-directory; do
    TEST_TMP=$base/$fault; export TEST_TMP
    up_cli_env
    local lock=$DXB_RUN_DIR/update-apply.lock
    case $fault in
      bad-directory) mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.real"; echo retained > "$DXB_RUN_DIR" ;;
      symlink-directory) mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.real"; ln -s "$DXB_RUN_DIR.real" "$DXB_RUN_DIR" ;;
      symlink) echo retained > "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$lock" ;;
      fifo) mkfifo "$lock" ;;
      hardlink) (umask 077; : > "$lock"); ln "$lock" "$TEST_TMP/other" ;;
      mode) : > "$lock"; chmod 644 "$lock" ;;
      content) (umask 077; echo retained > "$lock") ;;
    esac
    (
      source "$DXB_ROOT/provision/bin/dxberry-update"
      dxb_require_root() { :; }
      dxb_update_apply_system() { : > "$TEST_TMP/live-effect"; return 8; }
      case $fault in
        missing-flock) command() { [[ $* != '-v flock' ]] && builtin command "$@"; } ;;
        failed-flock) flock() { return 1; } ;;
        replaced-file) flock() { mv "$lock" "$lock.old"; (umask 077; : > "$lock"); } ;;
        replaced-directory) flock() { mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.old"; mkdir "$DXB_RUN_DIR"; } ;;
      esac
      main apply system
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/live-effect ]] || _fail "$fault entered update body"
    if [[ $fault == symlink ]]; then assert_eq "$(cat "$TEST_TMP/target")" retained; fi
  done
)
