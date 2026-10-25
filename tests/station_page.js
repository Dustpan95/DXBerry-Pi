// Loads station/web/app.js against a stub DOM and a fake fetch and prints what dxb.run did.
// Run: node tests/station_page.js station/web/app.js SCENARIO   (scenarios: exit, ok, refused, 401, 403, net)
"use strict";
const fs = require("fs");
const vm = require("vm");
const scenario = process.argv[3];

const calls = [];
const assigned = [];
const reply = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(body) });
const fetch = (url, opts) => {
  calls.push({ url, opts });
  switch (scenario) {
    case "ok": return reply(200, { exit: 0, stdout: "out\n", stderr: "" });
    case "exit": return reply(200, { exit: 6, stdout: "", stderr: "2026-10-25 12:00:00 [ERROR] bad" });
    case "refused": return reply(400, { error: "command not allowed" });
    case "401": return reply(401, { error: "login required" });
    case "403": return reply(403, { error: "forbidden" });
    default: return Promise.reject(new Error("network down"));
  }
};
const el = () => ({ hidden: false, textContent: "", addEventListener() {}, append() {}, replaceChildren() {}, setAttribute() {}, classList: { toggle() {} } });
const ctx = {
  document: { hidden: false, getElementById: el, addEventListener() {}, createElement: el },
  location: { pathname: "/radios", search: "?x=1", protocol: "https:", host: "pi.local", assign: (u) => assigned.push(u) },
  fetch, JSON, encodeURIComponent, Promise, setInterval() {}, WebSocket: function (u) { this.url = u; },
};
ctx.window = ctx;
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), ctx);

const out = { assigned, calls: calls.map((c) => ({ url: c.url, method: c.opts.method, credentials: c.opts.credentials, body: c.opts.body })) };
let settled = false;
ctx.dxb.run(["dxberry-status", "--json"], "secret").then(
  (v) => { settled = true; out.resolved = v; },
  (e) => { settled = true; out.rejected = { message: e.message, exit_status: e.exit_status, problem: e.problem, text: String(e) }; });
setTimeout(() => {
  out.settled = settled;
  out.calls = calls.map((c) => ({ url: c.url, method: c.opts.method, credentials: c.opts.credentials, body: c.opts.body }));
  out.ws = ctx.dxb.ws("/api/x").url;
  console.log(JSON.stringify(out));
}, 40);
