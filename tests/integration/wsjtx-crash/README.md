# Actual routing-server process death

This independent radio-free guest extends the actual console-CLI fixture with
a routing server in a separate OS process. That process runs the production
`ProgramService`, `ProgramHookServer`, receiver and persistent stores. It uses
the same declared synthetic eligibility assignment as the existing gates;
physical presence is not claimed.

The parent drives real service methods through bounded inherited private pipes.
Those fixture controls are not the production hook channel. Manager pre/post
processes still run the complete actual console CLI against the real root-only
hook socket, and the application-user helper still performs the real settings
transactions. Receiver snapshots and binding identity come directly from the
child's actual observer; the parent creates no fake binding owners, packets,
manager responses or process metadata.

Before the first successful session, a real inotify watch observes the actual
application-user helper's atomic move from its pending file to the profile.
The parent then kills and reaps the server. The unchanged helper and journal
must show the managed route on disk while the durable phase remains `prepared`;
missing that interruption window fails the test instead of retrying or claiming
coverage. The actual console start-pre and stop-post must both refuse while the
server is absent, and the manager must report no application process or screen
socket. The negative phase evidence is retained.

A new server must report recovery required with no adopted observation. Explicit
recovery at the actual failed/stopped boundary must restore the original current
and saved routing namespace, preserve consent and guard preferences, clear the
journal, and retain the invalidated acquisition generation. Subsequent normal
sessions must never reuse the interrupted endpoint. No production delay, helper
replacement, journal edit or simulated manager response creates this fault.

After an actual WSJT-X session supplies non-transmitting Heartbeat and Status,
the parent sends SIGKILL to only the routing-server process and reaps its actual
signal exit. No graceful service/store close runs. The application unit must
retain its actual identity and managed settings. Its stale root socket must
remain after the kill; a new server process must safely replace it and reopen
the retained history/journal, exercising real kernel descriptor/file-lock
release. The route must be recovery-required with no adopted observation.
Running preview/recovery/disable must refuse.

The next normal stop-post, through the actual console CLI and new server, must
restore the complete journaled original routing namespace. The second actual
application start must use a different persistent endpoint. Existing consent,
SWR/PTT preferences, N1MM/request policy, saved profiles, normal mixed-stop,
private VNC/X-cookie, executable membership and generation checks still apply.

The image uses the actual checksum-pinned WSJT-X package, TigerVNC/Openbox,
console and settings helper. The outer Docker container is unprivileged,
read-only, capability-free and network-disabled, with no host devices, bus,
cgroups, display or audio sockets. The 4 GiB emulated systemd guest has no NIC or
host device passthrough, only loopback and private null audio. Build with
`tests/integration/wsjtx-crash/Dockerfile` and use the CLI fixture's isolated
5 GiB invocation with a fresh `/evidence` bind only.

This tests a real process running the routing services, not the complete
console HTTP/PAM daemon. The second kill occurs between completed operations; the first covers only
the atomic profile replacement before installed-journal commit. Other helper
write boundaries, power loss, update/rollback, physical eligibility
and public radio/session preparation, GUI/Decode/Clear, ARM64 capture workload
and physical radio/RF remain separate. No Pi state, feature flag or RF control
is used. A full passing job supplies process-death evidence; compilation alone
does not. Evidence is bounded synthetic text with an explicitly partial result.
