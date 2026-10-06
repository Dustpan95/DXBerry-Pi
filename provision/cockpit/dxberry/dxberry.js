/* DXBerry page for Cockpit (console spec sections 6, 7 and 9): station status, services and power.
 * Every reading and every action is a command run as superuser through cockpit.spawn; this file only
 * shows what those commands return and never reads or writes a file itself. Text from the Pi goes on
 * the page as text nodes, never as HTML. */
"use strict";

const STATUS = "/opt/dxberry/bin/dxberry-status";
const LOGS = "/system/logs/#/?prio=debug&service=";
const REFRESH_MS = 10000;
const DASH = "—";
// Stopping these takes the station, the network or this page away, so Stop asks first.
const STOP_WARNINGS = {
  "graywolf.service": "APRS, the iGate and the digipeater stop until Graywolf is started again.",
  "dxberry-netwatch.service": "Network failover stops. If the Pi then loses its connection it will not switch over, and this page may become unreachable.",
  "cockpit.socket": "This console closes and stays unreachable until cockpit.socket is started again over SSH or the Pi restarts.",
};

/* fields-begin: every path of dxberry-status --json this page reads. tests/test_page.sh checks each
 * one against the command's real output; a * stands for every element or entry. */
const FIELDS = [
  "generated",
  "pi.ok", "pi.model", "pi.temp_c", "pi.load", "pi.mem_total", "pi.mem_used", "pi.disk_total", "pi.disk_used", "pi.uptime_s",
  "pi.throttle.under_voltage_now", "pi.throttle.freq_capped_now", "pi.throttle.throttled_now", "pi.throttle.soft_temp_limit_now",
  "pi.throttle.under_voltage_since_boot", "pi.throttle.freq_capped_since_boot", "pi.throttle.throttled_since_boot",
  "pi.throttle.soft_temp_limit_since_boot",
  "network.ok", "network.state", "network.address", "network.prefix", "network.gateway", "network.hostname",
  "network.wifi.ssid", "network.wifi.signal_dbm",
  "graywolf.ok", "graywolf.active", "graywolf.version", "graywolf.web_port", "graywolf.api_ok", "graywolf.api_error",
  "graywolf.igate.connected", "graywolf.igate.server", "graywolf.igate.rf_to_is_gated", "graywolf.igate.is_to_rf_gated",
  "graywolf.channels.*.name", "graywolf.channels.*.rx_frames", "graywolf.channels.*.tx_frames", "graywolf.channels.*.rx_bad_fcs",
  "graywolf.position_log.enabled", "graywolf.position_log.bytes",
  "radios.ok", "radios.radios.*.label", "radios.radios.*.present", "radios.radios.*.owner", "radios.radios.*.rigctld",
  "radios.radios.*.rigctld_port", "radios.radios.*.freq", "radios.radios.*.mode",
  "radios.gps.fix", "radios.gps.receiver", "radios.gps.grid", "radios.gps.sats_used", "radios.gps.sats_seen",
  "time.ok", "time.synced", "time.source", "time.reference", "time.stratum", "time.offset_ms",
  "release.ok", "release.dxberry", "release.dxberry_commit", "release.graywolf", "release.cockpit",
  "services.ok", "services.units.*.unit", "services.units.*.load", "services.units.*.active", "services.units.*.sub",
  "services.units.*.result", "services.units.*.type",
];
/* fields-end */

const state = { busy: false, stopped: false };

// ---- building blocks ----------------------------------------------------------------------
function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat(Infinity)) {
    if (k === null || k === undefined || k === false) continue;
    e.append(k instanceof Node ? k : String(k));
  }
  return e;
}
const badge = (text, kind) => el("span", { class: "badge " + kind }, text);
const btn = (label, fn, cls) => el("button", { class: "btn small" + (cls ? " " + cls : ""), type: "button", onclick: fn }, label);

function kv(rows) {
  const dl = el("dl", { class: "kv" });
  for (const [k, v] of rows) dl.append(el("dt", {}, k), el("dd", {}, v));
  return dl;
}

function table(head, rows) {
  return el("div", { class: "table-wrap" },
    el("table", {},
      el("thead", {}, el("tr", {}, head.map(h => el("th", {}, h)))),
      el("tbody", {}, rows.map(r => el("tr", {}, r.map(c => el("td", {}, c)))))));
}

function card(title, part, body, actions) {
  const c = el("section", { class: "card" }, el("h2", {}, title));
  if (!part || part.ok === false) {
    c.append(el("p", { class: "error" }, (part && part.error) || "No data."));
    return c;
  }
  c.append(...[].concat(body || []));
  if (actions && actions.length) c.append(el("div", { class: "actions" }, actions));
  return c;
}

// ---- formatting ---------------------------------------------------------------------------
const num = n => (typeof n === "number" ? n.toLocaleString("en-US") : DASH);
const temp = c => (typeof c === "number" ? `${Math.round(c * 9 / 5 + 32)}°F (${c.toFixed(1)}°C)` : DASH);

function bytes(b) {
  if (typeof b !== "number") return DASH;
  const units = ["bytes", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
  return `${i ? b.toFixed(1) : b} ${units[i]}`;
}

function uptime(s) {
  if (typeof s !== "number") return DASH;
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : `${m} min`;
}

function freq(f, mode) {
  const hz = Number(f);
  if (!f || f === "?" || Number.isNaN(hz)) return DASH;
  return `${(hz / 1e6).toFixed(3)} MHz${mode && mode !== "?" ? " " + mode : ""}`;
}

function signal(dbm) {
  if (typeof dbm !== "number") return DASH;
  const q = dbm >= -50 ? "excellent" : dbm >= -60 ? "good" : dbm >= -70 ? "fair" : "weak";
  return `${dbm} dBm (${q})`;
}

function powerState(t) {
  if (!t) return DASH;
  const now = [t.under_voltage_now && "under-voltage", t.throttled_now && "throttled",
    t.freq_capped_now && "frequency capped", t.soft_temp_limit_now && "soft temperature limit"].filter(Boolean);
  if (now.length) return badge("now: " + now.join(", "), "bad");
  const past = [t.under_voltage_since_boot && "under-voltage", t.throttled_since_boot && "throttling",
    t.freq_capped_since_boot && "frequency capping", t.soft_temp_limit_since_boot && "soft temperature limit"].filter(Boolean);
  if (past.length) return badge("since boot: " + past.join(", "), "warn");
  return badge("OK", "good");
}

function gps(g) {
  if (!g) return DASH;
  if (g.fix >= 2) return `${g.fix}D fix, grid ${g.grid}, ${g.sats_used} of ${g.sats_seen} satellites`;
  return g.receiver ? "receiver found, no fix yet" : "no receiver";
}

// ---- cards --------------------------------------------------------------------------------
function stationCard(g) {
  if (!g || !g.ok) return card("Station", g);
  const running = g.active === "active";
  const rows = [["Graywolf", [
    badge(running ? "running" : g.active === "failed" ? "failed" : "stopped", running ? "good" : g.active === "failed" ? "bad" : "warn"),
    " ", g.version ? `version ${g.version}` : "not installed"]]];
  const body = [];
  if (!g.api_ok) {
    rows.push(["Details", g.api_error || DASH]);
  } else {
    const ig = g.igate;
    rows.push(["iGate", ig ? [badge(ig.connected ? "connected" : "not connected", ig.connected ? "good" : "warn"), " ", ig.server,
      ` · ${num(ig.rf_to_is_gated)} sent to APRS-IS, ${num(ig.is_to_rf_gated)} to RF`] : DASH]);
    const pl = g.position_log;
    rows.push(["Position log", pl ? (pl.enabled ? `on, ${bytes(pl.bytes)} in RAM` : "off") : DASH]);
  }
  body.push(kv(rows));
  if (g.api_ok) {
    body.push(g.channels.length
      ? table(["Channel", "Received", "Sent", "Bad FCS"], g.channels.map(c => [c.name, num(c.rx_frames), num(c.tx_frames), num(c.rx_bad_fcs)]))
      : el("p", { class: "muted" }, "No radio channels yet."));
  }
  return card("Station", g, body, [
    el("a", { class: "btn primary", href: `http://${location.hostname}:${g.web_port || 8080}/`, target: "_blank", rel: "noopener noreferrer" }, "Open Graywolf"),
    btn("Graywolf log", () => openLog("graywolf.service")),
    btn("Restart Graywolf", () => unitAction("restart", "graywolf.service")),
  ]);
}

function radiosCard(r) {
  if (!r || !r.ok) return card("Radios", r);
  const names = Object.keys(r.radios || {});
  const body = [];
  if (!names.length) {
    body.push(el("p", { class: "muted" }, "No radios set up. Plug one in, then over SSH run: sudo dxberry-radio scan"));
  } else {
    body.push(table(["Radio", "State", "Owner", "rigctld", "Frequency"], names.map(n => {
      const x = r.radios[n];
      return [[el("strong", {}, n), x.label ? " " + x.label : ""],
        badge(x.present ? "present" : "unplugged", x.present ? "good" : "warn"),
        x.owner || "none", `${x.rigctld} on port ${x.rigctld_port}`, freq(x.freq, x.mode)];
    })));
  }
  return card("Radios", r, body);
}

function networkCard(n) {
  if (!n || !n.ok) return card("Network", n);
  const how = { ETH: "Ethernet", WIFI: "WiFi", NONE: "no connection" }[n.state] || n.state;
  const rows = [
    ["Connection", badge(how, n.state === "ETH" || n.state === "WIFI" ? "good" : "bad")],
    ["Address", n.address ? `${n.address}/${n.prefix}` : DASH],
    ["Gateway", n.gateway || DASH],
    ["Hostname", n.hostname || DASH],
  ];
  if (n.wifi) rows.push(["WiFi network", n.wifi.ssid || DASH], ["Signal", signal(n.wifi.signal_dbm)]);
  return card("Network", n, [kv(rows)]);
}

function piCard(p) {
  if (!p || !p.ok) return card("Pi", p);
  return card("Pi", p, [kv([
    ["Model", p.model || DASH],
    ["Temperature", temp(p.temp_c)],
    ["Power", powerState(p.throttle)],
    ["Load", Array.isArray(p.load) ? p.load.map(x => Number(x).toFixed(2)).join("  ") : DASH],
    ["Memory", `${bytes(p.mem_used)} of ${bytes(p.mem_total)}`],
    ["Disk", `${bytes(p.disk_used)} of ${bytes(p.disk_total)}`],
    ["Up", uptime(p.uptime_s)],
  ])]);
}

// The GPS readout comes with the radios part (dxberry-radio status --json carries it).
function timeCard(t, r) {
  if (!t || !t.ok) return card("Time and GPS", t);
  return card("Time and GPS", t, [kv([
    ["Clock", badge(t.synced ? "synced" : "not synced", t.synced ? "good" : "warn")],
    ["Source", t.source === "none" ? DASH : t.source === "NTP" ? `NTP ${t.reference} (stratum ${t.stratum})` : t.source],
    ["Offset", typeof t.offset_ms === "number" ? `${t.offset_ms.toFixed(1)} ms` : DASH],
    ["GPS", r && r.ok ? gps(r.gps) : DASH],
  ])]);
}

function aboutCard(r) {
  if (!r || !r.ok) return card("About", r);
  return card("About", r, [kv([
    ["DXBerry-Pi", r.dxberry ? r.dxberry + (r.dxberry_commit ? ` (${r.dxberry_commit})` : "") : DASH],
    ["Graywolf", r.graywolf || "not installed"],
    ["Cockpit", r.cockpit || DASH],
  ])]);
}

function unitState(u) {
  if (u.load === "not-found") return badge("not installed", "muted");
  if (u.active === "active") return badge(u.sub || "running", "good");
  if (u.active === "failed") return badge("failed", "bad");
  if (["activating", "deactivating", "reloading"].includes(u.active)) return badge(u.active, "warn");
  if (u.type === "oneshot") return u.result === "success" ? badge("done", "muted") : badge("last run: " + u.result, "bad");
  return badge("stopped", "warn");
}

function servicesCard(s) {
  if (!s || !s.ok) return card("Services", s);
  const rows = s.units.map(u => {
    if (u.load === "not-found") return [u.unit, unitState(u), "", ""];
    const acts = u.active === "active"
      ? [btn("Stop", () => unitAction("stop", u.unit)), btn("Restart", () => unitAction("restart", u.unit))]
      : [btn("Start", () => unitAction("start", u.unit))];
    return [u.unit, unitState(u), el("span", { class: "row-actions" }, acts), btn("Log", () => openLog(u.unit), "link")];
  });
  return card("Services", s, [table(["Unit", "State", "", ""], rows)]);
}

function powerCard() {
  return el("section", { class: "card" }, el("h2", {}, "Power"),
    el("p", { class: "muted" }, "Restarting or shutting down stops Graywolf and every radio until the Pi is back."),
    el("div", { class: "actions" },
      btn("Restart the Pi", () => power("reboot.target"), "danger"),
      btn("Shut down the Pi", () => power("poweroff.target"), "danger")));
}

// ---- actions ------------------------------------------------------------------------------
function run(args) {
  return cockpit.spawn(args, { superuser: "require", err: "message" });
}

function openLog(unit) {
  cockpit.jump(LOGS + encodeURIComponent(unit));
}

function notice(text, kind, sticky) {
  const n = el("div", { class: "notice " + kind, role: "status" }, el("span", {}, text));
  if (!sticky) n.append(el("button", { class: "close", type: "button", "aria-label": "Dismiss", onclick: () => n.remove() }, "×"));
  document.getElementById("notices").replaceChildren(n);
  if (kind === "good") setTimeout(() => n.remove(), 6000);
}

function failure(what, ex) {
  if (ex && (ex.problem === "access-denied" || ex.problem === "not-authorized")) {
    notice("Administrative access is off. Turn it on with the “Limited access” button at the top of the page, then try again.", "bad");
    return;
  }
  const code = ex && ex.exit_status ? ` (exit ${ex.exit_status})` : "";
  notice(`${what} failed${code}: ${(ex && (ex.message || ex.problem)) || "unknown error"}`, "bad");
}

function confirmThen(title, text, label, action) {
  const d = document.getElementById("confirm");
  d.querySelector("h2").textContent = title;
  d.querySelector("p").textContent = text;
  d.querySelector(".ok").textContent = label;
  d.returnValue = "";
  d.onclose = () => { if (d.returnValue === "ok") action(); };
  d.showModal();
}

function unitAction(verb, unit) {
  const go = () => run(["systemctl", verb, unit]).then(
    () => { notice(`${unit}: ${verb} done.`, "good"); refresh(true); },
    ex => failure(`systemctl ${verb} ${unit}`, ex));
  if (verb === "stop" && STOP_WARNINGS[unit]) confirmThen(`Stop ${unit}?`, STOP_WARNINGS[unit], "Stop", go);
  else go();
}

// The connection drops as the Pi goes down; Cockpit then shows its own Disconnected screen, from
// which the operator reconnects and logs in again once the Pi is back.
function power(target) {
  const reboot = target === "reboot.target";
  confirmThen(reboot ? "Restart the Pi?" : "Shut down the Pi?",
    reboot ? "The Pi restarts now. Log in again when it is back, usually within two minutes."
      : "The Pi turns off and stays off until its power is cycled.",
    reboot ? "Restart" : "Shut down",
    () => {
      state.stopped = true;
      notice(reboot ? "Pi restarting, reconnecting…" : "Pi shutting down…", "warn", true);
      run(["systemctl", "--no-block", "start", target]).then(null, ex => {
        // "disconnected"/"terminated": the connection going away as the Pi goes down, not a failure.
        if (ex && (ex.problem === "disconnected" || ex.problem === "terminated")) return;
        state.stopped = false;
        failure(reboot ? "Restart" : "Shut down", ex);
        refresh(true);
      });
    });
}

// ---- refresh ------------------------------------------------------------------------------
function render(s) {
  document.getElementById("host").textContent = (s.network && s.network.hostname) || "";
  document.getElementById("updated").textContent = s.generated ? "updated " + new Date(s.generated).toLocaleTimeString("en-US") : "";
  document.getElementById("cards").replaceChildren(
    stationCard(s.graywolf), radiosCard(s.radios), networkCard(s.network), piCard(s.pi),
    timeCard(s.time, s.radios), aboutCard(s.release), servicesCard(s.services), powerCard());
}

function refresh(force) {
  if (state.busy || state.stopped || (cockpit.hidden && force !== true)) return;
  state.busy = true;
  run([STATUS, "--json"]).then(out => {
    state.busy = false;
    let s;
    try { s = JSON.parse(out); } catch (e) { notice("dxberry-status did not return a report.", "bad"); return; }
    render(s);
  }, ex => { state.busy = false; failure("Reading the station status", ex); });
}

function applyTheme() {
  let style = "auto";
  try { style = localStorage.getItem("shell:style") || "auto"; } catch (e) { /* storage blocked: follow the system */ }
  if (style === "dark" || style === "light") document.documentElement.dataset.theme = style;
  else delete document.documentElement.dataset.theme;
}

function init() {
  applyTheme();
  window.addEventListener("storage", applyTheme);
  document.getElementById("refresh").addEventListener("click", () => refresh(true));
  cockpit.addEventListener("visibilitychange", () => { if (!cockpit.hidden) refresh(true); });
  refresh(true);
  setInterval(() => refresh(false), REFRESH_MS);
}

init();
