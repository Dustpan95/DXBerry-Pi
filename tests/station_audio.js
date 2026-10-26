// Loads station/web/audio.js over a stub DOM, a fake RTCPeerConnection, a fake WebSocket and a
// fake getUserMedia, and prints what it did: the signalling messages, the microphone, the
// 20 dB duck, the lost chip and its reconnect.
// Run: node tests/station_audio.js station/web/audio.js   (prints one JSON object)
"use strict";
const fs = require("fs");
const vm = require("vm");

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
        micTrack = { id: "mic", enabled: true, stopped: false, stop() { this.stopped = true; } };
        return Promise.resolve({ getAudioTracks: () => [micTrack] });
      },
    },
  },
  dxb: { ws: (p) => new FakeSocket("wss://pi.local" + p), tab: () => "iPhone#tab-1" },
  RTCPeerConnection: FakePC, MediaStream: FakeStream, JSON, Promise, Math, String, encodeURIComponent,
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
const A = ctx.dxbAudio;

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
  pc.ontrack({ track: { id: "radio" } });
  out.playing = audioEl.srcObject.tracks;

  // ducking without a mic, the mic on the first press only, then PTT
  A.setTX(true);
  out.volTXNoMic = audioEl.volume;
  A.setTX(false);
  out.volRX = audioEl.volume;
  out.mic = await A.openMic();
  await A.openMic();
  out.gum = gum;
  out.micReplaced = pc.replaced;
  out.micIdle = micTrack.enabled;
  A.setTX(true);
  out.tx = { mic: micTrack.enabled, volume: audioEl.volume };
  A.setTX(false);
  out.rx = { mic: micTrack.enabled, volume: audioEl.volume };

  // audio lost: the connection goes, the chip shows; tapping it reconnects with the same mic
  s.onmessage({ data: JSON.stringify({ type: "audio-lost" }) });
  const chip = header.inserted.node;
  out.lost = { chipShown: !chip.hidden, chipText: chip.text, chipTag: chip.tag, chipType: chip.type, pcClosed: pc.closed, closeSent: s.sent[s.sent.length - 1], socketClosed: s.closed, elRemoved: audioEl.removed };
  s.onclose();
  out.socketsAfterLost = sockets.length;
  chip.listeners.click[0]();
  out.reconnect = { chipHidden: chip.hidden, sockets: sockets.length, url: sockets[1].url, mic: pcs[1].replaced };

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
  A.stop();
  out.stopped = { mic: micTrack.stopped, pc: pcs[4].closed, socket: sockets[4].closed, close: sockets[4].sent[sockets[4].sent.length - 1] };
  console.log(JSON.stringify(out));
})();
