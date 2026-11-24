# Complete console daemon recovery

This independent isolated guest extends the actual process-crash fixture with
the complete production console executable and its production systemd and PAM
templates. A private unit drop-in changes only the fixed fixture paths and
loopback HTTP address. The original automatic-restart policy and service limits
remain. The Docker build supplies the real PAM modules and a disposable nonroot
sudo-group account; the test generates its password only in guest memory and
sets it with the real password utility. No password or session cookie is logged.

The earlier actual-helper interruption and two real managed WSJT-X observations
still run. While the second application remains running, the routing-server
fixture process is killed and replaced by the complete actual console daemon.
Its production eligibility function is unchanged. Synthetic records identify
the test radio for authenticated reads, but have no CAT, rigctld port, physical
pin or radio sound server. This does not establish physical eligibility or
permit a new acquisition through the complete daemon.

The guest requires a real systemd invocation, root process identity, matching
console executable/cgroup and private root hook socket. The sole new TCP listener
must be the actual daemon's loopback HTTP socket, with ownership verified from
kernel socket and process descriptor metadata. Missing credentials must receive
401. An incorrect password must fail real PAM authentication; a correct one must
issue the real Secure/HttpOnly/SameSiteStrict cookie. Foreign-origin requests
must receive 403. Actual authenticated status must show the retained installed
route as recovery-required, with no adopted observation; running preview,
recovery and disable requests must be refused.

The manager then sends SIGKILL to the actual daemon main process. A kernel pidfd
must report the old task's exit. The production restart policy must establish
exactly one automatic restart, a new PID and invocation, and the same verified
executable/listener/socket ownership. The previous in-memory login session must
be rejected. A new real PAM login must produce a different cookie, with the same
recovery/refusal evidence. The live application identity, managed profile,
generation, approval and guard preferences must remain unchanged throughout.

Finally, the actual console CLI in the application's normal stop-post must
restore the entire owed routing namespace through the complete restarted daemon.
Authenticated status must then show no retained route or observation. Original
raw spellings, saved profiles, request/N1MM policy and SWR/PTT preferences retain
the shared fixture checks. Test cleanup stops the actual daemon through systemd.

The client explicitly carries the actual returned cookie to the private HTTP
backend. This is not browser cookie transport or nginx/TLS acceptance. No fake
authenticator, session store, service response, process metadata, manager or
settings helper supplies the evidence. Package staging and compilation occur
before runtime isolation; execution has no network or host capabilities,
devices, service bus, display/audio sockets or cgroup mounts. The emulated guest
has no NIC and uses private null audio. Build with this directory's Dockerfile
and use the crash fixture's unprivileged, read-only, capability-free 5 GiB outer
invocation with a fresh evidence bind.

Only a complete successful job establishes these paths. Full public preparation
and acquisition with real eligibility, TLS/nginx/browser authentication, other
helper interruption boundaries, power loss, actual updater tree swap/rollback,
GUI/Decode/Clear, ARM64 capture workload and physical radio/RF remain separate.
No Pi state, feature flag, hardware setting or RF control is used.
