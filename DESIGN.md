---
version: alpha
name: DXBerry
description: A phone-first radio workbench with clear ownership, instrument readouts and complete program windows.
colors:
  primary: "#8e2453"
  background: "#f2f4f7"
  surface: "#ffffff"
  text: "#1c2430"
  muted: "#5d6878"
  border: "#d9dee6"
  success: "#1e7a3c"
  warning: "#8a5a00"
  danger: "#b3261e"
  dark-background: "#12161c"
  dark-surface: "#1b2028"
  dark-text: "#e6e9ee"
  dark-primary: "#e784ae"
typography:
  sans:
    fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
    fontSize: "14px"
    lineHeight: "1.45"
  mono:
    fontFamily: 'ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace'
rounded:
  DEFAULT: "8px"
  control: "6px"
  badge: "999px"
spacing:
  card: "16px"
  grid-gap: "16px"
  action-gap: "8px"
components:
  button:
    borderRadius: "6px"
  card:
    borderRadius: "8px"
  dialog:
    borderRadius: "8px"
---

# DXBerry design system

## Overview

The reference is a radio workbench: large, aligned frequency digits, compact
operating information and clearly separated actions. The signature is the
monospaced instrument readout against quiet cool-gray panels, with a raspberry
accent reserved for navigation, selection and safe primary actions.

This is a product interface for radio operators on phones, tablets and computers.
The current continuation serves an English-speaking operator in southwest Kansas;
the existing product remains radio-independent. Its language is English, its
protocol times are explicitly UTC, and its existing light/dark themes remain.
Do not introduce a market-specific visual style or localization policy without
product evidence. Familiar controls and honest unavailable states take priority
over decorative motion. Avoid marketing heroes, novelty gauges, flashing decode
rows, or color-only transmitter claims.

**Token ownership is Model B:** `station/web/dxberry.css` and `vfo.css` remain the
canonical runtime sources. This document mirrors their values and records intent;
it does not generate CSS or introduce a second theme. Baseline desktop, narrow
dark radio and sibling dashboard screenshots are the visual comparison points.

## Colors

The frontmatter records light-theme roles and the principal dark counterparts.
Complete semantic mappings live in `dxberry.css`: `--bg`, `--card`, `--text`,
`--muted`, `--line`, `--accent`, `--accent-text`, and good/warn/bad foreground and
background pairs. Both system dark mode and explicit `data-theme` overrides use
those variables. Feature styles consume these roles instead of copying colors.

STOP is the established exception: a solid `#b3261e` surface with white lettering
in both themes. Its emergency meaning must remain visible and distinct from the
ordinary outline danger button. Status always includes words as well as color.
In forced-colors mode, leave platform contrast and scroll affordances usable.

## Typography

Body copy and controls inherit the system sans stack, 14px with 1.45 line height.
Technical values use the existing mono stack and tabular numerals. The primary
radio frequency remains the largest readout; a program's reported frequency is
secondary and must not resemble a tunable control. Use sentence case and exact
action verbs. Decoded strings are literal text, may wrap, and are never HTML.

## Layout

The established document has 16px outer padding and 16px grid/card spacing.
Radio and dashboard columns grow from a 340px minimum, collapsing to one column
on phones. Existing controls keep their order and existing forms keep document
scrolling. The native WSJT-X program card may span the radio grid; its band
activity region owns its own bounded scrolling. Never size the page or a sibling
form to make a decode table fit.

Reserve the native status summary, activity viewport and footer geometry through
loading, empty, error and live states. Loading uses plain app-owned status text;
there is no skeleton animation. Full program access remains before the activity
list. Growing the displayed history must not move that action or RF controls.

## Elevation & Depth

Static content uses flat surfaces and thin borders. Dialog backdrops separate
transient work from the page. A status feature adds no shadow, gradient or new
overlay system.

## Shapes

Cards and dialogs use 8px corners, controls 6px and compact status badges pills.
Reuse these shapes; do not introduce another card or button family for native
program status.

## Components

| Role | Runtime owner | Consumers / verification |
| --- | --- | --- |
| Palette and theme | `dxberry.css` root variables | Shared cards, controls, badges; browser computed styles |
| Body and numeric type | `dxberry.css` body/clock and `vfo.css` readouts | Existing radio, dashboard and program values |
| Buttons | `.btn` and emphasis/intent modifiers | Existing actions and native retry/history controls |
| Emergency STOP | `.btn.vfo-stop` in `vfo.css` | White label contrast in light and dark browser checks |
| Cards and spacing | `.card`, `.vfo`, `.vfo-box` | Existing radio layout and native program card |
| Status feedback | Shared semantic color variables and `.badge` | Native text status; no per-decode live announcements |
| Scrollbars | Global application baseline in `dxberry.css` | All owned scroll regions, including native activity |
| Decode table geometry | Native program stylesheet | Fixed activity footprint; complete values at phone width |

Enabled actions have hover, visible keyboard focus and a pressed state. Disabled
controls use native semantics and explain the reason in visible text when needed.
Read-only data has no button chrome or hover treatment. Native program feedback
is persistent and inline; use the existing notice system for general app actions.
Existing native dialogs remain the overlay owner. This feature adds no forms,
dropdowns, date pickers, permissions or destructive actions.

The scrollbar baseline derives thumb/track/hover/active colors from semantic
variables, applies without an opt-in class, includes standards properties and
engine fallbacks, and permits forced-color defaults. Only geometry such as a
stable gutter belongs on a feature scroll region.

New native data appears without animation. User-selected older history stays
bounded; missing data is a dash or an explicit unavailable state. Distinguish a
quiet decode interval from lost program updates and distinguish program-reported
transmit state from verified RF output.

## Do's and Don'ts

- Do preserve the full program window and authoritative server-side controls.
- Do keep numeric columns comparable, times labeled UTC and unknowns explicit.
- Do compare the radio view with the existing dashboard in both themes.
- Don't infer reception failure from an empty activity list.
- Don't add decorative instrument controls or animate routine background data.
- Don't make a new feature redefine the global product identity.

### Reconciled baseline findings

The light-theme STOP label was red on red because `.btn.danger` overrode
`.vfo-stop`. The emergency variant receives an explicit white foreground;
its established red surface remains. The new activity scroller also establishes
the previously missing global scrollbar baseline. Other legacy workflows remain
outside this feature's migration scope. Official design-format lint and runtime
verification must be recorded before declaring this increment ready.
