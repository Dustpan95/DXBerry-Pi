# DXBerry-Pi — The Station App

Sub-project 5 of the DXBerry-Pi roadmap. Replaces the console
(`docs/design/2026-09-29-console.md`, "the console spec") and re-homes the
applications page (`docs/design/2026-10-19-applications.md`, "the
applications spec"). Builds on the base image and radio plumbing specs.
Section numbers in this document are its own.

## 1. Purpose

Give DXBerry-Pi its own station app in place of Cockpit. The operator opens
the Pi's address on a phone, tablet or desktop and is looking at the radio:
a VFO page with real frequency, mode, VFO and split readback, meters the
radio actually reports, filter and gain controls, a hold-to-talk button that
carries two-way audio over the LAN, and the guards that keep a remote
operator from leaving a transmitter keyed or driving it into a bad match.
Every radio gets its own page; two radios can be worked at once by two
people.

Everything the Cockpit-era pages did comes across into the app: the
dashboard, radios, programs, services, system settings, updates, logbook, a
terminal, and the program windows. Cockpit itself is removed from the image.

This is the platform release. The modes inside the UI (FT8, JS8, SSTV and
the rest driven through their programs' network interfaces), the pairing
tiers and guest passes for operating from away, idle mode, presets and the
conditions page are later sub-projects that stand on this one (§19).

### Who it is for

Any ham with any radio, as before. Nothing here may depend on a particular
radio: rig control is whatever Hamlib supports, audio is any sound device,
PTT is any method the plumbing spec supports. Where a radio cannot report
something (SWR, power), the control is hidden with a note saying why, never
shown dead.

### Principles

- **The station is authoritative.** The app on the Pi owns the radio, the
  audio and the state. Browsers send intents and settle on the state the
  station echoes back; a tap never latches on its own.
- **The real program, every knob.** Unchanged from the applications spec.
  Digital-mode programs run as themselves; this release still opens their
  full windows. Nothing wraps a program in a simplified front end.
- **One owner at a time.** A radio's transmitter has one controller: a
  browser session or a program session. A second screen sees who has it and
  can take over only between transmissions.
- **Honest UI.** Never show a control that looks live and does nothing.
  Never hide a control whose feature is on. Every refusal names the fix, in
  the page and in the log, with the same words.
- **Refuse, never trim.** An action the radio or the rules cannot carry out
  is refused with the reason. Nothing is silently altered to fit.
- **Guards live where the radio is.** Timeouts, the SWR latch and the owner
  lock run on the Pi, never in the browser, so a dropped link cannot leave a
  key down.
- **The CLI is still the contract for configuration.** Settings, radio
  records, program installs and updates go through the existing `dxberry-*`
  commands with `--json`. The app calls them; it does not re-implement them.
  Live things (rig polling, audio, sessions) are the app's own.
- **Phone first.** Every page works at phone width with a thumb; the desktop
  gets more columns, not more features.
- **Same discipline as before.** Idempotent provisioning, atomic writes,
  tests without hardware, no version pins, GPLv2, nothing copied from any
  other project, no attribution lines of any kind.

## 2. Scope

**In scope (v0.5.0):**

- `dxberryd`, one Go service on the Pi, behind nginx on port 443, serving the
  app, the API, WebSockets, program windows and the terminal.
- Login with the Pi's own user accounts (PAM), sessions, rate limiting.
- Every Cockpit-era page re-homed: dashboard, radios, programs, services
  with logs and power, system settings, updates with jobs, logbook settings,
  program windows (noVNC), plus a terminal and a login page.
- The rig layer: one connection per radio to its rigctld, capability
  discovery, polled state, serialized commands, per-radio guards.
- The VFO page per radio: readout, tuning, mode, VFO/split, filter width,
  RF gain, TX power, AF level, meters the radio reports, ATU tune where the
  radio has a real tune command, a tune carrier, PTT.
- Two-way audio between browser and radio over WebRTC on the LAN, with
  hold-to-talk, mic gated on PTT, RX ducking on TX.
- Guards: PTT safety timeout, force-RX, SWR latch where SWR is readable,
  owner lock, tune-carrier cap, audio-device refusal.
- Two radios at once, each with its own page, audio and owner.
- Cockpit removed from the image; nginx auth gate for Pat and Wavelog
  repointed at the app.
- Provisioning, image build, update tarball and CI carry the binary.

**Out of scope (later sub-projects, §19):** modes inside the UI; pairing
tiers, guest passes and anything about reaching the Pi from outside the LAN
beyond "it works over the VPN you bring"; a relay service (never); a native
phone app (never); rewriting any modem (never); presets and VFO profiles;
idle mode; a conditions page; band scope or SDR; memory channels; a CW keyer
in the UI; macros.

**Removed:** Cockpit (`cockpit-ws`, `cockpit-bridge`, `cockpit-system`), the
Cockpit package under `provision/cockpit/`, the `cockpit.socket` drop-in, the
`cockpit-listen.conf` template, and `provision/lib/console.sh`.

## 3. User flow

1. Flash, boot, open `https://<pi>/`. The browser warns once about the
   self-signed certificate (§6.3); the login page explains that in one line
   and offers the certificate for download.
2. Log in as `dietpi` with the Pi's password. The dashboard: callsign, UTC
   clock, every radio as a card (program running, owner, a small readout),
   recent contacts, the station box.
3. Tap a radio card. The VFO page: frequency in a large tabular readout,
   mode, VFO A/B and SPLIT badges, the S-meter, filter and gain sliders, band
   and step buttons, a keypad on tap. Spin the dial on the radio and the page
   follows within a quarter second. Tap a band, the radio moves.
4. Tap the speaker. RX audio plays in the browser. Hold PTT: the mic opens,
   RX ducks, the radio keys. Let go: unkeyed, mic closed. Hold it too long
   and the station drops PTT at the timeout and says so.
5. Hand the radio to WSJT-X from the radio card, as today. The VFO page now
   says "WSJT-X has this radio"; tuning still works, PTT and audio are
   disabled with that reason, and the program's own window opens from the
   same card.
6. Plug a second radio in. A second card, a second page. Someone on a phone
   holds one, someone at the desk holds the other.
7. Settings, services, updates, programs, logbook and the terminal sit
   behind a menu; they look like the old pages because they are the same
   functions.

## 4. Repository layout (additions and moves)

```
station/                         Go module, github.com/Dustpan95/DXBerry-Pi/station
  cmd/dxberryd/main.go
  internal/httpapi/              routes, sessions, CSRF, static files
  internal/auth/                 PAM login, session store, rate limit
  internal/rig/                  rigctld client, capability probe, poller, guards
  internal/audio/                per-radio sound server client, Opus, WebRTC bridge
  internal/session/              radio owner lock, browser sessions, PTT timers
  internal/jobs/                 transient units, journal follow
  internal/cli/                  wrappers that exec dxberry-* --json
  internal/term/                 pty for the terminal
  internal/vnc/                  WebSocket to unix-socket proxy for program windows
  web/                           the frontend, plain HTML/CSS/JS, served embedded
    index.html login.html app.css app.js vfo.js dashboard.js radios.js
    programs.js services.js settings.js updates.js logbook.js terminal.js
    screen.html screen.js manifest.webmanifest icons/
    vendor/novnc/ vendor/xterm/   vendored as files, as noVNC is today
  go.mod go.sum
provision/lib/station.sh         installs the binary, unit, nginx site, cert; removes Cockpit
provision/templates/dxberryd.service
provision/templates/nginx-dxberry.conf        the 443 site
provision/templates/dxberry-audio@.service    per-radio sound server (moved from the session)
tests/test_station.sh            provisioning tests (bash suite)
.github/workflows/station.yml    go vet, staticcheck, tests with -race, arm64 build
```

`provision/cockpit/` is deleted. The noVNC files move to `station/web/vendor/novnc/`.

## 5. The service

### 5.1 Process and privilege

`dxberryd` runs as root under systemd (`dxberryd.service`, `Restart=always`,
`ProtectHome=read-only`, `ProtectSystem=strict` with `ReadWritePaths` for
its state, `/run/dxberry` and the places the CLIs write, `NoNewPrivileges`
off because it starts jobs and the terminal, `MemoryMax=400M`,
`TasksMax=256`). Root because every function behind the login already
needed root (radio records, services, updates, settings, power), Cockpit
gave the logged-in `dietpi` user the same through sudo, and a privilege
broker would be a second service to get right. The cost is that a defect in
request handling is a defect as root. The mitigations are structural (§5.4):
no path from the client ever reaches the filesystem, every command the app
runs is one of a fixed set with validated arguments, and the API is only
reachable with a session cookie behind TLS.

Listens on `127.0.0.1:8090` (HTTP and WebSocket) for nginx, and on UDP
`50000–50199` on every interface for WebRTC media (§8.3). Nothing else.

### 5.2 nginx in front

nginx already fronts Pat and Wavelog. It becomes the one TLS front for the
Pi:

- `nginx-dxberry.conf`: `listen 443 ssl` and `listen 80` (redirect to
  https), `server_name _`, the app's certificate (§5.3),
  `proxy_pass http://127.0.0.1:8090` with WebSocket upgrade headers,
  `client_max_body_size 1m`, `proxy_read_timeout 1h` on WebSocket paths.
- `nginx-dxberry-auth-endpoints.conf`: `/.dxberry-auth` now proxies to
  `http://127.0.0.1:8090/api/auth/check` (200 with a valid session cookie,
  401 otherwise); `@dxberry-login` redirects to `https://$http_host/login?next=…`.
  The Pat (8081) and Wavelog (8082) sites are unchanged otherwise.
- `dxb_webproxy_site_refresh` learns the new certificate source (§5.3) in
  place of Cockpit's `ws-certs.d`.

### 5.3 Certificate

On first start, if `/etc/dxberry/tls/station.crt` is missing, `dxberryd`
writes a self-signed certificate and key there (ECDSA P-256, 10 years, CN
`dxberry`, SANs: the hostname, `dxberry.local`, and every current non-loopback
IPv4 address), key 0600 root, then asks nginx to reload through a job. The
certificate is regenerated only when the operator asks (Settings → "New
certificate") or when the file is missing. `dxberry-config set hostname`
does not regenerate it; the SAN list is a convenience, since every browser
warns on a self-signed certificate regardless (verified: Chrome, Safari and
Firefox warn on any untrusted issuer). The login page offers the certificate
at `/station.crt` so a phone can install it and stop warning.

One certificate serves the app, Pat and Wavelog: `dxb_webproxy_cert` reads
`/etc/dxberry/tls` instead of Cockpit's `ws-certs.d`. After the update from
0.4.x, each browser therefore sees one new warning for the Pi (the old
Cockpit certificate is gone with Cockpit) and none afterwards; the release
notes say so. Port 443 stays port 443, so bookmarks keep working.

### 5.4 API shape

Everything under `/api/`, JSON in and out, same-origin only. Requests must
carry the session cookie and either a `Sec-Fetch-Site: same-origin` header or
an `Origin` matching the request host; otherwise 403. WebSockets check
`Origin` the same way.

- `POST /api/auth/login` {user, password} → sets `dxb_session` cookie
  (HttpOnly, Secure, SameSite=Strict, Path=/), 12 h idle expiry, renewed on
  use; `POST /api/auth/logout`; `GET /api/auth/check`.
- `GET /api/status` → the live status model (§10).
- `WS /api/events` → a stream of state deltas: radios, rig state at the poll
  rate for radios that have a page open, sessions, jobs, services (§10.2).
- `POST /api/radio/<name>/{freq,mode,vfo,split,level,ptt,tune,atu,claim,release}`
  → rig intents (§7.4). Each reply is `{ok:true}` or
  `{ok:false, reason:"…", fix:"…"}`.
- `POST /api/cli/<command>` → runs one allowlisted `dxberry-*` command with
  validated arguments (§5.5), stdin for secrets, returns its JSON and exit
  code. Long-running ones return a job id.
- `WS /api/jobs/<id>` → journal follow of a transient unit (§11).
- `WS /api/vnc/<app>-<radio>` → bytes to and from
  `/run/dxberry/screens/<app>-<radio>.sock` (§12).
- `WS /api/term` → the terminal pty (§13).
- `WS /api/audio/<radio>/signal` → WebRTC offer/answer and ICE (§8.3).

### 5.5 Running the CLIs

`internal/cli` holds one function per allowed call, each with its argument
schema: `dxberry-status --json`, `dxberry-radio {list,add,remove,set,…}`,
`dxberry-config {get,set,apply}`, `dxberry-update {check,…}`, `dxberry-app
{list,install,remove,update,open,…}`, `dxberry-logbook {get,set,test,…}`,
`systemctl {start,stop,restart} <unit>` for units in the known list,
`journalctl -u <unit> -n N`. Arguments are validated against the same
patterns the commands use (radio and program names
`^[a-z][a-z0-9-]{0,31}$`, unit names from the services list, numbers as
numbers). Anything else is refused before exec. Secrets arrive on stdin as
today. There is no generic "run a command" endpoint.

## 6. Login and sessions

### 6.1 PAM

`internal/auth` authenticates against the Pi's accounts through PAM (service
name `dxberry`, a one-line `/etc/pam.d/dxberry` that includes
`common-auth` and `common-account`). Root is refused by name. A user may log
in only if PAM accepts the password and the user is an administrator: a
member of group `sudo`, or a user whom `sudo -l -U <user>` reports "may run
the following commands" (sudoers-based rights, which is how DietPi grants
`dietpi` root). Anyone else is refused with "this account cannot
administer the Pi". Every login is an administrator login, because every
function behind it changes the system; there are no lesser roles in this
release. This keeps "one password for the Pi" exactly as Cockpit had it and
as `dxberry-config` manages it. PAM needs cgo (§16); `internal/auth` keeps
PAM behind an interface so the unit tests use a fake and the one real PAM
test runs only where `/etc/pam.d` exists (the test container has it).

### 6.2 Sessions and limits

Sessions live in memory with a 32-byte random id, user, creation and
last-use times, and the client address; the server keeps at most 64 and
drops the oldest. A restart logs everyone out (verified acceptable: Cockpit
did the same). Login attempts are limited to 5 per minute per source
address and 20 per minute total; over the limit the reply is 429 with a
retry time, and the page says so. Every login, logout, refusal and limit hit
is logged to the journal with the source address and user name, never the
password.

### 6.3 Pages before login

`/login` and `/station.crt` are the only unauthenticated paths. Everything
else redirects to `/login?next=` for page requests and answers 401 for API
requests.

## 7. The rig layer

### 7.1 One client per radio

For every radio record with rig control (the plumbing spec's `rigctld@<radio>`
on `127.0.0.1:<port>`), `internal/rig` keeps one TCP connection, reconnecting
with backoff (1 s doubling to 30 s) and logging transitions. Radios with no
rig control (VOX only) get a page with audio and PTT-by-VOX and no readout;
the page says "no rig control on this radio" in place of the dial.

Commands use rigctld's extended response protocol (`+\get_freq`,
`+\set_freq 14074000`, …) so every reply carries the command echo and an
`RPRT n` status line; the parser is strict and a malformed reply resets the
connection. One command at a time per radio, queued, with a 2 s deadline.

### 7.2 Capability discovery

On connect: `\dump_caps` for the model name, mode list, VFO list, "Can set/get
Split", "Can set/get PTT", "Can set/get Level" lists; then one `\get_level`
probe for each of `STRENGTH`, `SWR`, `ALC`, `RFPOWER`, `RFPOWER_METER_WATTS`,
`AF`, `RF`, `SQL`. A level the radio answers with `RPRT 0` and a value is
"readable"; a level listed as settable in the caps is "settable". Only
readable meters and settable controls appear on the page; the rest are
hidden, and a "Why isn't X here?" line under the controls lists what this
radio does not report over CAT. Discovery is cached per radio model name in
`/var/lib/dxberry/rigcaps/<model>.json` with a one-week age, and re-run on
demand from the radio's settings.

### 7.3 Polling

Per radio, a poll loop reads frequency, mode and passband, PTT, the current
VFO and split (when supported), and, while a VFO page is open, the readable
meters. Rates: 250 ms while any browser has that radio's VFO page open,
2 s while only the dashboard is open, 10 s when no browser is connected.
SWR and ALC are read only while PTT is on (they mean nothing in receive),
at 200 ms, with one zero frame emitted when PTT drops. Reads are skipped
while a write is pending and the written value is shown optimistically until
the next read confirms or corrects it.

When a program session owns the radio (§9.2) polling continues at the slow
rate; rigctld multiplexes clients, as today with flrig and the programs.
Measured during the applications hardware check: rigctld handled two clients
without complaint; the poll cost is one short TCP exchange.

### 7.4 Intents

`freq` (Hz, within the radio's range from the caps), `mode` (from the caps
list, with an optional passband), `vfo` (A/B/toggle, if the caps allow),
`split` (on/off, if allowed), `level` (name from the settable list, value in
the level's own range), `ptt` (on/off, §9.3), `tune` (a carrier through
`\set_ptt 1` in the current mode for at most 10 s, refused in a data mode
where it would key the sound card with silence; it keys the radio only),
`atu` (a real tune command where Hamlib's backend exposes one through
`\set_func TUNER`; where the backend only toggles the tuner the control is
hidden with the reason). Every intent is checked against the owner lock
(§9.2) and the guards (§9.4) before it goes to the radio.

## 8. Audio

### 8.1 The radio's sound server

Today each program session starts a private PulseAudio for its radio. That
server moves to the radio: `dxberry-audio@<radio>.service` (as `dxbapps`),
`pulseaudio -n` with `module-alsa-sink` as `<CARD>` and
`module-alsa-source` as `<CARD>.in`, exactly the names the applications spec
and the program modules already use, plus `module-native-protocol-unix` on
`/run/dxberry/audio/<radio>/native`. Program sessions connect to it instead
of starting their own (`PULSE_SERVER` in the session environment); the
program modules' sound-device names do not change. Graywolf is untouched: it
opens its ALSA device directly, and a radio whose card Graywolf holds is not
given a sound server (the radio record already knows this, as the
applications spec's "CODEC fallback" test showed).

Who starts it: the radio setup, not `dxberryd`. `dxberry-radio`'s apply (at
boot, on hotplug, and after an add, set or remove) starts the server for a
radio that is present, has an audio device and is owned by no program, and
stops it for a radio that is unplugged or removed (whoever owns it) or owned
by Graywolf. `dxberry-radio claim` and `release` run no apply, so they do
the same for the radio they hand over: a claim by Graywolf stops the server
before Graywolf starts, and a release starts it once the old owner has let
go of the card. For a plugged-in radio a program session owns, the session's
own steps start or stop the server around that session (below), and the
apply leaves it alone: after a restart of the Pi, or an unplug and replug of
the radio, with that program's window not running, the radio has no server,
and no browser audio, until the window starts (JS8Call, fldigi, QSSTV) or has
run and closed (WSJT-X, Pat: the session's prepare stops the server and its
stopped step starts it), or the radio is released (an accepted gap; an unplug
stops the server, and a window that was not running leaves no waiting marker
for the replug to start). Presence, hotplug and
ownership already live in the radio setup, so the server follows them there;
stopping it while the radio is unplugged means a replug comes back to a
fresh server. (The first draft had `dxberryd` start it when a radio was
recorded; rc3 changed that.)

One more object per radio: a null sink `<CARD>.tx` whose monitor is
loop-backed to `<CARD>` only while PTT is held (module-loopback loaded and
unloaded by `dxberryd` through `pactl`). RX for the browser is `<CARD>.in`
itself, captured by `dxberryd` with `parec` on that source at 48 kHz mono; TX
from the browser is written with `pacat` into `<CARD>.tx`. Programs keep
using `<CARD>` and `<CARD>.in`. Latency budget: 100 ms end to end on the
LAN, measured in the hardware check.

**Programs that open the card themselves.** Two program modules use ALSA
directly today (`app_<a>_audio` is `alsa` for WSJT-X and Pat; it is `pulse`
for JS8Call, fldigi and QSSTV, and `none` for flrig, the radio's rig
control, which opens no audio: its session leaves the sound server alone).
A card cannot be held by the sound server and opened raw at the same time.
Rule: while a session of an ALSA program owns the radio, the session's
`dxberry-app prepare` step stops that radio's sound server before the
program starts and its `stopped` step starts it again after the session
ends (for a PulseAudio program, `prepare` starts it); the VFO page says
"WSJT-X has the sound card; no browser audio while it runs" and keeps
tuning and the readout. Pulse programs connect to the running server
through `PULSE_SERVER`, so the browser can listen alongside JS8Call, fldigi
and QSSTV. Moving WSJT-X and Pat onto the sound server (both support
PulseAudio devices) is a later item, not this release; the module contract
does not change here.

### 8.2 Codec

Opus, 48 kHz mono, 20 ms frames, 32 kbit/s for RX and 24 kbit/s for TX, with
in-band FEC on. Encode and decode in `dxberryd` through libopus (cgo, §16).
One encoder per radio shared by every listener; one decoder per transmitting
browser.

### 8.3 WebRTC

One peer connection per browser per radio, built with Pion. Audio only, one
send track (RX) and one receive track (TX). ICE: host candidates only, UDP
port range 50000–50199, no STUN or TURN servers configured (LAN and the
operator's VPN carry host candidates; a relay is out of scope by decision).
Signalling over `WS /api/audio/<radio>/signal`. DTLS keys are per connection
as the standard requires. The browser's microphone is requested when the
operator taps Microphone or first presses PTT, never on page load; it is
released when the tab stops listening, and goes out only while PTT is held.
(The first draft asked for it on the first PTT press only; rc3 added the
Microphone button, which asks too and names the device.)

A connection that receives no RTP for 5 s is torn down and the page shows
"audio lost, tap to reconnect". If `parec` or `pacat` exits, the station
restarts it once and otherwise shows "no audio from the radio's sound card"
with the device name.

### 8.4 Who hears, who talks

Any number of logged-in browsers may listen to a radio. Only the owner's
browser may transmit (§9.2). RX in the owner's browser ducks by 20 dB while
PTT is held; other listeners hear the radio as it is (the sidetone of the
transmission if the radio monitors, otherwise near silence).

## 9. Sessions, owner lock and guards

### 9.1 Browser sessions

A browser session is a login plus a tab. Each tab opens one `/api/events`
WebSocket; the station knows which radios have a VFO page open in which tab,
and that drives the poll rate and the audio.

### 9.2 Owner lock

Per radio, at most one owner: a browser tab, or a program session
(`dxberry-screen@<app>-<radio>`). Owning means PTT, audio TX and the tune
carrier. Tuning, mode, VFO, levels and listening are open to every
logged-in browser (the station is a single trust domain; the lock is about
the transmitter, not about secrecy). Rules:

- A radio handed to a program is owned by that program while its session is
  active. The VFO page shows "WSJT-X has this radio" and disables PTT, tune
  and audio TX with that reason; "Stop WSJT-X" is the fix it names.
- A free radio is claimed by the first tab that presses PTT or Tune; the
  claim is shown on every other tab ("Phone in the truck has the
  transmitter"). The label is a device name the operator sets once per
  browser (kept in that browser's storage and sent when the events socket
  connects); until set, it is derived from the user agent ("iPhone", "Chrome
  on Linux"). The user name is not shown, since it is almost always `dietpi`.
- Another tab may Take over. The take-over is refused while PTT is on
  ("busy transmitting; try again when the carrier drops") and otherwise
  moves the claim and tells the previous owner.
- A claim is released when the tab closes its WebSocket, after 10 minutes
  without PTT or tune, or by the owner's Release.

### 9.3 PTT

PTT is press-and-hold: the page sends `ptt on` on press and `ptt off` on
release, pointer cancel, blur, or page hide, and resends `ptt on` every 5 s
as a heartbeat while held. The station drops PTT when:

- the heartbeat stops for 7 s;
- PTT has been on for the safety timeout (default 180 s, 30–600 s in
  Settings), with "PTT timed out at 3:00" shown on the page;
- the owner's WebSocket closes;
- the SWR guard trips (§9.4);
- any other tab presses STOP, which is always visible on a radio's page and
  also stops a tune carrier.

The mic track is enabled only while PTT is on and the TX loopback (§8.1) is
loaded only in that window, so VOX radios also work: for a radio whose PTT
method is VOX the "ptt on" intent keys nothing and opens the audio path.

**PTT methods.** The plumbing spec's methods map as follows. `cat`, `cm108`
and `gpio` go through the radio's rigctld, which already keys CM108 and GPIO
for a session program (`PTT_ARGS` in `rigctld@`); the app is one more
rigctld client and keys the same way. A radio with CM108 or GPIO PTT and no
CAT gets a rigctld on Hamlib's dummy model (model 1) carrying only the PTT
arguments, so PTT still has one path; if the plumbing does not start one for
such a radio today, rc2 adds it. `vox` and `none` are audio only. `digirig_tone`
is audio only plus the plumbing spec's keying tone on the `.tx` sink for the
PTT window. The "rig link lost" guard applies to any radio with a rigctld;
audio-only radios have no link to lose and no guard to show.

### 9.4 Guards

- **SWR latch.** For a radio with a readable SWR level: while PTT is on, if
  SWR exceeds the limit (default 3.0:1, settable 1.5–10 per radio) on three
  consecutive 200 ms reads, the station drops PTT, ends any tune carrier and
  latches. While latched every transmit intent is refused with "SWR guard:
  5.8:1 against 3.0:1; tune the antenna, change band, or override". The
  latch clears on a band change, a successful ATU tune (if the radio has one
  and the next SWR read is under the limit), or an Override that asks for
  confirmation in the page. A radio without a readable SWR shows "no SWR
  guard: this radio does not report SWR over CAT" once in its settings.
- **Tune carrier cap.** 10 s, then off.
- **Audio refusal.** PTT with audio TX is refused if the radio's sound server
  or the `.tx` sink is missing, with the device name in the reason. The
  station never substitutes another device.
- **Rig link lost.** Every transmit intent is refused while the rigctld
  connection is down ("rig control unreachable; the radio's rigctld is
  restarting"), and PTT that was on when the link dropped is retried as
  `ptt off` every second until the link returns and confirms it.
- **Program owns the radio.** As in §9.2.

**Transmissions the app did not start.** A program keys the radio through
rigctld itself and Graywolf through its own channel; the app sees PTT on in
its poll either way. STOP and the SWR guard act on the radio, not only on
the app's own keying: both send `\set_ptt 0` through rigctld whatever keyed
it, and the SWR guard watches SWR whenever PTT reads on. The app cannot stop
a program from keying again on its next period, so the notice names that fix
("WSJT-X keyed again; Stop WSJT-X to end it") and the Stop button for the
program sits beside it. Graywolf's channel is not a session and is left to
its own PTT; the guard still unkeys and reports. A guard cannot act on a
radio with no rigctld.

Every guard event is logged (`dxberryd[…]: radio codec: SWR guard tripped
5.8:1 (limit 3.0)`) and sent to every tab as a transient notice.

## 10. Status and events

### 10.1 The status model

`dxberryd` holds one in-memory model: station (callsign, grid, clock), radios
(record, program, owner, rig state, caps summary, audio state, guard state),
programs (installed, versions, kept notices), logbook, services (unit states),
jobs, system (temperatures, load, memory, disk, uptime, network), updates.
The system, programs, logbook and updates parts come from `dxberry-status
--json` run every 30 s (every 60 s when no browser is connected) and from
`dxberry-update check` on its own schedule, so the CLI stays the one source
for those facts; the rig, audio, owner and job parts are the station's own.
`GET /api/status` returns the whole model; the dashboard renders from it.

### 10.2 Events

`/api/events` sends `{part, key, value}` deltas as they happen and a full
model on connect. Rig state for a radio goes out at that radio's poll rate.
Jobs send progress lines. Guards and owner changes send notices. The page
never polls; it reconnects the socket with backoff and shows a "reconnecting"
chip meanwhile, with controls dimmed.

## 11. Jobs

Unchanged in substance from the console spec: long operations (updates,
applying settings, installing programs) run as transient systemd units
started by the CLI, and the app follows the unit's journal. Today
`dxberry-config set` returns the unit as `job` in its JSON and keeps a job
file; `dxberry-update` (which also runs program installs, updates and
removals as job kinds) keeps its own job file and `job` subcommand. rc1 makes
this uniform: every CLI call that starts a unit returns `{"job": "<unit>"}`
in its `--json` answer, and every CLI with jobs answers `job --json` with the
current unit and whether it is active. `internal/jobs` records the unit
name per job id, streams `journalctl -f -u <unit> -o cat` lines over
`WS /api/jobs/<id>`, and ends with the unit's result and exit code. The CLIs
keep the state (their job files); the app only remembers ids for the
session. A job survives the browser leaving and can be reopened from the
Updates or Programs page, and on connect the app lists any unit the CLIs
report as active.

## 12. Program windows

`screen.html` and the vendored noVNC move into the app. The page opens
`WS /api/vnc/<app>-<radio>`; the station validates the name, checks the
session is active, connects to `/run/dxberry/screens/<app>-<radio>.sock` and
relays bytes both ways. The X cookie handling, display numbering, sessions
and units from the applications spec are unchanged. "Open" on a radio card
opens the window in a new tab as today; the VFO page also offers it when a
program owns the radio.

## 13. Terminal

`/terminal` opens `WS /api/term`. The station starts `/bin/login -f dietpi`
in a pty (the logged-in user, which is `dietpi` on the image; a different
local user who logged in gets their own account), 80×24 resized to the
browser, with `TERM=xterm-256color`. The pty closes when the socket does.
`sudo` works as it does over SSH. One terminal per browser session.

## 14. Pages

All pages share one shell: a top bar with the callsign, the UTC clock
(boundary-aligned, as today), a radio switcher when more than one radio
exists, and a menu. At phone width the menu is a sheet; on desktop it is a
sidebar.

- **Dashboard**: as the applications spec's dashboard, with the owner and a
  live mini readout (frequency, mode) on each radio card, and a Listen
  button that opens the radio's VFO page as `/radio/<name>#listen`, where the
  first tap starts RX audio. (Changed in rc3; the first draft played RX on
  the dashboard itself. The tap on Listen loads the radio's page, a new
  document, so a stream started on the dashboard would end there, and the
  dashboard would need its own audio controls; a browser starts sound only
  from a gesture on the page that plays it, so the radio's page asks for one
  tap.)
- **VFO** (`/radio/<name>`): readout, dial (drag and wheel, with a step
  selector 10 Hz to 1 MHz), keypad on tap, band buttons from the radio's
  range, mode buttons from the caps, VFO A/B and SPLIT, filter width slider
  with presets (200, 500, 1000, 1800, 2400, 3000 Hz, where the mode allows),
  RF gain, TX power and AF sliders where settable, S-meter always, SWR and
  ALC and power meters where readable (shown only while transmitting for the
  TX meters), ATU where real, Tune, PTT, STOP, speaker and mic buttons, the
  owner line, the guard banner, and "Open <program>" when a program owns the
  radio. Section order is fixed in this release.
- **Radios**: the console spec's radio management, unchanged in function,
  plus per-radio settings for the SWR limit, the PTT timeout, and a "re-probe
  capabilities" button.
- **Programs**, **Logbook**: as the applications spec.
- **Services**: the console spec's services card with logs and power.
- **Settings**: the console spec's settings, plus "New certificate" and the
  audio port range (read-only, shown for firewall notes).
- **Updates**: as the console spec, with jobs.
- **Terminal**, **Login**.

The frontend is plain HTML, CSS and JavaScript with no build step and no
framework, vendored noVNC and xterm.js as files, served embedded in the
binary. Installable to a phone's home screen through a web manifest; no
service worker (nothing works offline anyway, and a stale cache would lie).

## 15. Provisioning, build and update

- `provision_station` (in `station.sh`): installs the arm64 binary from the
  tarball to `/opt/dxberry/bin/dxberryd` (0755 root), the unit, the PAM
  service file, the nginx site (through `dxb_webproxy_site`), the
  `dxberry-audio@` unit; creates `/etc/dxberry/tls` (0700); enables and
  starts the service; removes Cockpit packages and files if present (an
  update from 0.4.x), including the `cockpit.socket` drop-in and the
  `/usr/share/cockpit/dxberry` link; leaves `systemd-logind` unmasked (the
  app's power actions use it as Cockpit did). Idempotent, write-if-changed,
  one daemon-reload.
- The sessions' private PulseAudio is gone: the session runner gives the
  program `PULSE_SERVER`, its radio's sound server. There is no
  `Requires=` between `dxberry-screen@` and `dxberry-audio@`: the session's
  `dxberry-app prepare` starts the server for a PulseAudio program and stops
  it for an ALSA one, and `stopped` starts it again after an ALSA program
  (§8.1). The radio setup starts and stops it for radios no program owns;
  on an update the radio step runs before `provision_station` has written the
  unit, so `provision_station` runs that start and stop once more after it.
- The image build cross-builds nothing: CI builds `dxberryd` on an arm64
  runner with cgo for PAM and Opus and places the binary in the update
  tarball and the image. The tarball's `install-remote` path and
  `dxberry-update` copy it like any other file; `/etc/dxberry-release` is
  unchanged.
- `dxberry-update` copies the binary like any other file of the tree, and
  gains one step: after a setup run with no failed step it restarts every
  running program window (`dxberry-screen@`) once, so a session started
  before the update moves from its private PulseAudio onto the radio's sound
  server (a stale private server would hold the card and silently break
  browser audio; the cost is one program restart per update).

## 16. Verified facts and open risks

Verified before writing:

- Pion WebRTC is pure Go, supports audio-only peer connections with host
  candidates and a fixed UDP port range (`SettingEngine.SetEphemeralUDPPortRange`).
- rigctld's extended response protocol (`+` prefix) returns the command,
  each value on its own line and `RPRT n`; `\dump_caps` lists supported
  levels, functions, modes and VFO operations; `\get_level SWR` answers on
  backends that implement it and `RPRT -11` (not available) on those that
  do not.
- The applications hardware check showed two rigctld clients on one radio
  (flrig and a program) working, and the per-session PulseAudio with sink
  `CARD` and source `CARD.in`.
- GitHub Actions offers arm64 Linux runners for public repositories, which
  lets CI build the cgo binary natively with Debian's arm64 toolchain.
- Browsers require a secure context (HTTPS) for `getUserMedia`; the Pi is
  already HTTPS-only.

Open risks, each with the plan:

- **cgo and cross-building.** PAM and libopus need cgo; the build runs on an
  arm64 runner with `libpam0g-dev` and `libopus-dev` installed, never on a
  developer machine. If arm64 runners are unavailable at build time, the
  fallback is Debian's cross toolchain (`gcc-aarch64-linux-gnu` with
  multiarch `-dev:arm64` packages) in the same job. Decided and costed: a
  build environment, not a code change.
- **Self-signed certificate on phones.** iOS requires installing and trusting
  the certificate in Settings for `getUserMedia` to be allowed on some
  versions; the login page's download link and a short note cover it. The
  hardware check includes an iPhone and an Android phone.
- **Audio latency through PulseAudio loopbacks.** Budget 100 ms; if the
  measured path is over 150 ms the TX path changes to `dxberryd` writing
  directly to the card's sink through `pacat` with the loopback dropped.
- **Two radios of CPU.** Budget: `dxberryd` under 10% of one core per radio
  with audio running, under 40 MB resident idle. Measured in the hardware
  check with WSJT-X on one radio and the VFO page with audio on the other.
- **rigctld writes while a program polls.** Hamlib serializes per
  connection, not across them; a program's poll can interleave with the
  app's write and the app's optimistic value is corrected by the next read.
  Accepted; the program modules do the same today.
- **`/bin/login -f`** depends on the binary accepting `-f` for root; util-linux
  login does. If a DietPi change removes it, the fallback is `su - dietpi`.

## 17. Decisions

Each as: decision — why — cost if wrong.

1. **Cockpit is removed, not kept as an admin page** — the operator's call;
   every function it gave is re-homed, and two UIs would split the login and
   the certificate — if a Cockpit-only function is missed, it is one page to
   add to the app.
2. **One Go binary, cgo for PAM and Opus** — coordination work, mature
   WebRTC stack, single static-ish binary, fast builds — a cgo build is
   harder to produce than a pure-Go one; mitigated by building in CI only.
3. **Plain JS frontend, no framework, embedded in the binary** — continues
   the console's approach, no toolchain on the Pi or in the repo, reviewable
   files — pages grow hand-written; accepted for a UI of this size.
4. **nginx stays as the TLS front** — it already fronts Pat and Wavelog with
   the auth gate; one place for certificates and ports — one more hop; the
   cost is a few milliseconds.
5. **`dxberryd` runs as root** — every function already needs it, Cockpit
   gave the same through sudo, a broker is a second service — a request
   handling defect is root; mitigated by the allowlisted CLI set, no client
   paths, cookie-only API behind TLS. The unit runs with no mount-namespace
   or capability sandbox (no `ProtectSystem`, `ProtectHome`,
   `ProtectKernel*`, `ProtectControlGroups` or `RestrictRealtime`): the CLIs
   it runs synchronously (udevadm trigger, modprobe, apt) and the terminal's
   login shell need the real system. The limits are cgroup limits only
   (`MemoryMax`, `TasksMax`); §5.1's sandbox list is read accordingly.
6. **The CLI remains the contract for configuration** — tests, idempotency
   and the hardware-proven paths stay as they are; the Go side stays small —
   a round trip through a bash command for every setting; acceptable, these
   are rare actions.
7. **The sound server belongs to the radio, not the program session** — the
   browser and a program must share one card; one owner per card — program
   sessions gain a dependency on `dxberry-audio@`; a radio whose card
   Graywolf holds gets none, as today.
8. **Host candidates only, no STUN, no TURN, no relay** — the operator's
   decision; LAN and the operator's VPN carry host candidates — nothing
   works from the open internet without a VPN, which is the intent.
9. **Owner lock is about the transmitter only** — tuning and listening from
   a second screen is how people use a shack; the thing that needs one owner
   is keying — a second person can retune under the owner's feet; the owner
   sees it happen on the readout.
10. **PTT is press-and-hold with a heartbeat and a 180 s cap** — a dropped
    link or a stuck page must never leave a key down — a long transmission
    needs the operator to re-press; 600 s is the ceiling in Settings.
11. **SWR guard only where SWR is readable; no prediction** — SWR means
    nothing in receive and a stale number would refuse good transmissions —
    radios without CAT SWR get no guard and are told so.
12. **Audio over Opus at 48 kHz mono** — the WebRTC standard codec, low
    bandwidth, FEC for Wi-Fi — digital-mode tones through the browser are
    for the operator's ear, not for decoding; decoding stays in the programs
    on the Pi.
13. **Sessions in memory; restart logs out** — simplest correct store; the
    service restarts only on update — a reboot means logging in again.
14. **Root refused at login; `dietpi` is the account** — same as Cockpit;
    the terminal gives sudo anyway — none.
15. **Release in three pre-releases: parity, VFO, audio** — never lose a
    feature the Pi has today; each step is testable on hardware alone —
    three hardware checks instead of one.
16. **The sound server yields to ALSA programs** (WSJT-X, Pat; flrig opens
    no audio and leaves it alone) and the page says so — a card has one
    holder; changing those programs' audio is its own change — no browser
    audio while WSJT-X runs in this release.
17. **PTT through rigctld for every keyed method, a dummy-model rigctld for
    CAT-less CM108 or GPIO radios** — one keying path, one guard — a radio
    with no CAT gets a rigctld process it did not have.
18. **STOP and the SWR guard unkey the radio whoever keyed it** — the guard
    promise is about the radio — a program may key again; the notice names
    Stop <program> as the fix.
19. **Login requires an administrator** — PAM accepts the password AND the
    user is a member of group `sudo` or `sudo -l -U <user>` reports "may run
    the following commands"; root is always refused; the provisioner does not
    change group membership — on a stock DietPi image the `sudo` group is
    empty (`dietpi` gets root from /etc/sudoers.d/dietpi), so a group-only
    rule would refuse every login. A second, lesser account cannot use the
    app; none exists on the image.
20. **Owner shown by a per-browser device name** — the user name is always
    the same — a label is one more setting to keep.

## 18. Testing

- **Go**: `go vet`, `staticcheck`, `go test -race ./...`. Unit tests for the
  rigctld parser against recorded transcripts (dummy rig model 1 and the
  applications check's real exchanges), capability discovery against
  recorded `\dump_caps`, the poller's rate changes, the owner lock and guard
  state machines (table-driven), session and rate limiting, CLI argument
  validation (every refusal path), the events delta encoder, the audio
  loopback state. An integration test runs `rigctld -m 1` (the dummy rig)
  from Debian's `libhamlib-utils` inside the test container and drives the
  whole rig layer against it.
- **Frontend**: pure logic modules (frequency formatting, step math, the
  events reducer) tested under node in the container; pages checked by hand
  in the hardware check.
- **Bash suite**: `tests/test_station.sh` for provisioning (install,
  idempotence, Cockpit removal from a 0.4.x tree, nginx site rendering, the
  audio unit wiring), run with the existing suite in the `dxberry-tests`
  container, which gains Debian's Go toolchain, `staticcheck`, `libhamlib-utils`,
  `libpam0g-dev` and `libopus-dev`. Nothing runs on the docker host.
- **Hardware check** (per pre-release): update from 0.4.0-rc1, Cockpit gone,
  login and logout, every page at phone and desktop width, program window,
  terminal, a job followed and reopened; VFO readback within 250 ms while
  spinning the knob, every control the CODEC radio can do, hidden controls
  listed with reasons; audio both ways on an iPhone and an Android phone and
  a desktop browser with the latency measured, PTT timeout, heartbeat loss
  (airplane mode mid-PTT), STOP from a second tab, take-over, a program
  owning the radio; two radios where a second interface is available; CPU
  and memory at idle and with audio; Graywolf's channel untouched before and
  after.

## 19. Build order and releases

- **v0.5.0-rc1 — parity.** `dxberryd` with login, sessions, status, events,
  jobs, CLI wrappers, dashboard, radios, programs, logbook, services,
  settings, updates, program windows, terminal; nginx front and certificate;
  Cockpit removed; provisioning and CI. The parity bar is the page as it
  ships in 0.4.0-rc1, not the console spec's text: the settings confirm and
  revert flow, the network settings staying gated off, update roll-back, the
  kept-settings notices, the logbook dialog with its stdin-only key entry,
  every refusal text. The port is read from the 0.4.0 page sources, and
  `tests/` for the page logic are ported with it. Of `console.sh` only the
  `systemd-logind` unmasking survives, moved into `station.sh`; `status.sh`
  reports `dxberryd` where it reported Cockpit and lists `dxberryd.service`
  in place of `cockpit.socket`.
- **v0.5.0-rc2 — the radio.** Rig layer, capability discovery, polling, the
  VFO page, owner lock, tune carrier, the rig-link guard.
- **v0.5.0-rc3 — audio.** The radio's sound server, WebRTC, PTT with
  hold-to-talk, timeouts, SWR guard, STOP, two radios.
- **v0.5.0** after the owner's checks.

What stands on this afterwards, each its own design: modes inside the UI
through the programs' network interfaces (WSJT-X and JS8Call UDP, fldigi
XML-RPC, Pat's HTTP); presets, VFO profiles and macros; the conditions page;
pairing tiers and guest passes for operating from away over a VPN; idle mode;
a scope from an SDR dongle; a CW keyer.

## 20. Left to the builder

- The exact dial widget behavior (drag sensitivity, wheel steps, keypad
  entry format) within the step selector's values.
- The visual design of the pages, within the existing console look and the
  phone-first rule.
- The events wire format beyond `{part, key, value}`.
- How `dxberryd` learns a radio's record changes: polling `dxberry-radio
  list --json` every 5 s or watching the records directory; either is fine.
- The exact systemd sandboxing set, as long as jobs, the terminal and the
  CLIs still work.
- Which `staticcheck` checks are enabled.
