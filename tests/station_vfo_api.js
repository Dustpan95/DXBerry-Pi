// Loads station/web/app.js over a stub DOM, a fake fetch and a fake WebSocket and prints how the
// radio pages' layer behaves: the tab token, dxb.api, and the events socket's extra messages.
// Run: node tests/station_vfo_api.js station/web/app.js   (prints one JSON object)
"use strict";
const fs = require("fs");
const vm = require("vm");

const calls = [];
const sockets = [];
const assigned = [];
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => (body === undefined ? Promise.reject(new Error("no json")) : Promise.resolve(body)) });
const fetch = (url, opts) => {
  calls.push({ url, method: opts.method, headers: opts.headers, body: opts.body });
  if (url.endsWith("/freq")) return reply(200, { ok: false, reason: "99 is outside this radio's range", fix: "this radio tunes 30000-74800000 Hz" });
  if (url.endsWith("/mode")) return reply(200, { ok: true });
  if (url.endsWith("/nope")) return reply(404, { error: "no such radio" });
  if (url.endsWith("/garbled")) return reply(500, undefined);
  if (url.endsWith("/login")) return reply(401, { error: "login required" });
  return Promise.reject(new Error("down"));
};
const stubEl = () => ({ hidden: false, textContent: "", addEventListener() {}, append() {}, replaceChildren() {}, setAttribute() {}, classList: { toggle() {} } });
function FakeSocket(url) { this.url = url; this.sent = []; sockets.push(this); }
FakeSocket.prototype.close = function () {};
FakeSocket.prototype.send = function (m) { this.sent.push(m); };
let uuid = 0;
const ctx = {
  document: { hidden: false, getElementById: stubEl, addEventListener() {}, createElement: stubEl },
  location: { pathname: process.argv[3] || "/radio/ic7300", search: "", hash: "", protocol: "https:", host: "pi.local", assign: (u) => assigned.push(u) },
  fetch, JSON, encodeURIComponent, Promise, setInterval() {}, setTimeout() { return 1; }, clearTimeout() {}, Date, Math, String,
  WebSocket: FakeSocket, crypto: { randomUUID: () => "uuid-" + ++uuid },
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
const dxb = ctx.dxb;

(async () => {
  const out = {};
  out.tab = dxb.tab();
  out.tabStable = dxb.tab() === out.tab;
  out.refused = await dxb.api("POST", "/api/radio/ic7300/freq", { hz: 99 });
  out.accepted = await dxb.api("POST", "/api/radio/ic7300/mode", { mode: "USB" });
  out.missing = await dxb.api("GET", "/api/radio/nope");
  out.garbled = await dxb.api("GET", "/api/radio/garbled");
  out.down = await dxb.api("GET", "/api/radio/x");
  out.calls = calls.slice(0, 2).map((c) => ({ url: c.url, method: c.method, tab: c.headers["X-DXB-Tab"], type: c.headers["Content-Type"], body: c.body }));
  dxb.api("GET", "/api/other");
  out.otherHasTab = Object.prototype.hasOwnProperty.call(calls[calls.length - 1].headers, "X-DXB-Tab");

  // the events socket: the tab token on the address, rig messages to their handlers, a sent watch
  const rig = [];
  let opened = 0;
  let closed = 0;
  dxb.events.on("rig", (m) => rig.push(m));
  dxb.events.onOpen(() => { opened++; });
  dxb.events.onClose(() => { closed++; });
  out.sendBeforeOpen = dxb.events.send({ type: "watch" });
  dxb.events.start(() => {});
  const s = sockets[0];
  out.socketUrl = s.url;
  s.onopen();
  out.opened = opened;
  out.sendOpen = dxb.events.send({ type: "watch", radio: "ic7300", level: "page" });
  out.sent = s.sent;
  s.onmessage({ data: JSON.stringify({ type: "rig", radio: "ic7300", data: { freq: 14074000 } }) });
  s.onmessage({ data: JSON.stringify({ type: "radios", data: [] }) });
  s.onmessage({ data: "junk" });
  out.rig = rig;
  s.onclose();
  out.closed = closed;
  console.log(JSON.stringify(out));
})();
