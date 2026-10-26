// Loads station/web/radiosettings.js over a small fake DOM and a fake fetch, builds the station
// settings section for radio1 and checks the field values filled from GET, the POST body of Save,
// the words of a refusal, and the re-probe call.
// Run: node tests/station_radiosettings.js station/web/radiosettings.js   (prints one JSON object)
"use strict";
const fs = require("fs");
const vm = require("vm");

class Elem {
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.listeners = {}; this.textContent = ""; this.value = ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "value") this.value = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  append(...k) { for (const x of k) this.kids.push(x); }
  addEventListener(t, fn) { this.listeners[t] = fn; }
  find(id) {
    if (this.attrs.id === id) return this;
    for (const k of this.kids) { const f = k instanceof Elem ? k.find(id) : null; if (f) return f; }
    return null;
  }
}

const calls = [];
let settingsReply = { swr_limit: 2.5, ptt_timeout_s: 90, probed: null };
let saveReply = { ok: true };
let probeReply = { ok: true };
let httpStatus = 200;
const fetch = (url, opt) => {
  calls.push({ url, method: opt.method, credentials: opt.credentials, body: opt.body || null });
  let r = settingsReply;
  if (opt.method === "POST") r = url.endsWith("/reprobe") ? probeReply : saveReply;
  return Promise.resolve({ ok: true, status: httpStatus, json: () => Promise.resolve(r) });
};
const ctx = { document: { createElement: (t) => new Elem(t) }, fetch, JSON, Promise, Object, String, Number, encodeURIComponent };
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx, { filename: "radiosettings.js" });

const flush = () => new Promise((r) => setImmediate(r));
(async () => {
  const out = {};
  const sec = ctx.window.dxbRadioSettings.section("radio1");
  out.initialSwr = sec.find("rs-swr").value;       // before GET answers: the defaults
  // the hint under each number field (the field box: label, input, hint)
  const text = (e) => (typeof e === "string" ? e : e.textContent + e.kids.map(text).join(""));
  const hintOf = (id) => {
    const box = (function up(e) { for (const k of e.kids || []) { if (k instanceof Elem && k.kids.includes(sec.find(id))) return k; const f = k instanceof Elem ? up(k) : null; if (f) return f; } return null; })(sec);
    const p = box ? box.kids.find((k) => k instanceof Elem && k.tag === "p") : null;
    return p ? text(p) : null;
  };
  out.swrHint = hintOf("rs-swr");
  out.pttHint = hintOf("rs-ptt");
  out.initialPtt = sec.find("rs-ptt").value;
  await flush();
  const swr = sec.find("rs-swr"), ptt = sec.find("rs-ptt"), msg = sec.find("rs-msg");
  out.swr = swr.value; out.ptt = ptt.value;
  out.swrStep = swr.attrs.step; out.swrMin = swr.attrs.min; out.swrMax = swr.attrs.max;
  out.pttMin = ptt.attrs.min; out.pttMax = ptt.attrs.max;
  out.getCall = calls[0];

  swr.value = "4.2"; ptt.value = "120";
  sec.find("rs-save").listeners.click();
  await flush();
  out.saveCall = calls[1];
  out.savedMsg = msg.textContent;

  saveReply = { ok: false, reason: "the SWR limit must be between 1.5 and 10", fix: "enter a value in that range" };
  swr.value = "20";
  sec.find("rs-save").listeners.click();
  await flush();
  out.refusedMsg = msg.textContent;
  out.refusedClass = msg.attrs.class;

  probeReply = { ok: false, reason: "rig control unreachable; the radio's rigctld is restarting", fix: "wait a few seconds" };
  sec.find("rs-reprobe").listeners.click();
  await flush();
  out.probeCall = calls[3];
  out.probeRefused = msg.textContent;
  probeReply = { ok: true };
  sec.find("rs-reprobe").listeners.click();
  await flush();
  out.probeOk = msg.textContent;

  // Enter in a number field saves and is kept from submitting the radio form
  let prevented = false;
  const before = calls.length;
  swr.value = "3"; saveReply = { ok: true };
  swr.listeners.keydown({ key: "Tab", preventDefault() { prevented = true; } });
  out.tabSaved = calls.length - before;
  swr.listeners.keydown({ key: "Enter", preventDefault() { prevented = true; } });
  await flush();
  out.enterSaved = calls.length - before;
  out.enterPrevented = prevented;

  // the "no SWR guard" line follows the settings answer
  const guardLine = async (reply) => {
    settingsReply = reply;
    const sec2 = ctx.window.dxbRadioSettings.section("radio1");
    await flush();
    return sec2.find("rs-guard").textContent;
  };
  out.guardLineOff = await guardLine({ swr_limit: 3, ptt_timeout_s: 180, probed: null, swr_guard: false });
  out.guardLineOn = await guardLine({ swr_limit: 3, ptt_timeout_s: 180, probed: null, swr_guard: true });
  out.guardLineUnsaid = await guardLine({ swr_limit: 3, ptt_timeout_s: 180, probed: null });

  // an expired session says so
  httpStatus = 401;
  sec.find("rs-save").listeners.click();
  await flush();
  out.expiredMsg = msg.textContent;
  console.log(JSON.stringify(out));
})().catch((e) => { console.error(e); process.exit(1); });
