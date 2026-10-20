# DXBerry-Pi — Applications

Sub-project 4 of the DXBerry-Pi roadmap. Builds on the base image
(`docs/design/2026-09-07-base-image.md`, "the base spec"), radio plumbing
(`docs/design/2026-09-09-radio-plumbing.md`, "the plumbing spec") and the
console (`docs/design/2026-09-29-console.md`, "the console spec"). Section
numbers in this document are its own.

## 1. Purpose

Turn DXBerry-Pi from an APRS box into a multi-mode station that is run
entirely from a browser. The operator opens the Pi's address and lands on a
station dashboard: who they are, the time in UTC, every radio with the
program it is running, and their latest contacts. From there they install
the digital-mode programs they want, hand each radio to a program, open that
program's real window in its own browser tab, and switch a radio from one
program to another (FT8 now, SSTV later) without SSH.

Programs in this release: **WSJT-X** (FT8, FT4, WSPR), **JS8Call**,
**fldigi** (RTTY, PSK, Olivia and the rest), **flrig**, **QSSTV** (SSTV),
**Pat + ARDOP** (Winlink), and a **logbook** (Cloudlog family, on the Pi or
the operator's own server).

### Who it is for

Any ham with any radio. Nothing in this design may depend on a particular
radio or interface: rig control is whatever Hamlib supports, audio is any USB
sound card, PTT is any method the plumbing spec already supports. The
profile table supplies defaults, never limits.

### Principles

- **The real program, every knob.** Each program runs as itself, with its
  own settings screens, and the operator can change anything in them.
  DXBerry never wraps a program in a simplified front end. (The owner's
  verdict on DigiPi: it "thought the user was too dumb to be able to tweak
  settings". That is the anti-goal.)
- **Wired for you, then yours.** DXBerry fills in a program's basics the
  first time (callsign, grid, sound card, rig control, PTT) so it works on
  first start. After that DXBerry only ever touches the *wiring* keys (sound
  card, rig control, PTT) and only when the radio's wiring changes; every
  other setting belongs to the operator and is never rewritten (§9).
- **Lean until asked.** The image ships none of these programs. The operator
  installs each one from the Programs page; removing one keeps its settings.
  (HamPi is the counter-example: good programs, but a heavy OS with
  everything preinstalled and nothing tying the programs to the radios.)
- **The CLI is the contract.** As in the console spec: every reading and
  every action is a DXBerry command with `--json` output and fixed exit
  codes; the page only displays and calls them.
- **Borrow the security-critical parts.** Program windows reach the browser
  through Cockpit's own authenticated session (§7.4); nothing new listens on
  the LAN without a login.
- **Same discipline as before.** Idempotent steps, atomic writes with
  read-back, `dxb_`-prefixed functions, bash 5 with awk and jq, tests
  without hardware, no version pins, no attribution lines of any kind.

## 2. Scope

**In scope (v0.4.0, one release):**

- The station dashboard: the DXBerry page reorganized around radios (§5.1).
- The Programs page: install, update, remove each program (§5.2, §12).
- Program tabs: each running program's window in its own browser tab (§5.3,
  §7).
- Program modules for WSJT-X, JS8Call, fldigi, QSSTV, Pat + ARDOP, and flrig
  as a per-radio rig-control option (§6, §10).
- The logbook: a local install or the operator's remote Cloudlog/Wavelog,
  automatic logging from every program that makes contacts, and recent
  contacts on the dashboard (§11).
- Per-program updates on the Updates card (§13).
- Two or more radios in use at once, each with its own program.

**Out of scope:**

- Remote access from outside the LAN, a hotspot, overlay-root (sub-project 5).
- SDR receivers, rotators, amplifiers, CW keyers.
- Sharing one radio between two programs at the same time.
- A program not in the list above. Adding one later is a new module under
  `provision/lib/apps/` and a catalog row (§6); nothing else changes.
- Hardware-specific setup beyond what `dxberry-radio` already does.

## 3. User flow

1. Flash, fill in `dxberry.txt`, boot, as today. Log in to the console.
2. The dashboard shows "Welcome, W0BTE" (the station's `CALLSIGN`), UTC and
   local clocks, one card per radio, and an empty "Recent contacts" box with
   a link to set up a logbook.
3. **Programs** lists every program with *Install*. The operator installs
   WSJT-X and QSSTV; each install runs as a job with live output, like an
   update.
4. The HF radio is added with the existing **Add** form (console spec §8.2)
   if it is not set up yet.
5. On the HF radio's card, **Switch program → WSJT-X**. DXBerry stops
   whatever the radio was running, writes WSJT-X's wiring and first-start
   basics, and starts it. The card shows "WSJT-X · Running" with the
   frequency and mode read through rigctld.
6. **Open WSJT-X** opens a new browser tab holding the real WSJT-X window,
   sized to the tab. Closing the tab leaves WSJT-X running; opening it again
   shows the same window.
7. A contact logged in WSJT-X goes to the logbook and appears under Recent
   contacts within a refresh.
8. Later, **Switch program → QSSTV** on the same radio stops WSJT-X and
   starts QSSTV with the same radio's wiring. The VHF radio keeps running
   Graywolf throughout.
9. **Updates** lists each installed program with installed and newest
   versions; **Update** runs it as a job and restarts the program if it was
   running.

## 4. Repository layout (additions)

```
provision/
├── bin/
│   ├── dxberry-app                    # install / remove / list / screen / start / stop (§12, §7)
│   └── dxberry-logbook                # logbook setup, recent contacts, the logging bridge (§11)
├── lib/
│   ├── apps.sh                        # catalog, sources, install/remove core, first-fill bookkeeping
│   ├── screen.sh                      # program sessions: Xvnc, window manager, sockets (§7)
│   ├── logbook.sh                     # logbook config, API calls, bridge (§11)
│   ├── inifile.sh                     # read/write one key in Qt INI and fldigi XML files (§9)
│   └── apps/
│       ├── graywolf.sh                # unchanged contract, gains app_graywolf_kind (§6.2)
│       ├── wsjtx.sh
│       ├── js8call.sh
│       ├── fldigi.sh
│       ├── qsstv.sh
│       └── pat.sh                     # Pat + ARDOP as one program
├── share/
│   └── apps.tsv                       # the program catalog: name, label, kind, source, ... (§6.1)
├── cockpit/dxberry/
│   ├── programs.js                    # Programs page
│   ├── screen.html, screen.js         # the program tab (§7.4)
│   └── novnc/                         # vendored noVNC core (MPL-2.0 files, notices kept)
└── templates/
    ├── dxberry-screen@.service        # one program session (§7.1)
    ├── dxberry-logbridge.service      # the logging bridge (§11.3)
    └── ...                            # web-proxy config for Pat and the local logbook (§10.2, §11.1)
tests/
├── fixtures/apps/                     # sample WSJT-X.ini, JS8Call.ini, fldigi_def.xml, qsstv conf, pat config.json
├── test_apps.sh
├── test_app_<name>.sh                 # one per module
├── test_screen.sh
└── test_logbook.sh
```

Every new file goes into `build/build-image.sh`'s `REQUIRED_FILES`, and the
release update tarball carries it automatically (it packs `provision/`).

## 5. The page

The DXBerry page keeps its address, login and every existing function; it is
reorganized so the station comes first. The owner approved a mockup of this
layout in principle (kept privately; example data only).

### 5.1 Dashboard (top of the DXBerry page)

- **Station strip:** "Welcome, CALLSIGN" (from `dxberry-status`; when
  `CALLSIGN` is empty, "Welcome" and a hint that it is set in `dxberry.txt`
  or Graywolf), DXBerry version, number of radios, a one-line health summary
  (all parts ok / N problems), and two clocks: **UTC** (seconds) and local
  (minutes), ticking in the browser from the Pi's time (offset taken once per
  status refresh so a wrong browser clock does not mislead).
- **One card per radio** (wide on a computer, stacked on a phone):
  label and name; what it is connected through (from the record); frequency
  and mode via rigctld when it has CAT; the owning program and its state
  (Running / Starting / Stopped / Failed); one short program line (Graywolf:
  heard / beacons / gated, as today; WSJT-X and JS8Call: "last decode HH:MM"
  where readable, otherwise nothing); and the buttons **Open \<program\>**
  (new tab, §5.3), **Switch program**, **Stop** (or **Start** when stopped),
  **Radio settings** (the existing Edit form, plus the rig-control choice of
  §10). A radio with no program shows **Choose a program**.
- **Stop keeps the radio assigned.** Stop stops the program's session for this
  radio and the card shows "WSJT-X · Stopped" with **Start**; the radio stays
  the program's until it is switched. *Builder's call.* Releasing a radio
  outright is **No program** at the top of the Switch program list (the
  existing `dxberry-radio release`). Graywolf is one service for all its
  channels, so a Graywolf radio shows no Stop: stopping APRS on one radio is
  **No program**, and stopping Graywolf entirely stays on the Services card.
- **Switch program** is a dialog listing installed programs that can drive a
  radio (§6.2), the current one marked, and "Install more…" linking to
  Programs. Confirming runs `dxberry-radio claim NAME APP` (the existing
  hand-over: stop old owner if idle, wire, start). It says first what will
  stop, as the console already does.
- **Recent contacts:** the last 10 contacts from the logbook (§11.4): UTC,
  call, band, mode, sent, received, grid. A link opens the logbook.
- **Station box:** logbook connected or not, APRS-IS, GPS time, network.
- Below the dashboard, the existing cards and sections stay as they are
  (Network, Pi, Time, About, Services, Power, Settings, Updates), and the
  Radios card's "Plugged in, not set up" list stays.

### 5.2 Programs page

A section (or second Cockpit page in the same package; builder's choice) with
one row per catalog entry: name and what it does, kind (§6.2), installed
version or "Not installed", source (Debian / upstream), and **Install**,
**Update**, **Remove**. Install and Update run as jobs with live output and
the same busy rules as updates (§12.3). Remove asks first and says what is
kept (§12.2). A program that a radio is running cannot be removed until the
radio is switched or stopped; the page says which radio.

The **Logbook** row has two choices: *Install on this Pi* and *Use my own*
(address and API key; §11).

### 5.3 Program tab

**Open \<program\>** opens
`https://<pi>/cockpit/@localhost/dxberry/screen.html#<session>` in a new
tab (a plain `<a target="_blank">`). The tab is a Cockpit package page
opened outside the shell frame, so the same login guards it; it shows only
the program's screen, filling the tab, with a thin bar: program, radio,
connection state, and **Reconnect**. The screen resizes to the tab. Closing
the tab never stops the program. (§7.4 has the mechanics.)

## 6. Programs

### 6.1 The catalog

`provision/share/apps.tsv` is data, not code: one row per program with its
name, label, one-line description, kind, source type and source details,
the Debian packages it needs, and its update source. The table below is the
content for this release, from sources checked 2026-10-19 (§17):

| Program | Kind | Comes from | Updates from |
|---|---|---|---|
| WSJT-X | radio | upstream `wsjtx-<ver>-linux-aarch64.deb` (GitHub `WSJTX/wsjtx` releases, newest non-beta) | the same releases |
| JS8Call | radio | JS8Call-improved (the project's official source now): its arm64 `.deb` when the release has one, else its `aarch64.AppImage` unpacked under `/opt/dxberry-apps/js8call` | the same releases |
| fldigi | radio | Debian `fldigi` (upstream ships source only) | apt |
| QSSTV | radio | Debian `qsstv` | apt |
| Pat + ARDOP | radio | Pat: upstream `pat_<ver>_linux_arm64.deb`; ARDOP: `ardopcf` arm64 binary from its last release | Pat releases; ardopcf releases |
| flrig | rig control (§10) | Debian `flrig` | apt |
| Logbook | service (§11) | see §11.1 | §11.1 |

Graywolf stays as it is (installed by the provisioner, updated as today) and
appears in the catalog as an installed radio program with no Install or
Remove.

"Newest" never means a beta or pre-release. No source is pinned; the
operator can pin a program by the same `*_VERSION`-style mechanism Graywolf
has only if the owner later asks for it.

### 6.2 Kinds and the module contract

- **radio** programs own a radio through `dxberry-radio claim`. They are
  application modules under `provision/lib/apps/` with the plumbing spec's
  contract (`app_<a>_unit`, `_wire`, `_unwire`, `_needs_service_restart`,
  optional `_wait_ready`, `_label`), extended here:
  - `app_<a>_kind` prints `radio` (Graywolf too).
  - `app_<a>_installed` returns 0 when the program is installed;
    `dxb_app_catalog` lists only installed programs, so **Switch program**
    and **Give to** never offer one that is missing.
  - `app_<a>_unit RADIO` takes the radio and prints that radio's unit
    (`dxberry-screen@<a>-<radio>.service`), because these programs run one
    copy per radio. Graywolf ignores the argument (one service, several
    channels). `dxb_app_stop_if_idle` then stops the unit of the radio being
    released rather than "the app's unit"; this is the one change to the
    hand-over core (`radio.sh` claim/release/stop-if-idle), and it keeps
    Graywolf's behavior identical.
  - `app_<a>_firstfill RADIO` writes the first-start basics (§9.2) and is
    called once per program per radio, before the first start.
  - `app_<a>_version` prints the installed version.
- **rig control** (flrig) is not an owner; it is a per-radio option (§10).
- **service** (the logbook) belongs to no radio (§11).

### 6.3 Same program on two radios

Allowed when the program supports it. WSJT-X and JS8Call get
`--rig-name=<radio>`, which gives each copy its own settings file
(`~/.config/WSJT-X - radio2.ini`); fldigi gets `--config-dir` per radio plus
distinct XML-RPC and ARQ ports; QSSTV and Pat get a per-radio config
location (§17 lists QSSTV's as to-verify). The module refuses (claim exit 5
with a message) if a program cannot run a second copy.

## 7. Program sessions

### 7.1 One screen per running program

Each running radio program is one systemd unit,
`dxberry-screen@<app>-<radio>.service`, which runs:

1. **Xvnc** (TigerVNC, Debian `tigervnc-standalone-server`) on its own
   display number, listening **only on a unix socket**
   (`/run/dxberry/screens/<app>-<radio>.sock`, group `dxbscreen`, mode 0660)
   with no TCP port and VNC security `None` (the socket's permissions and
   Cockpit's login are the protection).
2. A small window manager (**openbox**) so the program's dialogs, menus and
   settings windows behave.
3. The program itself, with that radio's arguments.

When the program exits, the unit stops (the radio card shows Stopped or
Failed, with the last log lines one click away through the existing Logs).
The screen geometry starts at 1280×800 and follows the browser tab through
Xvnc's remote resize.

### 7.2 After a reboot, power loss or replug

The station comes back the way it was left. *Builder's call.* A radio's
program session is **enabled** when it is claimed or started and
**disabled** when it is stopped, switched or released, so a reboot starts
exactly the sessions that were running. A session's unit requires its
radio: unplugging the radio stops the session (the card shows the radio
unplugged and the program waiting); plugging it back into the same port
starts the session again through the existing hotplug path, after
`rigctld@<radio>` is up. A session stopped with **Stop** stays stopped
across reboots and replugs. The boot-time wire step treats an enabled
session like Graywolf: it starts it if needed and no longer warns that the
owner is not running.

### 7.3 The user that runs programs

Programs run as a dedicated **system** user, `dxbapps` (home
`/var/lib/dxberry-apps`, in the `audio` and `dialout` groups). A system user
is exempt from logind's `RemoveIPC=yes`, which would otherwise delete the
shared memory WSJT-X's decoder uses whenever the operator's last login
session ends (the console spec turned logind on). The operator's login user
(`dietpi`) joins the `dxbscreen` group so the program tab can open the
screen socket without administrative access, and gets read access to
`dxbapps`' home (group-readable) to fetch saved SSTV pictures or logs over
SSH or Cockpit's file tools.

Programs' own files (WSJT-X's `ALL.TXT`, saved SSTV pictures, fldigi logs,
Pat's mailbox) are the operator's data and stay on the SD card where each
program puts them by default. *Builder's call:* they are small appends,
unlike the system logs the base spec moved to RAM, and the operator expects
to keep them.

### 7.4 How the tab reaches the screen

The tab uses Cockpit's **external channel**: a WebSocket to
`/cockpit/channel/<csrf-token>?<base64 JSON>` with
`{"payload":"stream","binary":"raw","unix":"/run/dxberry/screens/<s>.sock"}`.
Cockpit checks the session cookie and the CSRF token, then connects the
socket. noVNC (vendored core files, MPL-2.0, notices kept; the same approach
cockpit-machines uses for VM consoles) speaks VNC over it. Nothing listens on
port 6080; the reservation stays unused.

`screen.js` asks `dxberry-app screen <session> --json` for the session's
state and socket, and shows "Not running" with a link back to the dashboard
when there is none.

## 8. Audio

Each radio program gets exactly its radio's sound card and no other.

- Programs that open ALSA devices use `plughw:CARD=<RADIO>,DEV=0`, the same
  name Graywolf uses; the module writes that name into the program's audio
  keys.
- A program that can only see PulseAudio devices (to be determined per
  program at build, §17) gets a **per-session** PipeWire with the
  PulseAudio interface, started inside the screen unit, configured to open
  **only** that radio's card and to present it as one source and one sink
  named after the radio. It must never open any other card, so it can never
  take a card from Graywolf or another session.
- A card belongs to one session at a time because a radio belongs to one
  program at a time (the existing ownership rule).

## 9. Wiring a program

### 9.1 What DXBerry owns

For each program, the module names its **wiring keys** and writes nothing
else after first start:

| Program | Wiring keys (file) |
|---|---|
| WSJT-X | `[Configuration]` `SoundInName`, `SoundOutName`, `Rig`, `CATNetworkPort`, `PTTMethod`, `PTTport` (`WSJT-X - <radio>.ini`) |
| JS8Call | the same keys plus `SoundInId`, `SoundOutId` (`JS8Call - <radio>.ini`) |
| fldigi | `AUDIOIO`, `PORTINDEVICE`/`PORTOUTDEVICE` or `PULSESERVER`, `CHKUSEHAMLIBIS`, `HAMRIGMODEL`, `HAMRIGDEVICE`, `HAMLIBCMDPTT`, `RTSPTT`, `DTRPTT`, `CHKUSEXMLRPCIS`, `FLRIG_IP_ADDRESS`, `FLRIG_IP_PORT` (`fldigi_def.xml` in the radio's config dir) |
| QSSTV | `[SOUND]` input/output device and backend; `[radio1]` model, port, `enableCAT`, `pttType` |
| Pat + ARDOP | `ardop.addr`, `ardop.rig`, `ardop.ptt_ctrl`, `hamlib_rigs.<radio>` in `config.json`; ARDOP's command line (port, capture and playback device) |

Rig control always goes through the radio's existing `rigctld@<radio>` on
`127.0.0.1:<rigctld_port>` (Hamlib model 2, "NET rigctl"), so several
programs and the dashboard can read frequency and mode, and changing a radio
changes one place. PTT follows the radio record, and every radio must be able to transmit from
every program (the owner's "any radio" requirement):

- `rigctld` (CAT) → the program's CAT PTT through rigctld.
- `cm108` and `gpio` → **rigctld keys them** (Hamlib `-P CM108` / `-P GPIO`
  with the record's device or line) and the program uses CAT PTT through
  rigctld. Graywolf keeps keying these itself, as today. So `rigctld@<radio>`'s
  PTT arguments now depend on the owner: the env file is regenerated and
  rigctld restarted when a radio is handed to or from Graywolf. This changes
  the plumbing spec's "rigctld is never touched by a hand-over" for these two
  PTT methods only; the wiring hash includes the owner's kind so `apply`
  stays consistent. *Builder's call, derived from the owner's requirement.*
- `vox` and `digirig_tone` → the program's VOX setting (the interface keys
  on audio).

### 9.2 First start

Before a program's first start on a radio, `app_<a>_firstfill` writes the
basics: callsign (`CALLSIGN` without any `-SSID`), grid (six-character
Maidenhead from `LATITUDE`/`LONGITUDE`, or from the GPS fix when the keys
are empty), and the logbook hooks (§11.3). It records that the fill happened
(`/var/lib/dxberry/apps/firstfill/<app>-<radio>`); later starts never repeat
it, even if the operator blanks a field on purpose.

### 9.3 Re-wiring

Wiring keys are written on every claim and whenever `apply` re-wires the
owner (the plumbing spec's hash mechanism), always with the program stopped:
a running program would overwrite its file on exit. Each write is one key at
a time through `inifile.sh` (preserving the file's other lines, ordering and
comments), atomic with read-back, and a missing file is created with only
the wiring and first-start keys.

## 10. flrig and Pat's web interface

### 10.1 Rig control by flrig

**Radio settings** gains *Rig control: Hamlib (default) | flrig*. With
flrig, flrig owns the radio's CAT port in its own screen session
(`dxberry-screen@flrig-<radio>`), always running while the radio is
present, and the radio's `rigctld` switches to Hamlib model 4 (FLRig) talking
to flrig's XML-RPC port. Every program still talks to `rigctld`, so nothing
else changes. The radio card shows **Open flrig** beside the program.
fldigi on an flrig radio uses its native flrig connection instead.

### 10.2 Pat's web interface

Pat's own web interface has no login of its own (to be confirmed, §17). It
listens on `127.0.0.1` only and is published on the reserved port **8081**
through a small HTTPS reverse proxy (nginx, installed with Pat) that lets a
request through only when it carries a valid Cockpit session: the proxy asks
Cockpit (`/cockpit/login` with the browser's cookie) and refuses otherwise.
It uses Cockpit's own certificate. The Pat radio card's **Open Pat** goes
there. ARDOP's optional web GUI is not published. If the session check
cannot be made to work, the fallback is HTTP basic authentication with the
Pi's login password over the same HTTPS, never an open port.

## 11. Logbook

### 11.1 Local or remote

*Use my own:* the operator gives the logbook's address, an API key, and the
station profile (station ID); `dxberry-logbook test` checks them (version
call, then a read) before saving. Cloudlog and Wavelog are both accepted;
the type is detected from the server's answer, and each speaks its own API
(§11.4).

*Install on this Pi:* **Wavelog** (the owner's choice, 2026-10-20).
Cloudlog's own documentation supports PHP up to 8.2 and Debian Trixie ships
PHP 8.4; Wavelog, the actively maintained Cloudlog fork, supports PHP
8.2–8.5 and MariaDB 10.11+ (Trixie ships 11.8). A local install is nginx +
PHP-FPM + MariaDB + Wavelog from its git release tag, on its own HTTPS port
(8082, newly reserved), with Wavelog's own user accounts as its login. It is
a program on the Programs page like the others: installed only when the
operator chooses, updated by `dxberry-update app wavelog`, removed with its
database kept unless the operator asks otherwise.

*Pointing elsewhere is always possible* (the owner's requirement). The
logbook setting is one address, whatever it points to: the local Wavelog, a
Wavelog or Cloudlog server elsewhere on the network, or one on the
internet. Installing the local Wavelog does not lock the station to it; the
operator can switch to any other server and back at any time, and the local
one keeps its contacts while unused. WSJT-X and JS8Call log through the
bridge (§11.3), so a switch reaches them at once. fldigi keeps its own copy
of the address and key; on a switch DXBerry rewrites fldigi's four logbook
keys while fldigi is stopped, but only where they still hold what DXBerry
last wrote. Keys the operator changed inside fldigi are left alone, and the
Programs row says so. *Builder's call.*

### 11.2 Storing the key

The address and key live in `/var/lib/dxberry/logbook.json` (root, 0600).
The key is entered through standard input from the page, never in a command
line, never printed, and shown back only as "set".

### 11.3 Logging from programs

- **fldigi** has built-in Cloudlog support (`ENCLOUDLOG`,
  `CLOUDLOG_API_URL`, `CLOUDLOG_API_KEY`, `CLOUDLOG_STATION_ID`); first fill
  sets these.
- **WSJT-X** and **JS8Call** send each logged contact as an ADIF datagram to
  the N1MM/secondary UDP port; first fill turns it on (`BroadcastToN1MM`,
  `N1MMServer=127.0.0.1`, a per-radio `N1MMServerPort`).
- **dxberry-logbridge.service** (socat receiving those datagrams, handing
  each to `dxberry-logbook submit`) posts the ADIF to the logbook's QSO API
  with the stored key. A contact that cannot be posted (logbook unreachable)
  is queued in `/var/lib/dxberry/logbook-queue/` and retried every five
  minutes, so nothing is lost while the server is down; the station box
  shows the queue length.
- **QSSTV** has no logging interface; SSTV contacts are logged by hand in
  the logbook (said on the Programs row). **Pat** sends email, not contacts.
- When the operator changes these settings inside a program, DXBerry never
  sets them back (they are first-start keys, not wiring keys).

### 11.4 Recent contacts

`dxberry-logbook recent --json` returns the last N contacts in one shape
whatever the server: `{ok, type, contacts:[{time_utc, call, band, mode,
rst_sent, rst_rcvd, grid}]}`. Cloudlog: `GET /api/recent_qsos/<slug>/<n>`
(the logbook's public slug, asked for at setup). Wavelog: its contacts call
with a read key. The answer is cached for 60 s so the dashboard's 10 s
refresh does not hammer a remote server.

## 12. Installing and removing

### 12.1 `dxberry-app`

`dxberry-app list --json` (catalog with installed state and versions),
`install NAME`, `remove NAME`, `screen SESSION --json`, `start`/`stop` of
sessions for the hand-over core. Install and remove run as jobs exactly like
`dxberry-update` (transient `systemd-run` units, the job file, `job --json`
with the last lines, the page polling every 2 s), and share its locks
(`update.lock`, then `config.lock`), so an install, an update and a settings
change never overlap.

Exit codes follow `dxberry-update`: 0 ok, 2 usage, 3 no such program, 5
busy, 6 failed with nothing changed, 8 changed but a later step failed.

### 12.2 What install and remove do

- **Install:** download (checksums verified where the upstream publishes
  them; a missing checksum is said in the job output), install with apt
  (`apt-get install -y --no-upgrade` for Debian packages, `apt-get install
  ./file.deb` for upstream packages so their dependencies resolve), plus the
  shared session packages on first use (`tigervnc-standalone-server`,
  `openbox`, `socat`; PipeWire only if a program needs it).
- **Remove:** stop every session of it, `apt-get remove` (not purge) or
  delete the unpacked directory, and **keep** its settings under `dxbapps`'
  home and the first-fill marks, so reinstalling continues where the
  operator left off.

### 12.3 Busy rules

One install, update or removal at a time across the whole Pi; a radio's
buttons are disabled while its program is being installed, updated or
switched.

## 13. Updates

`dxberry-update check --json` gains `apps: {<name>: {installed, latest,
source, update} | {error}}` for installed programs, using each program's
update source (apt candidate version; GitHub releases newest non-beta).
`dxberry-update app NAME` updates one; it stops the program's running
sessions first and starts them again afterwards, and reports exit 8 if a
session does not come back (the rc6 lesson from Graywolf). The Updates card
shows one row per installed program. `dxberry-update system` (all Debian
packages) also upgrades the Debian-sourced programs, as it does today, and
restarts their running sessions.

## 14. Status and services

- `dxberry-status --json` gains a part `apps`: the installed programs, each
  running session (`app`, `radio`, state, since), and the logbook state
  (configured, type, reachable, queue length). Parts stay keyed objects; no
  existing field changes.
- The Services card lists every `dxberry-screen@*` unit, the log bridge and
  the web proxy, alongside the existing units.
- `CALLSIGN` is added to the `station` information the page reads (it is
  already parsed; the console only needs it shown).

## 15. Provisioning integration

`provision_apps` (new step after `provision_radio`) runs in every provisioner
run: it makes sure `dxbapps`, the groups, the template units, the log bridge
and the proxy configuration exist for programs that are installed, and does
nothing for programs that are not. It never installs or upgrades a program
(updates do that) and never rewrites a program's settings (only claim and
re-wire do). The image build adds files only.

## 16. Configuration file keys

No new `dxberry.txt` keys are needed: programs are chosen and set up from
the page. The reserved prefixes (`PAT_`, `WSJTX_`, `JS8CALL_`, `FLDIGI_`,
`RIG_`) stay reserved and still ignored.

## 17. Verified facts and open risks

Checked from primary sources on 2026-10-19:

- Debian Trixie arm64: `wsjtx` 2.7.0 (backports 3.0.2), `js8call` 2.2.0,
  `fldigi` 4.2.06, `flrig` 2.0.05, `qsstv` 9.5.8, `pat` 0.16.0, `hamlib`
  4.6.2, `tigervnc-standalone-server` 1.15.0, `openbox` 3.6.1, `php` 8.4,
  `mariadb-server` 11.8, `cockpit` 337.
- Upstream: WSJT-X 3.0.2 GA with an aarch64 `.deb`; JS8Call-improved 3.0.3
  (AppImage; 3.0.2 had an arm64 `.deb`) and its README calls it the
  official source; Pat 1.0.0 with an arm64 `.deb`; `ardopcf` 1.0.4.1.3
  with an arm64 binary, **development discontinued** by its author (still
  the current Linux ARDOP; risk accepted until a successor exists).
- WSJT-X and JS8Call: `--rig-name` gives per-radio settings files; settings
  keys as in §9.1; N1MM/ADIF UDP output exists. fldigi: native Cloudlog
  keys present in 4.2.06; `--config-dir` exists. Pat: config keys as in
  §9.1, web UI default `localhost:8080` (moved off Graywolf's port).
- Cockpit external channels with `payload: stream` and `unix` are documented
  and used by cockpit-machines' noVNC console; Cockpit's default page policy
  allows WebSockets to itself only.
- Cloudlog documents PHP ≤ 8.2; Wavelog documents 8.2–8.5. Cloudlog's
  `recent_qsos` and QSO API, Wavelog's v1 API documented; Wavelog 3.1+ also
  has a v2 API with bearer tokens.

To verify while building, on the live Pi (each is a plan task with a
hardware or package check, and the design changes only if the check fails;
Graywolf's configuration is backed up first and restored after):

1. Each program's audio: ALSA `plughw` names accepted, or PulseAudio only
   (decides §8 per program).
2. Xvnc `-rfbunixpath` with no TCP port, and remote resize through noVNC.
3. An external channel opened from a page outside the Cockpit shell frame
   (the program tab), as a non-admin user on a group-readable socket.
4. Pat's web UI has no built-in login; Cockpit's `/cockpit/login` answers a
   cookie check from the proxy.
5. QSSTV's per-instance config location; whether it accepts a second copy.
6. JS8Call AppImage unpacks and runs without FUSE.
7. CPU: WSJT-X decoding plus Graywolf on a Pi 4 (Graywolf's modem already
   uses about half a core on the live Pi); the Programs page warns on Pis
   with less than 2 GB of memory but does not block.
8. Wavelog/Cloudlog API answers match the documented shapes.

## 18. Decisions

**The owner's:**

- Everything through the browser; the dashboard as the front door, with the
  call, UTC, what is running, recent contacts, radio settings, open, switch
  and stop (2026-10-19).
- Each program opens in its own browser tab (mockup option A).
- All programs in one release, plus per-program updates.
- Programs are installed from the Programs page when the operator chooses,
  not preinstalled.
- DXBerry rewrites only wiring keys after first start; everything else in a
  program is the operator's.
- Logbook: the operator's own Wavelog or Cloudlog server anywhere, or a
  local install; the local install is Wavelog (2026-10-20). Any user can
  point the station at any other Wavelog/Cloudlog server at any time.
- Build-time checks may run on the live Pi, including on its radio, and may
  take it down (2026-10-20). Graywolf's configuration and channels are
  backed up before any check that touches the radio and restored after.
- HF and VHF on separate radios is the normal case to support; the design
  must not assume any particular radio.

**Builder's calls already made (owner may override):** the dashboard lives in
the existing DXBerry page; screens through Cockpit's channel rather than a
websockify port; programs run as the system user `dxbapps`; openbox as the
window manager; official WSJT-X (not the "WS"/Improved fork) and
JS8Call-improved; Debian packages where upstream ships no arm64 binary; Pat's
web UI behind a session-checking proxy on 8081; the logbook bridge with an
offline queue; on a logbook switch fldigi's logbook keys follow unless the
operator changed them inside fldigi (§11.1); Stop keeps the radio assigned (§5.1); sessions come back after
a reboot or replug (§7.2); rigctld keys `cm108`/`gpio` PTT for every program
but Graywolf (§9.1); programs' own files stay on the SD card (§7.3); the
version is 0.4.0.

## 19. Testing

- Every module: first fill writes exactly the first-start and wiring keys
  into fixture settings files and nothing else; re-wire changes only wiring
  keys, keeps the operator's edits, comments and order; a second program on
  a second radio gets its own file and ports.
- The hand-over core: per-radio units, stop-if-idle stops the right unit,
  Graywolf behavior unchanged (existing tests stay green unmodified).
- `dxberry-app`: install/remove for each source type with fake `apt-get`,
  `curl` and checksum files; busy and lock rules; remove keeps settings;
  refusing to remove a program a radio is running.
- `dxberry-logbook`: key never in argv or output; type detection; recent
  contacts normalized from both servers' fixture answers; bridge queueing and
  retry.
- `dxberry-update`: `apps` part of check, update stops and restarts
  sessions, exit 8 when a session does not return.
- Page: `tests/page_smoke.js` covers the dashboard (call, clocks, radio cards,
  switch dialog, recent contacts), the Programs page and the screen page's
  "not running" state; every JSON field the page reads is asserted against
  real command output, as today.
- Hardware: a checklist run on a Pi with two radios (an HF interface and the
  VHF one): install each program, switch the HF radio through all of them,
  make or simulate a contact in each logging program, open every tab on a
  computer and a phone, update one program, remove and reinstall one.

## 20. Build order and release

One plan, built in full, published as **v0.4.0-rc1** for the owner's
browser and hardware check (fix rounds as rc2, rc3, …), then **v0.4.0** with
the owner's explicit OK. Inside the plan the order is: hand-over core change
and module contract → sessions and the program tab → each program module →
flrig → logbook → Programs page and dashboard → updates → status, services
and provisioning → hardware checklist.
