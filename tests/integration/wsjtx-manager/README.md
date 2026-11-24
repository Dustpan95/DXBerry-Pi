# Actual WSJT-X lifecycle guard with a real manager

This separate QEMU guest checks the unchanged production settings-lifecycle
guard against real systemd start-pre, stop-post and stopped boundaries. It
installs the checksum-pinned official WSJT-X 3.0.2 package only in its disposable
root, with private synthetic Qt/PulseAudio settings and no NIC or host devices.
The outer Docker container has no capabilities, host bus or cgroup mounts.

The test controller and application have private device views. Root-prefixed
unit helpers run with systemd's actual INVOCATION_ID, as the production root
hooks do; the prefix can bypass a unit's filesystem namespace restrictions.
Those helpers perform only metadata checks in this fully emulated guest. The
application runs as dxbapps with rig None and UDP requests disabled. No GUI,
connect, transmit, CAT or PTT control is invoked.

The test requires real positive pre/post/stopped checks, actual managed idle
Heartbeat/Status, active-unit refusal, refusal of an actual same-profile manual
process and a fresh observed managed recovery. No unit metadata, process tree,
cgroup or application datagram is substituted. The two-second production guard
deadline remains unchanged. Phase logs contain pass/refusal labels only, never
private identity structures.

A complete successful job is required before claiming execution acceptance;
compilation alone is insufficient. The report remains partial: the public
session IPC, settings routing/restoration, physical-radio eligibility,
GUI/Decode/Clear input, ARM64 capture workload and RF remain separate. No
collection is enabled and no development test runs on the maintained Pi.
