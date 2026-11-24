# rigctld pin binding and restart retries

Status: development fix, not installed on the Pi. This was found while tracing
the effective-route evidence needed by preset preview. The production preview
verifier is still incomplete and its HTTP adapter remains unavailable.

## Problem and behavior

A registered radio's CAT selector can change from one USB function to another
while its command line still says `-r /dev/dxberry/radio1-cat`. The same applies
to `/dev/dxberry/radio1-ptt` and `/dev/dxberry/radio1-hid`. Previously the generated
environment contained only model, port and argument strings. Those strings did
not change when the pinned identity changed, so `dxb_rigctld_sync` could skip
the restart and leave rigctld with the old device open.

The generated environment now also contains `RIG_PINS_SHA256`. It hashes a
versioned, sorted compact JSON object containing only the CAT, serial PTT and
HID pins this rigctld process is configured to open. Each selected pin contributes
its path, VID/PID and serial; missing identity fields become null. Unused pins
become null. The hash includes a terminating newline, as written by the shell's
here-string. The hash is metadata, not a new rigctld command-line argument.

A changed used pin therefore changes the desired environment and requests a
restart. JSON key ordering, omitted versus null optional identity fields,
display labels, program-owner labels, unrelated audio and unused pins do not
cause a restart. In flrig mode, rigctld's XML-RPC connection does not own the CAT
serial pin, so that pin is excluded; serial or HID PTT still belongs in the hash
when rigctld actually uses it. This does not verify or repair flrig's own device
binding.

## Retained restart intent

Previously a rejected restart could also be forgotten: the new environment was
already on disk, so a later apply saw identical bytes and skipped the retry.

Before publishing a changed environment, sync now writes a private
`/run/dxberry/rigctld/<name>.restart-pending` file containing the desired complete
environment's SHA-256. Publication is read back. A failed publication or rejected
request for that change leaves the intent for a later apply. An active instance with a
pending intent requests restart even if the environment bytes are unchanged.
An absent radio is not started; a later present sync can satisfy the intent.
An inactive instance's ordinary start is already retried by the existing state
check, even when its environment was unchanged and no new intent was needed.

After systemd accepts the asynchronous request, sync checks the intent's content
before removing it. A different or unremovable intent is retained and reported
as an error. Non-regular environment/intent leaves are refused. Stale radio
cleanup also removes orphaned derived intents. These are temporary runtime
inputs, not a history or rollback archive.

The existing caller serialization still matters. The environment and intent are
separate files, and the content check before removal is not a filesystem CAS
transaction across concurrent writers. This change does not add a new shared
radio/TX operation lock or claim to solve all apply/owner interleavings.

## Operational limits

An accepted `systemctl --no-block` request is scheduling evidence, not completed
service or radio acceptance. Logs now say start/restart **requested**. Neither
the pending file nor the desired pin hash establishes that a running process has
adopted the environment or opened the intended kernel device. Preview still
needs fresh process/socket/configuration and device-identity checks.

The first apply after adopting this environment format can request one restart
for each running registered instance because the metadata line is new. That is
a maintenance transition, not a passive status read. Subsequent unchanged syncs
do not request another restart after the previous request was accepted. No
frequency, mode, power or PTT argument is added or changed by the fingerprint.
Actual radio behavior during restart remains a hardware acceptance item.

Unplug/replug under the same recorded pin identity is a separate case: unchanged
selection metadata does not prove that an existing descriptor points at the new
kernel device. Existing presence handling and eventual process/device checks
remain necessary. The current manual Graywolf/CODEC setup is not registered as
a convenient test radio, and no apply or restart was run on the Pi for this fix.

## Software evidence

Docker regressions first reproduced three missed restart requests (CAT, serial
PTT and CM108 HID) and a forgotten retry after restart rejection. The focused
regressions passed after the fix. Additional shell fixtures cover canonical
identity metadata, unused inputs, flrig-owned CAT exclusion, failed environment
publication, unavailable derivation, intent-content mismatch and orphan cleanup.
The full Docker shell suite passed 929 tests with zero failures, followed by the
tree check. Bash syntax and ShellCheck passed for the changed library/tests.
Temporary binary placeholders were used only for the isolated tree/archive
fixtures; no image or deployable update artifact was built for this shell fix.
Go and browser checks were not repeated because their source/behavior did not
change in this slice.

These fixtures record systemctl requests; they do not run systemd, open radio
devices, prove a successful restart or establish RF acceptance. Evidence is
retained at `/workspace/dxberry-rigctld-binding-tests/`. No Pi deployment, public
release or image was produced.
