# Rig process to current USB device binding

Status: a development-only, read-only Linux metadata reader is implemented.
It is not constructed by the daemon and does not enable preset preview or
recall. Nothing from this work is installed on the Pi.

## Supported comparison

`rig.NewDeviceReader` is lazy and fixes production roots to `/dev` and `/sys`.
`Read(ctx, serviceObservation, rigCommand)` requires root, a successful service
argument comparison and a declared native CAT port. Dummy/default-device,
flrig/proxy and active GPIO PTT routes remain unavailable. This reader supports
the CAT pin and any serial-PTT or CM108 HID pin used by the validated command.
Every used pin must have known USB ID and serial metadata; a known empty serial
is supported, while absent/null registration identity fields stay unknown.
The caller must derive the command from fresh trusted registration and module
classification and retain the registration's identity checks. This reader does
not read the registration file or determine program ownership on its own.

The reader performs metadata operations only. It never opens a character
device, duplicates its descriptor, issues ioctl, exchanges radio data or changes
a unit, device, GPIO line or USB setting. The service reader's metadata queries
remain read-only. There is no device discovery, registration or repair fallback.

## Evidence read on each call

1. Reinspect the exact service/peer identity and require it to match the supplied
   successful service observation.
2. Resolve each root-owned `/dev/dxberry/<radio>-<role>` symlink to one direct
   `ttyUSB*`, `ttyACM*` or `hidraw*` node. Only the normal single-parent relative
   target or canonical absolute `/dev` target is supported. Node ownership,
   character type, link count and permissions must validate; world-writable
   nodes are unavailable, while ordinary group access is retained.
3. Follow the corresponding root-owned `/sys/dev/char/<major>:<minor>` link
   into a canonical directory below `/sys/devices`, with no symlink ancestors.
   The kernel node name and `dev` attribute must agree. USB interface ancestry,
   vendor/product IDs and serial must match the declared pin. A missing kernel
   serial attribute is a known empty serial; other errors are unavailable.
4. Require the expected process to hold a descriptor with the same filesystem
   device, inode, character-device number, ownership, mode, link count and
   change time as the current node. Its procfs descriptor target must match the
   current canonical node path. Descriptor metadata must agree on inode and
   access flags: CAT requires read/write; PTT requires write access. Path-only
   descriptors and read-only access are refused.
5. Reinspect the service, repeat device/descriptor evidence and require both
   samples to agree. Process identity is checked around each device pass, and
   the second device pass follows the last service query so changes during
   that query are not hidden. Connection identity and cancellation are checked
   again before returning.

Filesystem traversal uses verified directory descriptors and fixed roots.
Work is bounded to 1,024 process descriptors, 4 KiB descriptor/link metadata
and small named sysfs attributes. The overall context is limited to two
seconds. Local kernel filesystem operations are not hard preemptible.
Missing, changed, malformed or unsupported evidence returns no observation;
an earlier success is never substituted.

`DeviceObservation` is private immutable data and serializes as `{}`.
`Same` compares two successful observations without re-reading live state or
extending a token. These are sequential samples, not continuous ownership of
one kernel file description or an atomic hardware-operation boundary. Matching
USB metadata also cannot identify an RF radio swapped behind an unchanged USB
interface or exclude other processes opening the device. Authoritative
program-free evidence, the production target source,
flrig/GPIO support and actual service/device/RF acceptance remain open.

## Software evidence

Docker Go vet and the full race suite passed. Ten focused race runs cover
synthetic CAT, separate serial PTT and CM108 HID bindings, known-empty versus
unknown serials, identity mismatches, missing descriptors, inaccessible or
untrusted inputs, stale-result refusal and changes during the last service
query. A malformed sysfs-link regression first reproduced a partial result
being returned without an error; that path and incomplete-result handling
are now closed. Descriptor metadata fuzzing passed 264,356 cases.

The environment forbids `mknod`. Whole-reader fixtures therefore use root-owned
regular files and a private synthetic stat function for device metadata,
alongside synthetic unit, procfs and sysfs data. Production construction always
uses kernel stat and exposes no such override. A separate real virtual-terminal
test exercises actual procfs descriptor matching, rejects a different inode
with the same device number and refuses a closed descriptor. That kernel test
also passed as UID 65534 with a read-only root and no capabilities; root-only
whole-reader positives are skipped in that run. No privileges were added.

Native and ARM64 builds and ARM64 test compilation passed in Docker; ARM64 was
not executed. Evidence is retained at `/workspace/dxberry-device-binding-tests/`.
These are software/kernel fixtures, not a live systemd, USB unplug/replug,
electrical PTT or RF acceptance result. Staticcheck remains unavailable.
