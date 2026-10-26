# DXBerry-Pi

A Raspberry Pi image for amateur radio digital modes that is easy to stand up and impossible to outgrow.

Flash it, edit one text file, boot. Minutes later the Pi is on your network at a fixed address running
[Graywolf](https://github.com/chrissnell/graywolf) (APRS iGate / digipeater / TNC with a web UI). The OS is
stock [DietPi](https://dietpi.com) with SSH; every application keeps its own real configuration surface;
every default this image applies lives in this repository.

## About this project

DXBerry-Pi is something W0BTE (Dustin) put together for fun: one Pi, one text file, a working
digital-modes station. It is not a product and has no roadmap promises. If you have a
better way to do any of it, or something is broken, open an issue or a pull request.
Suggestions are welcome.

## Quick start

1. Download `DXBerry-Pi-<version>-rpi234-arm64.img.xz` from the Releases page (Raspberry Pi 2/3/4, 64-bit).
2. Flash it with Balena Etcher or Raspberry Pi Imager.
3. Open the boot partition. Rename `dxberry.txt.example` to `dxberry.txt` and fill it in — `PASSWORD` is
   the only required line. Windows Notepad is fine.
4. Insert the drive, connect Ethernet, power on. First boot needs an Ethernet cable and internet access
   (DietPi installs packages and Graywolf is downloaded) and takes several minutes; WiFi is failover only,
   not usable for the first boot itself.
5. Open `http://<the Pi's address>:8080` for Graywolf (login `WEBUI_USER`, default `admin`, and
   `WEBUI_PASSWORD`, which defaults to the same password as `PASSWORD` if left blank). Open
   `https://<the Pi's address>/` for the DXBerry console (your browser warns once about the Pi's
   self-signed certificate; log in as `dietpi` with your `PASSWORD`). SSH as `root` or `dietpi` with
   your password.
6. In Graywolf, use **Detect Devices** to pick your sound card and PTT. Everything else is already seeded
   from `dxberry.txt` (callsign, iGate, position beacon) when `CALLSIGN` is set.

If `dxberry.txt` is missing or has errors, the Pi boots on DietPi defaults instead (DHCP, hostname
`dxberry-pi`, login `root` / `dietpi`) and writes `dxberry-ERROR.txt` on the boot partition explaining why.
Until a valid `dxberry.txt` is applied, the Pi is reachable on your LAN with the stock DietPi
password, so fix the file and re-run rather than leaving it that way.
After a run, `dxberry-status.txt` on the boot partition says what was applied and what, if anything, failed;
if a step failed for a fixable reason (for example no internet on first boot), the secrets that step needed
are left in place in `dxberry.txt` so `sudo dxberry-provision` can be re-run over SSH once the problem is
fixed. Secrets that were successfully applied are replaced with `<applied>`.

## What `dxberry.txt` controls

Hostname, password, time zone; static address or DHCP; WiFi failover; callsign, position, beacon and
iGate server; the Graywolf admin account; and a few advanced seeds (SSH key, beacon path/symbol,
digipeater preset). See `boot/dxberry.txt.example` — every line is documented. Passwords are replaced with
`<applied>` after they are used.

It is a bootstrap, not a ceiling: Graywolf's web UI owns station configuration after first boot, and
`sudo dxberry-provision` re-applies an edited file without touching anything you changed in the UI.
`dxberry-provision --check` validates the config and prints it with secrets masked, without changing
anything; `--reseed` pushes the file's station/iGate/beacon/digipeater/position-log values into Graywolf
again.

## Networking

Ethernet is primary. If `WIFI_SSID` is set, `dxberry-netwatch` brings WiFi up on the **same address** only
while the cable has no link, and hands back to Ethernet when it returns — exactly one interface is ever
configured. `dxberry-netwatch --status` shows the current state; `--simulate eth0-down|eth0-up|off`
exercises failover without touching cables. Network settings live in `/etc/network/interfaces.d/`; leave
`dietpi-config`'s network menu alone, it does not know about the failover service. `eth0-down`
takes the address off Ethernet, so with no WiFi configured it would leave the Pi unreachable — it is
refused there unless you add `--force` from the console.

## Storage

Logs and the journal live in RAM, swap is on zram, and Graywolf's position log (on by default,
`POSITION_LOG=off` to disable) is kept in RAM too, so routine operation is gentle on SD cards and USB
drives. The position log survives a Graywolf restart, starts empty after a reboot, and never uses more
than 5% of RAM. Real state (Graywolf configuration, mail, logs you keep) is on disk.

## The station app

Browse to `https://<the Pi's address>/` (plain `http://` redirects there). Log in as `dietpi` with the
Pi's password. The certificate is made by the Pi itself, so your browser warns once; to stop the
warning on a phone or another computer, open `https://<the Pi's address>/station.crt` and install the
certificate. The page opens on the dashboard and refreshes every 10 seconds: Graywolf (running or not,
iGate connection, packets per channel, position log), radios and GPS, the network (Ethernet or WiFi,
address, signal), the Pi (temperature, power warnings, load, memory, disk), the clock, and versions.
Buttons open Graywolf, restart it, start, stop or restart DXBerry's services, open each service's log,
and restart or shut down the Pi. A terminal for the Pi is in the same page, and each program you run on
a radio opens in its own window. Cockpit, which earlier versions used for this, is gone: the update
removes it, and the station app takes its place on the same ports (443 and 80), so bookmarks still work.

The terminal is a real root-capable shell for `dietpi` (which has sudo), exactly as over SSH. The
certificate's names (the Pi's hostname, `dxberry.local` and its addresses) are fixed when it is first
created: after renaming the Pi, delete `/etc/dxberry/tls` and restart `dxberryd` to reissue it. The
`CONSOLE=off` setting is still accepted in `dxberry.txt` but has no effect now.

The Radios card lists each radio DXBerry knows (its parts, rig, PTT, rigctld and frequency) with
**Give to Graywolf**, **Release**, **Edit** and **Remove**, and every USB radio interface that is
plugged in but not set up yet, with **Add**: a form filled from the interface's profile, with Hamlib's
searchable list of rig models. It says first when a change stops an application, and when Graywolf
has channels made by hand that a new DXBerry channel could compete with; **Add** notes that pinning a
sound card renames it at the next replug or reboot, which breaks a hand-made channel on it. All of it is
`sudo dxberry-radio` underneath; `sudo dxberry-radio models` lists the rig models.

The Settings section shows the Pi's own settings and changes its hostname, time zone, login password,
SSH key, GPS, position log and the console itself. They are written back to `dxberry.txt`, which stays
the one place they live, and applied by a setup run whose output the page shows; saving a setting
never upgrades Graywolf. Passwords are never shown back. The network settings (address, gateway, DNS
and WiFi) are shown there but, for now, changed in `dxberry.txt`: edit it, run `sudo dxberry-provision`,
then restart the Pi — an interface that is already up keeps its old settings until then. Station,
beacon, iGate and digipeater settings stay in Graywolf's own page. On the command line:
`sudo dxberry-config get`, `sudo dxberry-config set HOSTNAME=shackpi`, and for a password, without it
landing in the shell's history:
`read -rsp 'New password: ' p && printf 'PASSWORD=%s\n' "$p" | sudo dxberry-config set --stdin; unset p`.

The Updates card checks Graywolf, DXBerry and the Debian packages against what is out (cached for
six hours; **Check now** asks again) and installs each after you confirm: Graywolf from its releases
(unless `GRAYWOLF_VERSION` pins it), DXBerry from the newest DXBerry release that carries an update
file (`dxberry-pi-<version>.tar.gz`, checked against its sha256; pre-releases only with **Include
DXBerry pre-releases** on), and the system packages with `apt-get upgrade`. Each runs as a background
job whose output the page shows. A DXBerry update keeps the previous version for **Roll back**. The
checksum proves the file arrived intact, not who made it. On the command line: `sudo dxberry-update
check`, `sudo dxberry-update dxberry`, `sudo dxberry-update job`.

`sudo dxberry-status` prints the same report in a terminal, and
`sudo dxberry-status --json` gives it to scripts.

## Programs

Besides Graywolf, the DXBerry page can install the ham programs you run on a radio: WSJT-X, JS8Call,
fldigi, flrig, QSSTV, Pat (Winlink email), and Wavelog (a logbook that lives on this Pi). Nothing is
installed until you ask: the Programs card lists them, and **Install** and **Remove** work like the
Updates card, as a background job whose output the page shows. WSJT-X, JS8Call, Pat and Wavelog come
from their authors' newest release; fldigi, flrig and QSSTV come from Debian, in the version Debian
ships. The Updates card brings them all up to date.

Choose a program for a radio with **Switch program** on the radio's card and it opens in its own browser tab through the console, so you
use it as if it were a window on the Pi, signed in with the same login as the console. Each radio keeps
its own copy of a program, set up with your callsign and grid square and wired to that radio's sound
card and rig control, so two radios can run two copies side by side. Pat and QSSTV can only be given
to one radio at a time. A program's own settings are kept when you remove it, so installing it again
picks up where you left off.

The logbook is one setting for all programs: this Pi's own Wavelog, or any Wavelog or Cloudlog you
already use. Contacts that WSJT-X and JS8Call log are sent to it, and held and sent later if it cannot
be reached; fldigi follows the same choice unless you changed it inside fldigi. The newest contacts show
on the DXBerry page. On the command line: `sudo dxberry-app list`, `sudo dxberry-app install fldigi`,
and `sudo dxberry-logbook get`.

## Release notes for 0.5.0-rc3

- **Browser audio:** every radio page has a speaker. Any logged-in user can listen, in up to four tabs
  per login on each radio (Opus over WebRTC, one stream per tab). Nothing is recorded or stored. The
  dashboard's **Listen** opens the radio's page, and the first tap there starts the sound (a browser
  plays sound only after a tap on the page itself).
- **Each radio with a sound card gets its own sound server,** started at boot and when the radio is
  added or plugged in, and stopped when it is unplugged or removed. A radio whose sound card Graywolf
  uses has no browser audio.
- **Hold-to-talk PTT** for the radio's transmitter owner. The microphone is asked for once (the
  **Microphone** button or the first PTT press) and released when you stop listening; it is used only
  while PTT is held. A press right after **Listen** waits up to two seconds for the browser's audio to
  reach the station before the radio is keyed. The page sends a heartbeat every 5 seconds, and PTT
  drops after 7 seconds without one, at the radio's PTT timeout (from the per-radio settings, 180
  seconds by default), when the owner's tab closes, on **STOP** from any tab, and on an SWR trip. The
  SWR limit from the per-radio settings is enforced where the radio can read SWR: three readings in a
  row over the limit unkey the radio and latch until you change band, tune the ATU, or confirm an
  override. PTT sends no PTT command to VOX and "none" radios; a VOX radio keys itself on the audio the
  page sends while PTT is held.
- **Phones need the station's certificate trusted.** A browser only hands out the microphone on a page
  it trusts: with the self-signed certificate still untrusted, listening may work but the microphone
  is refused. Install the station's certificate on the phone (or use a browser that accepts it) before
  using PTT.
- **WSJT-X and Pat take the sound card.** While one of them runs there is no browser audio for that
  radio, and the page says which program has it. JS8Call, fldigi and QSSTV share the card with the
  browser. flrig (rig control) opens no audio, so browser audio carries on while it runs.
- **A program that stops while it transmits no longer leaves the radio keyed.** When a program window
  stops (**Stop** on the Programs page, or an update restarting it) on a radio keyed over CAT, the
  radio is unkeyed. flrig's own window is the exception.
- **Firewalls:** audio uses UDP ports 50000 to 50199 on the LAN, host addresses only (no STUN or TURN,
  so no internet is involved). Allow that range between your phone or computer and the Pi.
- **Two radios at once,** each in its own tab, get their own sound server and their own audio stream.
- **Tune** still works only in FM and AM (since rc2).
- **An update restarts any running program windows once;** they come back within seconds. It installs
  the audio packages (libopus and PulseAudio) when they are missing, refreshing the package lists
  first. If the new station app still cannot start, the update says so and keeps the running one, so
  the Updates page and **Roll back** stay reachable.
- **Not yet checked on hardware:**
  - listening in a real browser, and audio quality;
  - latency: from a press to the radio keying, and through the transmit path (a 100 ms budget);
  - the microphone path to a real radio, and a VOX radio keying on the page's audio;
  - iPhones: listening in Safari (the radio plays through Web Audio) and the 20 dB duck while you
    talk;
  - PTT from Safari on an iPhone, which needs Safari to send audio for a muted microphone;
  - the keying tone for DigiRig radios set to tone keying (`digirig_tone`: 1 kHz at -6 dBFS on the
    right channel; your speech goes to both channels);
  - the "Receiving" line and the level meter against a real radio's noise floor;
  - the SWR guard against a real mismatch.

  The hardware check before the final 0.5.0 release covers what a radio on a USB sound card can show;
  the SWR guard and the DigiRig tone need hardware it does not have.

## Release notes for 0.5.0-rc2

- **A live VFO page for each radio:** frequency readout and dial, bands, modes, filter, levels, S-meter
  and ATU. **Tune** sends a carrier in FM or AM for up to 10 seconds, and **STOP** works from any tab.
- **One transmitter owner per radio.** A tab takes over the radio to transmit and releases it when done.
- **Per-radio station settings:** SWR limit and PTT timeout. The guards that use them arrive in the
  next release.
- **Re-probe** a radio from its page to read its rig again.
- **The sound card's name is restored when a radio is removed.**
- **Tune is refused in SSB, CW and data modes**, since PTT alone makes no carrier there.
- **Known gap:** nothing on the page yet shows the station's own transmissions (rc3).

## Release notes for 0.5.0-rc1

- **The station app replaces Cockpit, and the update removes Cockpit.** Log in at the same address with
  the same password. A user can log in when the password is right and the user is an administrator
  (in group `sudo`, or allowed to run commands through `sudo`, as `dietpi` is); root cannot. The
  dashboard, radios, settings, updates, the Graywolf and service controls and logs carried over.
  Program windows (noVNC) open from the programs list on the page, and the terminal is part of the page.
- **One new certificate warning per browser.** Cockpit's certificate is gone; the station app made its
  own, so each browser asks once more.
- **The terminal** is part of the page, a real shell for `dietpi`, as over SSH.
- **Ports 443 and 80 are nginx now**, which forwards to the station app. Bookmarks still work.
- **Nothing changes for your radios, programs, Graywolf or the logbook.**
- **Rolling back to 0.4.x** through the Updates page first removes the station app (its service and
  its nginx site), then runs 0.4.x's own setup, which installs Cockpit again. The station certificate
  stays in `/etc/dxberry/tls`.
- **Not in this release candidate:** the VFO page, audio and PTT arrive in rc2 and rc3.

## Release notes for 0.4.0-rc1

- **Recent contacts from a Wavelog logbook can take up to five minutes to show** on the dashboard:
  the Pi fetches them on the same five-minute schedule it uses to send waiting contacts.
- **Log out of the console and back in once** after the first program is installed, before you open
  a program's tab. Installing it lets your login open program windows, and a login that started
  before that cannot yet; the tab says so.
- **Ports 8081 and 8082** answer on your network once Pat (8081) or Wavelog (8082) is installed. Both
  ask for the console's login first.
- **Before you roll back to 0.3.x**, set every radio to Graywolf or to No program. 0.3.x does not
  know the programs, so a radio left with one would have nothing running it.
- **The logbook's address must start with https://**, except this Pi's own Wavelog. A logbook on
  your network over plain http (or with a self-signed certificate) is not accepted in this release.

## Radio plumbing

`sudo dxberry-radio scan` lists the USB sound cards, serial ports and HID PTT interfaces currently
plugged in. `sudo dxberry-radio add radio1 --audio N --cat N` pins one as `radio1`, giving it a stable
name (`hw:RADIO1`, `/dev/dxberry/radio1-cat`) that survives replugging into a different USB port.
`--audio` and `--cat` also take a port path as `scan` prints it (`--audio usb-0:1.3:1.0`).
`sudo dxberry-radio claim radio1 graywolf` hands it to Graywolf, wiring an audio device, channel and PTT
through Graywolf's API; `release` takes it back. A radio has exactly one owning application at a time,
handed over automatically (the previous owner is unwired first); rigctld runs for every pinned radio
regardless of ownership, since CAT control is not exclusive. `sudo dxberry-radio status` shows what is
present and who owns it.

GPS is configured through `dxberry.txt`: `GPS_DEVICE` (`auto`, `none`, `uart` for a receiver on
the GPIO serial pins, or a `/dev/tty...` path),
`GPS_BAUD`, and `GPS_PPS` (a BCM GPIO number for a 1PPS signal). gpsd feeds both chrony (system time)
and Graywolf (beacon position); `sudo dxberry-radio gps` prints the current fix.

See `docs/design/2026-09-09-radio-plumbing.md` for the full design.

## Building the image yourself

```
sudo build/build-image.sh            # downloads and verifies the official DietPi image, injects /opt/dxberry
build/build-image.sh --check         # verifies the repository tree only (no root, no network)
build/build-image.sh --help          # --version, --dietpi-image, --keep-work
tests/run.sh                         # unit tests (bash, awk, jq)
```

Building needs root plus `losetup`, `xz`, `sha256sum`, `curl`, `partprobe`, `mount` and `udevadm` on the host.
Output goes to `out/DXBerry-Pi-<version>-rpi234-arm64.img.xz` and a matching `.sha256` file.

## Layout

- `boot/` — files for the boot partition and DietPi's automation hooks
- `provision/` — the provisioner installed at `/opt/dxberry` (`dxberry-preboot`, `dxberry-provision`, `dxberry-netwatch`, `dxberry-radio`, `dxberry-status`, `dxberry-config`, `dxberry-update`, and `bin/dxberryd`, the station app built from `station/`)
  - `bin/dxberry-config` — settings management
  - `lib/settings.sh` — settings provisioning library
  - `bin/dxberry-update` — update check and install
  - `lib/update.sh` — update library
- `build/` — image build, and the update file (`make-update-tarball.sh`)
- `docs/design/` — design specifications; `docs/plans/` — implementation plans

## Credits and license

DXBerry-Pi is a thin layer over other people's work:

- [DietPi](https://dietpi.com) by MichaIng and contributors is the operating system. The image
  is the official DietPi Raspberry Pi 2/3/4 64-bit Trixie image with DXBerry-Pi's automation
  files added; DietPi does the installing.
- [Graywolf](https://github.com/chrissnell/graywolf) by Chris Snell provides APRS, the TNC and the web UI.
- [Hamlib](https://hamlib.github.io) (`rigctld`), [gpsd](https://gpsd.io) and
  [chrony](https://chrony-project.org) handle CAT control, GPS and time.

DietPi and Graywolf are GPL-2.0. DXBerry-Pi is licensed GPL-2.0-or-later; see `LICENSE`.
