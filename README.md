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

## Console

Browse to `http://<the Pi's address>`; it redirects to `https://`, and your browser warns once about the
Pi's self-signed certificate. Log in as `dietpi` with your `PASSWORD`. The DXBerry page opens first and
refreshes every 10 seconds: Graywolf (running or not, iGate connection, packets per channel, position
log), radios and GPS, the network (Ethernet or WiFi, address, signal), the Pi (temperature, power
warnings, load, memory, disk), the clock, and versions. Buttons open Graywolf, restart it, start, stop
or restart DXBerry's services, open each service's log, and restart or shut down the Pi. Cockpit's own
Overview, Services, Logs, Terminal and Accounts pages are in the same menu.

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

The console is [Cockpit](https://cockpit-project.org) from Debian with one extra page; `CONSOLE=off` in
`dxberry.txt` turns it off. `sudo dxberry-status` prints the same report in a terminal, and
`sudo dxberry-status --json` gives it to scripts.

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
- `provision/` — the provisioner installed at `/opt/dxberry` (`dxberry-preboot`, `dxberry-provision`, `dxberry-netwatch`, `dxberry-radio`, `dxberry-status`, `dxberry-config`, `dxberry-update`, and the console page under `cockpit/`)
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
- [Cockpit](https://cockpit-project.org) provides the web console's login, terminal, service control and logs.

DietPi and Graywolf are GPL-2.0; Cockpit is LGPL-2.1-or-later. DXBerry-Pi is licensed GPL-2.0-or-later; see `LICENSE`.
