// Runs station/web/vfo.js as a page (at /radio/ic7300 or the path given) over a small fake DOM,
// a fake fetch and the events layer of app.js, and prints what it drew and sent.
// Run: node tests/station_vfo_page.js station/web SCENARIO   (scenarios: ic7300, dummy, missing)
"use strict";
const fs = require("fs");
const vm = require("vm");
const dir = process.argv[2];
const scenario = process.argv[3] || "ic7300";

const registry = new Map(); // id -> element, for the elements that are still in the page
class El {
  constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.dataset = {}; this.handlers = {}; this._text = ""; this.className = ""; this.style = { setProperty() {} }; this.hidden = false; this.value = ""; }
  set textContent(t) { this._text = String(t); this.children = []; }
  get textContent() { return this._text + this.children.map((c) => (typeof c === "string" ? c : c.textContent)).join(""); }
  append(...k) { this.children.push(...k); }
  replaceChildren(...k) { this._text = ""; this.children = k; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener(t, f) { (this.handlers[t] = this.handlers[t] || []).push(f); }
  get classList() {
    const s = this;
    const set = () => new Set((s.className || "").split(/\s+/).filter(Boolean));
    const put = (x) => { s.className = [...x].join(" "); };
    return {
      add: (c) => { const x = set(); x.add(c); put(x); },
      remove: (c) => { const x = set(); x.delete(c); put(x); },
      toggle: (c, on) => { const x = set(); const want = on === undefined ? !x.has(c) : !!on; if (want) x.add(c); else x.delete(c); put(x); return want; },
      contains: (c) => set().has(c),
    };
  }
  querySelectorAll(sel) { const want = sel.startsWith(".") ? sel.slice(1) : null; return this.find((c) => want && c.has(want)); }
  has(c) { return (this.className || "").split(/\s+/).includes(c); }
  find(fn) { const r = []; const walk = (e) => { for (const c of e.children) if (typeof c === "object") { if (fn(c)) r.push(c); walk(c); } }; walk(this); return r; }
  focus() {} close() {} showModal() {}
}
// the page's own elements are found by walking what is really in the tree
const doc = {
  hidden: false, body: new El("body"), documentElement: { dataset: {} }, activeElement: null,
  getElementById: (id) => {
    if (id === "notices") return els.notices;
    // the page's own ids are looked up in what is really in its tree; the shell's are stubs
    if (id.startsWith("vfo-")) return els.vfo ? els.vfo.find((e) => e.attrs.id === id)[0] || null : null;
    return (shell[id] = shell[id] || new El("x"));
  },
  createElement: (t) => new El(t), querySelectorAll: () => [], addEventListener() {},
};
const els = {};
const shell = {};
els.notices = new El("div");
els.notices.nextSibling = null;
els.notices.parentNode = { insertBefore(node) { els.vfo = node; } };
const record = { name: "ic7300", label: "IC-7300", owner: scenario === "other" ? "" : "", has_cat: scenario !== "dummy" };
const state = { connected: true, freq: 14074000, mode: "USB", passband: "2400", ptt: false, vfo: "VFOA", split: false, levels: { AF: 0.4, RFPOWER: 0.5 }, meters: { STRENGTH: -12 }, optimistic: {} };
const caps = scenario === "dummy"
  ? { ModelName: "Dummy", Modes: ["USB"], FreqRanges: [{ Lo: 0, Hi: 1000000000 }], Hidden: [{ Name: "STRENGTH", Kind: "meter", Why: "dummy" }] }
  : { ModelName: "IC-7300", Modes: ["USB", "LSB", "CW", "AM", "FM"], VFOs: ["VFOA", "VFOB"], CanSetSplit: true, CanSetVFO: true, CanSetPTT: true,
      SettableLevels: ["AF", "RFPOWER"], SettableFuncs: ["TUNER"], Hidden: [{ Name: "RF", Kind: "control", Why: "this radio cannot set RF over CAT" }], FreqRanges: [{ Lo: 30000, Hi: 74800000 }] };
const owner = scenario === "other" ? { kind: "tab", label: "iPhone", mine: false } : null;
const calls = [];
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  calls.push({ url, method: opts.method, tab: opts.headers["X-DXB-Tab"], body: opts.body });
  if (scenario === "missing" && opts.method === "GET") return reply(404, { error: "no such radio" });
  if (opts.method === "GET") return reply(200, { radio: record, state, caps, owner });
  if (scenario === "dummy") return reply(409, { ok: false, reason: "this radio has no rig control", fix: "give the radio a rig control port in its settings" });
  if (scenario === "refuse" && url.endsWith("/mode")) return reply(200, { ok: false, reason: "this radio has no mode CW", fix: "modes: USB LSB" });
  return reply(200, { ok: true });
};
const sockets = [];
function FakeSocket(u) { this.url = u; this.sent = []; sockets.push(this); }
FakeSocket.prototype.send = function (m) { this.sent.push(m); };
FakeSocket.prototype.close = function () {};
const ctx = {
  document: doc, console, fetch, JSON, Promise, encodeURIComponent, Date, Math, Object, String, Number, Array, Set, WebSocket: FakeSocket,
  location: { pathname: "/radio/" + (scenario === "missing" ? "gone" : "ic7300"), search: "", hash: "", protocol: "https:", host: "pi.local", assign() {} },
  setInterval() { return 1; }, setTimeout() { return 1; }, clearTimeout() {}, clearInterval() {}, crypto: { randomUUID: () => "tab-1" },
  navigator: { userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)" },
  localStorage: { getItem: () => null, setItem() {} }, notice(t, k) { ctx.notices.push([k, t]); }, notices: [], addEventListener() {},
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(dir + "/app.js", "utf8"), ctx);
ctx.dxb.events.start(() => {}); // dxberry.js does this in the real page
vm.runInContext(fs.readFileSync(dir + "/vfo.js", "utf8"), ctx);
const flush = () => new Promise((r) => setImmediate(r));

(async () => {
  await flush(); await flush();
  const out = {};
  const s = sockets[0];
  out.socketUrl = s.url;
  s.onopen(); await flush(); await flush();
  out.watch = s.sent;
  out.getTabs = [...new Set(calls.filter((c) => c.method === "GET").map((c) => c.tab))];
  const root = els.vfo;
  const rig = (d) => s.onmessage({ data: JSON.stringify({ type: "rig", radio: "ic7300", data: d }) });
  const one = (fn) => root.find(fn)[0];
  const readout = () => (one((e) => e.has("vfo-readout")) || { textContent: null }).textContent;
  const ownerBox = () => doc.getElementById("vfo-owner");
  const chip = () => doc.getElementById("vfo-chip");
  out.readout = readout();
  out.hasDial = root.find((e) => e.has("vfo-dial")).length > 0;
  out.dialRange = (one((e) => e.has("vfo-dial")) || { attrs: {} }).attrs["aria-valuemin"] + "-" + (one((e) => e.has("vfo-dial")) || { attrs: {} }).attrs["aria-valuemax"];
  out.sectionTitles = root.find((e) => e.tag === "h3").map((e) => e.textContent);
  out.bandButtons = root.find((e) => e.dataset.band).map((e) => e.dataset.band).join(",");
  out.modeButtons = root.find((e) => e.dataset.mode).map((e) => e.dataset.mode).join(",");
  out.stopCount = root.find((e) => e.has("vfo-stop")).length;
  out.stopDimmable = root.find((e) => e.has("vfo-stop") && e.has("vfo-ctl")).length;
  out.ownerText = ownerBox() ? ownerBox().textContent : null;
  out.ownerButtons = ownerBox() ? ownerBox().find((e) => e.tag === "button").map((e) => e.textContent) : null;
  out.nocat = root.textContent.includes("no rig control on this radio");
  out.missing = root.textContent.includes("There is no radio called gone");
  out.why = (one((e) => e.tag === "details") || { textContent: "" }).textContent;
  out.chipHiddenBefore = chip() ? chip().hidden : null;
  // before any drop nothing is disabled except what waits for the transmitter (Tune and ATU)
  const disabled = () => root.find((e) => e.has("vfo-ctl") && e.attrs.disabled !== undefined);
  out.disabledBefore = disabled().map((e) => e.textContent).sort();
  out.needsHint = root.find((e) => e.has("vfo-needs") && !e.hidden).length;
  out.tuneHint = (one((e) => e.tag === "p" && e.has("hint") && e.textContent.startsWith("Sends a carrier")) || { textContent: null }).textContent;
  if (scenario === "dummy") {
    const stop = one((e) => e.has("vfo-stop"));
    stop.handlers.click[0]();
    await flush(); await flush();
    out.stopNotice = ctx.notices.map((n) => n.join(": "));
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "missing") { console.log(JSON.stringify(out)); return; }
  if (scenario === "other") { console.log(JSON.stringify(out)); return; }
  rig({ ...state, freq: 7074000 });
  out.readoutAfterEvent = readout();
  const cw = one((e) => e.dataset.mode === "CW");
  const pressed = (b) => b.attrs["aria-pressed"];
  cw.handlers.click[0]();
  out.cwPressedAtOnce = pressed(cw);
  await flush();
  out.modeCall = calls.filter((c) => c.method === "POST").map((c) => `${c.url} ${c.body} ${c.tab}`);
  if (scenario === "refuse") {
    out.refusedPressed = pressed(cw);
    out.refusedNotice = ctx.notices.map((n) => n.join(": "));
    console.log(JSON.stringify(out));
    return;
  }
  // an event that still says mode is optimistic keeps the value shown; the first one that does not clears it
  rig({ ...state, mode: "CW", optimistic: { mode: true } });
  out.cwKeptWhileOptimistic = pressed(cw);
  rig({ ...state, mode: "USB", optimistic: { mode: true } });
  out.cwKeptWhileStillOptimistic = pressed(cw);
  rig(state);
  out.cwPressedAfter = pressed(cw);
  out.usbPressedAfter = pressed(one((e) => e.dataset.mode === "USB"));
  // the rig link drops: the chip shows (the real element), controls are disabled, STOP is not
  rig({ ...state, connected: false });
  out.chipHiddenAfter = chip().hidden;
  out.chipTextDown = chip().textContent;
  out.disabledAfterCount = disabled().length;
  out.rootDown = root.has("is-down");
  out.stopDisabled = root.find((e) => e.has("vfo-stop") && e.attrs.disabled !== undefined).length;
  rig(state);
  out.chipHiddenRecovered = chip().hidden;
  out.disabledRecovered = disabled().map((e) => e.textContent).sort();
  // connected but the radio behind rigctld does not answer: its own words, the same dimming
  rig({ ...state, answering: false });
  out.chipHiddenQuiet = chip().hidden;
  out.chipTextQuiet = chip().textContent;
  out.rootDownQuiet = root.has("is-down");
  out.disabledQuietCount = disabled().length;
  out.stopDisabledQuiet = root.find((e) => e.has("vfo-stop") && e.attrs.disabled !== undefined).length;
  rig({ ...state, answering: true });
  out.chipHiddenAnswering = chip().hidden;
  out.disabledAnswering = disabled().map((e) => e.textContent).sort();
  // the dashboard card: a rig event changes the frequency text in place
  const live = { dataset: { liveFreq: "ic7300", was: "old" }, textContent: "old" };
  const dctx = { ...ctx, DASH: "-", NAME_RE: /./, PROGRAM_RE: /./, updWatchers: [], last: null, el() {}, kv() {}, badge() {}, btn() {}, num() {}, table() {}, field() {}, choice() {} };
  dctx.document = { ...doc, getElementById: () => ({ addEventListener() {} }), querySelectorAll: (q) => (q === "[data-live-freq]" ? [live] : []) };
  dctx.window = dctx;
  dctx.dxb = { events: { on() {}, onOpen() {}, send() { return true; } }, api() { return Promise.resolve({}); }, hidden: false };
  dctx.dxbVFO = ctx.dxbVFO;
  vm.createContext(dctx);
  vm.runInContext(fs.readFileSync(dir + "/dashboard.js", "utf8"), dctx);
  dctx.dashOnRig({ type: "rig", radio: "ic7300", data: { connected: true, freq: 14074000, mode: "USB" } });
  out.dashFreq = live.textContent;
  dctx.dashOnRig({ type: "rig", radio: "ic7300", data: { connected: false, freq: 14074000, mode: "USB" } });
  out.dashFreqDown = live.textContent;
  dctx.dashOnRig({ type: "rig", radio: "ic7300", data: { connected: true, answering: false, freq: 14074000, mode: "USB" } });
  out.dashFreqQuiet = live.textContent;
  console.log(JSON.stringify(out));
})();
