#!/usr/bin/env bash
# shellcheck disable=SC1090,SC1091,SC2034,SC2317

package_generation_effect() { printf '%s\n' "$*" >> "$TEST_TMP/package-effects"; }

package_generation_refusal() (
  local entry=$1 fault=$2
  mkdir -p "$TEST_TMP"
  case $entry in install|remove|app) fx_apps_env ;; *) up_env ;; esac
  generation_record_env
  case $entry in
    install)
      dxb_apps_user_ensure() { package_generation_effect accounts; return 1; }
      ;;
    remove)
      dxb_apps_in_use() { :; }
      package_control() { case $1 in stop|disable) package_generation_effect "$*" ;; *) return 1 ;; esac; }
      DXB_SYSTEMCTL=package_control
      ;;
    app)
      dxb_apps_installed() { return 0; }
      _dxb_update_app_sessions() { echo dxberry-screen@qsstv-radio1.service; }
      package_control() { package_generation_effect "$*"; }
      DXB_SYSTEMCTL=package_control
      dxb_apps_install() { package_generation_effect install; return 6; }
      _dxb_update_sessions_start() { package_generation_effect recovery; }
      ;;
    graywolf)
      systemctl() { [[ $1 == is-active ]] && return 0; package_generation_effect "$*"; }
      dxb_gw_installed_version() { echo 0.14.13; }
      dxb_gw_install() { package_generation_effect graywolf; return 1; }
      ;;
    system)
      package_dpkg() { package_generation_effect dpkg; return 1; }
      DXB_DPKG_CMD=package_dpkg
      ;;
    tree|rollback)
      up_provision_stub
      up_tree "$DXB_OPT" 0.3.0-rc3
      if [[ $entry == tree ]]; then
        dxb_update_set_prereleases on
        up_release_file 0.3.0-rc4
      else
        up_tree "$DXB_OPT.prev" 0.3.0-rc2
      fi
      # The real native release preflight can pause acquisition, so it is
      # already live work even when the subsequent tree swap never happens.
      _dxb_update_program_release_guard() { package_generation_effect preflight; return 0; }
      sync() { package_generation_effect swap; }
      ;;
  esac
  case $fault in
    missing) DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper ;;
    uncertain)
      printf '%s\n' '{"ok":true,"protocol":1}' > "$TEST_TMP/generation-output"
      printf '7\n' > "$TEST_TMP/generation-exit"
      ;;
    unexpected)
      printf '%s\n' '{"ok":true,"protocol":1,"nonce":"private-fixture"}' > "$TEST_TMP/generation-output"
      printf '0\n' > "$TEST_TMP/generation-exit"
      ;;
  esac
  case $entry in
    install) dxb_apps_install qsstv ;;
    remove) dxb_apps_remove qsstv ;;
    app) dxb_update_apply_app qsstv ;;
    graywolf) dxb_update_apply_graywolf ;;
    system) dxb_update_apply_system ;;
    tree) dxb_update_apply_dxberry ;;
    rollback) dxb_update_apply_rollback ;;
  esac > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 6
  [[ ! -e $TEST_TMP/package-effects ]] || _fail "$entry reached live work after $fault tracking"
  assert_not_contains "$(cat "$TEST_TMP/err" "$TEST_TMP/out")" private-fixture
  if [[ $entry == tree || $entry == rollback ]]; then
    assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc3
    [[ ! -e $TEST_TMP/provision-ran ]] || _fail 'refused swap reached provisioning'
  fi
)

test_package_changes_require_confirmed_generation() (
  local entry fault
  for entry in install remove app graywolf system tree rollback; do
    for fault in missing uncertain unexpected; do
      TEST_TMP="$TEST_TMP/$entry-$fault" package_generation_refusal "$entry" "$fault"
    done
  done
)

test_package_validation_refusals_do_not_advance_generation() (
  fx_apps_env
  generation_record_env
  dxb_apps_install qsstv bad-mode >/dev/null 2>&1; assert_eq "$?" 2
  dxb_apps_install no-such-program >/dev/null 2>&1; assert_eq "$?" 3
  dxb_apps_install graywolf >/dev/null 2>&1; assert_eq "$?" 6
  dxb_apps_in_use() { echo 'fixture radio still uses this program'; }
  dxb_apps_remove qsstv >/dev/null 2>&1; assert_eq "$?" 5
  dxb_update_apply_app graywolf >/dev/null 2>&1; assert_eq "$?" 6
  dxb_apps_installed() { return 1; }
  dxb_update_apply_app qsstv >/dev/null 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
)

test_package_update_noops_do_not_advance_generation() (
  up_env
  generation_record_env
  printf 'PASSWORD=examplepass\nGRAYWOLF_VERSION=0.14.14\n' > "$DXB_BOOT_DIR/dxberry.txt"
  dxb_update_apply_graywolf > /dev/null 2>&1; assert_eq "$?" 0
  dxb_update_dxberry_info() { echo '{"update":false,"installed":"fixture"}'; }
  dxb_update_apply_dxberry > /dev/null 2>&1; assert_eq "$?" 0
  dxb_update_apply_rollback > /dev/null 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
)

test_package_update_attempts_recovery_after_nested_tracking_failure() (
  fx_apps_env
  generation_record_env
  printf '2\n' > "$TEST_TMP/generation-fail-at"
  dxb_apps_installed() { return 0; }
  _dxb_update_app_sessions() { echo dxberry-screen@qsstv-radio1.service; }
  package_control() { package_generation_effect "$*"; }
  DXB_SYSTEMCTL=package_control
  dxb_apps_user_ensure() { : > "$TEST_TMP/unconfirmed-install"; return 1; }
  _dxb_update_sessions_start() { : > "$TEST_TMP/recovery"; return 0; }
  dxb_update_apply_app qsstv > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 2
  assert_contains "$(cat "$TEST_TMP/package-effects")" 'stop dxberry-screen@qsstv-radio1.service'
  [[ -e $TEST_TMP/recovery ]] || _fail 'nested tracking failure suppressed recovery attempt'
  [[ ! -e $TEST_TMP/unconfirmed-install ]] || _fail 'nested tracking failure reached installer effects'
)

test_package_attempts_keep_generation_after_later_failure() (
  fx_apps_env
  generation_record_env
  dxb_apps_user_ensure() { package_generation_effect accounts; return 1; }
  dxb_apps_install qsstv >/dev/null 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 1
  dxb_apps_install qsstv >/dev/null 2>&1; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 2
  assert_eq "$(wc -l < "$TEST_TMP/package-effects")" 2
)
