// Runs station/web/vfo.js as a page (at /radio/ic7300 or the path given) over a small fake DOM,
// a fake fetch and the events layer of app.js, and prints what it drew and sent.
// Run: node tests/station_vfo_page.js station/web SCENARIO   (scenarios: ic7300, dummy, missing)
"use strict";
const fs = require("fs");
const vm = require("vm");
const dir = process.argv[2];
const scenario = process.argv[3] || "ic7300";

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
  get classList() { const s = this; return { add: (c) => { s.className += " " + c; }, toggle() {}, remove() {} }; }
  querySelectorAll(sel) {
    const out = [];
    const want = sel.startsWith(".") ? sel.slice(1) : null;
    const walk = (e) => { for (const c of e.children) if (typeof c === "object") { if (want && (c.className || "").split(/\s+/).includes(want)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  has(c) { return (this.className || "").split(/\s+/).includes(c); }
  find(fn) { const r = []; const walk = (e) => { for (const c of e.children) if (typeof c === "object") { if (fn(c)) r.push(c); walk(c); } }; walk(this); return r; }
  focus() {} close() {} showModal() {}
}
const els = {};
const doc = {
  hidden: false, body: new El("body"), documentElement: { dataset: {} }, activeElement: null,
  getElementById: (id) => (els[id] = els[id] || new El("x")),
  createElement: (t) => new El(t), querySelectorAll: () => [], addEventListener() {},
};
els.notices = new El("div");
els.notices.nextSibling = null;
els.notices.parentNode = { insertBefore(node) { els.vfo = node; } };
const record = { name: "ic7300", label: "IC-7300", owner: "", has_cat: scenario !== "dummy" };
const state = { connected: true, freq: 14074000, mode: "USB", passband: "2400", ptt: false, vfo: "VFOA", split: false, levels: { AF: 0.4, RFPOWER: 0.5 }, meters: { STRENGTH: -12 }, optimistic: {} };
const caps = scenario === "dummy"
  ? { ModelName: "Dummy", Modes: ["USB"], FreqRanges: [{ Lo: 0, Hi: 1000000000 }], Hidden: [{ Name: "STRENGTH", Kind: "meter", Why: "dummy" }] }
  : { ModelName: "IC-7300", Modes: ["USB", "LSB", "CW", "AM", "FM"], VFOs: ["VFOA", "VFOB"], CanSetSplit: true, CanSetVFO: true, CanSetPTT: true,
      SettableLevels: ["AF", "RFPOWER"], SettableFuncs: ["TUNER"], Hidden: [{ Name: "RF", Kind: "control", Why: "this radio cannot set RF over CAT" }], FreqRanges: [{ Lo: 30000, Hi: 74800000 }] };
const calls = [];
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  calls.push({ url, method: opts.method, tab: opts.headers["X-DXB-Tab"], body: opts.body });
  if (scenario === "missing" && opts.method === "GET") return reply(404, { error: "no such radio" });
  if (opts.method === "GET") return reply(200, { radio: record, state, caps, owner: null });
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
  out.getTabs = calls.filter((c) => c.method === "GET").map((c) => c.tab);
  const root = els.vfo;
  const text = root.textContent;
  out.readout = (root.find((e) => e.has("vfo-readout"))[0] || { textContent: null }).textContent;
  out.hasDial = root.find((e) => e.has("vfo-dial")).length > 0;
  out.sectionTitles = root.find((e) => e.tag === "h3").map((e) => e.textContent);
  out.bandButtons = root.find((e) => e.attrs["aria-pressed"] !== undefined && e.dataset.band).map((e) => e.dataset.band).join(",");
  out.modeButtons = root.find((e) => e.dataset.mode).map((e) => e.dataset.mode).join(",");
  out.stopCount = root.find((e) => e.has("vfo-stop")).length;
  out.stopDimmable = root.find((e) => e.has("vfo-stop") && e.has("vfo-ctl")).length;
  out.ownerText = els["vfo-owner"] ? els["vfo-owner"].textContent : null;
  out.nocat = text.includes("no rig control on this radio");
  out.missing = text.includes("There is no radio called gone");
  out.why = (root.find((e) => e.tag === "details")[0] || { textContent: "" }).textContent;
  // a rig event moves the readout; a tap on a mode button sends the intent and shows it at once
  s.onmessage({ data: JSON.stringify({ type: "rig", radio: "ic7300", data: { ...state, freq: 7074000 } }) });
  out.readoutAfterEvent = (root.find((e) => e.has("vfo-readout"))[0] || { textContent: null }).textContent;
  const cw = root.find((e) => e.dataset.mode === "CW")[0];
  if (cw) {
    cw.handlers.click[0]();
    out.cwPressedAtOnce = cw.attrs["aria-pressed"];
    await flush();
    out.modeCall = calls.filter((c) => c.method === "POST").map((c) => `${c.url} ${c.body} ${c.tab}`);
    // the next state, no longer optimistic, clears the marker; USB is shown again
    s.onmessage({ data: JSON.stringify({ type: "rig", radio: "ic7300", data: state }) });
    out.cwPressedAfter = cw.attrs["aria-pressed"];
  }
  // the rig link drops: the chip shows and controls are disabled; STOP is not
  s.onmessage({ data: JSON.stringify({ type: "rig", radio: "ic7300", data: { ...state, connected: false } }) });
  const chip = els["vfo-chip"];
  out.chipHidden = chip ? chip.hidden : null;
  out.dimmed = root.find((e) => e.has("vfo-ctl") && e.attrs.disabled !== undefined).length > 0;
  out.stopDisabled = root.find((e) => e.has("vfo-stop") && e.attrs.disabled !== undefined).length;
  console.log(JSON.stringify(out));
})();
