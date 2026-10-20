# DXBerry-Pi Applications (v0.4.0-rc1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn DXBerry-Pi into a browser-run multi-mode station: install WSJT-X, JS8Call, fldigi, QSSTV, Pat + ARDOP, flrig and a Wavelog logbook from the console, hand each radio to a program, open the program's real window in its own browser tab, log contacts to any Wavelog/Cloudlog server, and update each program on its own.

**Architecture:** Each radio program is an application module under `provision/lib/apps/` with the plumbing contract, extended (kind, installed, per-radio unit, first fill, version). A running program is one `dxberry-screen@<app>-<radio>.service`: a root `prepare` step (first fill, wiring, an env file) and then, as the system user `dxbapps`, Xvnc on a unix socket + openbox + the program (+ a private one-card PulseAudio when the program needs PulseAudio). The browser tab is a Cockpit page that speaks VNC (vendored noVNC) over Cockpit's own authenticated external channel to that socket. `dxberry-app` installs/removes programs through the same job machinery as `dxberry-update` (one job at a time, shared locks); `dxberry-logbook` keeps the logbook setting, posts contacts (a UDP bridge with an offline queue) and reads recent contacts. The page gains a station dashboard, a Programs section and the program tab.

**Tech Stack:** bash 5, jq, awk, curl, systemd (template units, `ExecStartPre=+`, `ExecCondition=`), TigerVNC `Xvnc`, openbox, PulseAudio (per-session, `-n`), socat, nginx (`auth_request`), PHP-FPM + MariaDB (Wavelog), Cockpit 337 (external channels), noVNC (vendored core, MPL-2.0), plain HTML/CSS/JS.

**Spec:** `docs/design/2026-10-19-applications.md` (approved by the owner 2026-10-20). Read §5–§13 first. Earlier specs it builds on: `docs/design/2026-09-09-radio-plumbing.md` (hand-over §9.2, rigctld §7.3), `docs/design/2026-09-29-console.md` (page rules §6, jobs §11). Previous plan, whose job pattern this one reuses: `docs/plans/2026-10-06-console-rc4.md`.

## Rulings this plan makes on the spec (recorded in the SDD ledger)

1. **"Newest" is GitHub's own `releases/latest`** for every upstream source (WSJT-X, JS8Call-improved, Pat, ardopcf, Wavelog), never a sort by version number: ardopcf's newest-numbered release, 2.0.3.2.1, is a mis-numbered old build its author asks people not to use (checked 2026-10-20). `releases/latest` never returns a pre-release or a draft.
2. **The PulseAudio path (§8) is a per-session `pulseaudio -n`** that loads only the radio's own card (`module-alsa-sink` + `module-alsa-source` on `plughw:CARD=<ID>,DEV=0`) and a native socket in the session's private directory. The spec named PipeWire; the requirement it serves (one card per session, never another) is met with no session manager to configure. The Pi-wide `pulseaudio.socket`/`.service` user units are masked globally so no login session can ever start a desktop sound server that would take Graywolf's card (installing `pulseaudio` enables them for every user — seen on the live Pi 2026-10-20).
3. **Which programs use PulseAudio:** JS8Call-improved (it bundles Qt 6, whose audio is PulseAudio-only on Linux), fldigi and QSSTV (their ALSA device lists name cards by description, which two identical interfaces share; through the private PulseAudio they see exactly one device). WSJT-X (Qt 5) and ARDOP open `plughw:CARD=<ID>,DEV=0` directly.
4. **One logging port for all radios:** WSJT-X and JS8Call broadcast to `127.0.0.1:2333` (their own default `N1MMServerPort`); one bridge listens there.
5. **Single-copy programs in this release:** Pat (one web interface, one ARDOP port) and QSSTV (its settings path is fixed: `~/.config/ON4QZ/qsstv_9.0.conf`). Claiming a second radio for either is refused with exit 5 and a message.
6. **Per-radio ports** are derived from the radio's rigctld port: index `i = (rigctld_port - 4532) / 2`; fldigi XML-RPC `7362 + i`, ARQ `7322 + i`; flrig XML-RPC `12345 + i`. Fixed: ARDOP `8515` (data 8516), Pat's web interface `127.0.0.1:8180`, Pat proxy `8081`, Wavelog `8082` (HTTPS, LAN) and `127.0.0.1:8083` (plain, for the bridge only).
7. **A session is wired by its own start:** the unit's root `prepare` step runs first fill (once) and re-wires when the radio's wiring hash differs from the last one written for that session. So `apply` never re-wires a session program; it only starts or stops sessions by presence. A running session whose radio changed is re-wired at its next start (the card says so).
8. **rigctld keys `cm108`/`gpio` PTT for every owner except Graywolf** (spec §9.1): `dxb_rigctld_env` takes the owner's kind; the env file is regenerated in claim and release, and rigctld restarts only when its arguments changed. The wiring hash itself is unchanged, so Graywolf radios see no re-wire after the update.
9. **Stopping a program** sends it SIGTERM through the session runner, waits 10 s, then kills it. Every program here writes its settings when the operator presses OK/Save in it, so a stop loses only window positions. A program that the operator quits from its own menu ends the unit (Stopped); it stays enabled, so it comes back at the next boot or Start.
10. **Wavelog on the Pi:** DXBerry installs nginx, PHP-FPM, MariaDB and Wavelog's files, creates the database and a MariaDB user `www-data` that logs in through its unix socket (no password exists to show or leak), and leaves the first user to Wavelog's own installer at `https://<pi>:8082/install`, which only a logged-in console user can reach (the same session check as Pat's proxy). PHP packages are Debian's unversioned names (`php-fpm`, `php-mysql`, …), never `php8.4-*`.
11. **Installing nginx never fights Cockpit for port 80:** a `policy-rc.d` that refuses service starts is in place for the install, Debian's default site is removed, and nginx starts only with DXBerry's sites (8081, 8082, 127.0.0.1:8083).
12. **One job system:** `dxberry-app install|remove` and `dxberry-update app` are jobs of `dxberry-update` (kinds `app-install`, `app-remove`, `app`), sharing its job file, result, locks and busy rules. The Programs section follows them with `dxberry-update job --json`.
13. **The program tab needs no administrative access:** `dxberry-app screen SESSION --json` runs as the logged-in user (it reads only unit state and a 0640 `root:dxbscreen` env file), and the screen socket is `dxbscreen`-group 0660. The operator's login user joins `dxbscreen`; a Cockpit session started before that join needs a log out and in once (the tab says so when the socket is refused).
14. **Dashboard code lives in a new `dashboard.js`** (prefix `dash`/`DASH`, one state object `dashboard`), the Programs section in `programs.js` (prefix `prog`/`PROG`, state `programs`), next to `settings.js` and `updates.js` in the shared page scope.
15. **noVNC is vendored** from its newest release by `build/vendor-novnc.sh` (core + `vendor/pako`, licence and headers kept) into `provision/cockpit/dxberry/novnc/`; re-running the script is how it is updated (no version is written into any file but the vendored `VERSION` note).

## Global Constraints

- License GPL-2.0-or-later. **No author or attribution lines of any kind** in files, commits or tags (no AI-assistant or model-vendor names, no co-author or session trailers, no "Generated with" footers). CI runs `git grep -qIiE 'co-?auth[o]red-?by|cla[u]de|anthr[o]pic'`; vendored noVNC files are checked with the same grep before they are committed. Commit messages: imperative subject + plain body.
- Commit as the owner: `git -c user.name="Dustpan95" -c user.email="Dustpan95@users.noreply.github.com" commit ...`, on branch `feat/applications`.
- No secrets in the tree or in any argument list: API keys go through standard input or a `curl -K <(...)` config, never `argv`, never printed (shown back only as `"set"`).
- **No version pins.** Packages by name; upstream programs from `releases/latest`; `VERSION` is `0.4.0`.
- All scripts `#!/bin/bash`, `set -uo pipefail` in commands (no `set -e`); library functions `dxb_` (private `_dxb_`); module functions `app_<name>_*`; every path and external command a library touches has a `DXB_*` override, worked out when the function runs if a test exports it later.
- shellcheck-clean: `shellcheck -x provision/bin/* provision/lib/*.sh provision/lib/apps/*.sh build/*.sh boot/*.sh tests/*.sh tests/fixtures/*.sh`. `build/build-image.sh --check` prints `tree ok`. `tests/run.sh` prints `0 failures` (allow 600 s).
- Tests need only bash 5, awk, jq, tar, sha256sum, dpkg (`--compare-versions`); node optional. Every `tests/test_*.sh` is sourced into **one** shell: each new file uses its own helper prefix (listed per task); stubs of real command names (`systemctl`, `curl`, `apt-get`, `dpkg-query`, `runuser`, `chown`, `useradd`) only through `DXB_*` overrides or inside a subshell. `sleep` may be a no-op left by another file: use `command sleep` where a real wait matters.
- Existing tests stay green **unmodified** except where a task names the test it changes and why (spec §19: "Graywolf behavior unchanged").
- The page loads only its own files; no inline scripts, handlers or `style=` in HTML; text nodes only (never `innerHTML`); every command through `run()` (superuser) except `dxberry-app screen` in the program tab. New top-level JS names follow ruling 14.
- Nothing in a test reaches the network or a Pi. **Only the controller touches the live Pi (Task 16)**; implementers never ssh to it.
- The live Pi: Graywolf's hand-made channel "VHF APRS" (id 3) on ALSA card `CODEC` must be intact at the end. Before any hardware step that touches the radio, back up Graywolf's database and configuration; restore and verify after.

## Verified facts this plan relies on (2026-10-20, live Pi 4 4 GB, Trixie, kernel 6.18.50)

- `Xvnc -rfbunixpath /run/x.sock -rfbunixmode 0660 -rfbport -1 -SecurityTypes None -nolisten tcp` (TigerVNC 1.15.0) listens only on the socket (`srw-rw----`, no TCP listener) and greets with `RFB 003.008`. `-displayfd`, `-AcceptSetDesktopSize` exist.
- WSJT-X 3.0.2 (`wsjtx-3.0.2-linux-aarch64.deb`, GitHub `WSJTX/wsjtx` latest; no checksum file on that release, `SHA256SUMS` on newer betas) is Qt 5 and depends on Debian's Qt 5 libraries, `libqt5multimedia5-plugins`, `libportaudio2`.
- JS8Call-improved: latest `v3.0.3` ships only `JS8Call-v3.0.3-aarch64.AppImage`; `v3.0.2` shipped `js8call_3.0.2_arm64.deb`. The `.deb` bundles Qt 6 under `/usr/lib/js8call/Qt` and needs `libxcb-cursor0` (not in its Depends; without it: "xcb-cursor0 or libxcb-cursor0 is needed to load the Qt xcb platform plugin", abort).
- Pat 1.0.0 `pat_1.0.0_linux_arm64.deb` (ships `/usr/bin/pat` and `pat@.service`). ardopcf latest `1.0.4.1.3`, asset `ardopcf_arm_Linux_64`. Wavelog latest `3.2.4`, no assets (source tarball of the tag); its API has `version`, `qso`, `get_contacts_adif`, no `recent_qsos`; its installer is `install/index.php` (database step, then "First User"). Cloudlog's API has `auth/<key>`, `qso`, `recent_qsos/<slug>/<n>`.
- Debian Trixie candidates on the Pi: fldigi 4.2.06, flrig 2.0.05, qsstv 9.5.8, tigervnc-standalone-server 1.15.0, openbox 3.6.1, socat 1.8.0.3, nginx 1.26.3, php 8.4, mariadb-server 11.8.6, pulseaudio 17.0.
- Settings files written by each program on first run (captured on the Pi; now `tests/fixtures/apps/`):
  - `~/.config/WSJT-X - <rig-name>.ini`, `[Configuration]`: `MyCall=`, `MyGrid=`, `Rig=None`, `CATNetworkPort=`, `PTTMethod=@Variant(\0\0\0\x7f\0\0\0\x1eTransceiverFactory::PTTMethod\0\0\0\0\xfPTT_method_VOX\0)`, `PTTport=`, `BroadcastToN1MM=false`, `N1MMServer=127.0.0.1`, `N1MMServerPort=2333`, `Polling=0`; `SoundInName`/`SoundOutName` appear once a device is chosen. Enum settings are Qt binary variants; a string with a comma is written quoted (QSettings rule).
  - `~/.config/JS8Call - <rig-name>.ini`, `[Configuration]`: the same keys plus `BroadcastToN3FJP`; same variant format.
  - fldigi `--config-dir DIR` → `DIR/fldigi_def.xml`, one `<KEY>value</KEY>` per line after a comment: `AUDIOIO` (0 OSS, 1 PortAudio, 2 PulseAudio, 3 File), `PULSESERVER`, `CHKUSEHAMLIBIS`, `HAMRIGMODEL`, `HAMRIGNAME`, `HAMRIGDEVICE`, `HAMLIBCMDPTT`, `RTSPTT`, `DTRPTT`, `CHKUSEXMLRPCIS`, `FLRIG_IP_ADDRESS`, `FLRIG_IP_PORT`, `FLRIG_AUTO_ENABLE`, `XMLRPC_PORT` (7362), `ARQ_PORT` (7322), `MYCALL`, `MYLOC`, `ENCLOUDLOG`, `CLOUDLOG_API_URL`, `CLOUDLOG_API_KEY`, `CLOUDLOG_STATION_ID`.
  - QSSTV `~/.config/ON4QZ/qsstv_9.0.conf`: `[PERSONAL] callsign=NOCALL locator=NOLOCATOR`; `[SOUND] alsaSelected pulseSelected inputAudioDevice outputAudioDevice`; `[radio1] enableCAT serialPort radioModel="29001 ADAT www.adat.ch,ADT-200A" pttType=1 enableSerialPTT enableXMLRPC XMLRPCPort`.
  - flrig `--config-dir DIR` → `DIR/flrig.prefs` (FLTK prefs: `key:value` lines under `[.]`; first run has `xcvr_name:NONE`).
- Spike leftovers on the live Pi (removed in Task 16): user `dxbspike` (`/var/lib/dxbspike`), `/root/dxb-spike/`, packages `x11-apps netpbm xdotool libxcb-cursor0 pulseaudio wsjtx js8call fldigi flrig qsstv tigervnc-standalone-server openbox socat`, and the global mask of `pulseaudio.socket`/`.service` (kept: the provisioner sets it anyway).

## Review Focus

1. **Two radios with the same interface model** must each get their own card: the session env names its own ALSA id, the private PulseAudio loads only that device, and nothing ever opens `CODEC` or another radio's card (tests: Task 4 `test_screen_env_names_only_its_own_card`, `test_screen_pulse_loads_only_the_session_card`).
2. **The operator's edits survive every re-wire:** a key the operator changed (MyCall, DialFreq, a macro), the file's comments and order, and keys DXBerry never owned are untouched; only wiring keys change; a missing file is created with only first-fill and wiring keys (tests in Tasks 2, 6–9: `*_rewire_keeps_operator_keys`).
3. **A contact is never lost or leaked:** logbook unset, unreachable or refusing → the contact is queued on the SD card and posted later; the key never appears in a process argument, the log or any `--json` answer (Task 12: `test_logbook_queues_when_unreachable`, `test_logbook_key_never_in_argv_or_output`).
4. **Unplug and replug while a program runs:** the session stops (still enabled), the card says the radio is unplugged; on replug it starts again after rigctld; a Graywolf radio on the same Pi is untouched (Task 1: `test_apply_stops_and_restarts_sessions_by_presence`).
5. **Conflicting actions are refused with a reason:** removing a program a radio runs names the radio (exit 5); an install while any update/settings job runs is exit 5; a second copy of a single-copy program is exit 5 (Tasks 1, 4: `test_app_remove_refused_while_a_radio_runs_it`, `test_claim_refuses_second_copy_of_single_copy_program`).

## File Structure

| File | Responsibility |
|---|---|
| `provision/lib/radio.sh` | contract extensions, session-aware claim/release/stop-if-idle/apply |
| `provision/lib/rigctld.sh` | owner-kind PTT arguments, flrig model 4 |
| `provision/lib/apps/graywolf.sh` | `app_graywolf_kind`, `_installed`, `_version` |
| `provision/lib/inifile.sh` (new) | one-key reads/writes: Qt INI, fldigi XML, FLTK prefs; QSettings string quoting |
| `provision/lib/apps.sh` (new) | catalog (`apps.tsv`), sources, install/remove/update bodies, `dxbapps` user, first-fill marks, per-radio wiring facts |
| `provision/lib/screen.sh` (new) | session names, env files, the session runner, private PulseAudio |
| `provision/lib/apps/{wsjtx,js8call,fldigi,qsstv,pat,flrig,wavelog}.sh` (new) | program modules |
| `provision/lib/logbook.sh` (new) | logbook setting, API calls, queue, recent contacts, fldigi follow |
| `provision/lib/webproxy.sh` (new) | nginx install without port fights, the session-check include, sites for Pat and Wavelog |
| `provision/bin/dxberry-app` (new) | list, install, remove, start, stop, screen, prepare, present, session |
| `provision/bin/dxberry-logbook` (new) | get, set, test, recent, submit, flush |
| `provision/bin/dxberry-update`, `provision/lib/update.sh` | job start moved into the library; `app` kind; `apps` part of check |
| `provision/bin/dxberry-radio` | `--rig-control hamlib|flrig` |
| `provision/lib/status.sh`, `provision/bin/dxberry-status` | parts `station` and `apps`; more units under services |
| `provision/share/apps.tsv` (new) | the catalog |
| `provision/templates/dxberry-screen@.service`, `dxberry-logbridge.service`, `dxberry-logbook-flush.{service,timer}`, `dxberry-apps.tmpfiles`, `nginx-dxberry-auth.conf`, `nginx-pat.conf`, `nginx-wavelog.conf` (new) | units and web configuration |
| `provision/cockpit/dxberry/{index.html,dxberry.js,dxberry.css,manifest.json}` | dashboard and Programs sections hooked in |
| `provision/cockpit/dxberry/{dashboard.js,programs.js,screen.html,screen.js}`, `novnc/` (new) | dashboard, Programs, program tab |
| `build/vendor-novnc.sh` (new), `build/build-image.sh` | noVNC vendoring; REQUIRED/EXECUTABLE lists, links |
| `tests/fixtures/apps/*` (captured), `tests/fixtures/apps.sh` (new) | fixtures and a shared test environment |
| `tests/test_{inifile,apps,screen,app_wsjtx,app_js8call,app_fldigi,app_qsstv,app_pat,flrig,logbook,app_wavelog,webproxy}.sh` (new); `test_radio.sh`, `test_rigctld.sh`, `test_update.sh`, `test_status.sh`, `test_page.sh`, `test_build.sh`, `page_smoke.js` | tests |
| `provision/VERSION`, `README.md`, `boot/README-DXBERRY.txt`, `docs/design/2026-10-19-applications.md` | version and documentation |

---

### Task 1: Module contract and the hand-over core

**Files:**
- Modify: `provision/lib/radio.sh` (application section, `dxb_radio_claim`, `dxb_radio_release`, `_dxb_radio_apply`)
- Modify: `provision/lib/rigctld.sh` (`dxb_rigctld_env`, `dxb_rigctld_sync`)
- Modify: `provision/lib/apps/graywolf.sh`
- Test: `tests/test_radio.sh` (new tests appended; `fake_apps` gains a session app `gamma`), `tests/test_rigctld.sh`, `tests/test_app_graywolf.sh`

**Interfaces:**
- Consumes: the existing contract (`app_<a>_unit/_wire/_unwire/_needs_service_restart/_wait_ready/_label`), `dxb_rigctld_sync`, `_dxb_radio_set_owner`, `_dxb_radio_mark_wired`.
- Produces:
  - Optional module functions, with defaults when absent: `app_<a>_kind` → `radio` | `rigcontrol` | `service` (absent ⇒ `radio`); `app_<a>_session` → `yes`|`no` (absent ⇒ `no`); `app_<a>_installed` → 0/1 (absent ⇒ 0); `app_<a>_multi` → `yes`|`no` (absent ⇒ `yes`).
  - `dxb_app_kind A`, `dxb_app_is_session A` (0 when `yes`), `dxb_app_is_installed A`, `dxb_app_unit A [RADIO]` (calls `app_A_unit RADIO`).
  - `dxb_app_session_unit A RADIO` → `dxberry-screen@A-RADIO.service`.
  - `dxb_app_session_start A RADIO` → `systemctl enable` + `start` the unit, then waits up to `DXB_SESSION_WAIT_S` (30) for `is-active`; 0 ok, 5 failed. `dxb_app_session_stop A RADIO` → `systemctl disable --now`; 0.
  - `dxb_app_load_owner A` → `dxb_app_load` plus: kind must be `radio` (else error "A cannot own a radio", 3) and the program installed (else "A is not installed; install it from Programs", 3).
  - `dxb_app_catalog` → `[{name, label, kind, session, installed}]` listing only `kind == radio` modules (the page's "Give to"/Switch list reads `name`/`label` as today).
  - `dxb_app_stop_if_idle A [RADIO]`: a session app stops+disables exactly `RADIO`'s unit; any other app keeps today's behavior (stop its one unit when it owns no radio).
  - `dxb_radio_owner_kind NAME` → `graywolf` (owner is a non-session app), `session`, or `none`.
  - `dxb_rigctld_env NAME RADIO_JSON [OWNER_KIND]`: when `OWNER_KIND == session`, `ptt.method == cm108` adds `PTT_ARGS=-P CM108 -p /dev/dxberry/NAME-hid`, `gpio` adds `PTT_ARGS=-P GPIO -p <gpio_line>`; anything else unchanged. `dxb_rigctld_sync NAME RADIO_JSON PRESENT [OWNER_KIND]` passes it through (absent ⇒ today's output byte for byte).
  - `app_graywolf_kind` → `radio`; `app_graywolf_installed` → `dxb_gw_installed_version` non-empty; `app_graywolf_version` → that version.

- [ ] **Step 1: Write the failing tests** in `tests/test_radio.sh` (extend `fake_apps` with a session module) and `tests/test_rigctld.sh`:

```bash
# in fake_apps(), after beta:
  cat > "$DXB_APPS_DIR/gamma.sh" <<'EOF'
app_gamma_kind() { echo radio; }
app_gamma_session() { echo yes; }
app_gamma_installed() { return "${GAMMA_MISSING:-0}"; }
app_gamma_multi() { echo "${GAMMA_MULTI:-yes}"; }
app_gamma_unit() { echo "dxberry-screen@gamma-$1.service"; }
app_gamma_wire() { echo "gamma wire $1" >> "$TEST_TMP/appcalls"; }
app_gamma_unwire() { echo "gamma unwire $1" >> "$TEST_TMP/appcalls"; }
app_gamma_needs_service_restart() { echo no; }
EOF
  cat > "$DXB_APPS_DIR/delta.sh" <<'EOF'
app_delta_kind() { echo rigcontrol; }
app_delta_unit() { echo delta.service; }
EOF

test_claim_session_app_enables_and_starts_its_radio_unit() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 gamma
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-screen@gamma-r1.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start dxberry-screen@gamma-r1.service"
  assert_not_contains "$(appcalls)" "gamma wire"   # a session is wired by its own prepare step
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "gamma"
}

test_handover_from_session_app_stops_only_that_radios_unit() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null; dxb_radio_claim r2 gamma > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-r1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-screen@gamma-r2.service"
}

test_claim_refuses_uninstalled_and_non_radio_programs() {
  radio_env; fake_apps; two_radios
  GAMMA_MISSING=1 dxb_radio_claim r1 gamma 2> "$TEST_TMP/err"; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma is not installed"
  dxb_radio_claim r1 delta 2> "$TEST_TMP/err"; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "delta cannot own a radio"
}

test_claim_refuses_second_copy_of_single_copy_program() {
  radio_env; fake_apps; two_radios
  GAMMA_MULTI=no; dxb_radio_claim r1 gamma > /dev/null
  dxb_radio_claim r2 gamma 2> "$TEST_TMP/err"; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma runs on one radio at a time (r1)"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r2)")" ""
  unset GAMMA_MULTI
}

test_claim_session_failure_disables_unit_and_releases() {
  radio_env; fake_apps; two_radios
  DXB_SESSION_WAIT_S=0 FX_SYSTEMCTL_FAIL_START='dxberry-screen@gamma-r1.service' dxb_radio_claim r1 gamma > /dev/null 2>&1
  assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-r1.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
}

test_apply_stops_and_restarts_sessions_by_presence() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null; : > "$TEST_TMP/calls"
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio            # the r1 card disappears
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-screen@gamma-r1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "disable"
  : > "$TEST_TMP/calls"; fx_replug "$DXB_SYSFS_ROOT" r1-audio
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-r1.service"
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start dxberry-screen@gamma-r1.service"
  assert_not_contains "$(appcalls)" "gamma wire"
}

test_catalog_lists_installed_radio_programs_with_kind() {
  radio_env; fake_apps
  assert_eq "$(dxb_app_catalog | jq -c 'map(select(.name == "gamma" or .name == "delta"))')" \
    '[{"name":"gamma","label":"gamma","kind":"radio","session":true,"installed":true}]'
  GAMMA_MISSING=1; assert_eq "$(dxb_app_catalog | jq -r '.[] | select(.name == "gamma") | .installed')" "false"; unset GAMMA_MISSING
}

# tests/test_rigctld.sh
test_rigctld_env_keys_cm108_and_gpio_for_session_owners_only() {
  local r='{"cat":null,"ptt_serial":null,"hid":{"path":"usb-0:1.3:1.3"},"ptt":{"method":"cm108","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4534}'
  assert_contains "$(dxb_rigctld_env r2 "$r" session)" 'PTT_ARGS=-P CM108 -p /dev/dxberry/r2-hid'
  assert_contains "$(dxb_rigctld_env r2 "$r" graywolf)" 'PTT_ARGS='$'\n'
  assert_eq "$(dxb_rigctld_env r2 "$r")" "$(dxb_rigctld_env r2 "$r" graywolf)"
  r=$(jq -c '.ptt = {method: "gpio", gpio_line: 17}' <<< "$r")
  assert_contains "$(dxb_rigctld_env r2 "$r" session)" 'PTT_ARGS=-P GPIO -p 17'
}
```

`fx_unplug`/`fx_replug ROOT r1-audio` are new helpers in `tests/fixtures/sysfs.sh` (move the scene's `class/sound/<card>` link aside and back); `fx_systemctl` learns `FX_SYSTEMCTL_FAIL_START=<unit>` (start returns 1 for it) and answers `is-enabled U` from `$TEST_TMP/enabled-U`.

- [ ] **Step 2: Run** `tests/run.sh 2>&1 | grep -E 'FAIL|failures'` → the new tests FAIL (unknown functions / wrong calls); every existing test still `ok`.

- [ ] **Step 3: Implement** in `radio.sh`, `rigctld.sh`, `apps/graywolf.sh`:
  - `dxb_radio_claim NAME APP`: `dxb_app_load_owner` replaces `dxb_app_load` for the new owner. For a session app: refuse a second copy when `app_A_multi` is `no` and another radio has owner A (exit 5, message `A runs on one radio at a time (OTHER)`); hand over from the old owner as today, then `_dxb_radio_set_owner`, then `dxb_rigctld_sync NAME R 1 session` (Graywolf must have let go of the HID before rigctld opens it), then `dxb_app_session_start` (no `dxb_app_wire`, no wiring mark). On failure: `dxb_app_session_stop`, owner cleared, rigctld synced back to `none`, exit 5. Same-owner claim of a session app = `dxb_app_session_start`.
  - Handing a radio from a session app to Graywolf: release order is session stop → owner set → `dxb_rigctld_sync … graywolf` → Graywolf start/wire (as today).
  - `dxb_radio_release`: session apps → `dxb_app_session_stop A NAME`, then rigctld synced with kind `none`.
  - `_dxb_radio_apply`: compute `dxb_radio_owner_kind` per radio and pass it to `dxb_rigctld_sync`. For a session owner: absent → `systemctl stop` (never disable); present and `systemctl is-enabled` says `enabled` and not active → `systemctl --no-block start`; skip the re-wire block entirely (ruling 7). Non-session owners: unchanged.
- [ ] **Step 4: Run** `tests/run.sh` → `0 failures`; `shellcheck -x provision/lib/radio.sh provision/lib/rigctld.sh provision/lib/apps/graywolf.sh tests/test_radio.sh tests/test_rigctld.sh tests/fixtures/sysfs.sh` clean.
- [ ] **Step 5: Commit** `git add -A provision/lib tests && git -c user.name=… commit -m "Let session programs own radios: per-radio units, rigctld PTT for them"`.

---

### Task 2: One-key settings writers

**Files:**
- Create: `provision/lib/inifile.sh`, `tests/test_inifile.sh` (prefix `ini_`)
- Add fixtures: `tests/fixtures/apps/{wsjtx.ini,js8call.ini,fldigi_def.xml,qsstv_9.0.conf,flrig.prefs}` (already captured; commit them in this task)

**Interfaces:**
- Produces (all return 0 written, 1 unchanged, 6 could not write; read-back after every write; a missing file is created with mode 0644; an existing file keeps its mode; atomic via `dxb_write_if_changed`):
  - `dxb_ini_get FILE SECTION KEY` → the raw value (exit 1 when absent). `dxb_ini_set FILE SECTION KEY RAW` → sets `KEY=RAW` in `[SECTION]`, appending the key at the section's end when missing and the section at the file's end when missing; every other line (order, blank lines, `;` comments, other sections) untouched. Keys match exactly (case-sensitive).
  - `dxb_ini_qstring TEXT` → TEXT encoded as QSettings writes a string: wrapped in `"…"` when it contains `,` `;` `"` or `=` or starts/ends with a space, with `\` → `\\` and `"` → `\"` inside; otherwise as-is. `dxb_ini_qenum TYPE VALUE` → `@Variant(\0\0\0\x7f\0\0\0<len(TYPE)+1 as \xNN>TYPE\0\0\0\0<len(VALUE)+1 as \xNN>VALUE\0)` with the length escapes exactly as Qt writes them (see `wsjtx.ini`: `TransceiverFactory::PTTMethod` → `\x1e`, `PTT_method_VOX` → `\xf`).
  - `dxb_xml_get FILE TAG`, `dxb_xml_set FILE TAG TEXT` for fldigi's one-tag-per-line XML: replaces the single line `<TAG>…</TAG>`; TEXT escaped (`&` `<` `>`); a missing tag is inserted before `</FLDIGI_DEFS>`; a missing file is created as `<?xml version="1.0"?>\n<FLDIGI_DEFS>\n<TAG>…</TAG>\n</FLDIGI_DEFS>`.
  - `dxb_prefs_get FILE KEY`, `dxb_prefs_set FILE KEY VALUE` for FLTK prefs (`key:value` lines; a missing key appended after the `[.]` section's last line).

- [ ] **Step 1: Write the failing tests:**

```bash
test_ini_set_changes_one_key_and_keeps_everything_else() {
  cp "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini"
  assert_ok dxb_ini_set "$TEST_TMP/w.ini" Configuration MyCall W0BTE
  assert_eq "$(diff "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini" | grep -c '^[<>]')" "2"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Configuration MyCall)" "W0BTE"
  dxb_ini_set "$TEST_TMP/w.ini" Configuration MyCall W0BTE; assert_eq "$?" "1"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Common DialFreq)" "14074000"   # same key name, other section untouched
}
test_ini_set_appends_missing_key_and_section() {
  cp "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini"
  dxb_ini_set "$TEST_TMP/w.ini" Configuration SoundInName "$(dxb_ini_qstring 'plughw:CARD=RADIO2,DEV=0')"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Configuration SoundInName)" '"plughw:CARD=RADIO2,DEV=0"'
  assert_eq "$(awk '/^\[/{s=$0} /^SoundInName=/{print s}' "$TEST_TMP/w.ini")" "[Configuration]"
  dxb_ini_set "$TEST_TMP/new.ini" Configuration Rig 'Hamlib NET rigctl'
  assert_eq "$(cat "$TEST_TMP/new.ini")" $'[Configuration]\nRig=Hamlib NET rigctl'
}
test_ini_qenum_matches_what_qt_wrote() {
  assert_eq "$(dxb_ini_qenum TransceiverFactory::PTTMethod PTT_method_VOX)" \
    "$(dxb_ini_get "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" Configuration PTTMethod)"
  assert_eq "$(dxb_ini_qenum TransceiverFactory::PTTMethod PTT_method_CAT)" '@Variant(\0\0\0\x7f\0\0\0\x1eTransceiverFactory::PTTMethod\0\0\0\0\xfPTT_method_CAT\0)'
}
test_xml_set_replaces_one_tag() {
  cp "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$TEST_TMP/f.xml"
  assert_ok dxb_xml_set "$TEST_TMP/f.xml" HAMRIGDEVICE 127.0.0.1:4534
  assert_eq "$(dxb_xml_get "$TEST_TMP/f.xml" HAMRIGDEVICE)" "127.0.0.1:4534"
  assert_eq "$(diff "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$TEST_TMP/f.xml" | grep -c '^[<>]')" "2"
  dxb_xml_set "$TEST_TMP/f.xml" MYQTH 'A & B <x>'
  assert_file_contains "$TEST_TMP/f.xml" '<MYQTH>A &amp; B &lt;x&gt;</MYQTH>'
  assert_eq "$(dxb_xml_get "$TEST_TMP/f.xml" MYQTH)" 'A & B <x>'
}
test_prefs_set_flrig_port() {
  cp "$DXB_ROOT/tests/fixtures/apps/flrig.prefs" "$TEST_TMP/p"
  assert_ok dxb_prefs_set "$TEST_TMP/p" xml_port 12346
  assert_eq "$(dxb_prefs_get "$TEST_TMP/p" xml_port)" "12346"
  assert_eq "$(dxb_prefs_get "$TEST_TMP/p" xcvr_name)" "NONE"
}
```

- [ ] **Step 2: Run** → FAIL (functions undefined).
- [ ] **Step 3: Implement** `provision/lib/inifile.sh` with awk, values passed through `ENVIRON` (never interpolated into the awk program).
- [ ] **Step 4: Run** `tests/run.sh` → `0 failures`; shellcheck clean.
- [ ] **Step 5: Commit** "Add one-key writers for Qt, fldigi and FLTK settings files".

---

### Task 3: The catalog, sources, install and remove

**Files:**
- Create: `provision/share/apps.tsv`, `provision/lib/apps.sh`, `tests/fixtures/apps.sh` (`fx_apps_env`), `tests/test_apps.sh` (prefix `ap_`)

**Interfaces:**
- Consumes: `common.sh`, `config.sh` (`DXB_CFG`), `gps.sh` (`dxb_maidenhead`, `dxb_gps_fix`), `radio.sh` (`DXB_RADIOS`, `dxb_radio_get`, `dxb_app_load`), `update.sh` (`dxb_update_newer`).
- Produces:
  - `provision/share/apps.tsv` — tab-separated `name label kind source packages description`, `#` comments, `-` for no packages, rows exactly:

```
graywolf	Graywolf	radio	builtin	-	APRS digipeater, iGate and beacons
wsjtx	WSJT-X	radio	github:WSJTX/wsjtx:deb=^wsjtx-[0-9.]+-linux-aarch64\.deb$	-	FT8, FT4, WSPR and the other weak-signal modes
js8call	JS8Call	radio	github:JS8Call-improved/JS8Call-improved:deb=^js8call_[0-9.]+_arm64\.deb$,appimage=^JS8Call-v?[0-9.]+-aarch64\.AppImage$	libxcb-cursor0 pulseaudio	Keyboard-to-keyboard messages and relays over weak-signal FSK
fldigi	fldigi	radio	apt:fldigi	pulseaudio	RTTY, PSK, Olivia, MFSK, CW and many more
qsstv	QSSTV	radio	apt:qsstv	pulseaudio	Slow-scan TV pictures, analog and digital
pat	Pat (Winlink)	radio	github:la5nta/pat:deb=^pat_[0-9.]+_linux_arm64\.deb$	nginx	Winlink email over the air with ARDOP
flrig	flrig	rigcontrol	apt:flrig	-	Rig control with your radio's full front panel
wavelog	Wavelog	service	tag:wavelog/wavelog	nginx php-fpm php-mysql php-curl php-mbstring php-xml php-zip php-gd mariadb-server	A logbook on this Pi
```

  - `dxb_apps_rows` → JSON array `[{name,label,kind,source,packages:[…],description}]`; `dxb_apps_row NAME` → one object (exit 3 unknown).
  - Base session packages `DXB_APPS_BASE_PACKAGES='tigervnc-standalone-server openbox socat'` (added to every non-`service`, non-`builtin` install).
  - `dxb_apps_latest NAME` → `{"version":"3.0.2","asset":"wsjtx-3.0.2-linux-aarch64.deb","type":"deb"|"appimage"|"bin"|"tarball"|"apt","url":…,"sha256":"<hex>"|null}`; github sources read `$DXB_GH_API/<owner>/<repo>/releases/latest` (default `https://api.github.com/repos`), first matching pattern wins in the order given (`deb=` before `appimage=`); `sha256` from a release asset named `SHA256SUMS`, `checksums.txt` or `<asset>.sha256` when present; `tag:` → the tag's source tarball `https://github.com/<owner>/<repo>/archive/refs/tags/<tag>.tar.gz`; `apt:` → `apt-cache policy` candidate. Version = tag without a leading `v`. Exit 1 when unreadable.
  - `dxb_apps_installed NAME` (0/1) and `dxb_apps_version NAME` (empty when not installed): `apt:`/`deb` → `dpkg-query` status `installed` and `Version`; `appimage`/`bin`/`tarball` → `$DXB_APPS_OPT/<name>/VERSION`; `builtin` → the module's own.
  - `dxb_apps_install NAME` (a job body: prints progress lines; 0 ok, 6 nothing changed, 8 changed but a later step failed): user/groups first (`dxb_apps_user_ensure`), packages (`apt-get install -y --no-install-recommends --no-upgrade BASE PACKAGES` + for `apt:` the package), the upstream file into `$DXB_APPS_WORK` (`$DXB_STATE_DIR/apps-work`, 0700) with checksum check ("no checksum published for ASSET" printed when none), `apt-get install -y --no-install-recommends ./FILE.deb` / AppImage `--appimage-extract` into `$DXB_APPS_OPT/<name>/` + `VERSION` / `bin` to `$DXB_APPS_OPT/<name>/<name>` mode 755 + `VERSION`, then the module's optional `app_<name>_install_extra` (Pat: ARDOP; Wavelog: web and database). Every apt call goes through `$DXB_APT_GET` with `DEBIAN_FRONTEND=noninteractive -o DPkg::Lock::Timeout=120`.
  - `dxb_apps_remove NAME`: stops and disables every `dxberry-screen@NAME-*` unit, `apt-get remove -y` (never purge) the program's own package (not the shared `PACKAGES`), deletes `$DXB_APPS_OPT/<name>`, runs `app_<name>_remove_extra`; keeps `$DXB_APPS_HOME` and first-fill marks. 0/6/8 as above.
  - `dxb_apps_user_ensure`: system user `dxbapps` (home `$DXB_APPS_HOME`=`/var/lib/dxberry-apps`, shell `/usr/sbin/nologin`, groups `audio dialout dxbscreen`), group `dxbscreen`, home mode 0750, the console login user (`dxb_console_user`, `dietpi` unless `DXB_CONSOLE_USER`) added to `dxbscreen` and to `dxbapps` (read access to saved pictures/logs), `systemctl --global mask pulseaudio.socket pulseaudio.service` (ruling 2). Every command through `DXB_USERADD`, `DXB_GROUPADD`, `DXB_USERMOD`, `DXB_SYSTEMCTL` overrides. Idempotent.
  - `dxb_apps_firstfill_done A RADIO` / `dxb_apps_firstfill_mark A RADIO` (files `$DXB_APPS_STATE/firstfill/A-RADIO`, `DXB_APPS_STATE=$DXB_STATE_DIR/apps`).
  - `dxb_apps_station` → `{"call":"W0BTE","grid":"EM29qa"}`: `CALLSIGN` without `-SSID`; grid from `LATITUDE`/`LONGITUDE` via `dxb_maidenhead`, else the GPS fix's `grid`, else `""`.
  - `dxb_apps_wiring RADIO` → `{"radio":"radio2","label":…,"index":1,"rigctld":"127.0.0.1:4534","card":"RADIO2","device":"plughw:CARD=RADIO2,DEV=0","ptt":"cat"|"vox"|"none","control":"hamlib"|"flrig","flrig_port":12346,"hash":"<wire hash>"}`; `ptt` is `cat` for `rigctld`, `cm108`, `gpio`; `vox` for `vox`, `digirig_tone`; `none` for `none`.
  - `dxb_apps_own PATH...` → `chown -R dxbapps:dxbapps` through `DXB_CHOWN`.

- [ ] **Step 1: Write the failing tests** (`fx_apps_env` sets `DXB_APPS_TSV`, `DXB_APPS_OPT`, `DXB_APPS_HOME`, `DXB_APPS_STATE`, `DXB_APPS_WORK`, `DXB_GH_API=http://gh`, `DXB_APPS_CURL=ap_curl` (answers from `$TEST_TMP/http/<path>`), `DXB_APT_GET=ap_apt`, `DXB_DPKG_QUERY=ap_dpkgq`, `DXB_APT_CACHE=ap_aptcache`, `DXB_USERADD`/`DXB_GROUPADD`/`DXB_USERMOD`/`DXB_CHOWN`/`DXB_SYSTEMCTL` recorders, and two radios in `DXB_RADIOS`):

```bash
test_apps_tsv_has_every_program_once() {
  fx_apps_env
  assert_eq "$(dxb_apps_rows | jq -r 'map(.name) | join(" ")')" "graywolf wsjtx js8call fldigi qsstv pat flrig wavelog"
  assert_eq "$(dxb_apps_row js8call | jq -c .packages)" '["libxcb-cursor0","pulseaudio"]'
  dxb_apps_row nope > /dev/null 2>&1; assert_eq "$?" "3"
}
test_apps_latest_prefers_deb_then_appimage_and_reads_checksums() {
  fx_apps_env
  ap_release JS8Call-improved/JS8Call-improved v3.0.3 JS8Call-v3.0.3-aarch64.AppImage
  assert_eq "$(dxb_apps_latest js8call | jq -c '{version, type, asset, sha256}')" \
    '{"version":"3.0.3","type":"appimage","asset":"JS8Call-v3.0.3-aarch64.AppImage","sha256":null}'
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb wsjtx-3.0.2-linux-x86_64.deb SHA256SUMS
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(printf a%.0s {1..64})" > "$TEST_TMP/http/SHA256SUMS"
  assert_eq "$(dxb_apps_latest wsjtx | jq -r '.type + " " + (.sha256 | .[0:4])')" "deb aaaa"
}
test_apps_install_upstream_deb_verifies_then_installs_with_apt() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  assert_ok dxb_apps_install wsjtx > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/out")" "no checksum published for wsjtx-3.0.2-linux-aarch64.deb"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends --no-upgrade tigervnc-standalone-server openbox socat"
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get install -y --no-install-recommends $DXB_APPS_WORK/wsjtx-3.0.2-linux-aarch64.deb"
}
test_apps_install_refuses_a_file_that_fails_its_checksum() {
  fx_apps_env
  ap_release WSJTX/wsjtx v3.0.2 wsjtx-3.0.2-linux-aarch64.deb SHA256SUMS
  echo deb-bytes > "$TEST_TMP/http/wsjtx-3.0.2-linux-aarch64.deb"
  printf '%s  wsjtx-3.0.2-linux-aarch64.deb\n' "$(printf 0%.0s {1..64})" > "$TEST_TMP/http/SHA256SUMS"
  dxb_apps_install wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "6"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "aarch64.deb"   # never handed to apt
}
test_apps_remove_keeps_settings_and_marks() {
  fx_apps_env; mkdir -p "$DXB_APPS_HOME/.config" "$DXB_APPS_STATE/firstfill"; : > "$DXB_APPS_STATE/firstfill/fldigi-radio2"
  AP_INSTALLED=fldigi
  assert_ok dxb_apps_remove fldigi > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" "apt-get remove -y fldigi"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "purge"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "remove -y pulseaudio"
  [[ -d $DXB_APPS_HOME/.config && -f $DXB_APPS_STATE/firstfill/fldigi-radio2 ]] || _fail "settings or marks were deleted"
}
test_apps_user_ensure_masks_desktop_sound_servers() {
  fx_apps_env
  assert_ok dxb_apps_user_ensure
  assert_contains "$(cat "$TEST_TMP/calls")" "useradd --system --home-dir $DXB_APPS_HOME --create-home --shell /usr/sbin/nologin --groups audio,dialout,dxbscreen dxbapps"
  assert_contains "$(cat "$TEST_TMP/calls")" "usermod -aG dxbscreen,dxbapps dietpi"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --global mask pulseaudio.socket pulseaudio.service"
}
test_apps_station_strips_ssid_and_computes_grid() {
  fx_apps_env; DXB_CFG[CALLSIGN]=W0BTE-10; DXB_CFG[LATITUDE]=38.9; DXB_CFG[LONGITUDE]=-94.6
  assert_eq "$(dxb_apps_station)" '{"call":"W0BTE","grid":"EM28qv"}'
}
test_apps_wiring_maps_ptt_and_ports() {
  fx_apps_env
  assert_eq "$(dxb_apps_wiring radio2 | jq -c '{index, rigctld, card, device, ptt, control, flrig_port}')" \
    '{"index":1,"rigctld":"127.0.0.1:4534","card":"RADIO2","device":"plughw:CARD=RADIO2,DEV=0","ptt":"cat","control":"hamlib","flrig_port":12346}'
}
```

(`EM28qv` is what `dxb_maidenhead 38.9 -94.6` prints today; the test compares against it, not a hand-computed grid.)
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** `apps.sh` (the source parsing in one function `_dxb_apps_source NAME` → `{type, repo, patterns:[{type, re}]}`; downloads only into `$DXB_APPS_WORK`, removed after the install).
- [ ] **Step 4: Run** `tests/run.sh` → `0 failures`; shellcheck clean.
- [ ] **Step 5: Commit** "Add the program catalog and install/remove core".

---

### Task 4: Program sessions and `dxberry-app`

**Files:**
- Create: `provision/lib/screen.sh`, `provision/bin/dxberry-app`, `provision/templates/dxberry-screen@.service`, `provision/templates/dxberry-apps.tmpfiles`, `tests/test_screen.sh` (prefix `sc_`)
- Modify: `provision/lib/update.sh` + `provision/bin/dxberry-update` (move `lock_update`, `lock_settings`, `cmd_start` into the library as `dxb_update_lock`, `dxb_update_lock_settings`, `dxb_update_job_start KIND [NAME]`; the CLI calls them; job file gains `name`), `tests/test_update.sh` (only the tests that called the moved functions by their old names)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  - `dxberry-screen@.service` (exact):

```ini
[Unit]
Description=DXBerry-Pi program session %i
Documentation=file:///opt/dxberry/bin/dxberry-app
After=dxberry-radio-hotplug.service network.target
StartLimitIntervalSec=60
StartLimitBurst=3

[Service]
Type=simple
User=dxbapps
Group=dxbapps
SupplementaryGroups=audio dialout dxbscreen
UMask=0007
ExecCondition=+/opt/dxberry/bin/dxberry-app present %i
ExecStartPre=+/opt/dxberry/bin/dxberry-app prepare %i
ExecStart=/opt/dxberry/bin/dxberry-app session %i
TimeoutStopSec=20
KillMode=mixed

[Install]
WantedBy=multi-user.target
```

  - `dxberry-apps.tmpfiles`: `d /run/dxberry/screens 2770 root dxbscreen -`.
  - Session name `A-RADIO` (`A` has no `-`; split at the first `-`). Paths under `DXB_SCREENS_DIR` (`/run/dxberry/screens`): `S.sock` (VNC), `S.env` (0640 root:dxbscreen), `S.d/` (0700 dxbapps: PulseAudio socket, client.conf, runtime dir).
  - `dxb_screen_env S` → the env file text: `APP=`, `RADIO=`, `LABEL=` (program label), `RADIO_LABEL=`, `INDEX=`, `DEVICE=`, `CARD=`, `RIGCTLD=`, `AUDIO=alsa|pulse`, `SCREEN=yes|no`, `GEOMETRY=1280x800`, one value per line, shell-quoted with `printf %q`.
  - `dxberry-app prepare S` (root): radio must exist, be owned by `A`, `A` installed; first fill once (`app_A_firstfill RADIO`, then mark); wire when `dxb_apps_wiring`'s `hash` differs from `$DXB_APPS_STATE/wired/S` (`app_A_wire RADIO`, then record the hash); `dxb_apps_own` the program's settings; write `S.env`; create `S.d`; waits up to 30 s for `rigctld@RADIO` to be active when the radio has rig control. Exit 0, 3 (not owned/installed), 4 (radio absent), 7 (first fill or wiring failed).
  - `dxberry-app present S` (root): exit 0 when the radio is plugged in, else 1 (systemd skips the start, the unit stays inactive, not failed).
  - `dxberry-app session S` (as `dxbapps`): sources `S.env`; when `SCREEN=yes` starts `Xvnc -displayfd 3 -rfbunixpath S.sock -rfbunixmode 0660 -rfbport -1 -SecurityTypes None -geometry $GEOMETRY -nolisten tcp -desktop "$LABEL · $RADIO_LABEL"` and `openbox --sm-disable`; when `AUDIO=pulse` starts `pulseaudio -n --daemonize=no --exit-idle-time=-1 --use-pid-file=no -F /dev/null` with `-L "module-alsa-sink device=$DEVICE sink_name=$CARD"`, `-L "module-alsa-source device=$DEVICE source_name=$CARD"`, `-L "module-native-protocol-unix socket=S.d/pulse.sock"`, and exports `PULSE_SERVER=unix:S.d/pulse.sock PULSE_CLIENTCONFIG=S.d/client.conf` (`autospawn = no`) for every program (ALSA ones too, so nothing can spawn a sound server); runs `app_A_command RADIO INDEX` (argv one per line) with `HOME=$DXB_APPS_HOME DISPLAY=:N XDG_RUNTIME_DIR=S.d`; exits with the program's status; on TERM forwards TERM to the program, waits 10 s, then KILL (ruling 9); Xvnc, openbox and pulseaudio always end with it.
  - `dxberry-app screen S --json` (any user): `{"session":S,"app":A,"radio":R,"label":…,"radio_label":…,"state":"running"|"starting"|"stopped"|"failed","socket":"/run/dxberry/screens/S.sock"|null,"open":{"kind":"screen"}|{"kind":"url","port":8081}}`; exit 3 when the env file is missing and the unit is unknown.
  - `dxberry-app start RADIO` / `stop RADIO` (root, record lock fd 8 like `dxberry-radio`): the owner must be a session app; `start` = `dxb_app_session_start`, `stop` = `dxb_app_session_stop` (the radio stays owned). Exit 3 no radio / owner not a session program, 5 failed.
  - `dxberry-app list --json` (root): `[{name,label,kind,description,source:"debian"|"upstream"|"builtin",installed,version,sessions:[{radio,state}],single_copy:bool,open:…}]` from `apps.tsv` + modules.
  - `dxberry-app install NAME` / `remove NAME` (root): `dxb_update_lock` then `dxb_update_job_start app-install|app-remove NAME`; `remove` first refuses (exit 5) when a radio owns NAME: `NAME runs on radio2; switch radio2 to another program first`. Exit 3 unknown, 5 busy, 6 not started. `dxberry-update apply app-install NAME` runs `dxb_apps_install NAME`; `app-remove` runs `dxb_apps_remove NAME`.
  - Exit codes of `dxberry-app` are those of spec §12.1; usage text lines 2–14 like the other commands; `--json` on every subcommand.

- [ ] **Step 1: Write the failing tests:**

```bash
test_screen_env_names_only_its_own_card() {
  sc_env; sc_owned radio2 gamma
  local e; e=$(dxb_screen_env gamma-radio2)
  assert_contains "$e" "DEVICE=plughw:CARD=RADIO2\\,DEV=0"
  assert_not_contains "$e" "RADIO1"
  assert_not_contains "$e" "CODEC"
}
test_screen_pulse_loads_only_the_session_card() {
  sc_env; sc_owned radio2 gamma; GAMMA_AUDIO=pulse
  dxb_screen_session_argv gamma-radio2 pulse > "$TEST_TMP/argv"
  assert_eq "$(grep -c 'module-alsa-' "$TEST_TMP/argv")" "2"
  assert_eq "$(grep -c 'device=plughw:CARD=RADIO2,DEV=0' "$TEST_TMP/argv")" "2"
  assert_not_contains "$(cat "$TEST_TMP/argv")" "module-udev-detect"
}
test_prepare_fills_once_and_rewires_on_hash_change() {
  sc_env; sc_owned radio2 gamma
  assert_ok "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(tr '\n' ';' < "$TEST_TMP/appcalls")" "gamma firstfill radio2;gamma wire radio2;"
  : > "$TEST_TMP/appcalls"; "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(cat "$TEST_TMP/appcalls")" ""                        # nothing changed: neither again
  sc_set_ptt radio2 vox; "$DXB_ROOT/provision/bin/dxberry-app" prepare gamma-radio2
  assert_eq "$(tr '\n' ';' < "$TEST_TMP/appcalls")" "gamma wire radio2;"
}
test_present_skips_start_for_unplugged_radio() {
  sc_env; sc_owned radio2 gamma; sc_unplug radio2
  "$DXB_ROOT/provision/bin/dxberry-app" present gamma-radio2; assert_eq "$?" "1"
}
test_app_remove_refused_while_a_radio_runs_it() {
  sc_env; sc_owned radio2 gamma
  "$DXB_ROOT/provision/bin/dxberry-app" remove gamma 2> "$TEST_TMP/err"; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma runs on radio2; switch radio2 to another program first"
}
test_app_install_is_refused_while_an_update_runs() {
  sc_env; sc_job_running dxberry-job-update-system-1
  "$DXB_ROOT/provision/bin/dxberry-app" install wsjtx 2> /dev/null; assert_eq "$?" "5"
}
test_app_screen_answers_without_root() {
  sc_env; sc_owned radio2 gamma; sc_active dxberry-screen@gamma-radio2.service
  assert_eq "$(DXB_FAKE_EUID=1000 "$DXB_ROOT/provision/bin/dxberry-app" screen gamma-radio2 --json | jq -c '{state, socket}')" \
    "{\"state\":\"running\",\"socket\":\"$DXB_SCREENS_DIR/gamma-radio2.sock\"}"
}
test_session_unit_template_runs_prepare_as_root_and_program_as_dxbapps() {
  local u=$DXB_ROOT/provision/templates/dxberry-screen@.service
  assert_file_contains "$u" "User=dxbapps"
  assert_file_contains "$u" "ExecStartPre=+/opt/dxberry/bin/dxberry-app prepare %i"
  assert_file_contains "$u" "ExecCondition=+/opt/dxberry/bin/dxberry-app present %i"
  assert_file_not_contains "$u" "Restart="
}
```

(`sc_env` runs the real CLI with `DXB_LIB`, `DXB_APPS_DIR` (fake `gamma` module that records `firstfill`/`wire`/`command` and honours `GAMMA_AUDIO`), `DXB_SCREENS_DIR`, `DXB_SYSTEMCTL`, `DXB_RADIOS_FILE` under `$TEST_TMP`. `dxb_require_root` is bypassed in tests with `DXB_FAKE_EUID=0` — add that override to `common.sh`'s `dxb_require_root`, default `$EUID`.)
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** `screen.sh`, `dxberry-app`, the template, the tmpfiles line, and the update.sh/dxberry-update move. `dxb_screen_session_argv S` is a pure function printing the Xvnc/pulseaudio argument lists, so the session runner and the tests share it.
- [ ] **Step 4: Run** `tests/run.sh` → `0 failures`; shellcheck clean; `build/build-image.sh --check` still `tree ok` (lists updated in Task 15; until then `--check` only checks existing entries).
- [ ] **Step 5: Commit** "Run each program in its own screen session; add dxberry-app".

---

### Task 5: The program tab

**Files:**
- Create: `build/vendor-novnc.sh`, `provision/cockpit/dxberry/novnc/` (vendored), `provision/cockpit/dxberry/screen.html`, `provision/cockpit/dxberry/screen.js`
- Modify: `provision/cockpit/dxberry/manifest.json` (a `content-security-policy` adding `connect-src 'self' wss:`), `tests/test_page.sh`, `tests/page_smoke.js` (a second mode: `node tests/page_smoke.js --screen provision/cockpit/dxberry/screen.js`)

**Interfaces:**
- Consumes: `dxberry-app screen S --json` (Task 4).
- Produces:
  - `build/vendor-novnc.sh [DEST]`: reads `https://api.github.com/repos/novnc/noVNC/releases/latest`, downloads its source tarball, copies `core/` and `vendor/pako/` and `LICENSE.txt` into DEST (default `provision/cockpit/dxberry/novnc`), writes `DEST/VERSION` (the tag) and `DEST/README` (one paragraph: what it is, MPL-2.0, how to update), and fails when the attribution grep matches any copied file.
  - `screen.html`: loads `../base1/cockpit.js`, `dxberry.css`, and `<script type="module" src="screen.js">`; a top bar (`#program`, `#radio`, `#state`, `#reconnect` button, a link "Back to the station" to `index.html` in a new context: `/cockpit/@localhost/dxberry/index.html`), and `#screen` filling the rest.
  - `screen.js` (ES module): session from `location.hash` (must match `^[a-z][a-z0-9_]{0,15}-[a-z][a-z0-9]{0,11}$`, else "No program is named here."); `cockpit.spawn(["/opt/dxberry/bin/dxberry-app","screen",S,"--json"])` **without** superuser; when running, waits for `cockpit.transport.wait`, builds `wss://<host>/cockpit/channel/<cockpit.transport.csrf_token>?<base64(JSON {payload:"stream", binary:"raw", unix:socket})>` and creates `new RFB(#screen, url, {shared: true})` with `scaleViewport: false`, `resizeSession: true`; shows Connecting / Connected / Disconnected; **Reconnect** re-runs the whole sequence; when not running: "WSJT-X is not running on radio2. Start it from the station page." with the link; when the socket is refused: "This login cannot open the program's screen yet. Log out of the console and back in once, then reload this tab." (ruling 13). `document.title` = `"<Label> · <radio label>"`.

- [ ] **Step 1: Write the failing tests** in `tests/test_page.sh`:

```bash
test_page_screen_html_loads_only_its_own_files() {
  local h=$PG_DIR/screen.html
  assert_eq "$(grep -oE 'src="[^"]*"' "$h" | sort | tr '\n' ' ')" 'src="../base1/cockpit.js" src="screen.js" '
  assert_file_contains "$h" 'type="module"'
  assert_file_not_contains "$h" "style="
  if grep -qE '<script>|[[:space:]]on[a-z]+=' "$h"; then _fail "screen.html has an inline script or handler"; fi
}
test_page_screen_js_uses_the_external_channel_without_superuser() {
  local js=$PG_DIR/screen.js
  assert_file_contains "$js" '"/opt/dxberry/bin/dxberry-app"'
  assert_file_contains "$js" '/cockpit/channel/'
  assert_file_contains "$js" 'payload: "stream"'
  assert_file_not_contains "$js" 'superuser'
  assert_file_not_contains "$js" 'innerHTML'
  assert_file_contains "$js" 'from "./novnc/core/rfb.js"'
}
test_page_novnc_is_vendored_with_its_licence() {
  [[ -f $PG_DIR/novnc/core/rfb.js && -f $PG_DIR/novnc/LICENSE.txt && -f $PG_DIR/novnc/VERSION ]] || _fail "noVNC is not vendored"
  assert_file_contains "$PG_DIR/novnc/core/rfb.js" "MPL"
}
test_page_manifest_allows_secure_websockets_to_itself() {
  assert_contains "$(jq -r '."content-security-policy"' "$PG_DIR/manifest.json")" "connect-src 'self' wss:"
}
```

and in `page_smoke.js --screen`: hash `#wsjtx-radio2`, a fake `dxberry-app screen` answer with `state: "stopped"` → the page shows "WSJT-X is not running on radio2" and no RFB is created; `state: "running"` → one RFB created with a URL starting `wss://pi/cockpit/channel/TOKEN?` whose decoded query is `{"payload":"stream","binary":"raw","unix":"/run/dxberry/screens/wsjtx-radio2.sock"}`; a bad hash (`#../etc`) → "No program is named here." and no spawn. (The fake loads `screen.js` with its `import` line replaced by a stub `RFB` class — do the replacement in the test, not in the file.)
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**: run `build/vendor-novnc.sh` once (network: GitHub only), then the page and script. Run the attribution grep on `novnc/` before staging.
- [ ] **Step 4: Run** `tests/run.sh` → `0 failures` (with node: the screen smoke checks print `ok`).
- [ ] **Step 5: Commit** "Add the program tab: noVNC over Cockpit's channel".

---

### Task 6: WSJT-X

**Files:**
- Create: `provision/lib/apps/wsjtx.sh`, `tests/test_app_wsjtx.sh` (prefix `wx_`)

**Interfaces:**
- Consumes: Tasks 2–3 (`dxb_ini_*`, `dxb_apps_wiring`, `dxb_apps_station`, `dxb_apps_installed`, `dxb_apps_version`, `dxb_app_session_unit` from Task 1).
- Produces: `app_wsjtx_{kind=radio, session=yes, label=WSJT-X, installed, version, multi=yes, audio=alsa, screen=yes, unit RADIO, settings_file RADIO, firstfill RADIO, wire RADIO, unwire RADIO (no-op 0), needs_service_restart=no, command RADIO INDEX, open}`.
  - Settings file: `$DXB_APPS_HOME/.config/WSJT-X - RADIO.ini`, section `Configuration`.
  - First fill: `MyCall`=call, `MyGrid`=grid (when non-empty), `BroadcastToN1MM=true`, `N1MMServer=127.0.0.1`, `N1MMServerPort=2333`.
  - Wiring keys (only these, ever): `SoundInName` and `SoundOutName` = `dxb_ini_qstring "$device"`; `Rig=Hamlib NET rigctl`; `CATNetworkPort=<rigctld>`; `PTTMethod` = `dxb_ini_qenum TransceiverFactory::PTTMethod PTT_method_CAT` for `ptt=cat`, `PTT_method_VOX` for `vox`/`none`; `PTTport=` (empty); `Polling=1`.
  - Command: `wsjtx` `--rig-name=RADIO`.

- [ ] **Step 1: Write the failing tests:**

```bash
test_wsjtx_firstfill_then_wire_from_the_real_file() {
  wx_env   # fx_apps_env + radio2 (cat ptt, rigctld 4534) + call W0BTE, grid EM28qv; settings file copied from the fixture
  assert_ok app_wsjtx_firstfill radio2
  assert_ok app_wsjtx_wire radio2
  local f; f=$(app_wsjtx_settings_file radio2)
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "W0BTE"
  assert_eq "$(dxb_ini_get "$f" Configuration BroadcastToN1MM)" "true"
  assert_eq "$(dxb_ini_get "$f" Configuration SoundInName)" '"plughw:CARD=RADIO2,DEV=0"'
  assert_eq "$(dxb_ini_get "$f" Configuration Rig)" "Hamlib NET rigctl"
  assert_eq "$(dxb_ini_get "$f" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_contains "$(dxb_ini_get "$f" Configuration PTTMethod)" "PTT_method_CAT"
}
test_wsjtx_rewire_keeps_operator_keys() {
  wx_env; app_wsjtx_firstfill radio2; app_wsjtx_wire radio2
  local f; f=$(app_wsjtx_settings_file radio2)
  dxb_ini_set "$f" Configuration MyCall K0XYZ; dxb_ini_set "$f" Common DialFreq 7074000
  cp "$f" "$TEST_TMP/before"
  wx_set_ptt radio2 vox; app_wsjtx_wire radio2
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "K0XYZ"
  assert_eq "$(dxb_ini_get "$f" Common DialFreq)" "7074000"
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^[<>]')" "2"   # only PTTMethod changed
}
test_wsjtx_second_radio_gets_its_own_file_and_port() {
  wx_env; app_wsjtx_wire radio1; app_wsjtx_wire radio2
  assert_eq "$(dxb_ini_get "$(app_wsjtx_settings_file radio1)" Configuration CATNetworkPort)" "127.0.0.1:4532"
  assert_eq "$(dxb_ini_get "$(app_wsjtx_settings_file radio2)" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_eq "$(app_wsjtx_command radio2 1 | tr '\n' ' ')" "wsjtx --rig-name=radio2 "
}
test_wsjtx_missing_file_is_created_with_only_our_keys() {
  wx_env; rm -f "$(app_wsjtx_settings_file radio2)"
  app_wsjtx_firstfill radio2; app_wsjtx_wire radio2
  assert_eq "$(grep -c '=' "$(app_wsjtx_settings_file radio2)")" "12"
}
```
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → `0 failures`, shellcheck clean. **Step 5: Commit** "Add the WSJT-X module".

---

### Task 7: JS8Call

**Files:** Create `provision/lib/apps/js8call.sh`, `tests/test_app_js8call.sh` (prefix `js_`).

**Interfaces:** as Task 6 with:
- `label=JS8Call`, `audio=pulse`, `multi=yes`, settings `$DXB_APPS_HOME/.config/JS8Call - RADIO.ini` `[Configuration]`.
- First fill: `MyCall`, `MyGrid`, `BroadcastToN1MM=true`, `N1MMServer=127.0.0.1`, `N1MMServerPort=2333`.
- Wiring keys: `SoundInName` and `SoundOutName` = `dxb_ini_qstring "$card"` (the private PulseAudio's only sink/source are named by the card id, ruling 2), `Rig`, `CATNetworkPort`, `PTTMethod`, `PTTport`, `Polling` as WSJT-X.
- Command: `/usr/bin/JS8Call --rig-name=RADIO` when the `.deb` is installed (`dpkg-query` says `js8call` installed), else `$DXB_APPS_OPT/js8call/squashfs-root/AppRun --rig-name=RADIO`.

- [ ] **Step 1: Tests** — the four of Task 6 against `js8call.ini`, plus:

```bash
test_js8call_command_follows_how_it_was_installed() {
  js_env; JS_DEB_INSTALLED=0
  assert_eq "$(app_js8call_command radio2 1 | head -1)" "$DXB_APPS_OPT/js8call/squashfs-root/AppRun"
  JS_DEB_INSTALLED=1
  assert_eq "$(app_js8call_command radio2 1 | head -1)" "/usr/bin/JS8Call"
}
test_js8call_sound_names_are_the_private_pulse_devices() {
  js_env; app_js8call_wire radio2
  assert_eq "$(dxb_ini_get "$(app_js8call_settings_file radio2)" Configuration SoundOutName)" "RADIO2"
}
```
- [ ] **Steps 2–5** as Task 6. Commit "Add the JS8Call module".

---

### Task 8: fldigi

**Files:** Create `provision/lib/apps/fldigi.sh`, `tests/test_app_fldigi.sh` (prefix `fd_`).

**Interfaces:**
- `label=fldigi`, `audio=pulse`, `multi=yes`, settings `$DXB_APPS_HOME/.fldigi-RADIO/fldigi_def.xml`.
- First fill: `MYCALL`, `MYLOC`; logbook when one is set (Task 12 provides `dxb_logbook_fldigi_values` → `{"url","key","station_id"}` or `{}`; until Task 12 exists the module calls it only `declare -F`-guarded): `ENCLOUDLOG=1`, `CLOUDLOG_API_URL`, `CLOUDLOG_API_KEY`, `CLOUDLOG_STATION_ID`, then records what it wrote in `$DXB_APPS_STATE/fldigi-logbook-RADIO.json` (0600, values hashed with sha256, never the key itself).
- Wiring keys: `AUDIOIO=2`, `PULSESERVER=` (empty: the environment's `PULSE_SERVER` is used); hamlib control → `CHKUSEHAMLIBIS=1`, `CHKUSEXMLRPCIS=0`, `FLRIG_AUTO_ENABLE=0`, `HAMRIGMODEL=2`, `HAMRIGDEVICE=<rigctld>`, `HAMLIBCMDPTT=1` for `ptt=cat` else 0, `RTSPTT=0`, `DTRPTT=0`; flrig control → `CHKUSEHAMLIBIS=0`, `FLRIG_AUTO_ENABLE=1`, `FLRIG_IP_ADDRESS=127.0.0.1`, `FLRIG_IP_PORT=<flrig_port>`; always `XMLRPC_PORT=7362+i`, `ARQ_PORT=7322+i`.
- `app_fldigi_logbook_follow RADIO` (called by `prepare` before every start, Task 12 wires the call): for each of the four logbook keys whose current value still hashes to what DXBerry last wrote, write the new logbook's value; a key the operator changed is left alone and the function prints `kept CLOUDLOG_API_URL (changed in fldigi)` lines for the Programs row.
- Command: `fldigi --config-dir $DXB_APPS_HOME/.fldigi-RADIO/`.

- [ ] **Step 1: Tests** — firstfill+wire from `fldigi_def.xml` (assert each key above, and `diff` touches only those lines), `fd_rewire_keeps_operator_keys` (operator's `MYQTH` and a changed `HAMLIBCMDPTT` that is a wiring key is restored, `MYQTH` is not), `test_fldigi_flrig_radio_uses_flrig_not_hamlib`, two radios → `XMLRPC_PORT` 7362/7363 and `ARQ_PORT` 7322/7323, and:

```bash
test_fldigi_logbook_follow_keeps_what_the_operator_changed() {
  fd_env; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  app_fldigi_firstfill radio2
  local f; f=$(app_fldigi_settings_file radio2)
  dxb_xml_set "$f" CLOUDLOG_STATION_ID 7                         # the operator picked another station
  FD_LOGBOOK='{"url":"https://b.example/","key":"k2","station_id":"2"}'
  assert_contains "$(app_fldigi_logbook_follow radio2)" "kept CLOUDLOG_STATION_ID (changed in fldigi)"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://b.example/"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_STATION_ID)" "7"
  assert_file_not_contains "$DXB_APPS_STATE/fldigi-logbook-radio2.json" "k2"
}
```
- [ ] **Steps 2–5** as Task 6. Commit "Add the fldigi module".

---

### Task 9: QSSTV

**Files:** Create `provision/lib/apps/qsstv.sh`, `tests/test_app_qsstv.sh` (prefix `qs_`).

**Interfaces:**
- `label=QSSTV`, `audio=pulse`, `multi=no` (ruling 5), settings `$DXB_APPS_HOME/.config/ON4QZ/qsstv_9.0.conf`.
- First fill: `[PERSONAL] callsign`, `locator`.
- Wiring keys: `[SOUND] alsaSelected=false`, `pulseSelected=true`; `[radio1] enableCAT=true`, `radioModel=` `dxb_ini_qstring "2 Hamlib,NET rigctl"`, `serialPort=<rigctld>`, `pttType` = the value QSSTV uses for "CAT" when `ptt=cat` and for "none" otherwise, `enableSerialPTT=false`, `enableXMLRPC=false`. **Before writing the module, read QSSTV 9.5.8's source** (`src/rig/rigcontrol.cpp` and its config form in the Debian source package or the upstream repository) to confirm the `pttType` numbers and the `radioModel` text for Hamlib model 2; record both, with the file and line read, in the module's header comment. If they differ from this plan, use the source's values.
- Command: `qsstv`.

- [ ] **Step 1: Tests** — firstfill+wire from `qsstv_9.0.conf`, `qs_rewire_keeps_operator_keys` (operator's `[PERSONAL] qth`, `[TX]` keys untouched), missing file created with only our keys, and `app_qsstv_multi` = `no`.
- [ ] **Steps 2–5** as Task 6. Commit "Add the QSSTV module".

---

### Task 10: Pat + ARDOP and the web proxy

**Files:**
- Create: `provision/lib/apps/pat.sh`, `provision/lib/webproxy.sh`, `provision/templates/nginx-dxberry-auth.conf`, `provision/templates/nginx-pat.conf`, `tests/test_app_pat.sh` (prefix `pt_`), `tests/test_webproxy.sh` (prefix `wp_`)

**Interfaces:**
- Pat module: `label="Pat (Winlink)"`, `audio=alsa`, `screen=no`, `multi=no`, `open` → `{"kind":"url","port":8081,"path":"/"}`; settings `$DXB_APPS_HOME/.config/pat/config.json` (written with `jq`, 0600, never re-ordered beyond what jq does).
  - First fill: `mycall`, `locator`, `http_addr="127.0.0.1:8180"`.
  - Wiring keys: `ardop.addr="localhost:8515"`, `ardop.rig=RADIO` (only when `ptt=cat`, else removed), `ardop.ptt_ctrl` (`ptt=cat` → true, else false), `hamlib_rigs.RADIO={"address":"<rigctld>","network":"tcp"}`.
  - Read Pat 1.0.0's `cfg/config.go` and its default config before writing the module; record the key names, file and line in the module header (the plan's names come from its documentation: `mycall`, `locator`, `http_addr`, `ardop`, `hamlib_rigs`).
  - `app_pat_install_extra`: ARDOP from `github:pflarue/ardop:bin=^ardopcf_arm_Linux_64$` into `$DXB_APPS_OPT/ardop/ardopcf` + `VERSION` (through the Task 3 helpers), then `dxb_webproxy_install` and `dxb_webproxy_site pat`.
  - `app_pat_remove_extra`: removes the ARDOP directory and the Pat site (`dxb_webproxy_site_remove pat`).
  - Command: two processes, so the module prints a small wrapper invocation: `$DXB_LIB/../bin/dxberry-app` is not used; instead `app_pat_command` prints `bash`, `-c`, and a fixed script that starts `"$DXB_APPS_OPT/ardop/ardopcf" --logdir "$HOME/.local/share/ardop" 8515 "$DEVICE" "$DEVICE"` in the background and `exec pat http` in the foreground, killing ARDOP when Pat ends (DEVICE comes from the session env).
- `webproxy.sh`:
  - `dxb_webproxy_install`: when nginx is not installed: write `$DXB_POLICY_RC` (`/usr/sbin/policy-rc.d`, `exit 101`), `apt-get install -y --no-install-recommends nginx`, remove the policy file (always, also on failure), delete `$DXB_NGINX_DIR/sites-enabled/default`, write `$DXB_NGINX_DIR/snippets/dxberry-auth.conf` from the template, `systemctl enable --now nginx`. Idempotent; 0/6.
  - `nginx-dxberry-auth.conf` (exact, the include every DXBerry site uses):

```nginx
# DXBerry: let a request through only with a live Cockpit login (the browser's own Cockpit cookie)
auth_request /.dxberry-auth;
error_page 401 403 = @dxberry-login;
location = /.dxberry-auth {
    internal;
    proxy_pass https://127.0.0.1:443/cockpit/login;
    proxy_ssl_verify off;
    proxy_pass_request_body off;
    proxy_set_header Content-Length "";
    proxy_set_header Cookie $http_cookie;
    proxy_set_header Host $host;
}
location @dxberry-login { return 302 https://$host/; }
```

  - `nginx-pat.conf`: `listen 8081 ssl;` with Cockpit's own certificate (`ssl_certificate`/`ssl_certificate_key` from `@CERT@`/`@KEY@`, found by `dxb_webproxy_cert` → the newest `*.cert`/`*.key` pair in `/etc/cockpit/ws-certs.d/`), `include snippets/dxberry-auth.conf;`, `location / { proxy_pass http://127.0.0.1:8180; proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade"; proxy_set_header Host $host; }`.
  - `dxb_webproxy_site NAME` renders `nginx-NAME.conf` into `sites-available/dxberry-NAME.conf`, links it in `sites-enabled`, `nginx -t`, then `systemctl reload nginx`; a failing `nginx -t` removes the link again and returns 6. `dxb_webproxy_site_remove NAME`.

- [ ] **Step 1: Tests** — Pat firstfill+wire into a new `config.json` (keys and values above; the operator's `secure_login_password` and `connect_aliases` untouched on re-wire); `vox` radio → no `ardop.rig`, `ptt_ctrl` false; command script contains `ardopcf` with `plughw:CARD=RADIO2,DEV=0` twice and ends with `exec pat http`; webproxy: the policy file exists while apt runs and is gone after (also when apt fails), `sites-enabled/default` removed, a site with a failing `nginx -t` is unlinked and returns 6, the Pat site contains `listen 8081 ssl` and `include snippets/dxberry-auth.conf`.
- [ ] **Steps 2–5** as Task 6. Commit "Add Pat with ARDOP behind the console's login".

---

### Task 11: flrig as a radio's rig control

**Files:**
- Create: `provision/lib/apps/flrig.sh`, `tests/test_flrig.sh` (prefix `fr_`)
- Modify: `provision/lib/radio.sh` (`_dxb_radio_build` accepts `rig_control`; validation: `rig.control` absent or `hamlib`|`flrig`), `provision/lib/rigctld.sh` (flrig control → `MODEL=4`, `RIG_ARGS=-r 127.0.0.1:<flrig_port>`; CAT pin then belongs to flrig), `provision/bin/dxberry-radio` (`--rig-control hamlib|flrig` in usage and `parse_opts`), `_dxb_radio_apply` (for a present radio with `rig.control == flrig`: enable + `--no-block start` `dxberry-screen@flrig-RADIO`; otherwise, or absent: stop it, and disable when control is hamlib)

**Interfaces:**
- `app_flrig_kind=rigcontrol`, `session=yes`, `label=flrig`, `audio=alsa` (it opens no audio), `screen=yes`, settings `$DXB_APPS_HOME/.flrig-RADIO/flrig.prefs`; wiring writes only `xml_port=<flrig_port>` (the operator picks the transceiver and its serial port inside flrig; the card shows the port as `/dev/dxberry/RADIO-cat`); first fill none; command `flrig --config-dir $DXB_APPS_HOME/.flrig-RADIO/`.
- `prepare` accepts a flrig session for a radio whose `rig.control` is `flrig` regardless of the owner (it is not an owner).
- The record's wiring hash includes `rig` already, so switching control re-wires the owner at its next start.

- [ ] **Step 1: Tests** — `dxb_radio_set r1 '{"rig_control":"flrig"}'` stores `rig.control`; a bad value is refused (2); rigctld env for flrig control is `MODEL=4` and `RIG_ARGS=-r 127.0.0.1:12345` for index 0; apply starts `dxberry-screen@flrig-r1.service` for a present radio and stops+disables it after `rig_control hamlib`; a radio with no `rig.control` key produces today's env byte for byte; `dxberry-radio claim r1 flrig` → exit 3 ("flrig cannot own a radio").
- [ ] **Steps 2–5** as Task 6. Commit "Add flrig as a per-radio rig-control choice".

---

### Task 12: The logbook

**Files:**
- Create: `provision/lib/logbook.sh`, `provision/bin/dxberry-logbook`, `provision/lib/apps/wavelog.sh`, `provision/templates/dxberry-logbridge.service`, `provision/templates/dxberry-logbook-flush.service`, `provision/templates/dxberry-logbook-flush.timer`, `provision/templates/nginx-wavelog.conf`, `tests/test_logbook.sh` (prefix `lb_`), `tests/test_app_wavelog.sh` (prefix `wl_`), `tests/fixtures/logbook/` (server answers)
- Modify: `provision/bin/dxberry-app` (`prepare` calls `app_fldigi_logbook_follow` for fldigi sessions)

**Interfaces:**
- `logbook.json` (`DXB_LOGBOOK_FILE=$DXB_STATE_DIR/logbook.json`, 0600): `{"url":"https://log.example/","type":"wavelog"|"cloudlog","key":"…","station_id":"1","slug":""}`.
- `dxberry-logbook get --json` → `{"configured":bool,"url":…,"type":…,"station_id":…,"slug":…,"key":"set"|null,"queue":N,"local":{"installed":bool,"url":"https://<ip>:8082/"}}` — never the key.
- `dxberry-logbook set --json` reads `{"url","key","station_id","slug"}` from **stdin** (a missing `key` keeps the stored one; `{"clear":true}` forgets the logbook); tests the server first (as `test`), saves only when it answers, then runs `dxb_logbook_fldigi_follow_stopped` (stopped fldigi sessions follow now; running ones at their next start) and answers `{"ok":true,"type":…,"kept":["radio2: CLOUDLOG_STATION_ID (changed in fldigi)"]}`. Exit 2 bad input, 6 the server refused or did not answer (nothing saved).
- `dxb_logbook_fldigi_values` → `{"url","key","station_id"}` of the current logbook, or `{}` when none is set (fldigi's first fill and follow read it; Task 8 guards the call until this task exists).
- `dxberry-logbook test --json` (stdin like `set`, or the stored setting with no stdin) → `{"ok":bool,"type":…,"version":…|null,"error":text|null}`. Detection: `POST <url>index.php/api/version` with `{"key":…}` → `{"status":"ok","version"}` = Wavelog; else `GET <url>index.php/api/auth/<key>` answering `<status>Valid</status>` = Cloudlog; else not a logbook. Every request: `curl -fsS --connect-timeout 5 --max-time 15 -K <(…)` with the URL (Cloudlog's carries the key) and headers in the config, the body on stdin.
- `dxberry-logbook submit` (stdin: one ADIF record as WSJT-X/JS8Call send it): when configured, `POST <url>index.php/api/qso` with `{"key","station_profile_id","type":"adif","string":<adif>}`; success = HTTP 200/201 with `status` `created`/`ok`; otherwise (unset, unreachable, refused) the record goes to `DXB_LOGBOOK_QUEUE` (`$DXB_STATE_DIR/logbook-queue`, 0700) as `<epoch>-<n>.adi`; at most 1000 files (the oldest are kept, newer ones beyond 1000 are refused with an error line). Exit 0 posted or queued, 6 could not queue.
- `dxberry-logbook flush`: posts queued files oldest first, stops at the first failure. Run by `dxberry-logbook-flush.timer` every 5 minutes (`OnBootSec=2min`, `OnUnitActiveSec=5min`) and after every successful `set`.
- `dxberry-logbook recent --json [N]` (N ≤ 25, default 10) → `{"ok":bool,"type":…,"contacts":[{"time_utc":"2026-10-20T17:04:00Z","call","band","mode","rst_sent","rst_rcvd","grid"}],"error":…}`, cached 60 s in `$DXB_RUN_DIR/logbook-recent.json`. Cloudlog: `GET index.php/api/recent_qsos/<slug>/<N>`; Wavelog: `POST index.php/api/get_contacts_adif` `{"key","station_id","fetchfromid":<last id>}`, the ADIF parsed and merged into `$DXB_STATE_DIR/logbook-wavelog.json` (`lastfetchedid` + the newest 25), so only new contacts are fetched after the first call. Build the Cloudlog fixture from Cloudlog's `recent_qsos` source and the Wavelog one from `get_contacts_adif`'s; note the source files read in the fixture's header.
- `dxberry-logbridge.service` (exact `ExecStart`): `/usr/bin/socat -u UDP4-RECVFROM:2333,bind=127.0.0.1,fork SYSTEM:/opt/dxberry/bin/dxberry-logbook\ submit`; `Restart=on-failure`; `WantedBy=multi-user.target`.
- Wavelog module (`kind=service`, `label=Wavelog`): `app_wavelog_install_extra`: unpack the tag tarball into `$DXB_APPS_OPT/wavelog` keeping `application/config/config.php`, `application/config/database.php`, `userdata/`, `uploads/`, `backup/` from an earlier install; owner `www-data`; `mariadb -e` (through `DXB_MARIADB`): `CREATE DATABASE IF NOT EXISTS wavelog CHARACTER SET utf8mb4; CREATE USER IF NOT EXISTS 'www-data'@'localhost' IDENTIFIED VIA unix_socket; GRANT ALL PRIVILEGES ON wavelog.* TO 'www-data'@'localhost';`; `dxb_webproxy_install`; `dxb_webproxy_site wavelog`. `nginx-wavelog.conf`: `listen 8082 ssl` (Cockpit's certificate) and `listen 127.0.0.1:8083`, root `$DXB_APPS_OPT/wavelog`, PHP through `unix:/run/php/php-fpm.sock`, and `location ^~ /install { include snippets/dxberry-auth.conf; … }` (ruling 10). `app_wavelog_remove_extra`: removes the site only; files and database stay. The Programs row (Task 13) shows after install: "Finish setting up Wavelog: open it, and on the database step enter localhost, wavelog, www-data and no password. Then create an API key in Wavelog and choose it under Logbook."

- [ ] **Step 1: Write the failing tests:**

```bash
test_logbook_key_never_in_argv_or_output() {
  lb_env   # LB_CURL records its own argv to $TEST_TMP/argv and answers from fixtures/logbook
  printf '{"url":"https://log.example/","key":"SECRETKEY123","station_id":"1"}' | "$LB_CMD" set --json > "$TEST_TMP/out"
  assert_eq "$(jq -r .ok "$TEST_TMP/out")" "true"
  assert_file_not_contains "$TEST_TMP/argv" "SECRETKEY123"
  assert_file_not_contains "$TEST_TMP/out" "SECRETKEY123"
  assert_eq "$("$LB_CMD" get --json | jq -r .key)" "set"
  assert_file_not_contains "$DXB_LOG_FILE" "SECRETKEY123"
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
test_wavelog_install_uses_socket_auth_and_gates_the_installer() {
  wl_env; assert_ok app_wavelog_install_extra > /dev/null
  assert_contains "$(cat "$TEST_TMP/mariadb")" "IDENTIFIED VIA unix_socket"
  assert_not_contains "$(cat "$TEST_TMP/mariadb")" "PASSWORD"
  assert_file_contains "$DXB_NGINX_DIR/sites-available/dxberry-wavelog.conf" "location ^~ /install"
  assert_file_contains "$DXB_NGINX_DIR/sites-available/dxberry-wavelog.conf" "listen 127.0.0.1:8083"
}
test_wavelog_update_keeps_config_and_userdata() {
  wl_env; app_wavelog_install_extra > /dev/null
  echo mine > "$DXB_APPS_OPT/wavelog/application/config/config.php"; echo pic > "$DXB_APPS_OPT/wavelog/userdata/x"
  wl_new_release 3.2.5; app_wavelog_install_extra > /dev/null
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/application/config/config.php")" "mine"
  assert_eq "$(cat "$DXB_APPS_OPT/wavelog/userdata/x")" "pic"
}
```
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → `0 failures`, shellcheck clean. **Step 5: Commit** "Add the logbook: any Wavelog or Cloudlog, a local Wavelog, and the logging bridge".

---

### Task 13: Station dashboard and Programs on the page

**Files:**
- Create: `provision/cockpit/dxberry/dashboard.js`, `provision/cockpit/dxberry/programs.js`
- Modify: `provision/cockpit/dxberry/index.html` (`<section id="dashboard">` right after `#notices`, `<section id="programs" class="card settings">` after `#updates`, a `<dialog id="switch-dialog">` and a `<dialog id="logbook-dialog">`, the two scripts after `updates.js`), `dxberry.js` (`render` calls `dashRender(s)` and `progRender(s)`; FIELDS gain the paths below; Radio settings form gains *Rig control: Hamlib | flrig*), `dxberry.css`, `tests/test_page.sh`, `tests/page_smoke.js`
- Modify: `provision/lib/status.sh` and `provision/bin/dxberry-status` (parts `station` and `apps`, needed by the page; tests in `tests/test_status.sh`)

**Interfaces:**
- `dxberry-status` part `station` → `{"callsign":"W0BTE"|"","grid":"EM28qv"|""}` (`CALLSIGN` from `dxberry.txt`, grid as `dxb_apps_station`). Part `apps` → `{"programs": <dxberry-app list --json>, "logbook": <dxberry-logbook get --json>, "recent": <dxberry-logbook recent --json 10>}`; a failing piece becomes `{"error":text}` inside the part, never a failed part.
- FIELDS added: `station.ok station.callsign station.grid apps.ok apps.programs.*.name apps.programs.*.label apps.programs.*.kind apps.programs.*.description apps.programs.*.source apps.programs.*.installed apps.programs.*.version apps.programs.*.sessions.*.radio apps.programs.*.sessions.*.state apps.programs.*.single_copy apps.programs.*.open.kind apps.logbook.configured apps.logbook.type apps.logbook.url apps.logbook.key apps.logbook.queue apps.logbook.local.installed apps.recent.ok apps.recent.contacts.*.time_utc apps.recent.contacts.*.call apps.recent.contacts.*.band apps.recent.contacts.*.mode apps.recent.contacts.*.rst_sent apps.recent.contacts.*.rst_rcvd apps.recent.contacts.*.grid radios.radios.*.rig.control` (the `test_page.sh` field check runs them against real `dxberry-status --json` output from the fake station it already builds; extend that station with one installed WSJT-X session and a configured logbook).
- Dashboard (spec §5.1), drawn by `dashRender(s)` into `#dashboard`:
  - Station strip: "Welcome, W0BTE" (or "Welcome" + "Set CALLSIGN in dxberry.txt or Graywolf to see it here."), DXBerry version, "N radios", "All parts OK"/"N problems" (parts with `ok: false`), and two clocks: UTC `HH:MM:SS` and local `HH:MM`, from `Date.now() + dashboard.skew` where `skew = Date.parse(s.generated) - Date.now()` at each refresh; one `setInterval` of 1 s updates only the two text nodes.
  - One card per radio: label and name; "connected through" (`parts(x)` from dxberry.js); frequency/mode; owner program and state (`Running`/`Starting`/`Stopped`/`Failed`/`Unplugged`); for Graywolf the heard/sent/gated line as the Station card has it; buttons: **Open <program>** (`<a target="_blank" rel="noopener">` to `/cockpit/@localhost/dxberry/screen.html#<app>-<radio>`, or for `open.kind == "url"` to `https://<location.hostname>:<port><path>`; shown only while running), **Open flrig** when `rig.control == flrig`, **Switch program**, **Stop**/**Start** (`dxberry-app stop|start RADIO`; no Stop for a Graywolf radio), **Radio settings** (`openRadioForm(name)`); a radio without an owner shows **Choose a program**.
  - Switch dialog: "No program" (release) first, then every `radios.apps` entry, the current one marked "(running now)", "Install more…" link scrolling to `#programs`; it states what stops first (`ownsOthers` wording reused from dxberry.js); confirming runs `radioAction(name, …, ["claim", name, app])` (dxberry.js) or `["release", name]`.
  - Recent contacts: a table of the last 10 (`UTC`, `Call`, `Band`, `Mode`, `Sent`, `Rcvd`, `Grid`), "No logbook yet. Set one up under Programs → Logbook." when not configured, the error text when `recent.ok` is false; a link "Open the logbook" (the logbook URL; for the local Wavelog `https://<host>:8082/`).
  - Station box: logbook (connected type / not set / "N contacts waiting to be sent"), APRS-IS (`graywolf.igate.connected`), GPS (`radios.gps`), network state.
- Programs (`progRender(s)` into `#programs`, spec §5.2): one row per program (name, description, kind in words, "Installed 3.0.2"/"Not installed", "From Debian"/"From its authors"), **Install**/**Update** (when `updates.check.apps[name].update`)/**Remove** (asks first: "WSJT-X is removed; its settings and logs stay, so a reinstall picks up where you left off."), disabled with a reason while a job runs or (Remove) while a radio runs it ("radio2 is running it"); single-copy programs say "Runs on one radio at a time."; QSSTV says "Log SSTV contacts by hand in the logbook."; Install/Remove run `dxberry-app install|remove NAME --json`, then `updLoadJob()` (updates.js) so the job's output shows; the row shows "Installing…/Removing…" while `updates.job.job.kind` is `app-install`/`app-remove` with `name` = this row.
  - Logbook row: **Use my own** opens `#logbook-dialog` (fields Address, API key (password input; "Leave empty to keep the stored key" when one is set), Station ID, Public slug (Cloudlog only, with a hint)), **Test** runs `dxberry-logbook test --json` with the fields as stdin, **Save** runs `set`; **Install on this Pi** installs `wavelog`; after a local install the finish-setup text of Task 12; **Forget** runs `set` with `{"clear":true}` after a confirm.
  - The Programs section warns once, above the rows, when `pi.mem_total` < 2 GiB: "This Pi has less than 2 GB of memory. One program at a time works best."
- Busy rules (spec §12.3): a radio card's buttons are disabled while `pending[name]` is set or while a job of kind `app`/`app-install`/`app-remove` runs for the radio's program; the Programs rows' buttons while any job runs.
- After a logbook Save, any `kept` lines are shown as notices ("radio2: fldigi keeps its own CLOUDLOG_STATION_ID").
- All text is plain words (no jargon such as "session", "unit", "socket" in labels; "program", "radio", "window").

- [ ] **Step 1: Write the failing tests** — `test_page.sh`: index.html script list is exactly `../base1/cockpit.js dxberry.js settings.js updates.js dashboard.js programs.js`; dashboard/programs JS use no `innerHTML`; every new FIELDS path exists in the fake station's output; status tests for parts `station` and `apps` (a failing `dxberry-logbook` makes `apps.logbook.error`, the part stays `ok: true`). `page_smoke.js`: with the fake station (radio1 Graywolf, radio2 WSJT-X running, a logbook with 3 recent contacts): the strip says "Welcome, W0BTE"; the UTC clock text matches `^\d\d:\d\d:\d\d$`; radio2's card has an "Open WSJT-X" link to `…/screen.html#wsjtx-radio2` with `target="_blank"`; radio1's card has no Stop; Switch on radio2 lists "No program", "Graywolf", "WSJT-X (running now)"; choosing Graywolf shows a confirm saying WSJT-X stops on radio2, and confirming spawns `dxberry-radio claim radio2 graywolf --json`; the recent table has 3 rows; Programs shows WSJT-X "Installed 3.0.2" with Remove disabled ("radio2 is running it"); Install on fldigi spawns `dxberry-app install fldigi --json` then `dxberry-update job --json`; the logbook dialog's Save sends its fields on stdin, not in the argument list.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → `0 failures` (node checks `ok`), shellcheck clean.
- [ ] **Step 5: Commit** "Put the station first: dashboard, programs and the logbook on the page".

---

### Task 14: Program updates

**Files:**
- Modify: `provision/lib/update.sh`, `provision/bin/dxberry-update`, `provision/cockpit/dxberry/updates.js`, `tests/test_update.sh`, `tests/test_page.sh`, `tests/page_smoke.js`

**Interfaces:**
- `dxb_update_apps_info` → `{"<name>": {"installed":"3.0.2","latest":"3.0.3","source":"upstream"|"debian","update":bool} | {"error":text}}` for every installed program except Graywolf (which keeps its own part); upstream through `dxb_apps_latest`, Debian through the apt candidate (the check's system part has already run `apt-get update`). `dxb_update_check` adds `apps` (errors count toward the short error TTL like the other parts).
- `dxberry-update app NAME` → job kind `app` with `name`; `dxb_update_apply_app NAME`: remember which `dxberry-screen@NAME-*` units are active, stop them, run the install path of Task 3 (which installs the newer file), start the remembered units again and wait up to 30 s each; 0 ok, 6 nothing changed (download/checksum/apt failed before anything was replaced; sessions started again), 8 installed but a session did not come back (named in the output).
- `dxb_update_apply_system` additionally restarts the active sessions of Debian-sourced programs whose package was in the upgrade list (8 when one does not come back).
- Updates card: one row per `check.apps` entry ("WSJT-X 3.0.2 → 3.0.3" with **Update**, "is the newest", or the error), `UPDATE_RUNNING.app = "Updating a program…"`, and the job view names the program; `UPDATE_FIELDS` gain `check:apps.*.installed check:apps.*.latest check:apps.*.update check:apps.*.source start:name job:job.name`.

- [ ] **Step 1: Write the failing tests:**

```bash
test_update_app_restarts_the_sessions_that_ran() {
  up_env; up_apps_env   # wsjtx installed 3.0.2 (upstream latest 3.0.3), sessions on radio1 (active) and radio2 (inactive)
  assert_ok dxb_update_apply_app wsjtx > "$TEST_TMP/out"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-screen@wsjtx-radio1.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start dxberry-screen@wsjtx-radio1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-screen@wsjtx-radio2.service"
}
test_update_app_exit_8_when_a_session_does_not_return() {
  up_env; up_apps_env; FX_SYSTEMCTL_FAIL_START=dxberry-screen@wsjtx-radio1.service; DXB_SESSION_WAIT_S=0
  dxb_update_apply_app wsjtx > "$TEST_TMP/out" 2>&1; assert_eq "$?" "8"
  assert_contains "$(cat "$TEST_TMP/out")" "WSJT-X did not start again on radio1"
}
test_update_check_reports_installed_programs() {
  up_env; up_apps_env
  assert_eq "$(dxb_update_check refresh | jq -c '.apps.wsjtx')" '{"installed":"3.0.2","latest":"3.0.3","source":"upstream","update":true}'
  assert_eq "$(dxb_update_check refresh | jq -c '.apps | has("graywolf")')" "false"
}
```
- [ ] **Steps 2–5** as before. Commit "Update each program on its own".

---

### Task 15: Status, services, provisioning, image and documentation

**Files:**
- Modify: `provision/lib/status.sh` (services list: every `dxberry-screen@*` unit `systemctl list-units --all --plain --no-legend 'dxberry-screen@*'` reports, plus `dxberry-logbridge.service`, `dxberry-logbook-flush.timer`, and `nginx.service`, `php*-fpm.service`, `mariadb.service` only when their unit file exists), `provision/bin/dxberry-provision` (new step `provision_apps` after `provision_radio`), `provision/lib/apps.sh` (`provision_apps`), `build/build-image.sh` (REQUIRED_FILES, EXECUTABLE_FILES, links for `dxberry-app` and `dxberry-logbook`), `provision/VERSION` (`0.4.0`), `README.md`, `boot/README-DXBERRY.txt`, `docs/design/2026-10-19-applications.md` (§18 gains this plan's rulings 1–15 under "Builder's calls", in plain words), tests `test_status.sh`, `test_provision.sh`, `test_build.sh`
- `provision_apps` (spec §15): `dxb_apps_user_ensure`; install the templates (`dxberry-screen@.service`, `dxberry-logbridge.service`, `dxberry-logbook-flush.{service,timer}`, `dxberry-apps.tmpfiles`) with `daemon-reload` and `systemd-tmpfiles --create` only when changed; enable `dxberry-logbook-flush.timer`; enable `dxberry-logbridge.service` once `socat` is installed; for installed Pat/Wavelog re-render their nginx sites (a renewed Cockpit certificate is picked up); never installs or upgrades a program, never rewrites a program's settings; a failure is a failed step named `apps`; status line `apps: N programs installed (manage them on the DXBerry page)`.

- [ ] **Step 1: Tests** — `test_provision.sh`: the step order lists `provision_apps` right after `provision_radio`; with no program installed it calls no `apt-get`; it writes the units once (a second run changes nothing). `test_status.sh`: the services list contains `dxberry-screen@wsjtx-radio2.service` from a fake `list-units` and omits `mariadb.service` when its unit file is absent. `test_build.sh`: `--check` fails when `provision/bin/dxberry-app` is missing or not executable; every file under `provision/lib/apps/` and `provision/cockpit/dxberry/` (except `novnc/`, checked as a directory with `core/rfb.js`) is in REQUIRED_FILES.
- [ ] **Steps 2–4** as before; `build/build-image.sh --check` → `tree ok`.
- [ ] **Step 5: Commit** "Wire programs into status, provisioning and the image; 0.4.0".

---

### Task 16: Hardware check and release (controller, not dispatched)

Run on the live Pi by the controller, with the owner's standing OK of 2026-10-20 ("If you take the Pi down, then so be it"). Report every failed step before the next.

- [ ] **Backup first:** stop nothing yet; copy Graywolf's database and configuration (`systemctl cat graywolf` names the paths; copy the whole state directory with Graywolf briefly stopped) to `/root/dxb-backup-<date>/`; record channel 3's JSON from the API; `sha256sum` the copies.
- [ ] **Install the branch** on the Pi the way rc releases were installed (`install-remote.sh`: tarball of `provision/` to `/opt/dxberry`, previous tree kept as `/opt/dxberry.old`), then `dxberry-provision` with `DXB_GW_UPGRADE=0`; check: `dxberry-status --json` parts all `ok`, Graywolf active, channel 3 unchanged, `systemctl --global is-enabled pulseaudio.socket` = `masked`.
- [ ] **Remove the spike leftovers** first (user `dxbspike`, `/root/dxb-spike`, the hand-installed `wsjtx`/`js8call`/`fldigi`/`flrig`/`qsstv`, `x11-apps netpbm xdotool`), so every install below goes through `dxberry-app`.
- [ ] **Programs:** install each program from the page's command (`dxberry-app install NAME`, following `dxberry-update job`), check versions in `dxberry-app list --json`; `dxberry-update check --refresh --json` lists every installed program under `apps` with installed and latest versions; `dxberry-update app fldigi` runs as a job and ends 0 ("is the newest" or updated) with any running fldigi started again; remove and reinstall one (settings kept).
- [ ] **A radio for the checks:** if the owner has plugged an HF interface in, add it as `hf` (the CODEC card stays Graywolf's). Otherwise: stop Graywolf, add the CODEC interface as radio **`codec`** (its ALSA id stays `CODEC`, so Graywolf's hand-made channel keeps its device after the test), and use it for the checks below.
- [ ] **Each program on the radio** (`dxberry-radio claim <radio> <app>`): the session is active; `ss -lx` shows only the `.sock`, no TCP; the program's settings file has our wiring keys (WSJT-X `Rig=Hamlib NET rigctl` is accepted by WSJT-X — its Settings → Radio tab shows it, frequency follows the radio); audio: WSJT-X's waterfall moves with band noise (ALSA path), JS8Call/fldigi/QSSTV hear through the private PulseAudio (`pactl -s unix:…/pulse.sock list short` shows exactly one sink and one source); a test transmit into a dummy load or with the radio's power at minimum keys PTT through rigctld (`rigctl -m 2 -r 127.0.0.1:<port> t` reads 1 during TX).
- [ ] **Program tab:** open `https://<pi>/cockpit/@localhost/dxberry/screen.html#<session>` in a browser as `dietpi` without administrative access: the window shows, resizes with the tab, keyboard/mouse work; Reconnect works after closing the tab; this check needs the owner's browser — list it in the owner's rc1 checklist with the exact URL.
- [ ] **Pat proxy:** `curl -k https://<pi>:8081/` without a cookie → 302 to the console; with the owner's browser logged in → Pat's page (owner checklist).
- [ ] **Logbook:** set the owner's remote Cloudlog only if the owner gives the address and key in the session; otherwise test against the local Wavelog (finish its installer in the owner's browser — owner checklist) and a WSJT-X logged contact made with the "Log QSO" dialog on a test call → queued/posted (`dxberry-logbook get --json` queue 0, contact visible in `recent`).
- [ ] **Unplug/replug** the test radio while a program runs: the session stops, starts again after replug.
- [ ] **CPU:** with WSJT-X decoding and Graywolf running, `top -bn1` load and graywolf-modem CPU noted in the ledger.
- [ ] **Restore:** release and remove the test radio, start Graywolf, check channel 3's JSON equals the backup's and APRS frames are heard (`rx_frames` grows) — if not, restore the backup copies.
- [ ] **Release:** merge `feat/applications` into `main` (fast-forward), tag `v0.4.0-rc1` annotated ("DXBerry-Pi 0.4.0-rc1: programs" + a short plain body), push, watch CI and the release workflow, verify the release assets and the update tarball's `VERSION`. Then the owner's checklist (browser checks above, Wavelog installer, a real contact) as a private page. The full `v0.4.0` needs the owner's explicit OK.
