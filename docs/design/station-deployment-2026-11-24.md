# Coordinated station deployment — November 24, 2026

The maintained Pi runs `0.5.0-ci.review.19e4cf1`. Its source tree
`ade64307046ea9329093594e2212be0284f4bd48` matches
[PR #14](https://github.com/Dustpan95/DXBerry-Pi/pull/14), merged into main as
`6c49c70e6581f867b1caaa87b7c77a2329590045`. This is an unpublished candidate;
no stable tag or release was created.

The installed Docker cross-build used Go 1.24.4. Its console SHA-256 is
`edd132c874f10aad8406aa9c719af9bbdb8db75db8584b4ff8ed0d15f6dc6b10`.
[CI run 81](https://github.com/Dustpan95/DXBerry-Pi/actions/runs/45322410069)
passed all eight jobs and separately built and started an ARM64 archive from
the same tracked tree. The two builds have separate binary identities.

## Installation and verification

The deployment installed the complete matching software tree and four built
executables, prepared private generation state, and synchronized the session
condition, tmpfiles policy and release metadata. An atomic directory exchange
published the tree while mutation gates were held. The apply gate was released
before service-manager waits. Only the console was restarted; the full
provisioner was not run.

Independent verification checked the running executable, all 81 installed file
hashes, HTTP and certificate-verified HTTPS versions. Six other service-process
identities, protected configuration/TLS files, Graywolf configuration, CODEC
mixer and both running PCM streams were preserved. The manual radio/TX setup
and empty registered-radio inventory were retained.

All 72 files of the original software tree remain at
`/opt/dxberry.candidate.295e629d271f`. The pre-existing `/opt/dxberry.prev` is
unchanged. Original session/tmpfiles/release/stamp files and the immutable tree
exchange journal are retained under
`/root/dxb-pr14-295e629d271f/rollback`. The same staging directory retains the
deployment, verification and integration receipts. Existing local-patch markers
and backups remain historical records; their old file-hash checks should not
be used as the expected current software baseline.

Six isolated tree-exchange checks covered restoration, interrupted durability
and identity refusal. Four installer fixtures covered publication, reload and
health failures, and refusal to overwrite an unexpected recovery directory.
Their manager and operational-health checks were mocked. Live rollback was not
exercised, and cold boot/spare-media recovery remain open.

## Activation and remaining acceptance

Generation tracking is installed. Native WSJT-X inventory remains uninitialized,
native Pat collection stays off, the experimental queue is inactive and its
three manager units are absent. Preset target authority remains unavailable.
Installing these foundations does not authorize radio controls or establish
complete managed-program ownership evidence.

No RF test, application-routing activation, network/package setup, firmware
update or reboot was performed. Actual application, browser/equipment, udev,
queue-retirement and hardware acceptance retain their separate gates. Preserve
the working manual CODEC configuration; registering it remains a planned
transition rather than an incidental acceptance setup step.
