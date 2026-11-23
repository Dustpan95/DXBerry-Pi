# WSJT-X application-user settings bridge

Run the privileged integration check from the repository root:

```sh
tests/integration/test-wsjtx-settings-bridge.sh
```

A separate Docker preparation step downloads the pinned, checksum-verified Go
modules. Compilation and execution then run inside Docker with networking disabled.
The script builds a static test binary and the real standalone settings helper,
then runs cross-user fixtures as container root. It uses Go 1.24.4 and Debian
bookworm images by default. `DXB_BRIDGE_GO_IMAGE` and `DXB_BRIDGE_RUN_IMAGE` may
select existing local images. `DXB_BRIDGE_GOPATH` can reuse a prepared Go module
cache; the isolated compiler mounts that cache read-only. `DXB_DOCKER` and `DXB_DOCKER_HOST` select the local
Docker command/endpoint. Temporary output is removed when the script ends.

Fixtures create root-private recovery state and application-owned settings.
The helper runs with UID/GID 1000, only its primary supplementary group and a
minimal environment. Tests require that root state and a parent-only environment
value are inaccessible. A root helper, root primary group, unsafe executable or
invalid radio/request is refused. The settings directory uses mode 0770 to
match the deployed service-group layout; file UID/GID and permissions are kept.
Separate file tests refuse everyone-write and a writable foreign group.

Install and restore use the actual journal permits at both private-pipe readiness
boundaries. The suite checks the standalone binary, denied decisions, malformed,
oversized or extra output, mismatched completion data, idempotence, cancellation
after a confirmed file write and an earlier caller deadline. A failure retains
the journal and is reconciled through a fresh application-user inspection and
conflict-aware restoration. Protocol frames are canonical, bounded JSON and
never contain a client-chosen path or shell command. Helper stderr/settings
contents are not copied into diagnostics.

The test-binary helper wrapper disables only the race runtime's default exit
sleep for its short-lived child processes. Race reporting and assertions remain
enabled; the production standalone helper has no test wrapper.

The same script now exercises the composed program controller and concrete
systemd/process guard. Fourteen controller tests and eight guard tests cover
the full prepare/install/restore sequence, durable phase checks, both denied
write boundaries, uncertain writes, journal faults before/after replacement,
missed stops, delayed callbacks, restart recovery, profile conflicts, independent
radio transactions and deadlines while waiting for the lifecycle lock. The
controller uses the actual standalone helper. Read-only Qt datagrams are supplied
through real loopback sockets with fixture files mounted into the container.

Service and root-socket tests now compose persisted approval, the controller
and actual helper through real Unix connections. They exercise default-off
startup, explicit new inventory initialization, stale preview refusal, restoration
after damaged approval/reassignment, daemon loss with an outstanding route,
recovery before an uncollected start, root peer verification, malformed/extra
frames, exclusive socket ownership and cancellation during shutdown. One test
deliberately broadens temporary socket access to prove kernel peer credentials
still reject an application-user client. Product socket permissions stay private.

The full Go suite separately covers strict guard-preserving preference updates,
fresh assignment/presence and command-mode checks, plus authenticated same-origin
HTTP routes with bounded approval input and private-error filtering. The shell
suite checks existing settings/cleanup ordering around the real command wrapper.
The native eligibility command uses `verify-present`, which shares the actual
presence check without creating a waiting/restart marker. A shell regression
verifies this separately from the unit condition's existing hotplug retry behavior.

Manager/unit/cgroup state is synthetic. Process checks also inspect actual Linux
UIDs using an inert cross-user process, and a composed controller transaction
uses the concrete guard with real process inspection. These fixtures have no
running WSJT-X program. Journal permits alone do not prove program quiescence
or exclusive lifecycle ownership. The production root hooks must enforce the
start-pre/stop-post barrier until the controller returns; those hooks are now
connected locally and their order is covered with inert shell fixtures. No app unit, radio,
audio device or RF transmitter is started by this check. Actual WSJT-X protocol
compatibility and physical acceptance remain separate.

Release fixtures now check the durable pause outside the native inventory,
receipt replacement, acquisition refusal, recovery while paused and root socket
maintenance actions. Authority checks hold transition/job-start locks and verify
the compatible installed bundle, running console inode and inactive updater job.
Offline pause requires exclusive retained-state audit; offline resume is refused.
Shell fixtures separately cover rejected update/rollback without backup rotation,
pause retention after failed rename/setup and lock scope through compatible setup.
Legacy archive fixtures omit the new protocol marker; declared-compatible bundle
fixtures include it explicitly. Release-state fuzzing checks the bounded canonical
codec. The Linux process guard also refuses an empty application command line
during exec, even when its temporary comm names a different launcher.

No collection or release guard is enabled on the Pi. Latest release pause,
endpoint history, routing journal and approvals must survive all transitions.
Real WSJT-X/systemd compatibility and workload/capture acceptance remain required.

Native event tests also run in this script. They exercise periodic idle expiry,
radio separation, wire-history bounds, coalesced signals, replaced watches,
cancellation and default-off reads that create no inventory or hardware work.
The controller transaction verifies sanitized transport counters without endpoint
disclosure. The status package separately tests real authenticated event sockets,
watch filtering, cleanup and login revocation between outgoing batch frames.

Ordinary nonroot Go test runs explicitly skip privileged fixtures. The CI and
release binary jobs call the Docker script so these checks are exercised with
the required identities. The recorded local run used existing builder/runtime
images because environment VPN enrollment still blocked external image/package
downloads. Remote jobs were not triggered by the local verification.
