# DXBerry-Pi

A Raspberry Pi image for amateur radio digital modes that is easy to stand up and impossible to outgrow.

Flash it, edit one text file, boot. Minutes later the Pi is on your network at a fixed address running
[Graywolf](https://github.com/chrissnell/graywolf) (APRS iGate / digipeater / TNC with a web UI). The OS is
stock [DietPi](https://dietpi.com) with SSH; every application keeps its own real configuration surface;
every default this image applies lives in this repository.

## Quick start

1. Download `DXBerry-Pi-<version>-rpi234-arm64.img.xz` from the Releases page (Raspberry Pi 2/3/4, 64-bit).
2. Flash it with Balena Etcher or Raspberry Pi Imager.
3. Open the boot partition. Rename `dxberry.txt.example` to `dxberry.txt` and fill it in — `PASSWORD` is
   the only required line. Windows Notepad is fine.
4. Insert the drive, connect Ethernet, power on. First boot needs an Ethernet cable and internet access
   (DietPi installs packages and Graywolf is downloaded) and takes several minutes; WiFi is failover only,
   not usable for the first boot itself.
5. Open `http://<the Pi's address>:8080` for Graywolf (login `WEBUI_USER`, default `admin`, and
   `WEBUI_PASSWORD`, which defaults to the same password as `PASSWORD` if left blank). SSH as `root` or
   `dietpi` with your password.
6. In Graywolf, use **Detect Devices** to pick your sound card and PTT. Everything else is already seeded
   from `dxberry.txt` (callsign, iGate, position beacon) when `CALLSIGN` is set.

If `dxberry.txt` is missing or has errors, the Pi boots on DietPi defaults instead (DHCP, hostname
`dxberry-pi`, login `root` / `dietpi`) and writes `dxberry-ERROR.txt` on the boot partition explaining why.
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
anything; `--reseed` pushes the file's station/iGate/beacon/digipeater values into Graywolf again.

## Networking

Ethernet is primary. If `WIFI_SSID` is set, `dxberry-netwatch` brings WiFi up on the **same address** only
while the cable has no link, and hands back to Ethernet when it returns — exactly one interface is ever
configured. `dxberry-netwatch --status` shows the current state; `--simulate eth0-down|eth0-up|off`
exercises failover without touching cables. Network settings live in `/etc/network/interfaces.d/`; leave
`dietpi-config`'s network menu alone, it does not know about the failover service.

## Storage

Logs and the journal live in RAM, swap is on zram, and Graywolf prunes its own position log, so routine
operation is gentle on SD cards and USB drives. Real state (Graywolf configuration, mail, logs you keep) is
on disk.

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
- `provision/` — the provisioner installed at `/opt/dxberry` (`dxberry-preboot`, `dxberry-provision`, `dxberry-netwatch`)
- `build/` — image build
- `docs/design/` — design specifications; `docs/plans/` — implementation plans

## Credits and license

Built on [DietPi](https://github.com/MichaIng/DietPi) and [Graywolf](https://github.com/chrissnell/graywolf),
both GPL-2.0. DXBerry-Pi is licensed GPL-2.0-or-later; see `LICENSE`.
