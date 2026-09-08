# DXBerry-Pi — Base Image and Provisioning

Sub-project 1 of 5. Design specification, 2026-09-07.

## 1. Purpose

DXBerry-Pi turns a Raspberry Pi into a multi-mode amateur radio station that is
easy to stand up and impossible to outgrow. A user flashes one image, edits one
text file, and boots. Minutes later the Pi is on their network at a fixed
address, running Graywolf (APRS iGate/digipeater/TNC) with a web UI, and every
part of the system is still fully theirs: the OS is stock DietPi/Debian with
SSH, every application exposes its own real configuration surface, and every
default the image applies lives in this repository where it can be read, changed,
and forked.

### Principles

1. **Easy defaults, nothing hidden.** Convenience never removes a knob. The
   station config file is a bootstrap, not a ceiling.
2. **One file to edit.** Everything needed for first boot comes from
   `dxberry.txt` on the boot partition. No terminal is required to get to a
   working station.
3. **Secrets never enter the repository.** The repo tracks templates and code.
   Real values exist only on the user's own boot partition and are scrubbed
   once applied.
4. **Verified, not assumed.** Every behavior in this document that depends on
   DietPi or Graywolf internals was checked against their current source.
   Remaining unknowns are listed in §16, not papered over.
5. **Failover by construction.** Network failover is designed so that the
   failure modes it exists to prevent cannot occur, rather than being
   corrected after the fact.

## 2. Scope

**In scope (this sub-project):**

- Image build pipeline: official DietPi image → DXBerry-Pi image, locally and in CI.
- `dxberry.txt` configuration file and its validation.
- First-boot flow: pre-network hook, DietPi automated first run, post-install provisioning, reboot.
- Static or DHCP addressing with Ethernet-primary / WiFi-failover on a single address (`dxberry-netwatch`).
- Graywolf installation (always the latest upstream release) and initial seeding through its REST API.
- Storage write-minimization (tier 1).
- Secrets handling and scrubbing.
- GitHub Releases publishing of the image.

**Out of scope (later sub-projects):**

2. Radio plumbing — `rigctld`, stable udev names for audio/serial devices, `gpsd` + `chrony`, the radio-slot ownership model.
3. DXBerry Console — the web front door and its API.
4. Applications — Pat (+ARDOP), browser desktop with WSJT-X / JS8Call / fldigi / flrig.
5. Overlay-root storage mode; then WiFi hotspot fallback, reverse proxy, Pi 5 image, offline fully-baked image, SDR, AX.25 node.

This design reserves the hooks those sub-projects need (§15) without implementing them.

## 3. User flow

1. Download `DXBerry-Pi-<version>-rpi234-arm64.img.xz` from GitHub Releases.
2. Flash it with Balena Etcher or Raspberry Pi Imager.
3. Open the boot partition (it mounts on Windows, macOS and Linux). Rename
   `dxberry.txt.example` to `dxberry.txt` and fill it in.
4. Insert the drive, connect Ethernet (recommended for first boot), power on.
5. After roughly five to eight minutes the Pi is reachable at the configured
   address: SSH on port 22, Graywolf at `http://<address>:8080`.
6. Configure radio hardware (audio device, PTT) in Graywolf's UI using its
   device detection. Everything else is already seeded.

If `dxberry.txt` is missing or invalid, the Pi still boots — with DietPi's
defaults (DHCP, hostname `DietPi`, password `dietpi`) — and writes
`dxberry-ERROR.txt` to the boot partition explaining exactly what was wrong.

## 4. Repository layout

```
DXBerry-Pi/
├── README.md
├── LICENSE                          # GPL-2.0-or-later
├── .gitignore                       # out/, *.img*, dxberry.txt, dietpi-wifi.txt
├── build/
│   └── build-image.sh               # stock DietPi .img.xz → DXBerry-Pi .img.xz
├── boot/                            # first-boot files (see §6 for where each lands)
│   ├── dietpi.overrides.txt         # DietPi keys applied onto the stock dietpi.txt at build time (no secrets)
│   ├── dxberry.txt.example          # the one file users edit (FAT partition)
│   ├── README-DXBERRY.txt           # three-line pointer on the FAT partition
│   ├── Automation_Custom_PreScript.sh   # → /opt/dxberry/bin/dxberry-preboot
│   └── Automation_Custom_Script.sh      # → /opt/dxberry/bin/dxberry-provision --first-boot
├── provision/                       # copied to /opt/dxberry/ on the image's root partition
│   ├── bin/
│   │   ├── dxberry-preboot
│   │   ├── dxberry-provision
│   │   └── dxberry-netwatch
│   ├── lib/                         # sourced bash modules, one concern each
│   │   ├── common.sh                # logging, status file, helpers
│   │   ├── config.sh                # dxberry.txt parse + validate (unit-tested)
│   │   ├── system.sh                # hostname, timezone, serial console, ssh keys
│   │   ├── network.sh               # interfaces, wpa_supplicant, netwatch install
│   │   ├── storage.sh               # journald, zram checks
│   │   ├── graywolf.sh              # download, verify, install, seed
│   │   └── scrub.sh                 # secret scrubbing
│   ├── templates/                   # rendered with values from config
│   │   ├── interfaces-eth0.tmpl
│   │   ├── interfaces-wlan0.tmpl
│   │   ├── journald-dxberry.conf
│   │   └── dxberry-netwatch.service
│   └── VERSION
├── tests/
│   ├── run.sh                       # dependency-free test runner
│   ├── lib.sh                       # assert helpers
│   └── test_*.sh                    # unit tests (config, common, preboot, network, netwatch, scrub, graywolf)
├── docs/
│   ├── design/                      # this document and successors
│   └── plans/                       # implementation plans
└── .github/workflows/
    ├── ci.yml                       # shellcheck + unit tests on every push/PR
    └── release.yml                  # build + attach image on v* tags
```

Tracked: everything above. Ignored: `out/`, built images, and any real
`dxberry.txt` / `dietpi-wifi.txt`.

## 5. Configuration: `dxberry.txt`

Plain `KEY=value` lines, `#` comments, editable in Notepad. Unknown keys are
reported as warnings, not errors. Values may be unquoted or double-quoted.
CRLF line endings are accepted (Windows editors).

| Key | Required | Default | Validation / notes |
|---|---|---|---|
| `HOSTNAME` | no | `dxberry-pi` | RFC 1123 label: lowercase letters, digits, hyphens, 1–63 chars |
| `PASSWORD` | yes | — | login password for `root` and `dietpi`; 8–100 chars |
| `TIMEZONE` | no | `UTC` | must exist under `/usr/share/zoneinfo` |
| `STATIC_IP` | no | blank = DHCP | CIDR, e.g. `192.168.1.90/24`; applies to both interfaces |
| `GATEWAY` | if `STATIC_IP` | — | IPv4 within `STATIC_IP`'s subnet |
| `DNS` | no | `GATEWAY` | one or more IPv4 addresses, space-separated |
| `WIFI_SSID` | no | blank = Ethernet only | 1–32 chars |
| `WIFI_PASSWORD` | if `WIFI_SSID` | — | WPA-PSK passphrase, 8–63 chars |
| `WIFI_COUNTRY` | if `WIFI_SSID` | — | ISO 3166-1 alpha-2, uppercase |
| `CALLSIGN` | no | blank = Graywolf left unconfigured | `^[A-Z0-9]{3,7}(-[0-9]{1,2})?$` |
| `LATITUDE` | no | — | decimal degrees, −90…90; `LATITUDE` and `LONGITUDE` must be given together |
| `LONGITUDE` | no | — | decimal degrees, −180…180 |
| `BEACON_COMMENT` | no | `DXBerry-Pi iGate` | ≤ 43 chars |
| `BEACON_INTERVAL_MIN` | no | `30` | integer 1–120 |
| `IGATE_SERVER` | no | `rotate.aprs2.net` | hostname |
| `WEBUI_USER` | no | `admin` | Graywolf admin username, 1–32 chars |
| `WEBUI_PASSWORD` | no | value of `PASSWORD` | Graywolf admin password, 8–100 chars |

Advanced seeds (blank = default):

| Key | Default | Notes |
|---|---|---|
| `SSH_PUBKEY` | — | one OpenSSH public key; added to `authorized_keys` for `root` and `dietpi` |
| `BEACON_SEND` | `is` | `is`, `rf`, or `both`; `is` keeps a receive-only station silent on RF |
| `BEACON_PATH` | `WIDE1-1,WIDE2-1` | used only when `BEACON_SEND` is `rf` or `both` |
| `BEACON_SYMBOL` | `R&` | two characters: table/overlay + symbol; `R&` is receive-only iGate |
| `DIGIPEATER` | `off` | `off`, `fillin`, or `wide` — mapped to Graywolf's digipeater presets |
| `IGATE_RF_TO_IS` | `on` | `on`/`off` |
| `IGATE_IS_TO_RF` | `off` | `on`/`off` |
| `GRAYWOLF_VERSION` | blank = latest release | exact upstream tag such as `v0.14.13` |
| `SERIAL_CONSOLE` | `off` | `on`/`off`; off leaves the GPIO UART free for GPS hardware |

Reserved for later sub-projects: keys prefixed `PAT_`, `WSJTX_`, `JS8CALL_`,
`FLDIGI_`, `RIG_`, `GPS_`, `CONSOLE_`. The parser accepts and ignores them
today so a config written for a later image version does not error on an
older one.

Radio hardware (audio device, PTT method and device path) is intentionally not
in this file. It varies per rig, and Graywolf's "Detect Devices" UI handles it
better than a text field can.

After the values are applied, `PASSWORD`, `WIFI_PASSWORD` and `WEBUI_PASSWORD`
are rewritten to `<applied>` in place (§13).

## 6. First-boot sequence

DietPi's first-boot script runs, in order: the custom pre-script, hostname,
password, network from `dietpi.txt`, then the automated first-run installs,
then the custom post-script. DXBerry-Pi uses both hooks.

**Where files live on a Raspberry Pi.** DietPi mounts the FAT partition at
`/boot/firmware`. DietPi's own `dietpi.txt`, `dietpi-wifi.txt` and the two
`Automation_Custom_*.sh` hooks live on the root filesystem under `/boot/`; at
first boot DietPi copies any newer user-edited copies of those specific files
from the FAT partition into `/boot/` and deletes them from FAT. Everything
DXBerry-Pi shows the user — `dxberry.txt`, `dxberry-ERROR.txt`,
`dxberry-status.txt`, `README-DXBERRY.txt` — lives on the FAT partition and
stays there. The provisioner locates it with `dxb_boot_dir` (`/boot/firmware`
when a vfat filesystem is mounted there, else `/boot`).

### 6.1 Pre-network: `dxberry-preboot`

Invoked by `/boot/Automation_Custom_PreScript.sh` before any network is up.

1. Parse and validate `<boot>/dxberry.txt` (`lib/config.sh`).
2. On failure: write `<boot>/dxberry-ERROR.txt` listing each problem with the
   line it came from, leave `dietpi.txt` untouched, exit 0. The Pi boots with
   DietPi defaults and stays reachable over DHCP.
3. On success, rewrite these `dietpi.txt` keys from the config:
   `AUTO_SETUP_NET_HOSTNAME`, `AUTO_SETUP_GLOBAL_PASSWORD`,
   `AUTO_SETUP_TIMEZONE`, `AUTO_SETUP_NET_ETHERNET_ENABLED=1`,
   `AUTO_SETUP_NET_WIFI_ENABLED=0`, `AUTO_SETUP_NET_USESTATIC` (1 if
   `STATIC_IP`, else 0), `AUTO_SETUP_NET_STATIC_IP`,
   `AUTO_SETUP_NET_STATIC_MASK`, `AUTO_SETUP_NET_STATIC_GATEWAY`,
   `AUTO_SETUP_NET_STATIC_DNS`, `AUTO_SETUP_NET_WIFI_COUNTRY_CODE`,
   `CONFIG_SERIAL_CONSOLE_ENABLE`. DietPi's own `dietpi.txt` takes a bare
   address plus a separate mask, not CIDR notation, so the address is
   written from the validated `_IP` with the mask derived from `_PREFIX`.
4. If `WIFI_SSID` is set, write slot 0 of `/boot/dietpi-wifi.txt`
   (`aWIFI_SSID[0]`, `aWIFI_KEY[0]`, `aWIFI_KEYMGR[0]='WPA-PSK'`).

WiFi is always left disabled for DietPi itself because DietPi's rule is "if
both are enabled, WiFi takes priority and Ethernet is disabled". WiFi is
managed exclusively by `dxberry-netwatch` (§8).

### 6.2 DietPi automated first run

The image build applies `boot/dietpi.overrides.txt` onto the stock
`dietpi.txt` (so the file tracks whatever DietPi ships, with only these keys
changed):

```
AUTO_SETUP_AUTOMATED=1
AUTO_SETUP_SSH_SERVER_INDEX=-2          # OpenSSH
AUTO_SETUP_LOGGING_INDEX=-1             # RAMlog, hourly clear
AUTO_SETUP_SWAPFILE_SIZE=1              # auto
AUTO_SETUP_SWAPFILE_LOCATION=zram
AUTO_SETUP_LOCALE=en_US.UTF-8
AUTO_SETUP_KEYBOARD_LAYOUT=us
AUTO_SETUP_CUSTOM_SCRIPT_EXEC=0         # run /boot/Automation_Custom_Script.sh
AUTO_SETUP_APT_INSTALLS=curl ca-certificates jq wpasupplicant
SURVEY_OPTED_IN=0
CONFIG_SERIAL_CONSOLE_ENABLE=0
```

DietPi's own automated first run needs internet access (it updates APT and
installs packages); that is a DietPi requirement, not something this design
can remove.

These are the shipped defaults. The keys listed in §6.1 are overwritten by
`dxberry-preboot` from `dxberry.txt` on every first boot (so, for example,
`CONFIG_SERIAL_CONSOLE_ENABLE` becomes 1 when `SERIAL_CONSOLE=on`). DietPi
wipes `AUTO_SETUP_GLOBAL_PASSWORD` from the file after applying it.

### 6.3 Post-install: `dxberry-provision --first-boot`

Invoked by `/boot/Automation_Custom_Script.sh` after DietPi's installs
finish. Runs the provisioning steps in §7 and reboots.

### 6.4 Second boot

`dxberry-netwatch` brings up the network, Graywolf starts under systemd, and
the station is operational. Nothing from the first-boot path runs again.

## 7. Provisioner: `dxberry-provision`

A bash program at `/opt/dxberry/bin/dxberry-provision`, sourcing the modules in
`/opt/dxberry/lib/`. Each module exposes one `provision_<concern>` function; the
driver runs them in a fixed order:

`config → system → network → storage → graywolf → scrub → status`

### 7.1 Modes

| Invocation | Behavior |
|---|---|
| `dxberry-provision --first-boot` | full run, then reboot |
| `dxberry-provision` | full run, no reboot; for re-applying an edited `dxberry.txt` over SSH |
| `dxberry-provision --reseed` | as above, and re-applies station/iGate/beacon/digipeater values to Graywolf even if it was already set up |
| `dxberry-provision --check` | parse and validate only; prints the effective configuration with secrets masked |

### 7.2 Idempotency rules

- Every step compares desired state to current state and only acts on a
  difference. A re-run on an already-provisioned system changes nothing and
  says so.
- Password fields that read `<applied>` are skipped. A new plaintext value is
  applied and then scrubbed again.
- Graywolf seeding runs only while Graywolf reports `needs_setup: true`
  (fresh install), unless `--reseed` is given. Changes made in Graywolf's UI
  are never overwritten by an ordinary re-run.
- `--reseed` needs Graywolf credentials. It uses `WEBUI_USER`/`WEBUI_PASSWORD`
  from the file if present, otherwise prompts on the terminal.
- The network step strips DietPi's own eth0/wlan0 stanzas from the main
  `/etc/network/interfaces` file (§16) before writing its own `interfaces.d/`
  drop-ins; a re-run with nothing left to strip changes nothing.

### 7.3 Failure handling

A step that fails is logged and reported in the status file; later steps that
do not depend on it still run. Network configuration is applied before
Graywolf installation so a Graywolf download failure (GitHub unreachable,
upstream outage, a pinned version that does not exist) still leaves a
reachable Pi. Graywolf download is
retried three times with backoff; a persistent failure is recorded and the
user is told to run `sudo dxberry-provision` once the Pi has internet.

### 7.4 Logging and status

- Persistent log: `/var/lib/dxberry/provision.log` (root-only, never on the
  RAM log filesystem).
- Human-readable summary: `/boot/dxberry-status.txt` — timestamp, image
  version, hostname, the **configured** network mode and address, Graywolf
  version and URL, and any step that failed. Written on every run, successful
  or not. The address it reports is the one just written to
  `interfaces.d/`, which becomes the live address when `dxberry-netwatch`
  next applies it — on first boot that is after the reboot, not while the
  provisioner is still running. Readable from a PC if the drive is pulled.

## 8. Network: `dxberry-netwatch`

### 8.1 Why not NetworkManager

DietPi removes NetworkManager from its images, masks D-Bus/logind by default,
and its network tooling assumes ifupdown. Reusing a NetworkManager dispatcher
design would mean fighting the distribution. The design below uses only
ifupdown, `wpa_supplicant` and iproute2, all present on the base image.

### 8.2 Invariant

**Exactly one of `eth0` / `wlan0` carries an address at any time, and only
`dxberry-netwatch` assigns addresses.** Neither interface is `auto` or
`allow-hotplug` in ifupdown, so nothing else can bring one up. This makes
duplicate addresses, duplicate-address-detection stalls and route-metric
tie-breaking impossible rather than merely handled.

### 8.3 Interface definitions

Rendered from templates into `/etc/network/interfaces.d/`:

```
# /etc/network/interfaces.d/eth0.conf — static form; DHCP form is "inet dhcp" with no address lines
iface eth0 inet static
address 192.168.1.90/24
gateway 192.168.1.1

# /etc/network/interfaces.d/wlan0.conf — same addressing; present only when WIFI_SSID is set
iface wlan0 inet static
address 192.168.1.90/24
gateway 192.168.1.1
wpa-conf /etc/wpa_supplicant/wpa_supplicant.conf
pre-up iw dev wlan0 set power_save off || true
post-down iw dev wlan0 set power_save on || true
```

This is the exact form DietPi's own `dietpi-network` writes (CIDR `address`,
same file names), so `dietpi-config` still displays the interfaces. DNS is not
in the stanza: DietPi images ship without `resolvconf`, so `dns-nameservers`
would be ignored; in static mode the provisioner writes `/etc/resolv.conf`
directly, as DietPi itself does. In DHCP mode the DHCP client manages it.
`wpa_supplicant.conf` is generated by DietPi's `dietpi-wifidb 1` from
`dietpi-wifi.txt`, so DietPi's WiFi tooling keeps working alongside ours.

Running `dietpi-config`'s network menu re-adds DietPi's own eth0/wlan0
stanzas to the main `/etc/network/interfaces` file; the next
`dxberry-provision` run strips them again (§16).

### 8.4 Behavior

Both interfaces are driven with `ifup`/`ifdown`. After `ifdown eth0`, netwatch
immediately re-raises the link (`ip link set eth0 up`) without an address:
carrier state is only reported for administratively-up links, and an
interface left down after `ifdown` would never signal that the cable came
back.

States: `ETH` (eth0 configured), `WIFI` (wlan0 configured), `NONE` (neither;
no carrier and no WiFi configured or WiFi association failed).

- **Start:** raise eth0. Carrier present → `ifup eth0` → `ETH`. Otherwise, if
  wlan0 is defined → `ifup wlan0` → `WIFI`; else `NONE`.
- **Carrier lost in `ETH`:** `ifdown eth0`, re-raise link, then `ifup wlan0`
  if defined → `WIFI`, else `NONE`.
- **Carrier returns in `WIFI` or `NONE`:** `ifdown wlan0` first (if up), then
  `ifup eth0` → `ETH`. Sequential; no overlap.
- **Association lost in `WIFI`** (access point gone, wlan0 reports no
  carrier): `ifdown wlan0` → `NONE`.
- **In `NONE`:** retry `ifup wlan0` every 30 s while wlan0 is defined.

Events come from `ip monitor link` (kernel netlink, immediate). A 2 s debounce
absorbs link flaps. A 5 s poll of `/sys/class/net/eth0/carrier` backs up the
event stream. All transitions are logged to the journal.

`ifdown` is always called before `ifup` on the other interface, so the same
address is never held by two interfaces even for an instant.

Starting netwatch is non-disruptive: if ifupdown already reports the right
interface configured (a service restart, not a boot), it adopts that state
instead of cycling the interface. A provisioner re-run restarts netwatch only
when an interface file actually changed, and does so as its last action, since
the SSH session running the provisioner may drop when the address moves.

### 8.5 DHCP mode

With `STATIC_IP` blank the stanzas use `inet dhcp`. The same state machine
applies; ifupdown starts and stops the system DHCP client with the interface.
The address may differ between Ethernet and WiFi in this mode, which is
inherent to DHCP and acceptable.

### 8.6 systemd integration

`dxberry-netwatch.service`: `Type=notify` with `NotifyAccess=all`,
`After=networking.service`, `Before=network-online.target`,
`WantedBy=multi-user.target network-online.target`, `Restart=always`,
`TimeoutStartSec=90`.

Readiness is signalled (`systemd-notify --ready`) as soon as the initial state
has been applied: `nw_startup` adopts whatever ifupdown already has, runs one
immediate tick — no debounce — and brings the chosen interface up, then the
daemon reports ready and enters its event loop. There is **no settle window
and no wait for an address**: a Pi that boots with no link at all decides
`NONE` and still reports ready rather than holding the boot open. So anything
ordered after `network-online.target`, including Graywolf's unit, waits for
netwatch's first decision, not for connectivity.

### 8.7 Test hooks

`dxberry-netwatch --simulate eth0-down` / `--simulate eth0-up` inject the
corresponding event into the running service so failover can be exercised
over SSH. Physical cable pull/replug is part of acceptance (§14).

### 8.8 Later extension

The `NONE` state is where sub-project 5's hotspot fallback attaches: instead
of retrying wlan0 forever, netwatch will bring up an access point. No change to
the invariant or the other states is required.

## 9. Graywolf

### 9.1 Installation

Always the latest upstream release unless `GRAYWOLF_VERSION` pins a tag.

1. Fetch `https://github.com/chrissnell/graywolf/releases/latest/download/checksums.txt`
   (or `releases/download/<tag>/checksums.txt` when pinned). This path redirects
   to the versioned asset without using the GitHub API, so it is not subject to
   API rate limits.
2. Select the line matching `graywolf_<version>_<arch>.deb` where `<arch>` is
   `dpkg --print-architecture` (`arm64` on this image; `armhf` is handled but
   not built or tested here).
3. Download the `.deb` from the same release path, verify its SHA-256 against
   `checksums.txt`, and install with `apt-get install ./graywolf_*.deb`.
4. Graywolf's package creates the `graywolf` system user (groups `audio`,
   `dialout`, `plugdev`, `gpio`), installs its hardened systemd unit, and
   enables it. The unit binds `0.0.0.0:8080`, so the UI is reachable on the LAN.
5. Start the service and wait for `GET /api/auth/setup` to answer.

### 9.2 Seeding

All calls go to `http://127.0.0.1:8080/api` with a session cookie.

1. `GET /auth/setup` → proceed only if `needs_setup` is true (or `--reseed`).
2. `POST /auth/setup {username, password}` — creates the administrator.
3. `POST /auth/login` — obtains the `graywolf_session` cookie.
4. `PUT /station/config {callsign}` — when `CALLSIGN` is set.
5. `PUT /igate/config {enabled: true, server, port: 14580, gate_rf_to_is,
   gate_is_to_rf}` — when `CALLSIGN` is set.
6. `POST /beacons` — when `LATITUDE`/`LONGITUDE` are set: a `position` beacon
   with `latitude`, `longitude`, `comment`, `interval` (seconds; minutes × 60),
   `send_path` (`is_only`, `rf` or `both`), `path`, `symbol_table`, `symbol`,
   `enabled: true`. The beacon inherits the station callsign. `rf`/`both`
   require a radio channel; if none exists yet the beacon is created as
   `is_only` and the status file says so — switching it is one click in the
   UI once a channel is configured. The beacon's id is remembered in
   `/var/lib/dxberry/graywolf-seed.env` so `--reseed` updates it rather than
   creating duplicates.
7. Digipeater — when `DIGIPEATER` is `fillin` or `wide`:
   `PUT /digipeater {enabled: true, my_call, dedupe_window_seconds: 30}`.
   Graywolf's "presets" are rule sets, and every rule is bound to a radio
   channel, which cannot exist before hardware is configured. The rules are
   therefore created by `--reseed` once at least one channel exists (bound to
   the first channel), if no rules exist yet:
   - fill-in: `{alias: <CALLSIGN>, alias_type: exact, max_hops: 1, priority: 1}`
     and `{alias: WIDE, alias_type: widen, max_hops: 1, priority: 10}`
   - wide: the same with `max_hops: 2` on the `WIDE` rule.
   The status file tells the user this step is pending until a channel exists.
8. `POST /auth/logout`.

All writes to existing Graywolf objects (`igate/config`, an existing beacon)
read the current object first and merge the seeded fields into it, so
settings the user changed in the UI that the file does not cover survive a
`--reseed`.

With `CALLSIGN` blank, steps 4–7 are skipped: Graywolf is installed with an
admin account and the user completes station setup in the UI.

### 9.3 Updates

Out of scope here. The console (sub-project 3) owns application updates. Until
then, `sudo apt-get install ./graywolf_<new>.deb` upgrades in place, and
Graywolf keeps its configuration across upgrades.

## 10. Storage protection, tier 1

Goal: remove routine writes to the SD card / USB drive while keeping real state
changes (configuration saves, logs the user wants) on disk.

| Measure | Mechanism |
|---|---|
| System logs in RAM | DietPi RAMlog (`AUTO_SETUP_LOGGING_INDEX=-1`), 50 MiB tmpfs, hourly clear |
| journald in RAM | drop-in `/etc/systemd/journald.conf.d/dxberry.conf`: `Storage=volatile`, `RuntimeMaxUse=32M` |
| Swap | zram (`AUTO_SETUP_SWAPFILE_LOCATION=zram`), never a file on flash |
| `/tmp` | DietPi's default tmpfs |
| `noatime` | DietPi's default mount options |
| Graywolf position log | Graywolf prunes it at 30 days by design; nothing to configure |
| Provisioner state | `/var/lib/dxberry/` — small, written only during provisioning |

Overlay-root mode with a persistent-state list is sub-project 5.

## 11. Image build

`build/build-image.sh [--version <v>] [--dietpi-image <path>]`, run as root
(loop devices). Steps:

1. Download `https://dietpi.com/downloads/images/DietPi_RPi234-ARMv8-Trixie.img.xz`
   and its published SHA-256 into `build/work/`; verify; skip download if the
   verified file is already present.
2. Decompress to `build/work/base.img`; `losetup -P` to expose partitions.
3. Mount partition 1 (boot, FAT) and partition 2 (root, ext4).
4. Copy `boot/*` to the boot partition. Copy `provision/` to `/opt/dxberry/`
   on the root partition with `bin/*` mode 0755 and everything else 0644,
   owner root. Symlink `/usr/local/sbin/dxberry-provision` and
   `dxberry-netwatch` to their `/opt/dxberry/bin` targets.
5. Write `/etc/dxberry-release` on the root partition:
   `DXBERRY_VERSION`, `DXBERRY_COMMIT`, `DXBERRY_BUILD_DATE`,
   `DIETPI_IMAGE`, `DIETPI_IMAGE_SHA256`.
6. Unmount, detach loop device, rename to
   `out/DXBerry-Pi-<version>-rpi234-arm64.img`, compress with `xz -T0`, write
   `.sha256` beside it.

Version comes from `--version`, else `git describe --tags --always`. The build
touches only files; it never boots or emulates the image, so it completes in
about a minute and needs no QEMU.

## 12. CI/CD

- `ci.yml` — on every push and pull request: `shellcheck` over all scripts,
  `bash -n` on every script, `bats tests/`, and `build-image.sh --check`
  (validates that every file `boot/` and `provision/` reference exists, without
  downloading anything).
- `release.yml` — on tags matching `v*`: runs the build on `ubuntu-latest`
  with `sudo`, and attaches `DXBerry-Pi-<tag>-rpi234-arm64.img.xz` and its
  `.sha256` to the GitHub Release created for the tag, with the DietPi image
  name and checksum in the release notes.

## 13. Security and secrets

- The repository never contains a real `dxberry.txt`, `dietpi-wifi.txt`, or
  built image; `.gitignore` enforces it and `ci.yml` fails if a `dxberry.txt`
  is present in the tree.
- After application, `PASSWORD`, `WIFI_PASSWORD` and `WEBUI_PASSWORD` in
  `/boot/dxberry.txt` are replaced with `<applied>`. DietPi wipes its own
  password key. `dietpi-wifidb` moves WiFi credentials from `dietpi-wifi.txt`
  into DietPi's root-only database; the provisioner verifies the text file no
  longer holds the key and clears it otherwise.
- `/etc/wpa_supplicant/wpa_supplicant.conf`, `/var/lib/dxberry/`, and the
  provisioning log are root-only (0600 / 0700).
- SSH is OpenSSH with password login enabled (the user chose the password);
  `SSH_PUBKEY` installs a key for those who prefer it. Disabling password
  login is a later console option, not a first-boot default, so a user can
  never lock themselves out with a typo in a key.
- Graywolf's UI requires the administrator account created during seeding.
  Because it binds all interfaces, the account is created before the service
  is reachable by anyone else: seeding happens on the first boot, before the
  user is told the Pi is ready.
- No telemetry: DietPi survey opted out.

## 14. Testing and acceptance

Unit (in CI): `tests/run.sh` — plain bash, awk and jq, no bats. It sources
every `tests/test_*.sh` and runs every `test_*` function it finds, capturing
each test's stderr and printing it only on failure. Coverage includes valid
configs, each validation rule in §5, CRLF and UTF-8-BOM input, quoted values,
unknown keys, missing required keys and the `<applied>` skip rule; the
netwatch state machine and its failover sequences; the network module's
files, WiFi import, stray-stanza scan and pre-reboot gate; Graywolf install
and seeding against a fake `curl`; and scrub behaviour for consumed and
unconsumed secrets.

Acceptance on a Raspberry Pi 4, before tagging `v0.1.0`:

1. Flash the built image, fill `dxberry.txt` with a static address and WiFi,
   boot with Ethernet: the Pi answers on the static address with SSH and
   Graywolf's UI, seeded with callsign, iGate and beacon; `dxberry-status.txt`
   reports success; secrets in `dxberry.txt` read `<applied>`;
   `dietpi-wifi.txt` holds no key.
2. `sudo dxberry-provision` on the running system reports no changes.
3. Failover: `--simulate eth0-down` moves the address to wlan0 with the
   session recoverable at the same address; `--simulate eth0-up` moves it
   back; at no point do both interfaces hold the address (`ip -4 addr` checked
   at each step). Then the same with a physical cable pull and replug.
4. Ethernet-only config (no `WIFI_SSID`): boots to the static address; cable
   pull leaves `NONE`; replug restores `ETH`.
5. DHCP config (`STATIC_IP` blank): boots and is reachable at the leased
   address; failover works.
6. Invalid config (missing `PASSWORD`, bad CIDR): Pi boots on DHCP with DietPi
   defaults and `dxberry-ERROR.txt` names both problems.
7. Graywolf install failure: with `GRAYWOLF_VERSION=v0.0.1` (a release that
   does not exist) the Pi still reaches the static address and the status
   file reports the Graywolf failure; clearing the key and re-running the
   provisioner completes the install and seeding.

## 15. Hooks reserved for later sub-projects

- **Ports:** console `80`, Graywolf `8080`, Pat `8081`, noVNC `6080`,
  terminal `7681`, Cockpit `9090` (optional). Nothing in this sub-project
  binds 80, 8081, 6080, 7681 or 9090.
- **Config namespaces:** key prefixes listed in §5 are accepted and ignored.
- **Module convention:** `/opt/dxberry/lib/<concern>.sh` exposing
  `provision_<concern>`; later sub-projects add modules and extend the order
  in the driver.
- **State directory:** `/var/lib/dxberry/` for anything that must persist.
- **Release metadata:** `/etc/dxberry-release` for the console's "about" and
  update checks.
- **netwatch `NONE` state:** attachment point for hotspot fallback.

## 16. Verified assumptions and open risks

Verified against DietPi and Graywolf source before implementation:

- DietPi's "wait for network at boot" is a drop-in making
  `dietpi-postboot.service` `Wants=`/`After=network-online.target`; netwatch
  is `Before=network-online.target` and `WantedBy=` it, so the ordering holds
  with no `auto` interfaces.
- `dietpi-wifidb 1` *moves* `/boot/dietpi-wifi.txt` into the root-only
  `/var/lib/dietpi/dietpi-wifi.db` and writes `wpa_supplicant.conf` with mode
  0600; the text file is gone afterwards.
- DietPi's automated first run no longer reboots by itself after installs;
  the post-script runs and DXBerry-Pi performs the reboot.
- Graywolf: beacon `interval` is seconds (`every_seconds`, default 1800);
  `send_path` is `rf` | `both` | `is_only`; digipeater rules require a
  channel; the iGate request has no passcode field.
- DietPi's stock `/etc/network/interfaces` only sources `interfaces.d/*`;
  per-interface files are `interfaces.d/<iface>.conf`, DHCP client is
  `isc-dhcp-client`, and `resolvconf` is not installed.

Open, to be confirmed on the first flashed image:

1. `PUT /igate/config` with a GET-then-merge body: Graywolf must ignore the
   read-only fields the GET response carries. If it rejects them, the
   provisioner sends only the seeded fields on a fresh install.
2. DietPi's first-boot import of `dietpi.txt` from the FAT partition is
   mtime-based (`cp -u`). The build stamps the FAT copy older than the root
   copy, mirroring DietPi's own imager; an unmodified FAT copy must therefore
   not overwrite the built `dietpi.txt`.
3. Loop-device mounting on GitHub-hosted runners (works with `sudo` today;
   the release job is the only place it matters, and a local build is always
   available as a fallback).

### To verify on hardware (Task 12)

Everything below is implemented against documented behaviour but has never run
on a real Pi. Each item is a thing the implementation would get wrong silently
if the assumption is false.

1. ~~DietPi's `/etc/network/interfaces` only sources `interfaces.d/*`~~ —
   **false, confirmed on hardware (v0.1.0-rc1, 2026-09-08).** DietPi Trixie's
   automated first run leaves its own `allow-hotplug`/`iface` stanzas for
   eth0 and wlan0 in the MAIN file too, alongside `source interfaces.d/*`.
   `provision_network` now runs `dxb_net_clean_main_interfaces` to strip
   those stanzas from the main file before `dxb_net_scan_stray_stanzas` runs,
   so the scan (and the first-boot reboot gate) only ever sees stanzas from a
   genuinely foreign file under `interfaces.d/`.
2. `wlan0` exists after `dietpi-set_hardware wifimodules enable`, and
   `dietpi-wifidb 1` still imports `/boot/dietpi-wifi.txt` with WiFi disabled
   in `dietpi.txt` (`AUTO_SETUP_NET_WIFI_ENABLED=0`).
3. Calling `reboot` from `Automation_Custom_Script.sh` is safe: DietPi has
   finalized `.install_stage` by then, and the installer does not re-run on
   the next boot.
4. `Type=notify` plus `systemd-notify --ready` from a shell script is accepted
   on Trixie (the unit reaches `active (running)`, not a start timeout).
5. `build/build-image.sh` run as root end to end, including the DietPi
   `.sha256` file's format and the FAT timestamp behaviour the build relies on
   (`verify_fat_older`).
6. Graywolf accepts the `PUT /igate/config` merge body — the GET response's
   read-only fields sent back unchanged are ignored, not rejected.
7. Physical failover in all three network shapes — static, DHCP and
   Ethernet-only — with `ip -4 addr` checked at each step: cable pull, cable
   replug, and (Ethernet-only) that `NONE` recovers to `ETH`.
