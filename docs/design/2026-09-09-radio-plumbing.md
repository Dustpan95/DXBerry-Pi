# DXBerry-Pi — Radio Plumbing

Sub-project 2 of the DXBerry-Pi roadmap. Builds on the base image
(`docs/design/2026-09-07-base-image.md`, referred to below as "the base spec").
Section numbers in this document are its own.

## 1. Purpose

A ham plugs one or more radios into the Pi and gets, without editing a single
device path: stable device names that survive reboots and boot-order changes,
a hamlib `rigctld` per radio for CAT and PTT, a GPS feeding time and position
to the whole station, and a record of which application owns which radio.
Ownership can be switched with one command; the DXBerry Console (sub-project 3)
later wraps that command in a web form.

### Principles

- **Discovery first.** Radio hardware is never typed into `dxberry.txt`. The
  Pi discovers what is plugged in; the operator names it.
- **Identity is the USB port.** Common dongles (DigiRig, CM108 adapters) carry
  no unique serial number, so the only universal key is the physical port path.
  A serial number is recorded when present, as a cross-check, never as the key.
- **rigctld owns the serial port.** Exactly one process opens a radio's CAT
  port: its `rigctld` instance. Applications talk hamlib NET rigctl on
  localhost. Audio, CM108 HID, GPIO and tone PTT go straight from the owning
  application to the device because they never touch the serial port.
- **One owner per radio, many radios per Pi.** Every radio has zero or one
  owning application. An application may own several radios.
- **The CLI is the contract.** Everything the console will do is a
  `dxberry-radio` subcommand with `--json` output and stable exit codes. No
  screen-scraping, no second code path.
- **Same discipline as the base image.** Idempotent steps, atomic writes,
  `dxb_`-prefixed functions, bash 5 with awk and jq only, tests without
  hardware, no version pins, no attribution lines of any kind.

## 2. Scope

**In scope:**

- `dxberry-radio`: scan, add, set, remove, apply, claim, release, status,
  hotplug, gps.
- Radio record `/var/lib/dxberry/radios.json` and its runtime mirror.
- Generated udev rules (ALSA card ids, serial and HID symlinks, hotplug
  trigger) and ALSA index pinning for onboard audio.
- `rigctld@.service` template, one instance per radio.
- Application wiring modules with Graywolf as the first application.
- gpsd + chrony: GPS as time source, live position into Graywolf, fix readout.
- `provision_radio` step in `dxberry-provision`; new `dxberry.txt` keys
  `GPS_DEVICE`, `GPS_BAUD`, `GPS_PPS`.
- Two small carry-overs ledgered for v0.1.1: bare IPv4 accepted in
  `STATIC_IP` (default `/24`) with a README pointer to `dxberry-ERROR.txt`,
  and `dxberry-netwatch --status` reading the live address instead of a
  trailing state file.
- Release `v0.2.0` (pre-releases `v0.2.0-rcN` first).

**Out of scope (later sub-projects):**

- The web console itself (sub-project 3) and its proxying of rigctld.
- Pat, ARDOP, WSJT-X, JS8Call, fldigi, flrig (sub-project 4). This design
  defines the wiring interface they plug into.
- Rotator control (`rotctld`), amplifier control, audio sharing between
  applications, SDR devices.
- Bluetooth. Selecting the GPIO UART for a GPS disables the Pi's onboard
  Bluetooth (§8.1); nothing in the roadmap uses it.

## 3. User flow

1. The operator plugs a radio interface into the Pi and runs
   `sudo dxberry-radio scan`. A table lists each USB candidate: port,
   what it is (from the profile table), its audio and serial functions.
2. `sudo dxberry-radio add radio1 --audio 1 --cat 1 --model 2036 --label "TM-V71"`
   pins candidate 1's audio and serial functions as `radio1`. Defaults for
   PTT and baud come from the profile; flags override them.
3. `apply` runs automatically: udev rules are regenerated and reloaded,
   `hw:RADIO1` and `/dev/dxberry/radio1-cat` appear, `rigctld@radio1` starts
   on port 4532.
4. `sudo dxberry-radio claim radio1 graywolf` creates a Graywolf audio device
   and channel pointing at those names, with PTT through rigctld, and
   Graywolf starts decoding. `status` shows owner, presence, frequency and
   mode.
5. Later, `claim radio1 wsjtx` (sub-project 4) removes the Graywolf channel,
   stops Graywolf if it owns nothing else, and starts WSJT-X against the same
   names. `release radio1` leaves the radio idle with rigctld still running.
6. Pulling the dongle stops its rigctld and marks the radio absent; plugging
   it back into the same port restores everything. Plugging it into a
   different port shows an unknown candidate in `scan` and `radio1` absent;
   `set radio1 --audio N --cat N` re-pins it.
7. A USB GPS is picked up by gpsd on plug-in. chrony trusts it when the
   internet is absent, Graywolf beacons the live position, and
   `dxberry-radio gps` prints fix, satellites and grid square.

## 4. Repository layout (additions)

```
provision/
├── bin/
│   └── dxberry-radio                  # CLI driver (argument parsing, dispatch, exit codes)
├── lib/
│   ├── radio.sh                       # record load/save/validate, scan, apply, hand-over core
│   ├── radio_udev.sh                  # udev rule + modprobe generation from the record
│   ├── rigctld.sh                     # rigctld@ env files, instance start/stop, freq/mode query
│   ├── gps.sh                         # gpsd/chrony config, fix readout, grid square
│   ├── apps/
│   │   └── graywolf.sh                # app_graywolf_* wiring functions
│   └── graywolf.sh                    # unchanged installer/seeder; gains dxb_gw_seed_gps
├── share/
│   └── radio-profiles.tsv             # vendor:product → profile defaults (data, not code)
└── templates/
    ├── rigctld@.service
    ├── dxberry-radio-hotplug.service
    ├── 70-dxberry-radio.rules.head    # static part of the generated rules file
    ├── dxberry-audio.conf             # /etc/modprobe.d slot pinning
    └── chrony-dxberry.conf            # /etc/chrony/conf.d drop-in
tests/
├── fixtures/sysfs/                    # fake /sys trees: digirig, ic7300, split, two-digirigs, none
├── test_radio.sh
├── test_radio_udev.sh
├── test_rigctld.sh
├── test_gps.sh
└── test_app_graywolf.sh
```

The `provision/lib/apps/` directory is the extension point: sub-project 4 adds
`pat.sh`, `wsjtx.sh`, etc. The core discovers applications by listing that
directory; nothing in `radio.sh` names an application.

## 5. Discovery and identity

### 5.1 Candidates

`dxb_radio_scan` walks `$DXB_SYSFS_ROOT` (default `/sys`) for:

- USB sound cards: `/sys/class/sound/card*` whose device chain contains a USB
  interface.
- USB serial ports: `/sys/class/tty/ttyUSB*` and `ttyACM*`.
- CM108-class HID nodes: `/sys/class/hidraw/hidraw*` whose parent USB device
  is also a sound card (same `idVendor:idProduct` and port).

For each function it reads from sysfs: the USB port path (the `devpath`
chain rendered the way udev's `ID_PATH` does, e.g. `usb-0:1.3:1.0`),
`idVendor`, `idProduct`, `product`, `serial` (may be empty), and the kernel
name (`card1`, `ttyUSB0`, `hidraw2`). Nothing is read from `udevadm`: the
scanner derives the path itself, so it works the same on a fixture tree.

Functions are grouped into candidates by their USB *device* port (the part
before the interface number). A DigiRig Mobile appears as two candidates: its
CM108 codec and its CP2102 CAT/PTT port are sibling USB devices behind the
DigiRig's internal hub, not functions of one device, so the operator pins them
separately (`--audio N --cat M`). An IC-7300 is one candidate with one audio
and two serial functions. A SignaLink plus a separate CAT cable are two
candidates.

Onboard audio (`vc4-hdmi*`, `bcm2835*`, anything not under a USB device) is
never a candidate.

### 5.2 Profile table

`provision/share/radio-profiles.tsv` is tab-separated, `#` comments allowed:

```
# vid:pid	name	ptt	ptt_type	cat	model	baud	notes
0d8c:013c	DigiRig Mobile	rigctld	RTS	separate	1	57600	CM108 codec; the CP2102 CAT/PTT port is a sibling USB device. Source: digirig.net product page and Digirig support forum lsusb reports (0d8c:013c, C-Media Electronics)
0d8c:0012	DigiRig Lite	digirig_tone	NONE	none	1	0	tone keyed on the right channel; CM108B codec, same C-Media vendor id as the Mobile. Source: digirig.net Digirig Lite page
1209:7388	AIOC	cm108	NONE	none	1	0	all-in-one cable, HID PTT. Source: pid.codes registry https://pid.codes/1209/7388/ (skuep/AIOC project)
08bb:29b6	SignaLink USB	vox	NONE	none	1	0	TI PCM2906C codec. Source: SignaLink USB support docs and reported lsusb output identifying the device as a Texas Instruments PCM2906C
0c26:0036	Icom IC-705	rigctld	RIG	same	3085	115200	IC-705 CI-V/audio USB interface (Prolific-chipset bridge under Icom's registered 0c26 vendor id). Source: Raspberry Pi forum thread lsusb capture ("ID 0c26:0036 Prolific Technology Inc. IC-705") and DeviceHunt vendor 0C26 listing; hamlib model 3085 = RIG_MODEL_IC705 per Hamlib's supported-radios list
10c4:ea60	CP2102 serial	rigctld	RTS	same	1	57600	serial-only candidate (CAT cable or DigiRig port); Silicon Labs factory-default CP210x id. Source: linux kernel cp210x driver USB_DEVICE table and usb-ids.gowdy.us/read/UD/10c4/ea60
0403:6001	FTDI serial	rigctld	RIG	same	1	38400	serial-only candidate; FTDI factory-default FT232R id. Source: FTDI Technical Note TN_100 (USB VID/PID Guidelines)
```

Columns: `ptt` is the default PTT method (§6.1); `ptt_type` is what rigctld
keys with (`RIG` = CAT command, `RTS`/`DTR` = serial line, `NONE`); `cat` is
`same` (the CAT serial port is on this candidate), `none`, or `separate`
(expect a second candidate); `model` is the hamlib model number (1 = dummy);
`baud` 0 means "not applicable". Entries are matched on `vid:pid` of the audio function
first, then the serial function. The table above matches
`provision/share/radio-profiles.tsv` as committed; every id was verified
against the vendor's documentation or `lsusb` output before committing it
(sources noted in the file). Unknown hardware gets profile `generic`
(`ptt=rigctld` when a serial port is present, else `vox`; `model=1`).

### 5.3 Radio names

`radio1`…`radio99` or any name matching `^[a-z][a-z0-9]{0,11}$`. The name
becomes the ALSA card id in upper case (`RADIO1`), the symlink prefix
(`/dev/dxberry/radio1-*`), and the systemd instance (`rigctld@radio1`).

## 6. The record and the CLI

### 6.1 `/var/lib/dxberry/radios.json`

Root-owned, mode 0600, written via `dxb_write_if_changed` after validation.

```json
{
  "version": 1,
  "radios": {
    "radio1": {
      "label": "Kenwood TM-V71",
      "profile": "0d8c:013c",
      "audio": {"path": "usb-0:1.3:1.0", "vidpid": "0d8c:013c", "serial": ""},
      "cat":   {"path": "usb-0:1.3:1.1", "vidpid": "10c4:ea60", "serial": "0001"},
      "hid":   null,
      "ptt_serial": null,
      "ptt":   {"method": "rigctld", "gpio_line": null},
      "rig":   {"model": 1, "baud": 57600, "ptt_type": "RTS"},
      "rigctld_port": 4532,
      "wiring": "full",
      "owner": ""
    }
  },
  "gps": {"device": "auto", "baud": 9600, "pps": ""}
}
```

- `audio`/`cat`/`hid`/`ptt_serial` are function pins: `path` is the USB
  interface port path; `null` when the radio has no such function.
  `ptt_serial` pins a second serial port for PTT when it is not the CAT port.
- `ptt.method` ∈ `rigctld`, `cm108`, `gpio`, `vox`, `digirig_tone`, `none`.
  `serial_rts`/`serial_dtr` are deliberately absent: serial PTT is rigctld's
  job (`rig.ptt_type` ∈ `RIG`, `RTS`, `DTR`, `NONE`).
- `rigctld_port` is allocated on `add` as the lowest free even port from 4532.
- `wiring` ∈ `full` (the application is configured to use the radio's
  devices on `claim`) or `names` (only stable names and rigctld are provided;
  the operator configures the application by hand).
- `owner` is an application name from `provision/lib/apps/` or empty.
- The `gps` block mirrors the `dxberry.txt` keys; `set --gps-device` can
  change it after first boot.

`/run/dxberry/radios-state.json` is the runtime mirror written by `apply` and
`hotplug`: per radio `present` (all pinned functions found), `rigctld`
(`active`/`inactive`/`failed`), the kernel names resolved this boot,
`alsa_id` (the id the audio card answers to right now, §7.1), `wire_hash`
(the hash of the wiring inputs in the record) and `wired_hash` (the hash the
owner was last wired with — when the two differ, `apply` re-wires the owner).
It is what `status` and the console read; it never holds configuration.

### 6.2 Subcommands

All require root (`dxb_require_root`). All accept `--json`, anywhere in the
argument list. `scan`, `status`, `gps` and, in JSON mode, `add`/`set`/`claim`
(which print the radio's status block) answer with their own object; the
four that otherwise print nothing on success — `apply`, `hotplug`, `release`,
`remove` — answer `{"ok":true}`, so a caller never has to read an empty
stdout as success. Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 2 | usage error |
| 3 | no such radio / application |
| 4 | device absent (pin not found in sysfs) |
| 5 | `claim` failed to start or wire the new owner; radio left released |
| 6 | apply failed (udev reload, unit install, or write error) |
| 7 | re-wiring the existing owner failed during `set` or `apply` (owner unchanged) |

- `scan` — candidates with a numeric index, port, profile name, functions.
- `add NAME --audio N|none --cat N[:K]|none [--hid N] [--ptt-serial N[:K]]
  [--ptt M] [--model K] [--baud B] [--ptt-type T] [--gpio-line N]
  [--wiring full|names] [--label S]` — validates, fills defaults from the
  profile, allocates the port, saves, runs `apply`. `N` is a `scan` index and
  `:K` picks the Kth function of that kind on the candidate (an IC-705's
  second serial port is `--cat 1:2`).
- `set NAME [same flags]` — changes fields; a pin change keeps the owner and
  re-wires it.
- `remove NAME` — releases first (stops the owner's use of it), deletes the
  record, runs `apply`.
- `apply` — regenerates derived state (§7), starts or stops
  `rigctld@` instances by presence, refreshes the runtime mirror, and
  re-runs the current owner's wiring for present radios when the wiring
  inputs changed. The `hotplug` subcommand is the same with quieter logging
  and no udev reload (udev is what called it).
- `claim NAME APP` — hand-over (§9).
- `release NAME` — unwire, stop the application if it owns nothing else,
  clear `owner`.
- `status [NAME]` — record + runtime + for present radios with rigctld
  active: frequency and mode from `rigctl -m 2 -r 127.0.0.1:PORT f m`
  (1 s timeout; failures show `?`), and the ALSA id the audio card carries
  right now (§7.1).
- `hotplug` — alias of `apply --hotplug`, the udev entry point.
- `gps` — §8.3.

Logging: `/var/lib/dxberry/radio.log` via the shared `dxb_log`, plus
journal output when invoked by systemd.

## 7. Derived state: udev, ALSA, rigctld

### 7.1 Generated udev rules

`apply` renders `/etc/udev/rules.d/70-dxberry-radio.rules` from the template
head (comment: generated, do not edit) plus, per radio:

```
# radio1 — Kenwood TM-V71
SUBSYSTEM=="sound", KERNEL=="card*", ENV{ID_PATH}=="*-usb-0:1.3:1.0", ATTR{id}="RADIO1", TAG+="dxberry-radio"
SUBSYSTEM=="tty", ENV{ID_PATH}=="*-usb-0:1.3:1.1", SYMLINK+="dxberry/radio1-cat", TAG+="dxberry-radio"
SUBSYSTEM=="hidraw", ENV{ID_PATH}=="*-usb-0:1.3:1.3", SYMLINK+="dxberry/radio1-hid", TAG+="dxberry-radio"
```

and two trailer rules:

```
TAG=="dxberry-radio", ACTION=="add|remove", RUN+="/bin/systemctl --no-block start dxberry-radio-hotplug.service"
ACTION=="remove", SUBSYSTEM=="sound|tty|hidraw", RUN+="/bin/systemctl --no-block start dxberry-radio-hotplug.service"
```

The second is a fallback: on a remove event udev replays the tags it stored
in its database, which is exactly the behaviour that cannot be checked
without hardware, and a missed remove would leave a `rigctld` instance
running against a device that is gone. Catching every remove in the three
subsystems costs nothing, because `apply hotplug` is idempotent.

`ID_PATH` is matched with a leading wildcard because its prefix names the
host controller (`platform-fd500000.pcie-pci-0000:01:00.0-`).
A separate `-ptt` symlink is generated whenever a `ptt_serial` pin is set,
which is that pin's only purpose: a radio whose PTT serial line is a
different device from its CAT port (rare).

After writing, `apply` runs `udevadm control --reload` and
`udevadm trigger --subsystem-match=sound --subsystem-match=tty
--subsystem-match=hidraw --action=add` so names appear without a reboot.
The ALSA id rename applies at card registration, so an already-registered
card keeps its old id until replug or reboot; `apply` reports this as
"radio1: audio id takes effect on replug or reboot" and `status` shows the
current id.

### 7.2 Onboard audio pinning

`/etc/modprobe.d/dxberry-audio.conf` installed once by `provision_radio`:

```
options snd slots=snd_usb_audio,snd_usb_audio,snd_usb_audio,snd_usb_audio
```

USB cards take indexes 0–3; `vc4-hdmi` and `bcm2835` land after them.
Applications never address cards by index anyway; this keeps `aplay -l`
readable and avoids Graywolf's detector listing HDMI first.

### 7.3 rigctld instances

`provision/templates/rigctld@.service`:

```
[Unit]
Description=Hamlib rigctld for radio %i (DXBerry-Pi)
Documentation=file:///opt/dxberry/bin/dxberry-radio
After=dxberry-radio-hotplug.service
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
EnvironmentFile=/run/dxberry/rigctld/%i.env
ExecStart=/usr/bin/rigctld -m $MODEL -T 127.0.0.1 -t $PORT $RIG_ARGS $PTT_ARGS
Restart=on-failure
RestartSec=5
```

`apply` writes `/run/dxberry/rigctld/radio1.env` from the record:
`MODEL=1`, `PORT=4532`, `RIG_ARGS=-r /dev/dxberry/radio1-cat -s 57600`
(empty when there is no CAT function), `PTT_ARGS=-P RTS -p
/dev/dxberry/radio1-cat` (or `-P RIG` for CAT PTT, empty for NONE).

The variables are unbraced and the env values unquoted on purpose: systemd
splits an unbraced `$RIG_ARGS` into separate arguments, while `${RIG_ARGS}`
would hand rigctld the whole string as one argument, and an unquoted empty
value drops off the command line entirely — which is what a radio with no
CAT port or no PTT needs.

`apply` then starts the instance when the radio is present and stops it when
absent. Instances are never `enable`d: `dxberry-radio-hotplug.service` (a oneshot,
`WantedBy=multi-user.target`, `After=local-fs.target systemd-udevd.service`)
runs `apply` once at boot, which starts the instances for radios already
present, and udev re-runs it on every later add or remove.

A radio with no CAT function runs the dummy model so PTT and the NET rigctl
interface stay uniform for every application.

## 8. GPS and time

### 8.1 Configuration

New `dxberry.txt` keys (advanced seeds, blank = default):

| Key | Default | Notes |
|---|---|---|
| `GPS_DEVICE` | `auto` | `auto` = USB GPS via gpsd hotplug only; `none` = GPS off; `uart` = GPIO pins 8/10 (`/dev/ttyAMA0`), which requires `SERIAL_CONSOLE=off` and disables onboard Bluetooth (`dtoverlay=disable-bt`); or an explicit `/dev/tty…` path |
| `GPS_BAUD` | `9600` | used only for `uart` and explicit paths; USB receivers are auto-detected |
| `GPS_PPS` | blank | BCM GPIO number carrying a PPS pulse; adds `dtoverlay=pps-gpio,gpiopin=N` and `/dev/pps0` to gpsd |

`dxberry-preboot` writes the `config.txt` changes for `uart` and `GPS_PPS`
(both need a reboot, which first boot already does). They are appended under
an `[all]` header whenever the file's last section header is a
model-specific one, so a line can never end up scoped to (say) `[cm4]`.
`CONFIG_NTP_MODE=0`, DietPi's hand-off of `systemd-timesyncd`, is written
into `dietpi.txt` by `provision_radio` instead — right after chrony is
enabled and timesyncd masked. Written in preboot it would take effect before
DietPi's own first run, leaving a Pi with no RTC on a stale clock through
DietPi's apt run and the Graywolf TLS download. `GPS_DEVICE=uart` with `SERIAL_CONSOLE=on` is a
validation error.

### 8.2 Services

`provision_radio` installs `gpsd`, `gpsd-clients`, `chrony`,
`libhamlib-utils`, `alsa-utils` (never pinned), disables and masks
`systemd-timesyncd`, and configures:

- `/etc/default/gpsd`: `USBAUTO="true"`, `DEVICES` = the uart/explicit path
  plus `/dev/pps0` when set, `GPSD_OPTIONS="-n"` — `-n -s GPS_BAUD` when
  `DEVICES` names a receiver, since `GPS_BAUD` only applies to a port
  DXBerry names itself. gpsd's own
  `60-gpsd.rules` handles USB hotplug; DXBerry adds no GPS udev rules.
- `/etc/chrony/conf.d/dxberry.conf`:

  ```
  refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2 noselect
  refclock SHM 1 refid PPS precision 1e-7 prefer
  ```

  `noselect` is dropped from SHM 0 when no PPS is configured, so NMEA time
  is selectable on its own (accuracy tens of milliseconds, enough for WSJT-X).
  Debian's stock `chrony.conf` keeps its NTP pool, so with internet the pool
  and GPS are both sources and chrony picks by quality; without internet GPS
  alone keeps the clock. SHM is used instead of SOCK because SOCK paths embed
  the device name, which changes with USB hotplug.
- Graywolf: `dxb_gw_seed_gps` PUTs `/gps` with `{source: "gpsd", gpsd_host:
  "localhost", gpsd_port: 2947}` (Graywolf derives `enabled` from `source`
  and rejects unknown fields) when `GPS_DEVICE` is not
  `none`, otherwise the existing fixed-position seed (base spec §9.2)
  stands. Same seed-state rules as the other Graywolf seeds: written once,
  re-applied only by `--reseed`.

### 8.3 Readout

`dxberry-radio gps [--json]` reads the `DEVICES` report gpsd sends on
connect plus one `TPV` and one `SKY` report through `gpspipe -w -n 20`
(3 s timeout). The JSON carries fix mode, `receiver`, latitude, longitude,
altitude in feet, speed in mph, satellites used/seen, UTC time, and the
6-character Maidenhead grid computed in awk. The text form is the state line
`status` and the status file use:

- `no receiver` — gpsd is unreachable, or it answers with an empty `DEVICES`
  list (nothing plugged in, or the uart/explicit path is not there);
- `no fix` — a receiver is present but has not reached mode 2 yet;
- `3D fix DM97hd (37.145833, -101.375), 8/12 satellites`.

With no gpsd, no receiver or no fix it still exits 0; the JSON then carries
`"fix": 0` with `"receiver": false` or `true`.

## 9. Ownership and application wiring

### 9.1 Application modules

`provision/lib/apps/<app>.sh` defines:

| Function | Contract |
|---|---|
| `app_<app>_unit` | prints the systemd unit name |
| `app_<app>_wire RADIO` | configure the application to use RADIO's devices; idempotent; returns 0/7 |
| `app_<app>_unwire RADIO` | remove that configuration so the application no longer opens the devices; idempotent |
| `app_<app>_needs_service_restart` | prints `yes` when wire/unwire only take effect after a restart (Graywolf prints `no`: its API applies live) |
| `app_<app>_wait_ready` | optional; blocks until the application accepts configuration (Graywolf: its API answers); failure makes `claim` return 5 |

Applications with `wiring: names` skip `wire`/`unwire`; the core still
starts and stops the unit.

Every module is sourced into the same shell as the core, and a single run may
source several, so a module's private helpers must carry its own prefix
(`dxb_gwapp_*` for Graywolf): an unprefixed helper silently replaces the
same-named helper of another module.

### 9.2 Hand-over (`claim NAME APP`)

1. Validate: radio exists, app module exists, radio present (else exit 4).
2. If the current owner is APP: re-run `wire` and exit 0.
3. If another app owns it: `unwire` that app for this radio; if that app now
   owns no radio, `systemctl stop` its unit and wait for it to exit.
4. Set `owner` = APP and save the record.
5. If APP's unit is inactive: `systemctl start` it and wait for it to be
   ready (Graywolf: `dxb_gw_wait_ready`).
6. `wire` APP for this radio. If start or wire fails: `unwire`, clear
   `owner`, save, stop the unit if it owns nothing else, exit 5.

rigctld is never touched by a hand-over; it belongs to the radio.
`release NAME` performs steps 3 and 4 with an empty owner.

### 9.3 Graywolf wiring

`app_graywolf_wire radio1` with `wiring: full`, all through the existing
`dxb_gw_api` client after `dxb_gw_login`:

1. Audio device named `radio1`: `GET /audio-devices`, find by `name`; `POST`
   or `PUT` `{name: "radio1", source_type: "soundcard", source_path:
   "plughw:CARD=RADIO1,DEV=0", sample_rate: 48000}`.
2. Channel named `radio1`: find by `name`; `POST` or `PUT` with
   `input_device_id`/`output_device_id` = that device, `modem_type`/`profile`
   left at Graywolf's defaults on create (never overwritten on update, so
   operator tuning survives), `input_channel`/`output_channel` 0.
3. PTT for that channel through the PTT resource (`GET /ptt/{channel}`,
   then `PUT /ptt/{channel}` or `POST /ptt` to create), body
   `{channel_id, method, device_path, gpio_line, gpio_pin, invert, persist}`
   according to `ptt.method`: `rigctld` → `device_path:
   "127.0.0.1:4532"` (Graywolf parses the rigctld target as a `host:port`
   string in `device_path`); `cm108` → `device_path:
   /dev/dxberry/radio1-hid`, `gpio_pin: 3`; `gpio` → `device_path:
   /dev/gpiochip0`, `gpio_line`; `vox`/`digirig_tone`/`none` → `method`
   only. Timing fields (`dwait_ms`, `slot_time_ms`) are never sent on update
   so operator tuning survives.
4. `POST /ptt/test-rigctld {host, port}` is used once after wiring to log
   whether Graywolf can reach the instance; a failure is a warning, not a
   failed wire, because rigctld may be mid-restart.

`app_graywolf_unwire radio1` deletes the channel (`DELETE
/channels/{id}?cascade=true`) and the audio device (`DELETE
/audio-devices/{id}`) by name, so the running Graywolf releases the ALSA
device immediately. Beacons and iGate settings are untouched; a beacon bound
to the deleted channel is re-bound by Graywolf's cascade rules, which the
implementation task checks and records.

Credentials: the Graywolf admin username and password are kept root-only in
`/var/lib/dxberry/graywolf.secret` (0600), written by the first-boot seed and
by any later successful login, so `dxberry-radio claim` and the console can
log in after `dxberry.txt` has been scrubbed. `dxb_gw_login_any` tries that
file, then `WEBUI_PASSWORD` from `dxberry.txt`, then a terminal prompt.

## 10. Provisioning integration

`provision_radio` is added to `dxberry-provision` after `provision_graywolf`
and before `provision_scrub`. It:

1. Installs the packages (§8.2) — a failed install is a failed step named
   `radio`; the rest continues.
2. Installs the templates: `rigctld@.service`, `dxberry-radio-hotplug.service`
   (enabled), `dxberry-audio.conf`, the chrony drop-in, `/etc/default/gpsd`,
   and creates `/run/dxberry/rigctld` via a `tmpfiles.d` entry.
3. Disables and masks `systemd-timesyncd`; enables `chrony` and
   `gpsd.socket`; writes `CONFIG_NTP_MODE=0` into `dietpi.txt` (§8.1).
4. Runs `dxberry-radio apply` (no radios on first boot: this installs the
   rules head and the empty runtime mirror).
5. Nothing for Graywolf: the GPS source is seeded by `dxb_gw_seed`
   (`provision_graywolf`, §8.2) under the same seed-state rules as the other
   seeds — written once per box, re-applied only by `--reseed`.
6. Status lines: `radio: N radios, M present`; `gps: <GPS_DEVICE policy>,
   <state>` with the three states of §8.3 (`gps: off (GPS_DEVICE=none)` when
   GPS is off); `time: chrony (gps <present|absent>)`, where present means
   the fix mode read in the same call was 2 or 3 — the line is derived from
   the fix, never by matching the text of the gps line.

`--check` validates the new keys. `--reseed` re-applies the GPS seed.
`boot/dietpi.overrides.txt` adds the new packages to
`AUTO_SETUP_APT_INSTALLS` so a first boot with internet already has them;
`provision_radio` still installs them itself because the base image found
that list unreliable (`jq` was missing on rc1).

The image build (`build/build-image.sh`) copies `provision/share` alongside
`bin`, `lib`, `templates`; `--check` verifies it.

## 11. Carry-overs from v0.1.0

- `STATIC_IP` accepts a bare IPv4 and defaults the prefix to `/24`;
  `dxberry.txt.example` and `README-DXBERRY.txt` say so and point at
  `dxberry-ERROR.txt` as the first place to look when the Pi does not come up
  at the expected address.
- `dxberry-netwatch --status` reports the interface that actually holds the
  address (from `ip -o -4 addr`) alongside the state file, so the two never
  disagree for longer than the read itself.

## 12. Verified assumptions and open risks

Verified while writing this design (sources in the SDD ledger):

1. Graywolf's REST API (v0.14.13) has `/audio-devices` (`GET /available` is
   the detector), `/channels` with `input_device_id`/`output_device_id`, and
   PTT methods `none`, `vox`, `digirig_tone`, `serial_rts`, `serial_dtr`,
   `gpio`, `cm108`, `rigctld`. Verified in the Graywolf source: PTT config
   is its own resource (`/api/ptt`, `/api/ptt/{channel}` GET/PUT/DELETE,
   `POST /api/ptt/test-rigctld {host, port}`); for the `rigctld` method the
   target is the `device_path` string parsed as `host:port` by the modem
   (`graywolf-modem/src/tx/ptt.rs`), and keying sends hamlib's `T 1`/`T 0`
   over TCP and checks `RPRT 0`. The Go API does not validate the
   `host:port` format; a bad value only fails when the channel keys, so the
   wiring module validates it itself.
2. Graywolf's `/gps` accepts `source: gpsd` with `gpsd_host`/`gpsd_port`
   (field verified on hardware 2026-09-10 against 0.14.13: the request struct
   is `source`, not `source_type`, and has no `enabled`), and its handbook
   recommends gpsd on the Pi.
3. Trixie ships `libhamlib-utils` 4.6.2 (`rigctld` supports `-T`, `-t`,
   `-P`, `-p`), `gpsd` 3.25 with `60-gpsd.rules`, and `chrony` with SHM and
   SOCK refclocks. DietPi's `CONFIG_NTP_MODE` only ever drives
   `systemd-timesyncd`; mode 0 is the documented hand-off to another daemon.
4. A udev rule can set `ATTR{id}` on a sound card at add time, which renames
   `hw:NAME`. DigiRig's codec (`0d8c:013c`) has no serial number; its CP2102
   often reports `0001`. Port path is therefore the identity key.

To verify on hardware (acceptance task):

5. `ATTR{id}` rename and `snd slots=` ordering on the Trixie kernel.
6. Verify the `*-usb-…` wildcard matches on the Pi 4.
7. rigctld dummy-model PTT via RTS on a DigiRig keys a real radio.
8. Graywolf decodes through `plughw:CARD=RADIO1,DEV=0` and keys through the
   rigctld PTT method with acceptable latency.
9. Hand-over `claim`/`release` leaves no process holding the ALSA device
   (`fuser /dev/snd/*`).
10. USB GPS hotplug → gpsd → `chronyc sources` must list GPS as reachable
    (not merely present in the config; see the SHM risk below);
    `dxberry-radio gps` shows a fix; Graywolf beacons the live position.
11. Unplug/replug of a radio restores names and rigctld without a reboot.
12. DigiRig internal-hub topology as seen on the Pi 4 (§5.1: codec and
    CP2102 as two sibling USB devices, not two functions of one).
13. `TAG==` matches on remove events (udev restores the tags from its
    database); the fallback rule of §7.1 covers the removal either way, so
    check that a replug does not run the apply twice for one event.

Named risk — chrony may not be able to read gpsd's SHM refclocks. gpsd
creates SHM units 0 and 1 for the user it runs as (root on Debian), while
chrony's daemon runs as `_chrony`, so the drop-in of §8.2 can end up pointing
at segments chrony cannot open — with no error beyond GPS never appearing in
`chronyc sources`. The two mitigations, in order of preference: point the
drop-in at gpsd's units 2 and 3, which gpsd creates world-readable for
exactly this case (`refclock SHM 2` / `SHM 3`), or run chrony as root
(`-u root`). Neither is applied pre-emptively — the acceptance run (item 10)
is what decides, and whichever it needs is a one-line change to the chrony
template.

## 13. Testing

- Unit tests, no hardware, `tests/run.sh` only: the scanner runs against
  fixture sysfs trees under `tests/fixtures/sysfs/` (DigiRig, IC-7300, split
  SignaLink + CAT cable, two DigiRigs on different ports, nothing plugged
  in). `udevadm`, `systemctl`, `rigctl`, `gpspipe`, `apt-get` and the Graywolf
  API (`DXB_CURL` stub returning canned JSON, as `test_graywolf.sh` does
  today) are stubbed. Each subcommand has tests for its success path, every
  exit code it can return, and idempotency (second run changes nothing).
  Generated udev rules and env files are compared byte for byte against
  expected files.
- Hand-over tests drive two fake application modules under a test
  `DXB_APPS_DIR` to prove the stop-if-owns-nothing-else and failure-rollback
  rules without Graywolf.
- Hardware acceptance (items 5–11) on the Pi 4 with whatever interface the
  operator can plug in, recorded in the SDD ledger like the base image.

## 14. Hooks reserved for the console (sub-project 3)

- `dxberry-radio … --json` and the exit-code table are the API surface.
- `/run/dxberry/radios-state.json` is the cheap read for a status page.
- `rigctld` instances bind to `127.0.0.1`; the console proxies if remote
  hamlib access is ever offered.
- `provision/lib/apps/` is where sub-project 4 adds applications; `claim`
  needs no change for them.
