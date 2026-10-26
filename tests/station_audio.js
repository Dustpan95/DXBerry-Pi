// Loads station/web/audio.js over a stub DOM, a fake RTCPeerConnection, a fake WebSocket, a fake
// AudioContext and a fake getUserMedia, and prints what it did: the signalling messages, the
// microphone, the 20 dB duck on the Web Audio gain, the lost chip and its reconnect.
// Run: node tests/station_audio.js station/web/audio.js [noaudioctx]   (prints one JSON object)
//   noaudioctx: a browser without Web Audio, where the element's volume ducks instead
"use strict";
const fs = require("fs");
const vm = require("vm");
const mode = process.argv[3] || "webaudio";

const sockets = [];
const pcs = [];
const els = [];
const gum = [];
const tick = () => new Promise((r) => setImmediate(r));

function el(tag) {
  const e = {
    tag, hidden: false, children: [], attrs: {}, listeners: {}, removed: false, played: 0, paused: 0, volume: 1,
    append(...k) { this.children.push(...k); },
    remove() { this.removed = true; },
    addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); },
    setAttribute(k, v) { this.attrs[k] = v; },
    play() { this.played++; return Promise.resolve(); },
    pause() { this.paused++; },
    get text() { return this.children.map((c) => (typeof c === "object" ? c.data || "" : String(c))).join(""); },
  };
  els.push(e);
  return e;
}
const header = el("header");
const link = el("span");
link.parentNode = { insertBefore(n, ref) { header.inserted = { node: n, after: ref === null ? null : "link-next" }; } };
const body = el("body");

function FakeSocket(url) { this.url = url; this.sent = []; this.closed = 0; sockets.push(this); }
FakeSocket.prototype.send = function (m) { this.sent.push(JSON.parse(m)); };
FakeSocket.prototype.close = function () { this.closed++; };

function FakePC(cfg) {
  this.cfg = cfg; this.transceivers = []; this.remote = null; this.added = []; this.closed = 0; this.replaced = [];
  this.localDescription = null; this.connectionState = "new"; pcs.push(this);
}
FakePC.prototype.addTransceiver = function (kind, init) {
  const pc = this;
  const t = { kind, init, sender: { replaceTrack(tr) { pc.replaced.push(tr.id); return Promise.resolve(); } } };
  this.transceivers.push(t);
  return t;
};
FakePC.prototype.createOffer = function () { return Promise.resolve({ type: "offer", sdp: "OFFER-" + pcs.indexOf(this) }); };
FakePC.prototype.setLocalDescription = function (d) { this.localDescription = d; return Promise.resolve(); };
FakePC.prototype.setRemoteDescription = function (d) { this.remote = d; return Promise.resolve(); };
FakePC.prototype.addIceCandidate = function (c) { this.added.push(c); return Promise.resolve(); };
FakePC.prototype.close = function () { this.closed++; };
function FakeStream() { this.tracks = []; }
FakeStream.prototype.addTrack = function (t) { this.tracks.push(t.id); };

// Web Audio: every connect is written down as "from>to"; the analyser reads a sine of amplitude amp
const graph = [];
const contexts = [];
const gains = [];
let amp = 0;
function Node(kind) { this.kind = kind; }
Node.prototype.connect = function (n) { graph.push(this.kind + ">" + n.kind); return n; };
Node.prototype.disconnect = function () { graph.push(this.kind + " off"); };
function FakeAudioContext() { this.state = "running"; this.destination = new Node("destination"); this.resumed = 0; this.suspended = 0; contexts.push(this); }
FakeAudioContext.prototype.resume = function () { this.resumed++; this.state = "running"; return Promise.resolve(); };
FakeAudioContext.prototype.suspend = function () { this.suspended++; this.state = "suspended"; return Promise.resolve(); };
FakeAudioContext.prototype.createGain = function () { const n = new Node("gain"); n.gain = { value: 1 }; gains.push(n); return n; };
FakeAudioContext.prototype.createAnalyser = function () {
  const n = new Node("analyser");
  n.fftSize = 2048;
  n.getByteTimeDomainData = (b) => { for (let i = 0; i < b.length; i++) b[i] = 128 + Math.round(127 * amp * Math.sin(i / 3)); };
  return n;
};
FakeAudioContext.prototype.createMediaStreamSource = function (st) { const n = new Node("source"); n.stream = st; return n; };
const gain = () => (gains.length ? gains[gains.length - 1].gain.value : null);

let micTrack = null;
const ctx = {
  document: {
    body,
    getElementById: (id) => (id === "link" ? link : null),
    createElement: el,
    createTextNode: (data) => ({ data }),
  },
  navigator: {
    mediaDevices: {
      getUserMedia(c) {
        gum.push(c);
        micTrack = { id: "mic", label: "USB Audio CODEC", enabled: true, stopped: false, stop() { this.stopped = true; } };
        return Promise.resolve({ getAudioTracks: () => [micTrack] });
      },
    },
  },
  dxb: { ws: (p) => new FakeSocket("wss://pi.local" + p), tab: () => "iPhone#tab-1" },
  RTCPeerConnection: FakePC, MediaStream: FakeStream, JSON, Promise, Math, String, encodeURIComponent,
};
if (mode === "webaudio") ctx.AudioContext = FakeAudioContext;
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
const A = ctx.dxbAudio;
const states = [];
if (A.onChange) A.onChange((m) => states.push(m.state));

(async () => {
  const out = {};
  out.api = Object.keys(A).filter((k) => typeof A[k] === "function").sort();
  out.duck = { db: A.DUCK_DB, gain: A.DUCK };
  out.bad = await A.listen("../x");

  // listen: the element plays inside the tap, before any network; the mic is not asked for
  const listening = A.listen("ic7300");
  const s = sockets[0];
  const pc = pcs[0];
  const audioEl = els.find((e) => e.tag === "audio");
  out.socketUrl = s.url;
  out.playedInTap = audioEl.played;
  out.audioAttached = body.children.includes(audioEl) && audioEl.autoplay === true && audioEl.hidden === true;
  out.pcConfig = pc.cfg;
  out.transceiver = pc.transceivers.map((t) => [t.kind, t.init.direction]);
  out.gumBeforeMic = gum.length;
  // a candidate before the offer waits for it; one after goes at once
  pc.onicecandidate({ candidate: { toJSON: () => ({ candidate: "candidate:1 1 udp 1 10.0.0.9 5000 typ host", sdpMid: "0", sdpMLineIndex: 0 }) } });
  out.sentBeforeOpen = s.sent.length;
  s.onopen();
  await tick();
  pc.onicecandidate({ candidate: { candidate: "candidate:2 1 udp 1 10.0.0.9 5001 typ host", sdpMid: "0", sdpMLineIndex: 0 } });
  pc.onicecandidate({ candidate: null });
  out.sent = s.sent.slice();
  s.onmessage({ data: JSON.stringify({ type: "answer", sdp: "ANSWER" }) });
  s.onmessage({ data: JSON.stringify({ type: "ice", candidate: { candidate: "candidate:9 1 udp 1 10.0.0.2 50001 typ host", sdpMid: "0", sdpMLineIndex: 0 } }) });
  s.onmessage({ data: "junk" });
  out.listened = await listening;
  out.remote = pc.remote;
  out.addedIce = pc.added;
  // the audio graph is made inside the tap; the radio's track joins it when it arrives
  out.contextInTap = contexts.length;
  out.levelBeforeTrack = A.level();
  pc.ontrack({ track: { id: "radio" } });
  out.playing = audioEl.srcObject.tracks;
  out.graph = graph.slice();
  out.muted = audioEl.muted === true;
  out.levelSilent = A.level();
  amp = 0.5;
  const loud = A.level();
  out.levelLoud = loud === null ? null : Math.round(loud * 100) / 100;
  amp = 0;

  // ducking without a mic, the mic on the first press only, then PTT
  A.setTX(true);
  out.gainTXNoMic = gain();
  out.volTXNoMic = audioEl.volume;
  A.setTX(false);
  out.gainRX = gain();
  out.micLabelBefore = A.micLabel();
  out.mic = await A.openMic();
  await A.openMic();
  out.micLabel = A.micLabel();
  out.gum = gum.slice();
  out.micReplaced = pc.replaced;
  out.micIdle = micTrack.enabled;
  A.setTX(true);
  out.tx = { mic: micTrack.enabled, gain: gain(), volume: audioEl.volume };
  A.setTX(false);
  out.rx = { mic: micTrack.enabled, gain: gain(), volume: audioEl.volume };

  // audio lost: the connection goes, the chip shows; tapping it reconnects with the same mic
  s.onmessage({ data: JSON.stringify({ type: "audio-lost" }) });
  const chip = header.inserted.node;
  out.lost = { chipShown: !chip.hidden, chipText: chip.text, chipTag: chip.tag, chipType: chip.type, pcClosed: pc.closed, closeSent: s.sent[s.sent.length - 1], socketClosed: s.closed, elRemoved: audioEl.removed };
  s.onclose();
  out.socketsAfterLost = sockets.length;
  out.graphAfterLost = graph.slice(out.graph.length);
  chip.listeners.click[0]();
  out.reconnect = { chipHidden: chip.hidden, sockets: sockets.length, url: sockets[1].url, mic: pcs[1].replaced };
  // the reconnection keeps the one audio context (resumed only when it was asleep)
  out.reconnectContext = { made: contexts.length, resumed: contexts.length ? contexts[0].resumed : null };

  // the station refuses: the reason and fix come back, nothing stays open, no chip
  const refusing = A.listen("ic7300");
  out.reconnectTornDown = pcs[1].closed;
  const s2 = sockets[2];
  const again = pcs[2];
  s2.onopen();
  await tick();
  s2.onmessage({ data: JSON.stringify({ type: "refused", reason: "no sound server for ic7300: WSJT-X has the sound card", fix: "start the radio's sound server or stop the program that holds the sound card" }) });
  out.refused = await refusing;
  out.refusedPC = again.closed;
  out.refusedChip = chip.hidden;

  // a socket that closes before the answer is unreachable
  const third = A.listen("ic7300");
  sockets[3].onclose();
  out.closedEarly = await third;

  // stop: the mic is released
  const fourth = A.listen("ic7300");
  sockets[4].onopen();
  await tick();
  sockets[4].onmessage({ data: JSON.stringify({ type: "answer", sdp: "A4" }) });
  await fourth;
  // transmitting when audio stops: the next connection starts at full gain
  A.setTX(true);
  A.stop();
  out.stopped = { mic: micTrack.stopped, pc: pcs[4].closed, socket: sockets[4].closed, close: sockets[4].sent[sockets[4].sent.length - 1] };
  out.stoppedContext = contexts.length ? contexts[0].state : null;
  out.micLabelStopped = A.micLabel();
  // stop while the microphone prompt is still open: the track it later grants is stopped at once
  let grant = null;
  ctx.navigator.mediaDevices.getUserMedia = (c) => {
    gum.push(c);
    return new Promise((r) => { grant = r; });
  };
  const late = { id: "late-mic", enabled: true, stopped: false, stop() { this.stopped = true; } };
  const asking = A.openMic();
  A.stop();
  grant({ getAudioTracks: () => [late] });
  out.raced = { result: await asking, stopped: late.stopped };
  // the next press asks again
  const again2 = A.openMic();
  out.askedAgain = gum.length;
  grant({ getAudioTracks: () => [{ id: "mic-2", enabled: true, stop() {} }] });
  out.againResult = await again2;
  // a listen replaced or stopped before its answer resolves (it does not hang)
  const replaced = A.listen("ic7300");
  A.listen("ic7300");
  out.replaced = await replaced;
  A.stop();
  out.gainAfterStop = gain();
  out.states = states;
  console.log(JSON.stringify(out));
})();
