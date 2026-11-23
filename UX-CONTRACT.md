# DXBerry UI contract

## Product context

Phone-first station operation in English, with desktop access to the same
features. Protocol time-of-day values are UTC and never gain an invented date.
New and changed surfaces target WCAG 2.2 AA. This contract covers native program
status and the shared primitives it consumes; existing broader workflows retain
their maintained specifications and are not certified by this increment.

## Business-context sources

| Domain / scope | Authoritative source | Source type | Reviewed date |
| --- | --- | --- | --- |
| Authentication and transmitter authority | `docs/design/2026-10-25-station-app.md` sections 6, 9, 12; HTTP/session implementations | Domain contract / server authorization | 2026-11-18 |
| Native observation, lifecycle and recovery | `docs/design/wsjtx-status-adapter.md`; `station/internal/wsjtx` | API / lifecycle contract | 2026-11-18 |
| Current scope and deployment acceptance | `docs/design/project-roadmap.md` | Continuation roadmap | 2026-11-18 |
| Data lifecycle and retention | Adapter design's bounded session/history and journal sections | Domain contract | 2026-11-18 |
| Market and content | Station-app purpose/principles; current operator's southwest Kansas context | Product brief | 2026-11-18 |

This read-only feature introduces no billing, legal text, retention change,
deletion, privilege grant or external dispatch. It does not change collection
consent or transmitter ownership.

## Visual contract

See `DESIGN.md`. Runtime CSS remains canonical; its mapping table connects the
document to shared components. Preserve light, dark, keyboard focus and forced
color behavior. Compare the program-owned radio view with the dashboard.

## Canonical UI Map

| Capability | Canonical owner | Source of truth | Allowed variants | Verification |
| --- | --- | --- | --- | --- |
| Scrollbar | Global `station/web/dxberry.css` baseline | DESIGN.md / runtime CSS | Bounded activity geometry and stable gutter | Browser computed style, keyboard and narrow viewport |
| Toast | Existing `notice` / `dxb.notice` and `#notices` | Existing console behavior | Existing good/warn/bad; native status stays inline | Existing page tests; no new toast path |

Buttons use `.btn`; badges use shared semantic roles. The native status renderer
owns a business-named read-only program variant. It does not duplicate a select,
date picker, form, dialog, search, selection or CRUD primitive. Those capabilities
are not introduced here.

## Component behavior

Actions are buttons, navigation uses real links, and data is non-interactive.
The full program window opens with the existing label and destination. Native
dashboard navigation uses `#wsjtx-status` to reveal and focus the status heading
once after it mounts; incoming data never repeats that movement. Ordinary radio
page navigation retains its existing position and the `#listen` gesture flow.
Native retry requests another read-only watch; it does not enable collection. History
expansion changes only the local displayed count. Controls retain visible focus,
hover, pressed and disabled states without resizing while status changes.

## Dataset navigation

Band activity is a bounded replacement snapshot, newest first. Show a small
initial range and expose older retained rows explicitly, with the shown and
retained counts and any source omissions. Never imply that the list is a
lossless log. History and the displayed range stay in this document's memory,
not URLs or persistent browser storage, because they belong to a transient radio
generation/continuity epoch. A new generation resets the view. There is no sort,
filter, row selection, mutation or export in this increment.

Loading, empty and error states share the activity footprint. A quiet program
can have zero decodes while its status remains current. Missing fields remain
unknown; zero and false are valid reported values when explicitly present.

## Flow ledger

| Operation | Trigger | Pending | Success destination | Success feedback | Failure recovery | Focus outcome | Source ref |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Observe WSJT-X | Open its radio page | Stable reading indicator | Same radio page | Inline current state | Inline unavailable/stale guidance | No focus movement | Adapter events contract |
| Open native panel | Dashboard WSJT-X status link | Radio page loading | Native panel fragment | Inline state | Existing missing-radio view | Reveal and focus status heading once | Radio page navigation |
| Refresh observation | Refresh status | Reading state | Same panel | Inline state | Bounded unavailable state; full window remains | Keep refresh focus | Read-only watch contract |
| Read older history | Show older decodes | No remote work | Same activity region | Range/count text | Disabled at retained boundary | Keep control focus | Bounded tracker history |
| Open full program | Open WSJT-X | Existing new-tab navigation | Existing program window | Existing window UI | Existing session guidance | Native link semantics | Station-app section 12 |

## Navigation and responsive behavior

The radio remains `/radio/<name>`. Its title identifies the radio and DXBerry;
missing-radio states identify the missing resource and retain dashboard access.
This feature adds no route, tabs, menu, permission page or modal. The existing
authentication layer owns login and access boundaries.

Keep the radio's control order. The program card can span the grid and its
activity viewport owns local scrolling. The page and all sibling forms retain
document scrolling. Decoded messages wrap with full-value access. Table headers
name units; no cell relies on hover for essential values. Focus remains visible
inside the activity region and on the full-window/retry/history actions.

## Overlays and feedback

No new overlay. Native condition text stays inline while true. A small polite
status region may announce availability transitions; routine decode arrival and
counter changes must not flood assistive technology. There is no native browser
alert/confirm/prompt and no confirmation for read-only refresh or local history.

## Async and resilience

Events are authenticated, radio-bound, versioned replacement views. Clear current
readings when disconnected, unavailable, unsupported, retired or expired. Rewatch
on reconnect and invalidate prior connection data. Use server send/observation
times with a local elapsed-time clock, never browser wall-clock agreement.
Protocol liveness and status freshness are separate. WSJT-X emits status on
changes, so keep the last report with its original age while program updates
continue; a heartbeat never makes an old frequency or transmit report new.
Clear readings when protocol liveness expires. A missing initial event reaches a bounded
unavailable state rather than spinning indefinitely.

Release pause can coexist with an already reporting session; show both facts.
Recovery and ambiguous settings states do not invent a connected program.
Default-off state offers the full window, with no collection enable/setup action.
Reassignment removes the native view and its cached content. The server remains
the authority for all ownership, RF and lifecycle actions.

## Validation and permissions

Render source strings through text nodes. Accept only the matching radio,
program and supported event version. Unknown numeric/null values never become
zero through coercion. No native settings writes, permissions, clipboard export,
secrets, raw errors or private transport identities are introduced. UI visibility
does not substitute for server authorization.

## Migration status

The touched workflow preserves the existing palette, cards, buttons and full
program link. Two shared corrections are scoped and verified: the emergency STOP
foreground and the global scrollbar baseline. Unrelated forms, program installs,
logbook credentials and radio controls are not redesigned by this feature.

## Verification

Run Go/web and JavaScript checks, strict static UI audit, design-format lint,
native console build and real Docker browser checks. Cover current, loading,
empty, default-off, paused, recovery, stale, closed, disconnected, unsupported,
long text and bounded history, including keyboard, phone, light/dark and reduced
motion. Browser evidence must verify zero new radio/collection mutation intents.
Physical Safari/device/RF acceptance remains separate from simulated software
fixtures. Record exact results and blocked checks; static audit is not runtime
compliance or a release approval.
