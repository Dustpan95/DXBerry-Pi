/* DXBerry page for Cockpit (console spec sections 6, 7 and 9): station status, services and power.
 * Every reading and every action is a command run as superuser through cockpit.spawn; this file only
 * shows what those commands return and never reads or writes a file itself. Text from the Pi goes on
 * the page as text nodes, never as HTML. */
"use strict";

const STATUS = "/opt/dxberry/bin/dxberry-status";
const LOGS = "/system/logs/#/?prio=debug&service=";
const REFRESH_MS = 10000;
const DASH = "—";
const RADIO = "/opt/dxberry/bin/dxberry-radio";
// dxberry-radio's exit codes in words (radio plumbing spec section 6.2).
const RADIO_EXITS = {
  1: "this needs administrative access",
  2: "the request was not valid",
  3: "there is no such radio or application",
  4: "the device is not plugged in",
  5: "the application could not take the radio, so it was left released",
  6: "applying the change failed",
  7: "the current owner could not be re-wired",
};
const PTT_METHODS = { rigctld: "rigctld", cm108: "CM108 HID", gpio: "Pi GPIO", vox: "VOX", digirig_tone: "DigiRig Lite tone", none: "none" };
const PTT_TYPES = { RIG: "a CAT command", RTS: "the RTS line", DTR: "the DTR line", NONE: "nothing" };
const FUNCTION_KINDS = { audio: "sound", serial: "serial", hid: "HID" };
const NAME_RE = /^[a-z][a-z0-9]{0,11}$/;
const PTT_METHOD_CHOICES = [["rigctld", "rigctld (CAT command or a serial line)"], ["cm108", "CM108 HID (the sound card's PTT pin)"],
  ["gpio", "Pi GPIO pin"], ["vox", "VOX (the radio keys on audio)"], ["digirig_tone", "DigiRig Lite tone"], ["none", "None (receive only)"]];
const PTT_TYPE_CHOICES = [["RIG", "A CAT command"], ["RTS", "The RTS line"], ["DTR", "The DTR line"], ["NONE", "Nothing"]];
const BAUDS = [0, 4800, 9600, 19200, 38400, 57600, 115200];
// Stopping these takes the station, the network or this page away, so Stop asks first.
const STOP_WARNINGS = {
  "graywolf.service": "APRS, the iGate and the digipeater stop until Graywolf is started again.",
  "dxberry-netwatch.service": "Network failover stops. If the Pi then loses its connection it will not switch over, and this page may become unreachable.",
  "cockpit.socket": "This console closes and stays unreachable until cockpit.socket is started again over SSH or the Pi restarts.",
};
// Restarting cockpit.socket drops this console's session too, just like Stop, so it asks first
// as well - but it comes back on its own, so the warning is softer than Stop's.
const RESTART_WARNINGS = {
  "cockpit.socket": "This console disconnects while cockpit.socket restarts. It comes back within a few seconds; reconnect and log in again.",
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
  "radios.radios.*.rigctld_port", "radios.radios.*.freq", "radios.radios.*.mode", "radios.radios.*.wiring",
  "radios.radios.*.alsa_id", "radios.radios.*.kernel.audio", "radios.radios.*.kernel.cat", "radios.radios.*.kernel.hid",
  "radios.radios.*.kernel.ptt_serial", "radios.radios.*.audio.path", "radios.radios.*.cat.path", "radios.radios.*.hid.path",
  "radios.radios.*.ptt_serial", "radios.radios.*.ptt.method", "radios.radios.*.ptt.gpio_line",
  "radios.radios.*.rig.model", "radios.radios.*.rig.baud", "radios.radios.*.rig.ptt_type",
  "radios.candidates.*.index", "radios.candidates.*.port", "radios.candidates.*.name", "radios.candidates.*.profile",
  "radios.candidates.*.defaults.ptt", "radios.candidates.*.defaults.ptt_type", "radios.candidates.*.defaults.cat",
  "radios.candidates.*.defaults.model", "radios.candidates.*.defaults.baud",
  "radios.candidates.*.functions.*.kind", "radios.candidates.*.functions.*.kernel", "radios.candidates.*.functions.*.path",
  "radios.candidates.*.functions.*.product",
  "radios.apps.*.name", "radios.apps.*.label",
  "radios.gps.fix", "radios.gps.receiver", "radios.gps.grid", "radios.gps.sats_used", "radios.gps.sats_seen",
  "time.ok", "time.synced", "time.source", "time.reference", "time.stratum", "time.offset_ms",
  "release.ok", "release.dxberry", "release.dxberry_commit", "release.graywolf", "release.cockpit",
  "release.update.graywolf", "release.update.dxberry", "release.update.system",
  "services.ok", "services.units.*.unit", "services.units.*.load", "services.units.*.active", "services.units.*.sub",
  "services.units.*.result", "services.units.*.type",
];
/* fields-end */
/* radio-fields-begin: what the page reads from dxberry-radio's own --json answers, as COMMAND:PATH
 * ('models' = dxberry-radio models; 'add' = the answer of add, set and claim, which is the status
 * block; 'release' = the {"ok":true} answer of release and remove). tests/test_page.sh checks each
 * one against the command's real output. */
const RADIO_FIELDS = ["models:*.model", "models:*.mfg", "models:*.name", "add:warnings", "release:warnings"];
/* radio-fields-end */

const state = { busy: false, stopped: false, again: false };
// The last report that parsed. Cards are drawn from it, and actions and dialogs read the station
// from it between refreshes.
let last = null;
// radio name -> what is running for it right now ("Giving to Graywolf"); no prototype, so a radio
// named like a member every object inherits (constructor is a valid name) is not busy
const pending = Object.create(null);
let models = null;    // Hamlib's rig models once dxberry-radio models has answered
let modelsLoading = null;
let modelsTried = false;   // radiosCard asks once per page load; opening the form asks again after a failure
let form = null;      // the open radio form: {edit, name, x (the radio when editing), saving}
// The notice a failed refresh is currently showing (or null). Cleared, and the notice removed,
// the moment a later refresh succeeds - but a notice from an action (Restart done, etc.) is a
// different one and is left alone.
let refreshFailureNotice = null;

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
// btn LABEL FN [OPTS]: OPTS.cls adds classes; OPTS.key keeps keyboard focus across a refresh
// (render() refocuses the button carrying the same key); OPTS.aria names what the button acts on
// for screen readers; OPTS.disabled and OPTS.title as on any button.
const btn = (label, fn, opts) => {
  const o = opts || {};
  return el("button", { class: "btn small" + (o.cls ? " " + o.cls : ""), type: "button", onclick: fn,
    "data-key": o.key, "aria-label": o.aria, disabled: !!o.disabled, title: o.title }, label);
};
const srOnly = text => el("span", { class: "sr-only" }, text);

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

function card(title, part, body, actions, cls) {
  const c = el("section", { class: "card" + (cls ? " " + cls : "") }, el("h2", {}, title));
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

// activeBadge: a unit's ActiveState in words; starting and stopping are not "stopped".
function activeBadge(active) {
  if (active === "active") return badge("running", "good");
  if (active === "failed") return badge("failed", "bad");
  if (["activating", "deactivating", "reloading"].includes(active)) return badge(active, "warn");
  return badge("stopped", "warn");
}

function gps(g) {
  if (!g) return DASH;
  if (g.fix >= 2) return `${g.fix}D fix, grid ${g.grid}, ${g.sats_used} of ${g.sats_seen} satellites`;
  return g.receiver ? "receiver found, no fix yet" : "no receiver";
}

// ---- cards --------------------------------------------------------------------------------
function stationCard(g) {
  if (!g || !g.ok) return card("Station", g);
  const rows = [["Graywolf", [activeBadge(g.active), " ", g.version ? `version ${g.version}` : "not installed"]]];
  const body = [];
  if (!g.api_ok) {
    rows.push(["Details", g.api_error || DASH]);
  } else {
    const ig = g.igate;
    rows.push(["iGate", ig ? [badge(ig.connected ? "connected" : "not connected", ig.connected ? "good" : "warn"), " ", ig.server,
      ` · ${num(ig.rf_to_is_gated)} sent to APRS-IS, ${num(ig.is_to_rf_gated)} to RF`] : DASH]);
    const pl = g.position_log;
    rows.push(["Position log", pl ? (pl.enabled ? (typeof pl.bytes === "number" ? `on, ${bytes(pl.bytes)} in RAM` : "on (in RAM)") : "off") : DASH]);
  }
  body.push(kv(rows));
  if (g.api_ok) {
    body.push(g.channels.length
      ? table(["Channel", "Received", "Sent", "Bad FCS"], g.channels.map(c => [c.name, num(c.rx_frames), num(c.tx_frames), num(c.rx_bad_fcs)]))
      : el("p", { class: "muted" }, "No radio channels yet."));
  }
  return card("Station", g, body, [
    el("a", { class: "btn primary", href: `http://${location.hostname}:${g.web_port || 8080}/`, target: "_blank", rel: "noopener noreferrer", "data-key": "gw:open" }, "Open Graywolf"),
    btn("Graywolf log", () => openLog("graywolf.service"), { key: "gw:log" }),
    btn("Restart Graywolf", () => unitAction("restart", "graywolf.service"), { key: "gw:restart" }),
  ]);
}

// ---- radios -------------------------------------------------------------------------------
// loadModels: Hamlib's rig list, fetched once per page load (names for the cards, choices for the form).
// Any failure - the command's, or an answer that does not parse - is forgotten, so the next ask retries.
function loadModels() {
  if (!modelsLoading) {
    modelsLoading = run([RADIO, "models", "--json"]).then(out => (models = JSON.parse(out)))
      .then(null, ex => { modelsLoading = null; throw ex; });
  }
  return modelsLoading;
}

function modelName(n) {
  const m = models && models.find(x => x.model === n);
  return m ? `${m.mfg} ${m.name} (model ${n})`.replace("  ", " ") : `model ${n}`;
}

function appLabel(r, name) {
  const a = ((r && r.apps) || []).find(x => x.name === name);
  return a ? a.label : name;
}

function warningsOf(out) {
  try {
    const j = JSON.parse(out);
    return Array.isArray(j.warnings) ? j.warnings : [];
  } catch (e) {
    return [];
  }
}

// ownsOthers: whether APP owns a radio besides NAME. When it does not, Release and Remove stop it.
function ownsOthers(app, name) {
  return Object.entries(last.radios.radios).some(([n, x]) => n !== name && x.owner === app);
}

// handMadeChannels: Graywolf's channels not named after a DXBerry radio - made by hand (spec 8.3).
function handMadeChannels() {
  const g = last && last.graywolf;
  if (!g || !g.ok || !g.api_ok) return [];
  const radios = (last.radios && last.radios.radios) || {};
  return (g.channels || []).map(c => c.name).filter(c => !Object.prototype.hasOwnProperty.call(radios, c));
}

function parts(x) {
  const k = x.kernel || {};
  const bits = [];
  if (x.audio) bits.push(`sound ${k.audio || "absent"}${x.alsa_id ? ` (${x.alsa_id})` : ""}`);
  if (x.cat) bits.push(`CAT ${k.cat || "absent"}`);
  if (x.ptt_serial) bits.push(`PTT serial ${k.ptt_serial || "absent"}`);
  if (x.hid) bits.push(`HID ${k.hid || "absent"}`);
  return bits.join(" · ") || DASH;
}

function pttText(x) {
  const m = x.ptt ? x.ptt.method : "";
  let t = PTT_METHODS[m] || m || DASH;
  if (m === "rigctld" && x.rig) t += `, keyed by ${PTT_TYPES[x.rig.ptt_type] || x.rig.ptt_type}`;
  if (m === "gpio" && typeof x.ptt.gpio_line === "number") t += ` line ${x.ptt.gpio_line}`;
  return t;
}

function radioActions(r, n, x, busy) {
  const acts = [];
  for (const a of r.apps || []) {
    if (a.name === x.owner) continue;
    acts.push(btn(`Give to ${a.label}`, () => giveTo(n, a), { key: `radio:${n}:give:${a.name}`, aria: `Give ${n} to ${a.label}`,
      disabled: busy || !x.present, title: x.present ? null : "Plug the radio in first" }));
  }
  if (x.owner) acts.push(btn("Release", () => release(n), { key: `radio:${n}:release`, aria: `Release ${n}`, disabled: busy }));
  acts.push(btn("Edit", () => openRadioForm(n), { key: `radio:${n}:edit`, aria: `Edit ${n}`, disabled: busy }));
  acts.push(btn("Remove", () => removeRadio(n), { cls: "danger", key: `radio:${n}:remove`, aria: `Remove ${n}`, disabled: busy }));
  return acts;
}

function radioBlock(r, n) {
  const x = r.radios[n];
  const busy = pending[n];
  const owner = x.owner ? appLabel(r, x.owner) : "";
  return el("div", { class: "radio" },
    el("div", { class: "radio-head" }, el("strong", {}, n), x.label ? " " + x.label : "", " ",
      badge(x.present ? "present" : "unplugged", x.present ? "good" : "warn"), " ",
      badge(owner ? `owned by ${owner}` : "not in use", owner ? "good" : "muted"),
      busy ? [" ", badge(busy + "…", "warn")] : null),
    kv([
      ["Parts", parts(x)],
      ["Rig", `${modelName(x.rig && x.rig.model)}${x.rig && x.rig.baud ? `, ${x.rig.baud} baud` : ""}`],
      ["PTT", pttText(x)],
      ["rigctld", `${x.rigctld} on port ${x.rigctld_port}`],
      ["Frequency", freq(x.freq, x.mode)],
      ["Wiring", x.wiring === "names" ? "stable names only (you set up the application)" : "full (DXBerry sets up the application)"],
    ]),
    el("div", { class: "actions" }, radioActions(r, n, x, !!busy)));
}

function candidateBlock(r, c) {
  const fns = (c.functions || []).map(f => `${FUNCTION_KINDS[f.kind] || f.kind} ${f.kernel}`).join(" · ");
  const product = (c.functions || []).map(f => f.product).find(Boolean);
  return el("div", { class: "radio" },
    el("div", { class: "radio-head" }, el("strong", {}, c.name), " ",
      c.profile === "generic" ? badge("no profile", "muted") : badge("known interface", "good")),
    kv([["USB port", c.port], ["Parts", fns || DASH], ["Reports as", product || DASH]]),
    el("div", { class: "actions" }, candidateActions(r, c)));
}

function candidateActions(r, c) {
  return [btn("Add", () => openRadioForm(null, c), { cls: "primary", key: `cand:${c.port}:add`, aria: `Add the interface on ${c.port}` })];
}

function radiosCard(r) {
  if (!r || !r.ok) return card("Radios", r, null, null, "wide");
  const names = Object.keys(r.radios || {});
  const cands = r.candidates || [];
  if (names.length && !modelsTried) {
    modelsTried = true;
    loadModels().then(show, () => {});
  }
  const body = [];
  if (!names.length && !cands.length) {
    body.push(el("p", { class: "muted" }, "No radios set up, and no USB radio interface is plugged in. Plug one in; it shows up here within 10 seconds."));
  }
  for (const n of names) body.push(radioBlock(r, n));
  if (cands.length) {
    body.push(el("h3", {}, "Plugged in, not set up"));
    for (const c of cands) body.push(candidateBlock(r, c));
  }
  return card("Radios", r, body, null, "wide");
}

// radioAction: run one dxberry-radio change for radio NAME, marked busy on the page meanwhile.
// T = {busy, what, done}: the badge while it runs, the failure's subject, the success notice.
function radioAction(name, t, args) {
  pending[name] = t.busy;
  show();
  return run([RADIO, ...args, "--json"]).then(out => {
    delete pending[name];
    notice(t.done, "good");
    for (const w of warningsOf(out)) notice(`${name}: ${w}`, "warn");
    refresh(true);
  }, ex => {
    delete pending[name];
    failure(t.what, ex, RADIO_EXITS);
    show();
    refresh(true);
  });
}

function giveTo(name, app) {
  const x = last.radios.radios[name];
  const go = () => radioAction(name, { busy: `Giving to ${app.label}`, what: `Giving ${name} to ${app.label}`, done: `${name} now belongs to ${app.label}.` },
    ["claim", name, app.name]);
  const warn = [];
  if (x.owner) {
    const prev = appLabel(last.radios, x.owner);
    warn.push(ownsOthers(x.owner, name) ? `${prev} lets go of ${name} first.` : `${prev} lets go of ${name} first and stops, because it owns no other radio.`);
  }
  // DXBerry makes Graywolf a channel for the radio only with full wiring (names wiring leaves
  // Graywolf to the operator), so only then can a hand-made channel compete with it
  if (app.name === "graywolf" && x.wiring !== "names") {
    const g = last.graywolf;
    const hand = handMadeChannels();
    if (!g || !g.ok || !g.api_ok) {
      warn.push("Graywolf is not answering, so its channels made by hand could not be checked.");
    } else if (hand.length) {
      warn.push(`Graywolf already has channels made by hand: ${hand.join(", ")}. It gets a new channel named ${name} for this radio; ` +
        "if a hand-made channel uses the same sound card, the two compete for it. Remove or re-point the hand-made channel in Graywolf's page.");
    }
  }
  if (warn.length) confirmThen(`Give ${name} to ${app.label}?`, warn.join(" "), `Give to ${app.label}`, go);
  else go();
}

function release(name) {
  const x = last.radios.radios[name];
  const label = appLabel(last.radios, x.owner);
  const go = () => radioAction(name, { busy: "Releasing", what: `Releasing ${name} from ${label}`, done: `${name} released.` }, ["release", name]);
  let text = `${label} stops using ${name}.`;
  if (x.owner === "graywolf" && x.wiring !== "names") text += ` Graywolf's channel named ${name} is deleted.`;
  if (!ownsOthers(x.owner, name)) {
    text += ` ${label} owns no other radio, so it stops altogether`;
    const hand = x.owner === "graywolf" ? handMadeChannels() : [];
    text += hand.length ? `, and its hand-made channels (${hand.join(", ")}) stop with it until it is started again under Services.` : ".";
  }
  confirmThen(`Release ${name}?`, text, "Release", go);
}

function removeRadio(name) {
  const x = last.radios.radios[name];
  const go = () => radioAction(name, { busy: "Removing", what: `Removing ${name}`, done: `${name} removed.` }, ["remove", name]);
  let text = `DXBerry forgets ${name} and stops its rigctld. The device itself is untouched and shows up under “Plugged in, not set up” while it is plugged in.`;
  if (x.owner) {
    const label = appLabel(last.radios, x.owner);
    text = (ownsOthers(x.owner, name) ? `${label} lets go of it first. ` : `${label} lets go of it first and stops, because it owns no other radio. `) + text;
  }
  confirmThen(`Remove ${name}?`, text, "Remove", go);
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
    ["Load", Array.isArray(p.load) ? p.load.map(x => (typeof x === "number" ? x.toFixed(2) : DASH)).join("  ") : DASH],
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
  const rows = [
    ["DXBerry-Pi", r.dxberry ? r.dxberry + (r.dxberry_commit ? ` (${r.dxberry_commit})` : "") : DASH],
    ["Graywolf", r.graywolf || "not installed"],
    ["Cockpit", r.cockpit || DASH],
  ];
  if (r.update) {
    rows.push(["Updates", r.update.graywolf || r.update.dxberry || r.update.system > 0
      ? badge("update available", "warn") : badge("up to date", "good")]);
  }
  return card("About", r, [kv(rows)]);
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
      ? [btn("Stop", () => unitAction("stop", u.unit), { key: `unit:${u.unit}:stop`, aria: `Stop ${u.unit}` }),
        btn("Restart", () => unitAction("restart", u.unit), { key: `unit:${u.unit}:restart`, aria: `Restart ${u.unit}` })]
      : [btn("Start", () => unitAction("start", u.unit), { key: `unit:${u.unit}:start`, aria: `Start ${u.unit}` })];
    return [u.unit, unitState(u), el("span", { class: "row-actions" }, acts),
      btn("Log", () => openLog(u.unit), { cls: "link", key: `unit:${u.unit}:log`, aria: `Log of ${u.unit}` })];
  });
  return card("Services", s, [table(["Unit", "State", srOnly("Actions"), srOnly("Log")], rows)]);
}

function powerCard() {
  return el("section", { class: "card" }, el("h2", {}, "Power"),
    el("p", { class: "muted" }, "Restarting or shutting down stops Graywolf and every radio until the Pi is back."),
    el("div", { class: "actions" },
      btn("Restart the Pi", () => power("reboot.target"), { cls: "danger", key: "power:reboot" }),
      btn("Shut down the Pi", () => power("poweroff.target"), { cls: "danger", key: "power:off" })));
}

// ---- actions ------------------------------------------------------------------------------
// run ARGS [INPUT]: the command as superuser; INPUT (passwords, for one) goes to its standard input,
// never into ARGS. Cockpit's spawn promise sends it with input() and then closes stdin.
function run(args, input) {
  const p = cockpit.spawn(args, { superuser: "require", err: "message" });
  return input === undefined ? p : p.input(input);
}

function openLog(unit) {
  cockpit.jump(LOGS + encodeURIComponent(unit));
}

function notice(text, kind, sticky) {
  const n = el("div", { class: "notice " + kind }, el("span", {}, text));
  if (!sticky) n.append(el("button", { class: "close", type: "button", "aria-label": "Dismiss", onclick: () => n.remove() }, "×"));
  document.getElementById("notices").append(n);
  if (kind === "good") setTimeout(() => n.remove(), 6000);
  return n;
}

// errorText: the useful part of a DXBerry command's stderr - its WARN and ERROR lines without the
// timestamp, and any line that is not a log line (usage errors, validator reasons). INFO lines
// are progress, not the problem.
function errorText(msg) {
  return String(msg || "").split("\n")
    .filter(l => l.trim() && !/^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d \[INFO\] /.test(l))
    .map(l => l.replace(/^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d \[(WARN|ERROR)\] /, "").trim())
    .join(" ");
}

// problemText: what went wrong, in words - the exit code's plain meaning (from EXITS, when the
// command has a table), then the command's own error text.
function problemText(ex, exits) {
  const code = ex && ex.exit_status;
  const head = code ? `Exit ${code}${exits && exits[code] ? ": " + exits[code] : ""}. ` : "";
  return head + (errorText(ex && ex.message) || (ex && ex.problem) || "unknown error");
}

function failure(what, ex, exits) {
  if (ex && (ex.problem === "access-denied" || ex.problem === "not-authorized")) {
    return notice("Administrative access is off. Turn it on with the “Limited access” button at the top of the page, then try again.", "bad");
  }
  return notice(`${what} failed. ${problemText(ex, exits)}`, "bad");
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
  else if (verb === "restart" && RESTART_WARNINGS[unit]) confirmThen(`Restart ${unit}?`, RESTART_WARNINGS[unit], "Restart", go);
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

// ---- the radio form -----------------------------------------------------------------------
function field(label, id, control, hint) {
  return el("div", { class: "field" }, el("label", { for: id }, label), control, hint ? el("p", { class: "hint muted" }, hint) : null);
}

function choice(id, choices, value) {
  const s = el("select", { id });
  for (const [v, t] of choices) s.append(el("option", { value: v }, t));
  s.value = String(value);
  return s;
}

// fnChoices: the plugged-in functions of KIND that no radio pins yet, as [port path, text] choices.
function fnChoices(kind) {
  const out = [];
  for (const c of (last.radios.candidates || [])) {
    for (const f of c.functions || []) if (f.kind === kind) out.push([f.path, `${f.kernel} · ${c.name} · ${f.path}`]);
  }
  return out;
}

function nextRadioName() {
  for (let i = 1; i < 100; i++) if (!Object.prototype.hasOwnProperty.call(last.radios.radios, `radio${i}`)) return `radio${i}`;
  return "";
}

function modelField(current) {
  if (!models) {
    return field("Hamlib rig model", "rf-model", el("input", { id: "rf-model", type: "number", min: "1", value: String(current || 1) }),
      "Hamlib's list could not be read; type the model number (1 when the radio has no CAT control).");
  }
  const filter = el("input", { id: "rf-model-filter", type: "search", placeholder: "Search: maker, name or number", autocomplete: "off",
    "aria-label": "Search Hamlib's rig models" });
  const sel = el("select", { id: "rf-model", size: "6" });
  const sorted = models.slice().sort((a, b) => `${a.mfg} ${a.name}`.localeCompare(`${b.mfg} ${b.name}`));
  const hits = (m, q) => `${m.mfg} ${m.name} ${m.model}`.toLowerCase().includes(q);
  const fill = () => {
    const q = filter.value.trim().toLowerCase();
    const keep = Number(sel.value || current || 1);
    sel.replaceChildren(...sorted
      .filter(m => m.model === keep || !q || hits(m, q))
      .map(m => el("option", { value: String(m.model) }, `${m.mfg} ${m.name} (${m.model})`.replace("  ", " "))));
    sel.value = String(keep);
  };
  filter.addEventListener("input", fill);
  // Enter in the search box would submit the whole form; it picks the first model the search finds instead
  filter.addEventListener("keydown", e => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    const q = filter.value.trim().toLowerCase();
    const first = q && sorted.find(m => hits(m, q));
    if (first) { sel.value = String(first.model); fill(); }
  });
  fill();
  return el("div", { class: "field" }, el("label", { for: "rf-model" }, "Hamlib rig model"), filter, sel,
    el("p", { class: "hint muted" }, "Hamlib Dummy (1) when the radio has no CAT control."));
}

function syncPttFields() {
  const m = document.getElementById("rf-ptt").value;
  document.getElementById("rf-gpio").closest(".field").hidden = m !== "gpio";
  document.getElementById("rf-ptt-type").closest(".field").hidden = m !== "rigctld";
}

function setFormError(text) {
  const p = document.getElementById("radio-form-error");
  p.textContent = text;
  p.hidden = !text;
}

function setSaving(on) {
  const b = document.getElementById("radio-save");
  b.disabled = on;
  b.textContent = on ? "Saving…" : "Save";
}

// openRadioForm NAME CAND: edit radio NAME, or (NAME null) add the interface CAND. Hamlib's list is
// fetched first; without it the model is a number field.
function openRadioForm(name, cand) {
  const open = () => fillRadioForm(name, cand);
  loadModels().then(open, open);
}

function fillRadioForm(name, cand) {
  const edit = !!name;
  const x = edit ? last.radios.radios[name] : null;
  form = { edit, name, x, saving: false };
  const df = (cand && cand.defaults) || {};
  const own = kind => cand && (cand.functions || []).find(f => f.kind === kind);
  const keep = pin => [["", `Keep: ${pin ? pin.path : "none"}`]];
  const bauds = BAUDS.slice();
  const baud = edit ? x.rig.baud : (df.baud || 0);
  // the pin renames the card's ALSA id, which a channel made by hand in Graywolf refers to
  const hand = edit ? [] : handMadeChannels();
  if (!bauds.includes(baud)) bauds.push(baud);
  bauds.sort((a, b) => a - b);
  document.getElementById("radio-dialog-title").textContent = edit ? `Edit ${name}` : `Add ${cand.name}`;
  setFormError("");
  setSaving(false);
  // replaceChildren would turn a null into the text "null", so the add-only Name field is filtered
  document.getElementById("radio-fields").replaceChildren(...[
    edit ? null : field("Name", "rf-name", el("input", { id: "rf-name", type: "text", value: nextRadioName(), maxlength: "12",
      autocomplete: "off", spellcheck: "false" }), "Lower-case letters and digits, starting with a letter: radio1, ic705."),
    field("Label", "rf-label", el("input", { id: "rf-label", type: "text", value: edit ? x.label : "", maxlength: "40" }),
      "Shown beside the name, for example Kenwood TM-V71."),
    field("Sound card", "rf-audio", choice("rf-audio", (edit ? keep(x.audio) : []).concat([["none", "None"]], fnChoices("audio")),
      edit ? "" : (own("audio") ? own("audio").path : "none")),
      hand.length ? `Graywolf has channels made by hand (${hand.join(", ")}). Pinning a sound card renames it to the radio's name in capitals ` +
        "at the next replug or reboot; a hand-made channel that uses this card stops working then." : null),
    field("CAT port", "rf-cat", choice("rf-cat", (edit ? keep(x.cat) : []).concat([["none", "None"]], fnChoices("serial")),
      edit ? "" : (own("serial") ? own("serial").path : "none")),
      !edit && df.cat === "separate" ? "This interface's CAT and PTT port is a separate USB device: pick its serial port here." : null),
    field("PTT serial port", "rf-ptt-serial", choice("rf-ptt-serial",
      (edit ? keep(x.ptt_serial) : []).concat([["none", "None (PTT goes over the CAT port)"]], fnChoices("serial")), edit ? "" : "none")),
    field("HID (CM108 PTT)", "rf-hid", choice("rf-hid",
      edit ? keep(x.hid).concat([["none", "None"]], fnChoices("hid")) : [["", "Automatic (from the sound card)"]].concat(fnChoices("hid")), "")),
    modelField(edit ? x.rig.model : df.model),
    field("Baud rate", "rf-baud", choice("rf-baud", bauds.map(b => [String(b), b ? String(b) : "Not used"]), String(baud))),
    field("PTT", "rf-ptt", choice("rf-ptt", PTT_METHOD_CHOICES, edit ? x.ptt.method : (df.ptt || "vox"))),
    field("rigctld keys PTT with", "rf-ptt-type", choice("rf-ptt-type", PTT_TYPE_CHOICES, edit ? x.rig.ptt_type : (df.ptt_type || "NONE"))),
    field("GPIO line", "rf-gpio", el("input", { id: "rf-gpio", type: "number", min: "0", max: "53",
      value: edit && typeof x.ptt.gpio_line === "number" ? String(x.ptt.gpio_line) : "" }), "The BCM GPIO number that keys the radio."),
    field("Wiring", "rf-wiring", choice("rf-wiring", [["full", "Full: DXBerry sets up the application"],
      ["names", "Names only: I set up the application myself"]], edit ? x.wiring : "full")),
  ].filter(Boolean));
  document.getElementById("rf-ptt").addEventListener("change", syncPttFields);
  syncPttFields();
  document.getElementById("radio-dialog").showModal();
}

// radioFormArgs: the dxberry-radio arguments for the open form - every field for add, only what
// changed for set ({args: null} when nothing did) - or {error} when the form is not complete.
function radioFormArgs() {
  const v = id => { const e = document.getElementById(id); return e ? e.value.trim() : ""; };
  const ptt = v("rf-ptt");
  const o = { label: v("rf-label"), audio: v("rf-audio"), cat: v("rf-cat"), ptt_serial: v("rf-ptt-serial"), hid: v("rf-hid"),
    model: v("rf-model"), baud: v("rf-baud"), ptt_type: v("rf-ptt-type"), gpio_line: v("rf-gpio"), wiring: v("rf-wiring") };
  if (!/^[0-9]+$/.test(o.model) || Number(o.model) < 1) return { error: "Pick a rig model (Hamlib Dummy, 1, when the radio has no CAT control)." };
  if (ptt === "gpio" && !/^[0-9]+$/.test(o.gpio_line)) return { error: "Give the GPIO line that keys the radio." };
  if (o.label.startsWith("--")) return { error: "The label cannot start with --." };
  const flags = [];
  const add = (flag, value) => flags.push(flag, value);
  if (!form.edit) {
    const name = v("rf-name");
    if (!NAME_RE.test(name)) return { error: "The name must be lower-case letters and digits, starting with a letter, 12 at most." };
    if (Object.prototype.hasOwnProperty.call(last.radios.radios, name)) return { error: `${name} already exists; pick another name.` };
    if (o.audio === "none" && o.cat === "none" && (!o.ptt_serial || o.ptt_serial === "none") && !o.hid) {
      return { error: "Pick a sound card, a CAT port, a PTT serial port or a HID: a radio needs at least one." };
    }
    add("--audio", o.audio);
    add("--cat", o.cat);
    if (o.ptt_serial && o.ptt_serial !== "none") add("--ptt-serial", o.ptt_serial);
    if (o.hid) add("--hid", o.hid);
    add("--model", o.model);
    add("--baud", o.baud);
    add("--ptt", ptt);
    // only rigctld keys by a CAT command or a serial line; anything else sends NONE, not the profile's type
    add("--ptt-type", ptt === "rigctld" ? o.ptt_type : "NONE");
    if (ptt === "gpio") add("--gpio-line", o.gpio_line);
    add("--wiring", o.wiring);
    add("--label", o.label);
    return { args: ["add", name, ...flags], name };
  }
  const x = form.x;
  if (o.label !== x.label) add("--label", o.label);
  for (const [k, flag] of [["audio", "--audio"], ["cat", "--cat"], ["ptt_serial", "--ptt-serial"], ["hid", "--hid"]]) {
    if (o[k]) add(flag, o[k]);
  }
  if (Number(o.model) !== x.rig.model) add("--model", o.model);
  if (Number(o.baud) !== x.rig.baud) add("--baud", o.baud);
  if (ptt !== x.ptt.method) add("--ptt", ptt);
  if (ptt === "rigctld" && o.ptt_type !== x.rig.ptt_type) add("--ptt-type", o.ptt_type);
  if (ptt === "gpio" && Number(o.gpio_line) !== x.ptt.gpio_line) add("--gpio-line", o.gpio_line);
  if (o.wiring !== x.wiring) add("--wiring", o.wiring);
  return { args: flags.length ? ["set", form.name, ...flags] : null, name: form.name };
}

function saveRadioForm(e) {
  e.preventDefault();
  if (!form || form.saving) return;
  const d = document.getElementById("radio-dialog");
  const r = radioFormArgs();
  if (r.error) { setFormError(r.error); return; }
  if (!r.args) { d.close(); notice(`${r.name}: nothing changed.`, "good"); return; }
  const f = form;
  f.saving = true;
  setFormError("");
  setSaving(true);
  run([RADIO, ...r.args, "--json"]).then(out => {
    f.saving = false;
    // a cancelled-then-reopened form is not this one any more: only the dialog that is still
    // this form's own gets re-enabled and closed; the notice and refresh happen regardless
    if (form === f) { setSaving(false); if (d.open) d.close(); }
    notice(f.edit ? `${r.name} updated.` : `${r.name} added.`, "good");
    for (const w of warningsOf(out)) notice(`${r.name}: ${w}`, "warn");
    refresh(true);
  }, ex => {
    f.saving = false;
    // the form is still open and still current: say it there; otherwise say it on the page
    if (form === f && d.open) { setSaving(false); setFormError(problemText(ex, RADIO_EXITS)); }
    else failure(f.edit ? `Updating ${r.name}` : `Adding ${r.name}`, ex, RADIO_EXITS);
    refresh(true);
  });
}

// ---- refresh ------------------------------------------------------------------------------
function render(s) {
  const a = document.activeElement;
  const focused = a && a.dataset ? a.dataset.key : undefined;
  document.getElementById("host").textContent = (s.network && s.network.hostname) || "";
  document.getElementById("updated").textContent = s.generated ? "updated " + new Date(s.generated).toLocaleTimeString("en-US") : "";
  document.getElementById("cards").replaceChildren(
    stationCard(s.graywolf), radiosCard(s.radios), networkCard(s.network), piCard(s.pi),
    timeCard(s.time, s.radios), aboutCard(s.release), servicesCard(s.services), powerCard());
  if (focused) {
    const again = [...document.querySelectorAll("[data-key]")].find(e => e.dataset.key === focused);
    if (again) again.focus();
  }
}

// show: draw the last report. A bug in one card must say so, not leave the page silently stale.
function show() {
  if (!last) return false;
  try {
    render(last);
    return true;
  } catch (e) {
    console.error(e);
    setRefreshFailure(notice(`Showing the station status failed: ${(e && e.message) || e}`, "bad"));
    return false;
  }
}

function clearRefreshFailureNotice() {
  if (refreshFailureNotice) { refreshFailureNotice.remove(); refreshFailureNotice = null; }
}

// setRefreshFailure: N is now the one refresh-failure notice. Before any report has been shown,
// the cards area says so instead of "Reading the station…".
function setRefreshFailure(n) {
  clearRefreshFailureNotice();
  refreshFailureNotice = n;
  if (!last) document.getElementById("cards").replaceChildren(el("p", { class: "muted" }, "The station status could not be read; see the message above."));
}

function refresh(force) {
  if (state.stopped || (cockpit.hidden && force !== true)) return;
  // an action's refresh asked for while the timer's one runs must still happen once that ends
  if (state.busy) { if (force === true) state.again = true; return; }
  state.busy = true;
  const finish = () => {
    state.busy = false;
    if (state.again) { state.again = false; refresh(true); }
  };
  run([STATUS, "--json"]).then(out => {
    let s = null;
    try { s = JSON.parse(out); } catch (e) { s = null; }
    if (s) {
      last = s;
      if (show()) clearRefreshFailureNotice();
    } else {
      setRefreshFailure(notice("dxberry-status did not return a report.", "bad"));
    }
    finish();
  }, ex => {
    setRefreshFailure(failure("Reading the station status", ex));
    finish();
  });
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
  document.getElementById("radio-form").addEventListener("submit", saveRadioForm);
  document.getElementById("radio-cancel").addEventListener("click", () => { form = null; document.getElementById("radio-dialog").close(); });
  refresh(true);
  setInterval(() => refresh(false), REFRESH_MS);
}

init();
