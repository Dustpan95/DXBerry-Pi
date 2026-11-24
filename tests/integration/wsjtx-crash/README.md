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
console HTTP/PAM daemon. The kill occurs between completed operations, so an
interrupted helper write is not covered. Update/rollback, physical eligibility
and public radio/session preparation, GUI/Decode/Clear, ARM64 capture workload
and physical radio/RF remain separate. No Pi state, feature flag or RF control
is used. A full passing job supplies process-death evidence; compilation alone
does not. Evidence is bounded synthetic text with an explicitly partial result.
