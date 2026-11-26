# Actual WSJT-X screen-session runner

This separate QEMU guest extends the manager component fixture with the actual
production `dxb_screen_session_run`, real module loader and WSJT-X command module.
It installs the checksum-pinned official package plus real TigerVNC, Openbox and
xauth in a disposable Debian guest. The outer Docker container uses no host
devices, privileges, networking, display, audio, service-manager or cgroup mounts.
The guest has loopback only and private radio-free device views.

The driver creates synthetic session data with root ownership and the session file modes,
N0CALL/AA00aa, rig None, disabled UDP requests and automatic sequencing off.
Audio uses only a private PulseAudio null sink/source. Actual Xvnc uses the
runner's private X cookie and VNC UNIX socket; TCP listeners are refused.
The receiver passively observes actual non-transmitting Heartbeat and Status.
It independently verifies actual WSJT-X, Xvnc and Openbox executables in the real
unit cgroup and the private socket/cookie ownership and modes.

Two fresh starts must have different real manager invocation IDs and new
observer bindings. Each normal stop uses the production mixed kill mode,
20-second manager timeout and unchanged runner's program/helper grace periods.
Both actual stop-post hooks and subsequent stopped checks must accept, before
synthetic re-preparation. No whole-unit kill substitutes for a normal stop.
The existing guard continues to refuse any remaining application-owned child.

The initial full-runner execution exposed a surviving `jt9` child at both
stop-post boundaries; the manager killed it only after each refused hook.
The runner now launches the program in a private process group and applies its
existing TERM/KILL grace to that group, including children whose parent exited.
The unit kill mode, lifecycle guard and radio cleanup policy remain unchanged.

This is partial session-runner acceptance. Synthetic preparation bypasses
physical eligibility and public prepare/session/stopped IPC. It does not prove
native route restoration, update/rollback, GUI changes, Decode/Clear input,
ARM64 capture workload or physical radio/RF operation. The fixture sends no
application controls, touches no station settings and enables no feature.

Build the image with `tests/integration/wsjtx-screen/Dockerfile`, then use the
same isolated invocation as the manager fixture: an unprivileged host UID,
network disabled, all capabilities dropped, read-only root, 5 GiB outer memory,
64 MiB private `/tmp`, and only a fresh writable `/evidence` directory bind.
The image runs a 4 GiB emulated guest with no NIC or host device passthrough.
Success requires the shared runner's single PASS marker and zero QEMU exit code;
failure retains bounded synthetic service text in the evidence log.

On both managed starts the display owner selects the verified WSJT-X main
window. In FT8 the unmodified F12 and F11 keys move the receive audio offset by
one hertz and restore it. The production observer must receive a fresh Status
for each change within five seconds on the same observation generation. The
fixture records elapsed time and checks that the dial frequency and transmit
offset remain stable and that transmit stays disabled. All input goes to the
guest's private display; its only audio devices are synthetic.
