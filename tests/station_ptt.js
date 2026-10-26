// Runs station/web/vfo.js as the page /radio/ic7300 over a small fake DOM, a fake fetch, fake
// timers and a fake dxbAudio, presses and releases its PTT button, and prints what it sent.
// Run: node tests/station_ptt.js station/web SCENARIO
//   scenarios: release (each release event, the heartbeat), notowner, noaudio, refused, dropped,
//   stale, hbrefused, rebuild, pendingmic, docrelease, txthrows (audio.js's setTX throws)
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
  focus() {} close() {} showModal() {}
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
  createElement: (t) => new El(t), querySelectorAll: () => [],
  addEventListener(t, f) { (docHandlers[t] = docHandlers[t] || []).push(f); },
};
const docHandlers = {};
els.notices = new El("div");
els.notices.nextSibling = null;
els.notices.parentNode = { insertBefore(node) { els.vfo = node; } };

const record = { name: "ic7300", label: "IC-7300", owner: "", has_cat: true, ptt: "rigctld" };
const state = { connected: true, freq: 14074000, mode: "USB", passband: "2400", ptt: false, vfo: "VFOA", split: false, levels: {}, meters: {}, optimistic: {} };
let caps = { ModelName: "IC-7300", Modes: ["USB", "LSB"], VFOs: ["VFOA", "VFOB"], CanSetPTT: true, SettableLevels: [], SettableFuncs: [], Hidden: [], FreqRanges: [{ Lo: 30000, Hi: 74800000 }] };
const owner = scenario === "notowner" ? { kind: "tab", label: "iPhone", mine: false } : { kind: "tab", label: "iPad", mine: true };
const audio = scenario === "noaudio" ? { available: false, reason: "no sound server for ic7300: WSJT-X has the sound card" } : { available: true, reason: "" };
const ptt = { on: false, mine: false, timeout_s: 180, method: "rigctld" };

const posts = []; // the PTT requests, in order: "on", "off", "hb"
let windows = 0; // the station's window numbers
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  if (opts.method === "GET") return reply(200, { radio: record, state, caps, owner, audio, ptt });
  if (url.endsWith("/ptt")) {
    const b = JSON.parse(opts.body);
    posts.push(b.hb ? "hb" : b.on ? "on" : "off");
    if (b.device !== "iPad#tab-1" || opts.headers["X-DXB-Tab"] !== "iPad#tab-1") posts.push("bad-tab");
    if (scenario === "refused" && b.on) {
      return reply(200, { ok: false, reason: "the microphone is not open yet", fix: "press PTT again once the browser has asked for the microphone" });
    }
    if (scenario === "hbrefused" && b.hb) return reply(200, { ok: false, reason: "PTT is not on", fix: "press PTT again" });
    if (b.on) return reply(200, { ok: true, window: ++windows });
  }
  return reply(200, { ok: true });
};

// timers that run only when the test says so
const intervals = new Map();
let nextTimer = 1;
const sockets = [];
function FakeSocket(u) { this.url = u; this.sent = []; sockets.push(this); }
FakeSocket.prototype.send = function (m) { this.sent.push(m); };
FakeSocket.prototype.close = function () {};
const audioCalls = { listen: 0, openMic: 0, tx: [] };
const winHandlers = {};
let micResolve = null;
const ctx = {
  document: doc, console, fetch, JSON, Promise, encodeURIComponent, Date, Math, Object, String, Number, Array, Set, WebSocket: FakeSocket,
  location: { pathname: "/radio/ic7300", search: "", hash: "", protocol: "https:", host: "pi.local", assign() {} },
  setInterval(fn, ms) { const id = nextTimer++; intervals.set(id, { fn, ms, cleared: false }); return id; },
  clearInterval(id) { const t = intervals.get(id); if (t) t.cleared = true; },
  setTimeout() { return nextTimer++; }, clearTimeout() {}, crypto: { randomUUID: () => "tab-1" },
  navigator: { userAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)" },
  localStorage: { getItem: () => null, setItem() {} }, notice(t, k) { ctx.notices.push(`${k}: ${t}`); }, notices: [],
  addEventListener(t, f) { (winHandlers[t] = winHandlers[t] || []).push(f); },
  dxbAudio: {
    listen() { audioCalls.listen++; return Promise.resolve({ ok: true }); },
    openMic() {
      audioCalls.openMic++;
      if (scenario === "pendingmic") return new Promise((r) => { micResolve = r; });
      return Promise.resolve({ ok: true });
    },
    setTX(on) {
      audioCalls.tx.push(!!on);
      if (scenario === "txthrows") throw new Error("the gain could not be set");
    },
  },
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(dir + "/app.js", "utf8"), ctx);
ctx.dxb.events.start(() => {});
vm.runInContext(fs.readFileSync(dir + "/vfo.js", "utf8"), ctx);
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setImmediate(r)); };

const button = () => els.vfo.find((e) => e.has("vfo-ptt"))[0];
const why = () => els.vfo.find((e) => e.has("vfo-ptt-why"))[0];
const timeText = () => els.vfo.find((e) => e.has("vfo-ptt-time"))[0];
// the heartbeat: the 5 s interval registered after the press, not yet cleared
const heartbeats = (since) => [...intervals.entries()].filter(([id, t]) => id >= since && t.ms === 5000);
const count = (kind) => posts.filter((p) => p === kind).length;

async function press() {
  const since = nextTimer;
  button().handlers.pointerdown[0]({ pointerId: 1, preventDefault() {} });
  await flush();
  return since;
}

function release(kind) {
  if (kind === "pointerup" || kind === "pointercancel") button().handlers[kind][0]({ pointerId: 1 });
  else for (const f of winHandlers[kind] || []) f({});
}

(async () => {
  await flush();
  sockets[0].onopen();
  await flush();
  const out = {};
  out.disabled = button().attrs.disabled !== undefined;
  out.why = why().hidden ? "" : why().textContent;
  out.label = button().textContent;
  if (scenario === "notowner" || scenario === "noaudio") {
    // pressing a disabled button sends nothing and asks for nothing
    await press();
    out.posts = posts;
    out.audioCalls = audioCalls;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "refused") {
    await press();
    out.posts = posts.slice();
    out.notices = ctx.notices;
    out.tx = audioCalls.tx;
    out.labelAfter = button().textContent;
    release("pointerup");
    await flush();
    out.postsAfterRelease = posts;
    console.log(JSON.stringify(out));
    return;
  }
  const notice = (m) => sockets[0].onmessage({ data: JSON.stringify(Object.assign({ type: "ptt", radio: "ic7300", on: false }, m)) });
  if (scenario === "stale") {
    // press 1 opens window 1 and is released; press 2 opens window 2; window 1's late notice
    // must not end press 2, window 2's own does
    await press();
    release("pointerup");
    await flush();
    await press();
    notice({ reason: "released", window: 1 });
    await flush();
    out.afterStale = button().textContent;
    notice({ reason: "heartbeat lost", window: 2 });
    await flush();
    out.afterOwn = button().textContent;
    out.posts = posts;
    out.notices = ctx.notices;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "hbrefused") {
    const since = await press();
    heartbeats(since)[0][1].fn();
    await flush();
    out.posts = posts;
    out.notices = ctx.notices;
    out.labelAfter = button().textContent;
    out.hbCleared = heartbeats(since).every(([, t]) => t.cleared);
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "rebuild") {
    // the station's answer changes the layout mid-hold: the section is replaced, the hold ends
    const since = await press();
    out.held = button().textContent;
    caps = Object.assign({}, caps, { Modes: ["USB", "LSB", "CW"] });
    const poll = [...intervals.entries()].find(([id, t]) => id < since && t.ms === 5000);
    poll[1].fn();
    await flush();
    out.posts = posts;
    out.hbCleared = heartbeats(since).every(([, t]) => t.cleared);
    out.labelAfter = button().textContent;
    out.tx = audioCalls.tx;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "pendingmic") {
    // released while the browser is still asking for the microphone: nothing goes to the station
    await press();
    out.labelPending = button().textContent;
    release("pointerup");
    await flush();
    micResolve({ ok: true });
    await flush();
    out.posts = posts;
    out.tx = audioCalls.tx;
    out.labelAfter = button().textContent;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "docrelease") {
    // a pointer lifted anywhere ends the hold it started; another finger lifting does not
    await press();
    for (const f of docHandlers.pointerup || []) f({ pointerId: 2 });
    await flush();
    out.otherFinger = button().textContent;
    for (const f of docHandlers.pointerup || []) f({ pointerId: 1 });
    await flush();
    out.afterUp = button().textContent;
    // the page going to the background ends a hold too
    await press();
    doc.hidden = true;
    for (const f of docHandlers.visibilitychange || []) f({});
    doc.hidden = false;
    await flush();
    out.afterHidden = button().textContent;
    await press();
    for (const f of docHandlers.lostpointercapture || []) f({ pointerId: 1 });
    await flush();
    out.afterLost = button().textContent;
    out.posts = posts;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "txthrows") {
    // audio.js's setTX throws (a Web Audio write that fails): the heartbeat still runs while
    // held, and the release still sends the off
    const since = await press();
    out.held = button().textContent;
    const hb = heartbeats(since);
    out.heartbeatTimers = hb.length;
    if (hb.length) hb[0][1].fn();
    await flush();
    release("pointerup");
    await flush();
    out.posts = posts;
    out.labelAfter = button().textContent;
    out.hbCleared = heartbeats(since).every(([, t]) => t.cleared);
    out.tx = audioCalls.tx;
    console.log(JSON.stringify(out));
    return;
  }
  if (scenario === "dropped") {
    const since = await press();
    out.postsBefore = posts.slice();
    out.transmitting = button().textContent;
    notice({ reason: "heartbeat lost", window: 1 });
    await flush();
    out.labelAfter = button().textContent;
    out.notices = ctx.notices;
    out.hbCleared = heartbeats(since).every(([, t]) => t.cleared);
    release("pointerup"); // the finger lifts afterwards: nothing more is sent
    await flush();
    out.posts = posts;
    out.tx = audioCalls.tx;
    console.log(JSON.stringify(out));
    return;
  }
  // release: every release event sends off exactly once, the heartbeat runs every 5 s while held
  out.rounds = {};
  for (const kind of ["pointerup", "pointercancel", "blur", "pagehide"]) {
    posts.length = 0;
    const since = await press();
    const r = { on: count("on"), label: button().textContent, time: timeText().hidden ? null : timeText().textContent };
    const hb = heartbeats(since);
    r.heartbeatTimers = hb.length;
    for (let i = 0; i < 3; i++) hb[0][1].fn(); // three 5 s periods
    await flush();
    r.hb = count("hb");
    release(kind);
    // and every other release event after it: still one off
    for (const other of ["pointerup", "pointercancel", "blur", "pagehide"]) release(other);
    await flush();
    r.off = count("off");
    r.hbCleared = hb.every(([, t]) => t.cleared);
    r.order = posts.join(",");
    r.labelAfter = button().textContent;
    r.timeHidden = timeText().hidden;
    out.rounds[kind] = r;
  }
  // a release before the microphone is ready: no on goes out, so no off either
  posts.length = 0;
  button().handlers.pointerdown[0]({ pointerId: 1, preventDefault() {} });
  release("pointerup");
  await flush();
  out.quickOrder = posts.join(",");
  out.audioCalls = audioCalls;
  console.log(JSON.stringify(out));
})();
