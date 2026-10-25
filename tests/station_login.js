// Loads station/web/login.js against a stub DOM and reports where a successful login goes.
const fs = require("fs");
const vm = require("vm");
const next = process.argv[3];
let target = null;
let submit = null;
const el = (extra) => Object.assign({ value: "", textContent: "", disabled: false, addEventListener(e, f) { submit = f; } }, extra);
const form = el({ querySelector: () => el() });
const ctx = {
  document: { getElementById: (id) => (id === "login" ? form : el()) },
  window: { location: { origin: "https://pi.local", search: "?next=" + encodeURIComponent(next), assign: (u) => { target = u; } } },
  URL, URLSearchParams,
  fetch: () => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ ok: true }) }),
};
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
submit({ preventDefault() {} });
setTimeout(() => console.log(target), 20);
