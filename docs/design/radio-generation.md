# Radio mutation generation guards

Status: development source only, not installed on the Pi. This extends the
[generation store, helper and activation condition](program-generation.md).
Production preset verification and radio-write permissions remain disconnected.

## Mutation boundaries

`dxb_radio_save` validates and canonicalizes the proposed record, then requires
a successful generation advance before publishing any bytes or changing its
in-memory record. This covers add/set/remove and the private owner setter,
including a change from one owner and back with no program activation. Even an
identical save conservatively advances the global generation.

Claim and nonempty release also advance before loading/controlling the old or
new owner: those handovers can stop or unwire a program before reaching the
shared save function. The publication guard remains active too. There is no
process-global flag that skips later advances, so compensating writes and a
change-and-return continue to invalidate older receipts. An already unowned
release remains a no-op.

The helper receives `--state "$DXB_STATE_DIR/station" advance`. The trusted
operator/test override is `DXB_PROGRAM_STATE_CMD`; the default is the fixed
installed helper. Browser JSON cannot select it. Success requires both exit 0
and the exact canonical `{"ok":true,"protocol":1}` response. Missing helpers,
uncertain exits, failed/malformed responses and unexpected fields refuse the
mutation with exit 6. Raw helper output is not printed or logged. Normal
boot/provisioning must already supply the trusted station-state parent; a failed
helper is not replaced with an untracked write.

A successful advance is never rolled back when record publication later fails.
The old active record and cached value may remain while old preview evidence is
invalidated. This is deliberate. The helper's own uncertain publication also
prevents the record write, even if the generation has already changed.

## Coordinated apply and hotplug

The apply coordinator now requires a generation advance after its existing JSON
record load and before scanning, udev/module writes, rigctld/audio synchronization,
runtime-mirror publication or owner rewiring. The public wrapper holds
`apply.lock` throughout this boundary. A failed JSON load or apply-lock
acquisition does not advance. A tracking failure returns 6 and the wrapper closes
its descriptor before returning. Both full and hotplug apply advance even when
the record bytes remain identical.

A later derived-state failure leaves the new generation in place. There is no
rollback to a generation that could make earlier evidence appear current.
This boundary covers work inside apply. Compound add/set/remove operations can
already have saved a record or released an owner before reaching it, and remove
retains its separate card-restore cleanup after apply. These operations are not
atomic transactions.

Tracking refusal prevents derived stop/cleanup actions inside apply as well as
starts and rewiring. Separate program-stop commands, the unit's stopped hook and
emergency PTT STOP retain their existing paths without a tracking dependency.
An advance still supplies no lease across subsequent hardware work.

The trusted-parent tmpfiles rule now belongs to radio setup, which installs it
before its first apply. The rule formerly lived in applications setup, a later
provisioning step. Moving the existing root:root 0700 rule closes that ordering
gap without letting the generation helper create an unverified parent or repair
an epoch. Applications tmpfiles no longer duplicates the rule.

## Failed handover and cleanup

A failed session start still attempts stop/cleanup before recording release.
A failed shared-program start/wire still attempts unwiring and idle cleanup.
If compensating release cannot be confirmed, the claim returns exit 8 with an
explicit unconfirmed-release diagnostic. It does not report `left released`.
The session branch does not switch rigctld to the unowned defaults until the
record release is confirmed. A failed record operation can follow earlier
cleanup; this is not an atomic transaction or a claim that no other action ran.

Emergency PTT STOP, program-stop commands and the stopped hook gain no tracking
dependency. Record reads/status do not advance the generation. Existing caller
record/apply serialization is still required; an advance is neither a lease nor
an exclusion boundary for a later hardware command.

## Verification and remaining work

Focused Docker fixtures reproduce the previous missing guards and incorrect
release claim, then check refusal before control work, unchanged record/cache,
validation before tracking, owner change-and-return, and cleanup with an
unconfirmed compensating release. Shell unit fixtures explicitly substitute a
metadata-only helper; that is not evidence of durable generation writes.

The separate actual-process fixture sources only the real common/record
libraries and invokes the compiled root helper in a hardware-free container.
It checks invalid input before initialization, real owner change-and-return with
identical final record bytes and distinct generation receipts, response loss
after advance, an unwritable record parent after successful advance, corrupt
tracker refusal and unchanged unrelated state. It invokes no scanner, wiring,
apply, service command or radio control, and uses a wholly synthetic pinned
selector in private tmpfs record data. This does not register a physical radio.

The shared guard also covers [package mutation entry points](package-generation.md).
Complete writer coverage remains open, particularly full provisioning
and routing helpers invoked outside the guarded coordinator. Effective
unit/helper/module binding, fresh program-free evidence and recall exclusion
still need implementation. A token
or successful guard alone cannot authorize a preset. Real manager/workload,
hardware and first-boot acceptance are separate gates. Evidence is retained at
`/workspace/dxberry-radio-generation-tests/`.

The record milestone passed all 944 shell tests in Docker, with repository Bash
syntax/ShellCheck, tree checks and full Go vet/race checks. The actual helper, activation-prefix
and record-writer process fixtures passed with read-only roots, no capabilities,
no network and private tmpfs data. The runtime image uses the existing Bash/jq
integration Dockerfile; local checks used a compatible cached image. Workflow
configuration was parsed locally; hosted CI was not run.

The ARM64 tagged fixture compiled but was not executed. Production Go/helper
code is unchanged from the preceding activation milestone; its verified static
native/ARM64 helpers were reused. A separate non-deployable archive fixture
preserved the exact new radio library and actual ARM64 helper bytes/mode/root
ownership. This is not an image or release, and closes no physical or real-manager
acceptance gate. No Pi installation or radio/service action occurs in this step.

## Apply extension evidence

Four additional focused Docker groups cover full/hotplug tracking refusal,
invalid JSON before initialization, repeated apply with unchanged record bytes,
failed derived work retaining the new generation, real apply-lock contention
and recovery. Marker actions verify that tracking and all derived work happen
under the kernel apply lock. They do not simulate durable storage acceptance.

A fifth fresh actual-helper container executes the real coordinator and runtime
state publisher against an empty synthetic radio record. Scanning, udev,
module loading and orphan-service actions are markers; no radio or service
command is executed. Real store receipts change on repeated full/hotplug runs,
including response loss and a real permission failure publishing the runtime
mirror. Held apply/store locks refuse work and recover after release. Corrupt
generation data is refused without repair; registration bytes and unrelated
state remain unchanged. A refused tracker does not publish a new runtime mirror.

The driver still runs the root, unprivileged, activation-prefix and record-writer
fixtures in separate containers, with read-only roots, no capabilities/network
or radio devices and private tmpfs state. The runtime fixture now explicitly
declares `util-linux` for `flock`; local execution used a compatible cached image.
The compiled native helper is unchanged and reused with its verified hash.
The extended ARM64 fixture compiled only; real systemd, ARM execution, workload,
RF/audio and first-boot acceptance remain open. Source tracking is not installed
on the Pi and cannot authorize a preset. Evidence for this extension is retained
at `/workspace/dxberry-apply-generation-tests/`.

A provisioning fixture starts with only the driver's state root, creates the
station parent from the installed radio tmpfiles rule and reaches guarded apply
before any applications setup. A separate real `systemd-tmpfiles --root` Docker
fixture creates the root-owned 0700 parent from that exact rule, retains unrelated
data on rerun and initializes no epoch. These are ordering and filesystem
checks; they do not establish real first-boot or service-manager acceptance.

The completed extension passed all 951 shell tests, repository Bash syntax and
ShellCheck, the tree check, full Go vet/race and tagged-fixture vet in Docker.
All five actual-helper containers passed. The bootstrap regression was checked
with the provision driver's state-root precondition: the former rule location
failed and the earlier radio rule passed. ARM64 fixture compilation and the
real-tool tmpfiles check passed; no ARM execution or hosted CI result is claimed.
