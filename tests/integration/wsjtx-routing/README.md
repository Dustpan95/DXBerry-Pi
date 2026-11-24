# Actual public WSJT-X hook routing

This gate runs in a separate radio-free QEMU/systemd guest using the screen
fixture's production runner and actual WSJT-X/TigerVNC/Openbox/xauth. The outer
container has no host devices, networking, display/audio sockets, manager bus,
cgroup mounts or privileges. The guest has loopback only, private device views,
null PulseAudio and synthetic N0CALL/AA00aa settings with rig None and automatic
sequencing off. The same socket/cookie, executable-membership, non-transmitting
protocol and normal mixed-stop checks remain required.

The guest additionally builds and installs the actual application-user settings
helper at its production path, with the actual application home and account.
A real `ProgramService` owns a new persistent endpoint history, routing journal,
approval store and program-generation store. A real root-only hook server runs
on its private UNIX socket. Actual manager start-pre/stop-post processes invoke
the production `CallProgramHook` client with the manager's `INVOCATION_ID`.
No fake settings helper, lifecycle provider, manager response or HTTP server
substitutes for those boundaries.

Fixture code explicitly approves its synthetic original destination. Start-pre
must install a fresh managed loopback route through the credential-switched
helper. Actual WSJT-X supplies Heartbeat and Status to that receiver. Running
disable and outdated stop callbacks must refuse, preserving routing and consent.
Each normal stop-post must restore the complete original routing namespace,
including raw spelling and saved profiles, and clear the durable pending route
and observation. Request policy, nondefault N1MM enable/server/port settings,
SWR/PTT preferences and approval must survive. Cleanup must not advance the
acquisition generation. A second fresh session repeats the real flow without
rewriting the restored application profile.

The first session also remains running while the fixture gracefully closes and
reopens the production hook server, service, receiver and persistent stores.
The reopened service must mark the owed route recovery-required without adopting
the old observation. Running preview/recovery/disable must refuse, with identical actual
unit identity and managed settings. Its subsequent real stop-post must restore
the journaled original destination. The next start must use a different durable
endpoint; generation counters are compared within each observer's identity.
This replaces service instances inside the same controller process; it does not
claim whole-process death or complete console restart acceptance.

This is partial software acceptance. Eligibility accepts only the declared
synthetic fixture assignment; physical eligibility and public radio preparation
are not exercised. The hook processes use the actual IPC client function, not
the complete console command-line entry point. Whole-process replacement,
abrupt death, interrupted helper writes, update/rollback, GUI changes, Decode/Clear, ARM64 capture workload
and physical radio/RF remain separate. No station data or feature flags are used.

Build with `tests/integration/wsjtx-routing/Dockerfile`. Use the screen fixture's
isolated Docker invocation (unprivileged host UID, all capabilities dropped,
network disabled, read-only root, 5 GiB memory, 64 MiB private `/tmp`, and a fresh
`/evidence` bind only). The image boots a 4 GiB emulated guest with no NIC or
device passthrough. The shared runner requires exactly one PASS marker and zero
QEMU exit code. Evidence contains only bounded synthetic service text and a
partial result; no raw audio is retained.
