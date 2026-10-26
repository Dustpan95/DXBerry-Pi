// Runs station/web/vfo.js as the page /radio/ic7300 over a small fake DOM and a fake fetch with
// the SWR guard latched (or not) and prints what the banner showed and what the page sent.
// Run: node tests/station_guard.js station/web SCENARIO
//   scenarios: latched (banner, dimmed Tune/PTT, the override dialog, the clear notice), clear
//   (nothing latched), noatu (no tuner: no Run ATU), notice (a latch notice arrives), noguard
"use strict";
const fs = require("fs");
const vm = require("vm");
const dir = process.argv[2];
const scenario = process.argv[3] || "release";

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
  setPointerCapture() {}
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
  focus() {} close() { this.open = false; } showModal() { this.open = true; }
}
const els = {};
const shell = {};
const doc = {
  hidden: false, body: new El("body"), documentElement: { dataset: {} }, activeElement: null,
  getElementById: (id) => {
    if (id === "notices") return els.notices;
    if (id.startsWith("vfo-")) return els.vfo ? els.vfo.find((e) => e.attrs.id === id)[0] || null : null;
    return (shell[id] = shell[id] || new El("x"));
  },
  createElement: (t) => new El(t), querySelectorAll: () => [], addEventListener() {},
};
els.notices = new El("div");
els.notices.nextSibling = null;
els.notices.parentNode = { insertBefore(node) { els.vfo = node; } };

const record = { name: "ic7300", label: "IC-7300", owner: "", has_cat: true, ptt: "rigctld" };
const state = { connected: true, freq: 14074000, mode: "USB", passband: "2400", ptt: false, vfo: "VFOA", split: false, levels: {}, meters: {}, optimistic: {} };
const caps = { ModelName: "IC-7300", Modes: ["USB", "LSB"], VFOs: ["VFOA", "VFOB"], CanSetPTT: true, SettableLevels: [], SettableFuncs: scenario === "noatu" ? [] : ["TUNER"], Hidden: [], FreqRanges: [{ Lo: 30000, Hi: 74800000 }] };
const owner = { kind: "tab", label: "iPad", mine: true };
const audio = { available: true, reason: "" };
const ptt = { on: false, mine: false, timeout_s: 180, method: "rigctld" };
const REASON = "SWR guard: 5.8:1 against 3.0:1; tune the antenna, change band, or override";
const latched = scenario === "latched" || scenario === "noatu";
const guard = scenario === "noguard" ? { active: false, latched: false, reason: "" } : { active: true, latched, reason: latched ? REASON : "" };

const posts = []; // [path, body] of each POST
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  if (opts.method === "GET") return reply(200, { radio: record, state, caps, owner, audio, ptt, guard });
  posts.push([url.replace("/api/radio/ic7300/", ""), JSON.parse(opts.body), opts.headers["X-DXB-Tab"]]);
  return reply(200, { ok: true });
};
let nextTimer = 1;
const sockets = [];
function FakeSocket(u) { this.url = u; this.sent = []; sockets.push(this); }
FakeSocket.prototype.send = function (m) { this.sent.push(m); };
FakeSocket.prototype.close = function () {};
const ctx = {
  document: doc, console, fetch, JSON, Promise, encodeURIComponent, Date, Math, Object, String, Number, Array, Set, WebSocket: FakeSocket,
  location: { pathname: "/radio/ic7300", search: "", hash: "", protocol: "https:", host: "pi.local", assign() {} },
  setInterval() { return nextTimer++; }, clearInterval() {}, setTimeout() { return nextTimer++; }, clearTimeout() {},
  crypto: { randomUUID: () => "tab-1" },
  navigator: { userAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)" },
  localStorage: { getItem: () => null, setItem() {} }, notice(t, k) { ctx.notices.push(`${k}: ${t}`); }, notices: [],
  addEventListener() {},
  dxbAudio: { listen() { return Promise.resolve({ ok: true }); }, openMic() { return Promise.resolve({ ok: true }); }, setTX() {} },
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(dir + "/app.js", "utf8"), ctx);
ctx.dxb.events.start(() => {});
vm.runInContext(fs.readFileSync(dir + "/vfo.js", "utf8"), ctx);
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setImmediate(r)); };

const box = () => els.vfo.find((e) => e.has("vfo-guard"))[0];
const buttonNamed = (root, text) => root.find((e) => e.tag === "button" && e.textContent === text)[0];
const disabled = (e) => e.attrs.disabled !== undefined;
const dialog = () => doc.body.children.find((e) => e.tag === "dialog");

function snapshot() {
  const b = box();
  const atu = buttonNamed(b, "Run ATU"), ov = buttonNamed(b, "Override");
  return {
    hidden: !!b.hidden,
    text: b.find((e) => e.has("vfo-guard-text"))[0].textContent,
    atuShown: !atu.hidden, overrideShown: !ov.hidden,
    tuneDisabled: disabled(buttonNamed(els.vfo, "Tune")),
    pttDisabled: disabled(els.vfo.find((e) => e.has("vfo-ptt"))[0]),
  };
}

(async () => {
  await flush();
  sockets[0].onopen();
  await flush();
  const out = { pure: ctx.dxbVFO.guardBanner({ active: true, latched: true, reason: "r" }, true), confirm: ctx.dxbVFO.GUARD_CONFIRM };
  out.start = snapshot();
  if (scenario === "latched") {
    out.atuDisabled = disabled(buttonNamed(box(), "Run ATU"));
    // Override asks first; Cancel sends nothing
    buttonNamed(box(), "Override").handlers.click[0]({});
    out.dialogText = dialog().find((e) => e.has("vfo-guard-ask"))[0].textContent;
    out.dialogOpen = dialog().open === true;
    buttonNamed(dialog(), "Cancel").handlers.click[0]({});
    await flush();
    out.postsAfterCancel = posts.length;
    out.dialogOpenAfterCancel = dialog().open === true;
    // Run ATU posts the ATU intent
    buttonNamed(box(), "Run ATU").handlers.click[0]({});
    await flush();
    // Transmit anyway sends the override
    buttonNamed(box(), "Override").handlers.click[0]({});
    buttonNamed(dialog(), "Transmit anyway").handlers.click[0]({});
    await flush();
    out.posts = posts.map((p) => p[0] + ":" + (p[2] || "no-tab"));
    out.afterOverride = snapshot();
    // another tab hears the guard latch again, then clear
    sockets[0].onmessage({ data: JSON.stringify({ type: "guard", radio: "ic7300", latched: true, reason: REASON, note: "WSJT-X may key again; Stop WSJT-X to end it" }) });
    await flush();
    out.relatched = snapshot();
    sockets[0].onmessage({ data: JSON.stringify({ type: "guard", radio: "ic7300", latched: false, reason: "band changed" }) });
    await flush();
    out.cleared = snapshot();
    out.notices = ctx.notices;
  } else if (scenario === "notice") {
    sockets[0].onmessage({ data: JSON.stringify({ type: "guard", radio: "ic7300", latched: true, reason: REASON }) });
    await flush();
    out.after = snapshot();
    sockets[0].onmessage({ data: JSON.stringify({ type: "guard", radio: "other", latched: false, reason: "band changed" }) });
    await flush();
    out.otherRadio = snapshot().hidden;
    out.notices = ctx.notices;
  }
  console.log(JSON.stringify(out));
})().catch((e) => { console.error(e); process.exit(1); });
