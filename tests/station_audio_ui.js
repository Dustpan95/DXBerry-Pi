// Runs the VFO page (/radio/ic7300) with the real app.js, audio.js and vfo.js over a small fake
// DOM, a fake fetch, fake sockets, a fake RTCPeerConnection, a fake AudioContext, a fake
// getUserMedia and a fake clock, and prints what the operator saw and what went to the station.
// Run: node tests/station_audio_ui.js station/web SCENARIO
//   scenarios: listen (the speaker toggle), mic, program (a program takes the sound card), lost
//   (the chip), ptt (release events, heartbeat, timer, duck), guard (the banner's flows), hash,
//   hashbutton, hashnoaudio (the dashboard's #listen), reasons, nojs, dashboard (the Listen link),
//   pagestop (the page's own stop: no stray notice; the Mic button's microphone let go),
//   connecting (the PTT label while the audio connects and the station answers the on)
"use strict";
const fs = require("fs");
const vm = require("vm");
const dir = process.argv[2];
const scenario = process.argv[3] || "listen";

// ---- a small DOM ------------------------------------------------------------------------------
class El {
  constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.dataset = {}; this.handlers = {}; this._text = ""; this.className = ""; this.style = { setProperty() {} }; this.hidden = false; this.value = ""; this.parentNode = null; }
  set textContent(t) { this._text = String(t); this.children = []; }
  get textContent() { return this._text + this.children.map((c) => (typeof c === "string" ? c : c.textContent)).join(""); }
  append(...k) { for (const c of k) { if (c && typeof c === "object") c.parentNode = this; this.children.push(c); } }
  replaceChildren(...k) { this._text = ""; this.children = []; this.append(...k); }
  remove() { if (this.parentNode) this.parentNode.children = this.parentNode.children.filter((c) => c !== this); this.removed = true; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener(t, f) { (this.handlers[t] = this.handlers[t] || []).push(f); }
  setPointerCapture() {}
  contains(n) { for (let e = n; e; e = e.parentNode) if (e === this) return true; return false; }
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
  find(fn) { const r = []; const walk = (e) => { for (const c of e.children) if (c && typeof c === "object" && c.children) { if (fn(c)) r.push(c); walk(c); } }; walk(this); return r; }
  focus() {} close() { this.open = false; } showModal() { this.open = true; }
  play() { this.played = (this.played || 0) + 1; this.paused = false; return Promise.resolve(); }
  pause() { this.paused = true; }
}
const els = {};
const shell = {};
const docHandlers = {};
const winHandlers = {};
const doc = {
  hidden: false, body: new El("body"), documentElement: { dataset: {} }, activeElement: null,
  getElementById: (id) => {
    if (id === "notices") return els.notices;
    if (id.startsWith("vfo-")) return els.vfo ? els.vfo.find((e) => e.attrs.id === id)[0] || null : null;
    return (shell[id] = shell[id] || new El("x"));
  },
  createElement: (t) => new El(t),
  createTextNode: (data) => ({ data, get textContent() { return this.data; } }),
  querySelectorAll: () => [],
  addEventListener(t, f) { (docHandlers[t] = docHandlers[t] || []).push(f); },
};
els.notices = new El("div");
els.notices.nextSibling = null;
els.notices.parentNode = { insertBefore(node) { els.vfo = node; } };

// ---- a clock and timers that run only when the test advances them -----------------------------
let now = 1700000000000;
const timers = new Map();
let nextTimer = 1;
const addTimer = (fn, ms, every) => { const id = nextTimer++; timers.set(id, { fn, ms: Number(ms) || 0, at: now + (Number(ms) || 0), every }); return id; };
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setImmediate(r)); };
async function advance(ms) {
  const end = now + ms;
  for (;;) {
    let due = null;
    for (const [id, t] of timers) if (t.at <= end && (!due || t.at < due[1].at)) due = [id, t];
    if (!due) break;
    const [id, t] = due;
    now = t.at;
    if (t.every) t.at += Math.max(t.ms, 1); else timers.delete(id);
    t.fn();
    await flush();
  }
  now = end;
}
class FakeDate extends Date {
  constructor(...a) { if (a.length) super(...a); else super(now); }
  static now() { return now; }
}

// ---- the station --------------------------------------------------------------------------------
const AVAILABLE = { available: true, reason: "" };
const WSJTX_CARD = { available: false, reason: "no sound server for ic7300: WSJT-X has the sound card" };
const GUARD_REASON = "SWR guard: 5.8:1 against 3.0:1; tune the antenna, change band, or override";
const st = {
  rec: { name: "ic7300", label: "IC-7300", owner: "", has_cat: true, ptt: "rigctld" },
  state: { connected: true, freq: 14074000, mode: "USB", passband: "2400", ptt: false, vfo: "VFOA", split: false, levels: {}, meters: {}, optimistic: {} },
  caps: { ModelName: "IC-7300", Modes: ["USB", "LSB"], VFOs: ["VFOA", "VFOB"], CanSetPTT: true, SettableLevels: [], SettableFuncs: ["TUNER"], Hidden: [], FreqRanges: [{ Lo: 30000, Hi: 74800000 }] },
  owner: { kind: "tab", label: "iPad", mine: true },
  audio: AVAILABLE,
  ptt: { on: false, mine: false, timeout_s: 120, method: "rigctld" },
  guard: { active: true, latched: false, reason: "" },
};
if (scenario === "guard") st.guard = { active: true, latched: true, reason: GUARD_REASON };
if (scenario === "hashnoaudio") st.audio = WSJTX_CARD;
const calls = [];
const posts = []; // the PTT requests, in order: "on", "off", "hb"
const hbAt = []; // the clock at each heartbeat
const answers = {}; // intent -> the replies to give, in order (then {ok:true})
let windows = 0;
let deferOn = false; // the next "on" is answered only when the test calls answerOn()
let answerOn = null;
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  const body = opts.body ? JSON.parse(opts.body) : null;
  calls.push({ url, method: opts.method, body, tab: opts.headers && opts.headers["X-DXB-Tab"] });
  if (opts.method === "GET") return reply(200, { radio: st.rec, state: st.state, caps: st.caps, owner: st.owner, audio: st.audio, ptt: st.ptt, guard: st.guard });
  const what = url.replace("/api/radio/ic7300/", "");
  if (what === "ptt") {
    posts.push(body.hb ? "hb" : body.on ? "on" : "off");
    if (body.hb) hbAt.push(now);
    if (body.on && deferOn) {
      deferOn = false;
      return new Promise((r) => { answerOn = () => r({ ok: true, status: 200, json: () => Promise.resolve({ ok: true, window: ++windows }) }); });
    }
    if (body.on) return reply(200, { ok: true, window: ++windows });
  }
  if (answers[what] && answers[what].length) return reply(200, answers[what].shift());
  return reply(200, { ok: true });
};
const gets = () => calls.filter((c) => c.method === "GET").length;
const posted = (what) => calls.filter((c) => c.method === "POST" && c.url.endsWith("/" + what)).map((c) => c.tab);

// ---- sockets, WebRTC, Web Audio, the microphone -------------------------------------------------
const sockets = [];
function FakeSocket(url) { this.url = url; this.sent = []; this.closed = 0; sockets.push(this); }
FakeSocket.prototype.send = function (m) { this.sent.push(JSON.parse(m)); };
FakeSocket.prototype.close = function () { this.closed++; };
const evs = () => sockets.find((s) => s.url.includes("/api/events"));
const sigs = () => sockets.filter((s) => s.url.includes("/signal"));

const pcs = [];
function FakePC(cfg) { this.cfg = cfg; this.replaced = []; this.closed = 0; this.localDescription = null; this.connectionState = "new"; pcs.push(this); }
FakePC.prototype.addTransceiver = function (kind) {
  const pc = this;
  return { kind, sender: { replaceTrack(t) { pc.replaced.push(t.id); return Promise.resolve(); } } };
};
FakePC.prototype.createOffer = function () { return Promise.resolve({ type: "offer", sdp: "OFFER-" + pcs.indexOf(this) }); };
FakePC.prototype.setLocalDescription = function (d) { this.localDescription = d; return Promise.resolve(); };
FakePC.prototype.setRemoteDescription = function (d) { this.remote = d; return Promise.resolve(); };
FakePC.prototype.addIceCandidate = function () { return Promise.resolve(); };
FakePC.prototype.close = function () { this.closed++; };
function FakeStream() { this.tracks = []; }
FakeStream.prototype.addTrack = function (t) { this.tracks.push(t.id); };

const graph = [];
const contexts = [];
const gains = [];
let amp = 0;
function Node(kind) { this.kind = kind; }
Node.prototype.connect = function (n) { graph.push(this.kind + ">" + n.kind); return n; };
Node.prototype.disconnect = function () { graph.push(this.kind + " off"); };
function FakeAudioContext() { this.state = "running"; this.destination = new Node("destination"); contexts.push(this); }
FakeAudioContext.prototype.resume = function () { this.state = "running"; return Promise.resolve(); };
FakeAudioContext.prototype.suspend = function () { this.state = "suspended"; return Promise.resolve(); };
FakeAudioContext.prototype.createGain = function () { const n = new Node("gain"); n.gain = { value: 1 }; gains.push(n); return n; };
FakeAudioContext.prototype.createAnalyser = function () {
  const n = new Node("analyser");
  n.fftSize = 2048;
  // the byte form floors the samples as browsers do (floor(128 * (1 + x)))
  n.getByteTimeDomainData = (b) => { for (let i = 0; i < b.length; i++) b[i] = Math.max(0, Math.min(255, Math.floor(128 * (1 + amp * Math.sin(i / 3))))); };
  n.getFloatTimeDomainData = (b) => { for (let i = 0; i < b.length; i++) b[i] = amp * Math.sin(i / 3); };
  return n;
};
FakeAudioContext.prototype.createMediaStreamSource = function (s) { const n = new Node("source"); n.stream = s; return n; };
const gain = () => (gains.length ? gains[gains.length - 1].gain.value : null);

const gum = [];
let micTrack = null;
const navigator = {
  userAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)",
  mediaDevices: {
    getUserMedia(c) {
      gum.push(c);
      micTrack = { id: "mic", label: "USB Audio CODEC <1>", enabled: true, stopped: false, stop() { this.stopped = true; } };
      return Promise.resolve({ getAudioTracks: () => [micTrack] });
    },
  },
};

const notices = [];
const history = { calls: [], replaceState(s, t, u) { this.calls.push(u); location.hash = ""; } };
const location = { pathname: scenario === "dashboard" ? "/" : "/radio/ic7300", search: "", hash: scenario.startsWith("hash") ? "#listen" : "", protocol: "https:", host: "pi.local", hostname: "pi.local", assign() {} };
const ctx = {
  document: doc, console, fetch, JSON, Promise, encodeURIComponent, Date: FakeDate, Math, Object, String, Number, Array, Set, Uint8Array,
  WebSocket: FakeSocket, RTCPeerConnection: FakePC, MediaStream: FakeStream, AudioContext: FakeAudioContext, navigator, location, history,
  setInterval: (fn, ms) => addTimer(fn, ms, true), setTimeout: (fn, ms) => addTimer(fn, ms, false),
  clearInterval: (id) => timers.delete(id), clearTimeout: (id) => timers.delete(id),
  crypto: { randomUUID: () => "tab-1" },
  localStorage: { getItem: () => null, setItem() {} }, notice(t, k) { notices.push(`${k}: ${t}`); },
  addEventListener(t, f) { (winHandlers[t] = winHandlers[t] || []).push(f); },
};
ctx.window = ctx;
vm.createContext(ctx);

// ---- the dashboard's Listen link (dashboard.js over stubs of dxberry.js's helpers) -------------
if (scenario === "dashboard") {
  const mk = (tag, attrs, ...kids) => ({ tag, attrs: attrs || {}, kids: kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false) });
  const text = (e) => (typeof e === "object" ? e.kids.map(text).join("") : String(e));
  const all = (e, fn, r = []) => { if (typeof e === "object") { if (fn(e)) r.push(e); for (const k of e.kids) all(k, fn, r); } return r; };
  Object.assign(ctx, {
    DASH: "—", NAME_RE: /^[a-z][a-z0-9]{0,11}$/, PROGRAM_RE: /^[a-z][a-z0-9-]*$/, pending: {}, updates: { job: null }, updWatchers: [], last: null,
    el: mk, kv: (rows) => mk("dl", {}, rows.map((r) => r[1])), badge: (t) => mk("span", {}, t), btn: (t) => mk("button", {}, t),
    parts: () => "", freq: () => "—", appLabel: (r, n) => ({ wsjtx: "WSJT-X", graywolf: "Graywolf" })[n] || n, perRadioProgram: () => true,
    openRadioForm() {}, num: String, dxb: { events: { on() {}, onOpen() {}, send() { return true; } }, api() { return Promise.resolve({}); }, hidden: false },
  });
  ctx.document = { getElementById: () => ({ addEventListener() {} }), querySelectorAll: () => [] };
  vm.runInContext(fs.readFileSync(dir + "/vfo.js", "utf8"), ctx); // its pure part: window.dxbVFO
  vm.runInContext(fs.readFileSync(dir + "/dashboard.js", "utf8"), ctx);
  const s = { radios: { ok: true, radios: {} }, apps: { ok: true, programs: [] } };
  const card = (n, x) => {
    const c = ctx.dashRadioCard(s, n, Object.assign({ present: true, kernel: {} }, x));
    const a = all(c, (e) => e.tag === "a" && text(e) === "Listen")[0];
    return a ? { href: a.attrs.href, aria: a.attrs["aria-label"], cls: a.attrs.class } : null;
  };
  console.log(JSON.stringify({
    withAudio: card("radio2", { audio: { path: "usb-0:1.4:1.0" }, cat: { path: "x" }, owner: "wsjtx" }),
    noCat: card("radio4", { audio: { path: "usb-0:1.5:1.0" }, owner: "" }),
    noAudio: card("radio3", { cat: { path: "x" }, owner: "" }),
    graywolf: card("radio1", { audio: { path: "usb-0:1.1:1.0" }, cat: { path: "x" }, owner: "graywolf" }),
  }));
  process.exit(0);
}

vm.runInContext(fs.readFileSync(dir + "/app.js", "utf8"), ctx);
ctx.dxb.events.start(() => {});
if (scenario !== "nojs") vm.runInContext(fs.readFileSync(dir + "/audio.js", "utf8"), ctx);
vm.runInContext(fs.readFileSync(dir + "/vfo.js", "utf8"), ctx);

// ---- what the operator sees ---------------------------------------------------------------------
const q = (cls) => els.vfo.find((e) => e.has(cls))[0];
const disabled = (e) => !!e && e.attrs.disabled !== undefined;
const shown = (e) => !!e && !e.hidden;
const click = (e, ev) => { for (const f of e.handlers.click || []) f(ev || { target: e, preventDefault() {} }); };
const tapDoc = (target) => { for (const f of docHandlers.pointerup || []) f({ target, pointerId: 9 }); };
const chip = () => doc.body.children.find((e) => e && e.has && e.has("audio-lost"));
const audioEl = () => doc.body.children.filter((e) => e && e.tag === "audio").pop();
const buttonNamed = (root, t) => root.find((e) => e.tag === "button" && e.textContent === t)[0];
const dialog = () => doc.body.children.find((e) => e && e.tag === "dialog");
function snap() {
  const L = q("vfo-listen"), M = q("vfo-mic"), why = q("vfo-audio-why"), rx = q("vfo-rx"), P = q("vfo-ptt"), pw = q("vfo-ptt-why");
  return {
    listenPressed: L.attrs["aria-pressed"], listenDisabled: disabled(L), listenText: L.textContent,
    micText: M.textContent, micDisabled: disabled(M), micLine: q("vfo-mic-line").textContent,
    rx: shown(rx) ? q("vfo-rx-text").textContent : "", why: shown(why) ? why.textContent : "",
    chip: shown(chip()), pttDisabled: disabled(P), pttWhy: shown(pw) ? pw.textContent : "",
  };
}
// connect I: the station answers signalling socket I and the radio's track arrives
async function connect(i) {
  await flush();
  sigs()[i].onopen();
  await flush();
  sigs()[i].onmessage({ data: JSON.stringify({ type: "answer", sdp: "ANSWER" }) });
  await flush();
  pcs[i].ontrack({ track: { id: "radio" } });
  await flush();
}
const button = () => q("vfo-ptt");
const press = () => button().handlers.pointerdown[0]({ pointerId: 1, preventDefault() {} });
function release(kind) {
  if (kind === "pointerup" || kind === "pointercancel") button().handlers[kind][0]({ pointerId: 1 });
  else for (const f of winHandlers[kind] || []) f({});
}
const evNotice = (m) => evs().onmessage({ data: JSON.stringify(m) });

(async () => {
  await flush();
  evs().onopen();
  await flush();
  const out = {};

  if (scenario === "listen") {
    // the speaker: a toggle that opens the radio's audio, never the microphone
    out.before = snap();
    click(q("vfo-listen"));
    // the audio context is made inside the tap itself
    out.contextAtTap = contexts.length;
    out.connecting = snap();
    const live = () => q("vfo-rx-state").textContent;
    out.live = { connecting: live() };
    out.urls = sigs().map((s) => s.url);
    await connect(0);
    out.context = { made: contexts.length, state: contexts[0].state };
    out.graph = graph.slice();
    out.muted = audioEl().muted === true;
    out.listening = snap().rx;
    out.live.listening = live();
    await advance(250);
    out.silent = snap().rx;
    out.live.silent = live();
    // a quiet noise floor (about -63 dBFS) is not "receiving"
    amp = 0.001;
    await advance(250);
    out.noise = snap().rx;
    amp = 0.5;
    await advance(250);
    out.receiving = snap().rx;
    out.live.receiving = live();
    // only the state is announced: the level line is not a live region
    out.ariaLive = { line: q("vfo-rx").getAttribute("aria-live"), state: q("vfo-rx-state").getAttribute("aria-live") };
    out.meterUp = Number(q("vfo-rx-level").attrs.value) > 0.5;
    // tapped again: the station is told, everything closes, the audio context sleeps
    click(q("vfo-listen"));
    out.stopped = snap();
    out.messages = sigs().map((s) => s.sent.map((m) => m.type));
    out.closed = { socket: sigs()[0].closed, pc: pcs[0].closed, context: contexts[0].state };
    const timersListening = [...timers.values()].filter((t) => t.ms === 250).length;
    out.levelTimers = timersListening;
    // and once more: a new connection on the same audio context
    click(q("vfo-listen"));
    await connect(1);
    out.again = { sockets: sigs().length, made: contexts.length, state: contexts[0].state, pressed: snap().listenPressed };
    out.gum = gum.length;
  } else if (scenario === "mic") {
    out.before = snap();
    click(q("vfo-mic"));
    await flush();
    out.after = snap();
    click(q("vfo-mic"));
    await flush();
    out.gum = gum.length;
    // listening afterwards carries the open microphone, idle until PTT
    click(q("vfo-listen"));
    await connect(0);
    out.onConnection = { replaced: pcs[0].replaced, enabled: micTrack.enabled };
    // stopping the audio lets the microphone go; the button offers it again
    click(q("vfo-listen"));
    out.released = { stopped: micTrack.stopped, text: q("vfo-mic").textContent, disabled: disabled(q("vfo-mic")) };
  } else if (scenario === "program") {
    click(q("vfo-listen"));
    await connect(0);
    out.listening = snap();
    // WSJT-X starts on this radio: the station stops the sound server; the page's next poll says so
    st.owner = { kind: "program", label: "WSJT-X", mine: false };
    st.rec.owner = "wsjtx";
    st.audio = WSJTX_CARD;
    await advance(5000);
    out.stopped = snap();
    out.close = sigs()[0].sent[sigs()[0].sent.length - 1];
    out.closedSocket = sigs()[0].closed;
    out.notices = notices.slice();
    await advance(5000);
    out.noticesLater = notices.length;
    // the program ends: the card is the sound server's again
    st.owner = null;
    st.rec.owner = "";
    st.audio = AVAILABLE;
    await advance(5000);
    out.freed = snap();
    out.freedNotice = notices[notices.length - 1];
    // listening again; the sound server stops under it before any poll (audio lost): the page asks
    // at once, the reconnect chip goes and the program's words show
    click(q("vfo-listen"));
    await connect(1);
    st.owner = { kind: "program", label: "WSJT-X", mine: false };
    st.rec.owner = "wsjtx";
    st.audio = WSJTX_CARD;
    const before = gets();
    sigs()[1].onmessage({ data: JSON.stringify({ type: "audio-lost" }) });
    out.chipAtLoss = shown(chip());
    await flush();
    out.askedAtOnce = gets() - before;
    out.afterLoss = snap();
    out.noticesAfterLoss = notices.slice(out.notices.length + 1);
  } else if (scenario === "lost") {
    click(q("vfo-listen"));
    await connect(0);
    press();
    await flush();
    out.holding = button().textContent;
    out.gainHeld = gain();
    // the station's connection is lost: the hold ends (one off), the chip offers a reconnect
    sigs()[0].onmessage({ data: JSON.stringify({ type: "audio-lost" }) });
    await flush();
    out.lost = snap();
    out.labelAfter = button().textContent;
    out.posts = posts.slice();
    // the station says why PTT dropped
    evNotice({ type: "ptt", radio: "ic7300", on: false, reason: "audio lost", window: 1 });
    await flush();
    out.notices = notices.slice();
    click(chip());
    out.reconnecting = snap();
    out.sockets = sigs().length;
    await connect(1);
    out.back = snap();
    out.gain = gain();
  } else if (scenario === "ptt") {
    // each release event ends the hold once, the RX gain comes back and the microphone goes idle
    out.rounds = {};
    for (const kind of ["pointerup", "pointercancel", "blur", "pagehide"]) {
      posts.length = 0;
      hbAt.length = 0;
      press();
      if (sigs().length === 1 && !sigs()[0].sent.length) await connect(0);
      await flush();
      const r = { on: posts.filter((p) => p === "on").length, label: button().textContent };
      r.held = { gain: gain(), mic: micTrack.enabled, duck: shown(q("vfo-duck")) ? q("vfo-duck").textContent : "" };
      await advance(65000);
      r.time = q("vfo-ptt-time").textContent;
      r.hb = posts.filter((p) => p === "hb").length;
      r.hbEvery = [...new Set(hbAt.slice(1).map((t, i) => t - hbAt[i]))];
      release(kind);
      // this event alone ended the hold (holdEnd paints at once) and sent the off ...
      r.labelAtRelease = button().textContent;
      await flush();
      r.offAtRelease = posts.filter((p) => p === "off").length;
      // ... and every release event after it sends nothing more
      for (const other of ["pointerup", "pointercancel", "blur", "pagehide"]) release(other);
      await flush();
      r.off = posts.filter((p) => p === "off").length;
      r.after = { gain: gain(), mic: micTrack.enabled, duckShown: shown(q("vfo-duck")), label: button().textContent, timeShown: shown(q("vfo-ptt-time")) };
      await advance(15000);
      r.hbAfter = posts.filter((p) => p === "hb").length - r.hb;
      r.first = posts[0];
      r.last = posts[posts.length - 1];
      out.rounds[kind] = r;
    }
    out.gum = gum.length;
    out.sockets = sigs().length;
  } else if (scenario === "guard") {
    const box = () => q("vfo-guard");
    const g = () => ({ hidden: !!box().hidden, pttDisabled: disabled(button()), pttWhy: snap().pttWhy });
    out.start = g();
    // Run ATU refused: the station's words, the banner stays
    answers.atu = [{ ok: false, reason: "the tuner did not answer", fix: "try Run ATU again" }];
    click(buttonNamed(box(), "Run ATU"));
    await flush();
    out.atuRefused = { notice: notices[notices.length - 1], hidden: !!box().hidden };
    // Run ATU again: a good tune; the station's clear notice hides the banner and PTT comes back
    click(buttonNamed(box(), "Run ATU"));
    await flush();
    st.guard = { active: true, latched: false, reason: "" };
    evNotice({ type: "guard", radio: "ic7300", latched: false, reason: "a good ATU tune" });
    await flush();
    out.atuCleared = g();
    out.atuPosts = posted("atu");
    // a trip while transmitting: the hold ends at once (one off), the RX gain comes back
    press();
    await connect(0);
    await flush();
    out.tripHeld = { label: button().textContent, gain: gain() };
    st.guard = { active: true, latched: true, reason: GUARD_REASON };
    evNotice({ type: "guard", radio: "ic7300", latched: true, reason: GUARD_REASON });
    await flush();
    out.tripped = { label: button().textContent, gain: gain(), posts: posts.slice(), banner: g() };
    // Override refused leaves the banner up; confirmed and accepted, it goes
    answers.override = [{ ok: false, reason: "the radio is not answering", fix: "check the radio" }];
    click(buttonNamed(box(), "Override"));
    click(buttonNamed(dialog(), "Transmit anyway"));
    await flush();
    out.overrideRefused = { notice: notices[notices.length - 1], banner: g() };
    click(buttonNamed(box(), "Override"));
    click(buttonNamed(dialog(), "Transmit anyway"));
    await flush();
    out.overridden = g();
    // Run ATU and Override are the owner's: when another tab holds the transmitter (and the
    // guard has latched again) they are disabled
    st.owner = { kind: "tab", label: "iPhone", mine: false };
    st.guard = { active: true, latched: true, reason: GUARD_REASON };
    await advance(5000);
    out.notOwner = { hidden: !!box().hidden, atu: disabled(buttonNamed(box(), "Run ATU")), override: disabled(buttonNamed(box(), "Override")), pttWhy: snap().pttWhy };
  } else if (scenario === "hash" || scenario === "hashbutton" || scenario === "hashnoaudio") {
    // the dashboard's Listen opens the page with #listen: nothing plays until the first tap
    out.before = snap();
    out.armed = q("vfo-listen").has("is-armed");
    out.socketsBefore = sigs().length;
    if (scenario === "hashbutton") {
      tapDoc(q("vfo-listen"));
      click(q("vfo-listen"));
    } else {
      tapDoc(q("vfo-readout"));
    }
    await flush();
    out.after = snap();
    out.sockets = sigs().length;
    out.armedAfter = q("vfo-listen").has("is-armed");
    out.history = history.calls;
    tapDoc(q("vfo-readout"));
    await flush();
    out.socketsLater = sigs().length;
  } else if (scenario === "reasons") {
    const V = ctx.dxbVFO;
    out.pure = {
      program: V.audioWhy(WSJTX_CARD, { owner: "wsjtx" }, { kind: "program", label: "WSJT-X" }, true),
      flrig: V.audioWhy({ available: false, reason: "no sound server for ic7300: /run/dxberry/audio/ic7300/native is missing" }, { owner: "flrig" }, { kind: "program", label: "flrig" }, true),
      down: V.audioWhy({ available: false, reason: "no sound server for ic7300: the server is not running" }, { owner: "" }, null, true),
      none: V.audioWhy(null, null, null, true),
      nojs: V.audioWhy(AVAILABLE, null, null, false),
      ok: V.audioWhy(AVAILABLE, { owner: "js8call" }, { kind: "program", label: "JS8Call" }, true),
      holder: V.cardHolder(WSJTX_CARD, null, null),
    };
    // listening when the sound server stops (no program). One answer that says so (a status call
    // that timed out on a busy Pi) leaves the stream alone; two in a row stop it with the
    // station's words
    const DOWN = { available: false, reason: "no sound server for ic7300: the server is not running" };
    const flowing = () => ({ pressed: snap().listenPressed, closed: sigs()[0].closed, notices: notices.length });
    click(q("vfo-listen"));
    await connect(0);
    st.audio = DOWN;
    await advance(5000);
    out.blip = flowing();
    st.audio = AVAILABLE;
    await advance(5000);
    out.afterBlip = flowing();
    st.audio = DOWN;
    await advance(5000);
    out.downOnce = flowing();
    await advance(5000);
    out.down = snap();
    click(q("vfo-listen"));
    click(q("vfo-mic"));
    await flush();
    out.downSockets = sigs().length;
    out.downGum = gum.length;
    // the server is back: the page says so (not in a program's terms)
    st.audio = AVAILABLE;
    await advance(5000);
    out.back = snap();
  } else if (scenario === "nojs") {
    out.page = snap();
  } else if (scenario === "connecting") {
    // the microphone allowed first (the Mic button); a press then waits for the audio to connect
    // and for the station's answer to the on (the station waits up to 2 s for the browser's first
    // audio): the button says Connecting…, not that the microphone is opening
    click(q("vfo-mic"));
    await flush();
    deferOn = true;
    press();
    await flush();
    out.audioConnecting = button().textContent;
    await connect(0);
    out.onPending = { label: button().textContent, posts: posts.slice() };
    answerOn();
    await flush();
    out.answered = button().textContent;
    release("pointerup");
    await flush();
    out.posts = posts.slice();
  } else if (scenario === "pagestop") {
    // Listen tapped off while a press is still opening: the hold ends, nothing goes to the
    // station, and the page does not tell the operator off for its own stop
    press();
    click(q("vfo-listen"));
    await flush();
    out.press = { posts: posts.slice(), notices: notices.slice(), label: button().textContent };
    // Listen tapped off while the Mic button's prompt is still open: the microphone it grants is
    // let go, again without a notice
    click(q("vfo-listen"));
    await connect(1);
    const realGum = navigator.mediaDevices.getUserMedia;
    let grant = null;
    navigator.mediaDevices.getUserMedia = (c) => { gum.push(c); return new Promise((r) => { grant = r; }); };
    const late = { id: "late-mic", label: "Headset", enabled: true, stopped: false, stop() { this.stopped = true; } };
    click(q("vfo-mic"));
    click(q("vfo-listen"));
    grant({ getAudioTracks: () => [late] });
    await flush();
    out.mic = { notices: notices.slice(), stopped: late.stopped, text: q("vfo-mic").textContent };
    // a microphone the Mic button opened, not listening: a program taking the sound card lets
    // it go too (the browser's recording light goes out), with the program's words once
    navigator.mediaDevices.getUserMedia = realGum;
    click(q("vfo-mic"));
    await flush();
    out.micOpen = q("vfo-mic").textContent;
    st.owner = { kind: "program", label: "WSJT-X", mine: false };
    st.rec.owner = "wsjtx";
    st.audio = WSJTX_CARD;
    await advance(5000);
    out.taken = { stopped: micTrack.stopped, text: q("vfo-mic").textContent, notices: notices.slice() };
  }
  out.notices = out.notices || notices;
  console.log(JSON.stringify(out));
})().catch((e) => { console.error(e); process.exit(1); });
