# Native WSJT-X display validation

This records local software evidence for the read-only status panel. No native
collector, new console binary or routing change is deployed to the Pi. Actual
WSJT-X 3.0.2/systemd compatibility and CPU/capture acceptance remain open.

## Container verification

- All-package Go vet passed. Web and HTTP package race tests passed; the native
  console and the tagged browser fixture compiled successfully.
- Seventeen JavaScript model/controller checks cover event identity, independent
  report age, unknown values, protocol expiry, unsupported data and bounds.
- Eleven real Chromium workflow groups passed against the production HTTP and
  events path with inert fixture observations. They cover current/empty/loading,
  off/paused/recovery/closed/error/unsupported/stale/disconnected states,
  reconnect, keyboard history, literal long text, omissions, phone layout,
  light/dark/reduced-motion/forced-colors behavior, STOP contrast and dashboard
  fragment navigation. Recorded radio requests contained no mutations and
  socket writes contained only watch messages; the fixture remained unkeyed.
- Screenshots were inspected before and after the change at desktop and phone
  sizes. The existing palette, card structure and complete program window remain.
- The full shell suite passed: 907 tests, zero failures. Repository ShellCheck
  passed, including the new integration wrapper. `build/build-image.sh --check`
  passed in a disposable source copy with the same placeholder console binary
  used by the CI shell job; the real native console build passed separately.

These checks use no network or radio devices. They do not establish login/PAM,
certificate trust, actual application compatibility, Safari, physical audio,
RF output or two-radio acceptance. CI includes the new workflow, but remote CI
has not run for this unpublished change.

## Static UI audit triage

The strict auditor returned exit 1 with 14 findings and no unresolved canonical
owners. None points to the new native panel. The raw result is retained; it is
not reported as a clean audit. Source inspection explains the existing findings:

| Finding | Existing implementation / disposition |
| --- | --- |
| `index.html` Menu, Log out, log Refresh/Close | External click bindings in `app.js` |
| `index.html` station Refresh, radio Cancel | External click bindings in `dxberry.js` |
| `index.html` settings Cancel | External click binding in `settings.js` |
| `index.html` switch Cancel | External click binding in `dashboard.js` |
| `index.html` logbook Cancel/Test | External click bindings in `programs.js` |
| `screen.html` Reconnect | External click binding in `screen.js` |
| `terminal.html` Close | External click binding in `terminal.js` |
| Confirmation form lacks `novalidate` | Existing `method="dialog"` form has no data-entry fields; its buttons return a dialog result |
| Login form lacks `novalidate` | Existing required credential fields use native browser validation before `login.js` submits; migrating that workflow to application-owned validation is separate work |

The auditor does not trace these external bindings or distinguish the dialog
form. This change adds no inline handlers to silence it and does not change
login validation. Runtime evidence above concerns the native workflow, not all
legacy actions in this table.

## Blocked design-format check

The required official `designmd lint DESIGN.md` could not run: the package is
absent from the cached images and an offline invocation returns `ENOTCACHED`.
The cloud environment reports `vpn_enrollment_failed` and blocks non-platform
outbound access, preventing the required download. No substitute parser is
presented as an official lint pass. Keep this check open before release.

The design sources, token mapping and native interaction contract are committed
for review. Static-audit and design-format limitations mean this record makes
no global UI-compliance or release-readiness claim.
