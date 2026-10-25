// Loads station/web/app.js and dxberry.js together over a permissive stub DOM, a fake fetch and a
// fake WebSocket, and checks how the page uses the events stream: no timer poll while the socket
// is open, a render per status message, the callsign in the top bar, and the poll back on close.
// Run: node tests/station_events.js station/web   (prints one JSON object)
"use strict";
const fs = require("fs");
const vm = require("vm");
const dir = process.argv[2];

const statusRuns = [];      // every dxberry-status the page ran
const sockets = [];         // every WebSocket it opened
const intervals = [];       // setInterval callbacks
const timeouts = [];        // setTimeout {fn, ms}
const els = {};

const report = (generated, call) => JSON.stringify({ generated, station: { ok: true, callsign: call }, network: { hostname: "pi" } });
const reply = (body) => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ exit: 0, stdout: body, stderr: "" }) });
const fetch = (url) => {
  if (url.endsWith("/dxberry-status")) { statusRuns.push(url); return reply(report(1, "POLL")); }
  return reply("");
};
// an element that tolerates everything the page does to it
function stub(id) {
  const e = { id, hidden: false, textContent: "", dataset: {}, className: "", children: [],
    classList: { add() {}, remove() {}, toggle() {} },
    addEventListener() {}, append() {}, replaceChildren() {}, setAttribute() {}, remove() {}, focus() {}, close() {}, showModal() {},
    querySelector: () => null, querySelectorAll: () => [], scrollIntoView() {} };
  return e;
}
const doc = {
  hidden: false, activeElement: null, documentElement: { dataset: {} },
  getElementById: (id) => (els[id] = els[id] || stub(id)),
  createElement: () => stub(""), querySelectorAll: () => [], querySelector: () => null, addEventListener() {},
};
function FakeSocket(url) { this.url = url; this.closed = false; sockets.push(this); }
FakeSocket.prototype.close = function () { this.closed = true; };

const ctx = {
  document: doc, console, fetch, JSON, Promise, encodeURIComponent, Date, Math, Object, String, Array, Node: function () {},
  location: { pathname: "/", search: "", protocol: "https:", host: "pi.local", assign() {} },
  localStorage: { getItem: () => null }, WebSocket: FakeSocket,
  setInterval: (fn, ms) => { intervals.push({ fn, ms }); return intervals.length; },
  setTimeout: (fn, ms) => { timeouts.push({ fn, ms }); return timeouts.length; }, clearTimeout() {},
  addEventListener() {}, dashRender() {}, progRender() {},
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(dir + "/app.js", "utf8"), ctx, { filename: "app.js" });
vm.runInContext(fs.readFileSync(dir + "/dxberry.js", "utf8"), ctx, { filename: "dxberry.js" });

const flush = () => new Promise((r) => setImmediate(r));
(async () => {
  await flush(); await flush();
  const out = {};
  const poll = intervals.find((i) => i.ms === 10000);
  out.pollTimer = !!poll;
  out.initialRuns = statusRuns.length;          // the first load
  out.socketUrl = sockets.length ? sockets[0].url : null;
  out.linkBeforeOpen = !!els.link;
  const s = sockets[0];

  // socket open: the timer poll stands down, a status message renders and fills the callsign
  s.onopen();
  poll.fn(); await flush();
  out.runsWhileOpen = statusRuns.length - out.initialRuns;
  out.chipHiddenWhileOpen = els.link.hidden;
  s.onmessage({ data: JSON.stringify({ type: "status", data: { generated: 5, stale: false, status: JSON.parse(report(5, "W1AW")) } }) });
  out.callsign = els.callsign.textContent;
  out.hostAfterMessage = els.host.textContent;
  s.onmessage({ data: JSON.stringify({ type: "ping" }) });
  s.onmessage({ data: "not json" });
  s.onmessage({ data: JSON.stringify({ type: "status", data: { generated: 0, stale: true, status: null } }) });
  out.callsignKeptOnNull = els.callsign.textContent;
  out.runsAfterMessages = statusRuns.length - out.initialRuns;

  // socket closed: chip shows, a retry is scheduled, the poll resumes
  s.onclose();
  out.chipShownOnClose = els.link.hidden === false;
  out.retryDelayMs = timeouts.length ? timeouts[timeouts.length - 1].ms : null;
  poll.fn(); await flush();
  out.runsAfterClose = statusRuns.length - out.initialRuns;

  // the retry opens a new socket; opening it hides the chip again; a second failure doubles the wait
  timeouts[timeouts.length - 1].fn();
  out.sockets = sockets.length;
  sockets[1].onclose();
  out.secondRetryDelayMs = timeouts[timeouts.length - 1].ms;
  timeouts[timeouts.length - 1].fn();
  sockets[2].onopen();
  out.chipHiddenAfterReopen = els.link.hidden;
  console.log(JSON.stringify(out));
})();
