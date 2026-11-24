# Actual console initialization and hook CLI

This independent guest extends the actual routing fixture with the complete
`dxberryd` executable. It builds the console with PAM/Opus support from the
repository source, then installs that fixture executable and its runtime
libraries only in the disposable guest. Application, manager, settings-helper,
private-socket, routing-journal and isolation checks remain the same as the
separate function-client gate.

Initialization invokes the actual `-wsjtx-initialize` entry with a fresh private
state and release-coordination directory. Its generation and native inventory
must then open normally. Each real manager pre/post process runs the actual
console executable with `-wsjtx-hook`, the fixture state/socket paths, radio and
manager-provided invocation ID. Its real exit status determines the phase trace.
This exercises the console client's production start/stop deadlines, not just
the generic IPC function. The approval store uses the console's actual
`state/radios` path.

Both actual WSJT-X sessions, normal stops, refusal/preservation checks, and
service-instance recovery must pass. No fake command response substitutes for
the actual executable. The outer container remains unprivileged, read-only and
network-disabled, with no host devices, display/audio sockets, service bus or
cgroup mounts. Guest networking is loopback only; audio is private null audio.

This proves only initialization and root-hook CLI composition when the complete
job passes. It does not establish the full console daemon, PAM authentication,
physical eligibility or public radio/session preparation. Whole-process server
replacement, abrupt death, interrupted helper writes, update/rollback,
GUI/Decode/Clear, ARM64 capture workload and physical radio/RF remain separate.
No Pi state, software installation, native feature flag or RF control is used.

Build `tests/integration/wsjtx-cli/Dockerfile` and use the routing fixture's
isolated invocation: unprivileged host UID, all capabilities dropped, no network,
read-only root, 5 GiB outer memory, 64 MiB private `/tmp`, and only a fresh
`/evidence` bind. The 4 GiB emulated guest has no NIC or host device passthrough.
Compilation and package staging happen before isolated execution; Go compilation
runs without network after checksum-verified module download. Evidence remains
bounded synthetic text with an explicitly partial result.
