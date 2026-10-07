// page_smoke.js: drives the DXBerry page (provision/cockpit/dxberry/dxberry.js) in a fake browser -
// a minimal DOM shaped like index.html and a fake cockpit object whose spawn answers like the Pi's
// commands - and checks what an operator would see: the cards, the radio actions and their
// confirmations, and the Add/Edit form. Plain node, no packages.
// Run: node tests/page_smoke.js provision/cockpit/dxberry/dxberry.js (tests/test_page.sh does when
// node is installed). Prints one ok/FAIL line per check; exits 1 when a check failed, 2 on a crash.
"use strict";
const fs = require("fs");
const vm = require("vm");
const path = process.argv[2];

let failures = 0;
const ok = (cond, what) => { if (cond) console.log("ok   " + what); else { failures++; console.log("FAIL " + what); } };

// ---- fake DOM ---------------------------------------------------------------------------------
class N { constructor() { this.childNodes = []; this.parentNode = null; } }
class T extends N { constructor(t) { super(); this.data = String(t); } get textContent() { return this.data; } }
const camel = s => s.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
function match(e, sel) {
  if (sel.startsWith("#")) return e.id === sel.slice(1);
  if (sel.startsWith(".")) return (" " + e.className + " ").includes(" " + sel.slice(1) + " ");
  if (sel.startsWith("[")) { const a = sel.slice(1, -1); return a === "hidden" ? e.hidden : e.getAttribute(a) !== null; }
  return e.tagName === sel.toUpperCase();
}
class E extends N {
  constructor(tag) {
    super(); this.tagName = tag.toUpperCase(); this.attributes = {}; this.listeners = {}; this.dataset = {};
    this.hidden = false; this.className = ""; this._value = ""; this.disabled = false; this.open = false; this.id = "";
    this.returnValue = ""; this._sel = undefined;
  }
  setAttribute(k, v) {
    v = String(v); this.attributes[k] = v;
    if (k.startsWith("data-")) this.dataset[camel(k.slice(5))] = v;
    if (k === "id") this.id = v;
    if (k === "value") this._value = v;
    if (k === "disabled") this.disabled = true;
    if (k === "hidden") this.hidden = true;
    if (k === "class") this.className = v;
  }
  getAttribute(k) { return k in this.attributes ? this.attributes[k] : (k === "id" && this.id ? this.id : null); }
  addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); }
  fire(t, ev) { ev = ev || {}; ev.preventDefault = ev.preventDefault || (() => {}); ev.target = this; (this.listeners[t] || []).forEach(f => f(ev)); if (this["on" + t]) this["on" + t](ev); }
  click() { if (!this.disabled) this.fire("click"); }
  append(...ks) {
    for (const k of ks) {
      const n = k instanceof N ? k : new T(k);
      if (n.parentNode) n.parentNode.removeChild(n);
      n.parentNode = this; this.childNodes.push(n);
    }
  }
  replaceChildren(...ks) { this.childNodes.forEach(c => { c.parentNode = null; }); this.childNodes = []; this.append(...ks); }
  removeChild(n) { this.childNodes = this.childNodes.filter(c => c !== n); n.parentNode = null; }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  get textContent() { return this.childNodes.map(c => c.textContent).join(""); }
  set textContent(t) { this.replaceChildren(String(t)); }
  get children() { return this.childNodes.filter(c => c instanceof E); }
  *walk() { for (const c of this.children) { yield c; yield* c.walk(); } }
  querySelectorAll(sel) { return [...this.walk()].filter(e => match(e, sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  closest(sel) { let e = this; while (e) { if (e instanceof E && match(e, sel)) return e; e = e.parentNode; } return null; }
  focus() { doc.activeElement = this; }
  showModal() { this.open = true; }
  close() { if (!this.open) return; this.open = false; this.fire("close"); }
  // select semantics: value = the chosen option's value; a value no option has selects nothing
  get options() { return this.querySelectorAll("option"); }
  get value() {
    if (this.tagName !== "SELECT") return this._value;
    const opts = this.options;
    if (this._sel === null) return "";
    if (this._sel !== undefined && opts.some(o => o._value === this._sel)) return this._sel;
    if (this.getAttribute("size")) return "";
    return opts.length ? opts[0]._value : "";
  }
  set value(v) {
    v = String(v);
    if (this.tagName !== "SELECT") { this._value = v; return; }
    this._sel = this.options.some(o => o._value === v) ? v : null;
  }
}
const doc = {
  activeElement: null,
  body: new E("body"),
  createElement: t => new E(t),
  getElementById(id) { return this.body.querySelector("#" + id); },
  querySelectorAll(sel) { return this.body.querySelectorAll(sel); },
  querySelector(sel) { return this.body.querySelector(sel); },
  documentElement: { dataset: {} },
};
const mk = (tag, id, cls) => { const e = new E(tag); if (id) e.setAttribute("id", id); if (cls) e.setAttribute("class", cls); return e; };
// index.html's elements, by hand (only what dxberry.js looks up)
const confirmD = mk("dialog", "confirm");
const cf = mk("form"); const okBtn = mk("button", "", "btn danger ok");
cf.append(mk("h2", "confirm-title"), mk("p"), okBtn); confirmD.append(cf);
const radioD = mk("dialog", "radio-dialog"); const rform = mk("form", "radio-form");
const rerr = mk("p", "radio-form-error", "error"); rerr.hidden = true;
rform.append(mk("h2", "radio-dialog-title"), mk("div", "radio-fields", "form-grid"), rerr, mk("button", "radio-cancel", "btn"), mk("button", "radio-save", "btn primary"));
radioD.append(rform);
const cards = mk("main", "cards", "cards"); cards.append("Reading the station…");
// settings.js's own elements: the section it fills, and its own dialog.
const settingsSec = mk("section", "settings", "card settings");
const settingsD = mk("dialog", "settings-dialog");
const sform = mk("form", "settings-form");
const serr = mk("p", "settings-form-error", "error"); serr.hidden = true;
sform.append(mk("h2", "settings-dialog-title"), mk("div", "settings-fields", "form-grid"), serr, mk("button", "settings-cancel", "btn"), mk("button", "settings-save", "btn primary"));
settingsD.append(sform);
// updates.js's own element: the section it fills (it reuses the shared #confirm dialog, no own one).
const updatesSec = mk("section", "updates", "card settings");
doc.body.append(mk("span", "host"), mk("p", "updated"), mk("button", "refresh"), mk("div", "notices"), cards, confirmD, radioD, settingsSec, settingsD, updatesSec);

// ---- fake cockpit -----------------------------------------------------------------------------
const calls = [];
let answer = () => Promise.reject({ problem: "not-found" });
const cockpit = {
  hidden: false,
  // spawn's promise carries input(data), like cockpit's own: it records the data onto this call and
  // returns the same promise, for run(args, input)'s p.input(input) to chain on.
  spawn(args, opts) {
    const call = { args, opts };
    calls.push(call);
    const p = answer(args);
    p.input = data => { call.input = data; return p; };
    return p;
  },
  jump(url) { calls.push({ jump: url }); },
  addEventListener() {},
};

const status = {
  generated: "2026-10-06T15:00:00Z",
  pi: { ok: true, model: "Raspberry Pi 4 Model B Rev 1.4", temp_c: 38.5, load: [0.5, 0.4, 0.3], mem_total: 4e9, mem_used: 2e8, disk_total: 1e11, disk_used: 1e9, uptime_s: 99999,
    throttle: { under_voltage_now: false, freq_capped_now: false, throttled_now: false, soft_temp_limit_now: false, under_voltage_since_boot: true, freq_capped_since_boot: false, throttled_since_boot: true, soft_temp_limit_since_boot: false } },
  network: { ok: true, state: "WIFI", interface: "wlan0", address: "10.0.0.90", prefix: 24, gateway: "10.0.0.1", hostname: "dxberry", wifi: { ssid: "<ssid>", signal_dbm: -48 } },
  graywolf: { ok: true, active: "active", version: "0.14.13", web_port: 8080, api_ok: true, api_error: "",
    igate: { connected: true, server: "rotate.aprs2.net:14580", rf_to_is_gated: 10, is_to_rf_gated: 1 },
    channels: [{ id: 3, name: "VHF APRS", rx_frames: 5, tx_frames: 1, rx_bad_fcs: 0 }, { id: 4, name: "radio1", rx_frames: 0, tx_frames: 0, rx_bad_fcs: 0 }],
    position_log: { enabled: true, path: "/run/graywolf/history.db", bytes: 4623960 } },
  radios: { ok: true,
    radios: { radio1: { label: "TM-V71", profile: "0d8c:013c", audio: { path: "usb-0:1.1:1.0", vidpid: "0d8c:013c", serial: "" }, cat: { path: "usb-0:1.2:1.0", vidpid: "10c4:ea60", serial: "0001" },
      hid: { path: "usb-0:1.1:1.3", vidpid: "0d8c:013c", serial: "" }, ptt_serial: null, ptt: { method: "rigctld", gpio_line: null }, rig: { model: 3073, baud: 57600, ptt_type: "RTS" },
      rigctld_port: 4532, wiring: "full", owner: "graywolf", present: true, kernel: { audio: "card2", cat: "ttyUSB1", hid: "hidraw2", ptt_serial: null }, rigctld: "active",
      alsa_id: "RADIO1", wire_hash: "a", wired_hash: "a", freq: "145390000", mode: "FM" } },
    gps: { fix: 0, receiver: false },
    candidates: [{ index: 3, port: "usb-0:1.3", profile: "0d8c:013c", name: "DigiRig Mobile", defaults: { ptt: "rigctld", ptt_type: "RTS", cat: "separate", model: 1, baud: 57600 },
      functions: [{ kind: "audio", kernel: "card1", path: "usb-0:1.3:1.0", vidpid: "0d8c:013c", serial: "", product: "USB Audio Device" }, { kind: "hid", kernel: "hidraw1", path: "usb-0:1.3:1.3", vidpid: "0d8c:013c", serial: "", product: "USB Audio Device" }] },
    { index: 4, port: "usb-0:1.4", profile: "10c4:ea60", name: "CP2102 serial", defaults: { ptt: "rigctld", ptt_type: "RTS", cat: "same", model: 1, baud: 57600 },
      functions: [{ kind: "serial", kernel: "ttyUSB0", path: "usb-0:1.4:1.0", vidpid: "10c4:ea60", serial: "0001", product: "CP2102 USB to UART Bridge Controller" }] }],
    apps: [{ name: "graywolf", label: "Graywolf" }], warnings: [] },
  time: { ok: true, synced: true, source: "NTP", reference: "1.2.3.4", stratum: 2, offset_ms: 0.2 },
  release: { ok: true, dxberry: "0.3.0-rc2", dxberry_commit: "abc", graywolf: "0.14.13", cockpit: "337", update: null },
  services: { ok: true, units: [{ unit: "graywolf.service", load: "loaded", active: "active", sub: "running", result: "success", type: "simple" },
    { unit: "cockpit.socket", load: "loaded", active: "active", sub: "listening", result: "success", type: "" }] },
};
const models = [{ model: 1, mfg: "Hamlib", name: "Dummy", status: "Stable" }, { model: 4, mfg: "FLRig", name: "", status: "Stable" },
  { model: 3073, mfg: "Icom", name: "IC-7300", status: "Stable" }, { model: 3085, mfg: "Icom", name: "IC-705", status: "Stable" }];

// settings.js's own fixtures: a provisioned Pi's console keys (effective values; passwords set),
// and the first, idle dxberry-config job answer.
const cfgKeys = {
  STATIC_IP: { value: "10.0.0.90/24", effective: "10.0.0.90/24" },
  GATEWAY: { value: "10.0.0.1", effective: "10.0.0.1" },
  DNS: { value: "", effective: "10.0.0.1" },
  WIFI_SSID: { value: "Shack Net" },
  WIFI_PASSWORD: { secret: true, set: true },
  WIFI_COUNTRY: { value: "US" },
  HOSTNAME: { value: "shackpi", effective: "shackpi" },
  TIMEZONE: { value: "America/Chicago", effective: "America/Chicago" },
  PASSWORD: { secret: true, set: true },
  SSH_PUBKEY: { value: "" },
  GPS_DEVICE: { value: "", effective: "auto" },
  GPS_BAUD: { value: "", effective: "57600" },
  GPS_PPS: { value: "" },
  POSITION_LOG: { value: "", effective: "off" },
  CONSOLE: { value: "", effective: "on" },
};
// network settings start switched off, as in v0.3.0-rc3 (dxberry-config get's network_editable)
let cfgNetworkEditable = false;
const cfgGetAnswer = () => JSON.stringify({ file: "/boot/dxberry.txt", valid: true, errors: [], network_editable: cfgNetworkEditable, keys: cfgKeys });
const cfgFirstJob = () => JSON.stringify({ now: 1000, pending: false, revert_at: null, reverted_at: null, reboot_required: false, job: null });

// updates.js's own fixtures: Graywolf and DXBerry both have an update, DXBerry's is a
// pre-release, 2 system packages are out, and a rollback to 0.3.0-rc2 is kept.
let updCheckAnswer = {
  checked_at: 1700000000, cached: false, reboot_required: false,
  graywolf: { installed: "0.14.13", latest: "0.14.14", pinned: null, update: true },
  dxberry: { installed: "0.3.0-rc3", latest: "0.3.0-rc4", prerelease: true, update: true, include_prereleases: true },
  system: { count: 2, packages: ["libfoo1", "bar"] },
  rollback: { available: true, version: "0.3.0-rc2", updates: true },
};
let updJobAnswer = { now: 1700000000, reboot_required: false, job: null };

const ctx = {
  document: doc, cockpit, console, setTimeout: (f) => 0, setInterval: () => 0, clearTimeout: () => 0,
  window: { addEventListener() {} }, localStorage: { getItem: () => null }, location: { hostname: "10.0.0.90" }, Node: N,
};
vm.createContext(ctx);
const flush = () => new Promise(r => setImmediate(r));
const settingsPath = path.replace(/dxberry\.js$/, "settings.js");
const updatesPath = path.replace(/dxberry\.js$/, "updates.js");

(async () => {
  answer = args => {
    if (args[0].endsWith("dxberry-status")) return Promise.resolve(JSON.stringify(status));
    if (args[1] === "models") return Promise.resolve(JSON.stringify(models));
    if (args[0].endsWith("dxberry-config") && args[1] === "get") return Promise.resolve(cfgGetAnswer());
    if (args[0].endsWith("dxberry-config") && args[1] === "job") return Promise.resolve(cfgFirstJob());
    if (args[0].endsWith("dxberry-update") && args[1] === "check") return Promise.resolve(JSON.stringify(updCheckAnswer));
    if (args[0].endsWith("dxberry-update") && args[1] === "job") return Promise.resolve(JSON.stringify(updJobAnswer));
    return Promise.reject({ problem: "not-found" });
  };
  vm.runInContext(fs.readFileSync(path, "utf8"), ctx, { filename: "dxberry.js" });
  vm.runInContext(fs.readFileSync(settingsPath, "utf8"), ctx, { filename: "settings.js" });
  vm.runInContext(fs.readFileSync(updatesPath, "utf8"), ctx, { filename: "updates.js" });
  await flush(); await flush(); await flush();
  const text = () => cards.textContent;
  ok(cards.querySelectorAll("section").length === 8, "eight cards render");
  ok(text().includes("radio1") && text().includes("TM-V71"), "the radio shows with its label");
  ok(text().includes("Icom IC-7300 (model 3073)"), "the rig name comes from Hamlib's list");
  ok(text().includes("sound card2 (RADIO1) · CAT ttyUSB1 · HID hidraw2"), "parts show kernel names and the ALSA id");
  ok(text().includes("rigctld, keyed by the RTS line"), "PTT in words");
  ok(text().includes("Plugged in, not set up") && text().includes("DigiRig Mobile") && text().includes("CP2102 serial"), "both candidates listed");
  ok(text().includes("owned by Graywolf"), "owner badge uses the app label");
  ok(!text().includes("Give to Graywolf"), "no Give to Graywolf on a radio Graywolf owns");
  const btnByKey = k => cards.querySelectorAll("[data-key]").find(e => e.dataset.key === k);
  ok(!!btnByKey("radio:radio1:release") && !!btnByKey("radio:radio1:edit") && !!btnByKey("radio:radio1:remove"), "Release, Edit, Remove buttons");
  const aria = k => btnByKey(k).getAttribute("aria-label");
  ok(aria("radio:radio1:release") === "Release radio1" && aria("radio:radio1:edit") === "Edit radio1" && aria("radio:radio1:remove") === "Remove radio1",
    "the radio's buttons name the radio for screen readers");
  ok(!!btnByKey("cand:usb-0:1.3:add") && !!btnByKey("cand:usb-0:1.4:add"), "an Add button per candidate");
  ok(text().includes("on, 4.4 MB in RAM"), "position log text");

  // focus survives a refresh
  btnByKey("radio:radio1:edit").focus();
  doc.getElementById("refresh").click();
  await flush(); await flush();
  ok(doc.activeElement && doc.activeElement.dataset.key === "radio:radio1:edit" && doc.activeElement.parentNode !== null, "keyboard focus lands on the rebuilt Edit button");

  // Release: asks first, names the hand-made channel and that Graywolf stops
  btnByKey("radio:radio1:release").click();
  ok(confirmD.open, "Release asks first");
  const ctext = confirmD.querySelector("p").textContent;
  ok(ctext.includes("VHF APRS") && ctext.includes("stops altogether"), "the confirmation names the hand-made channel and that Graywolf stops: " + ctext);
  ok(ctext.includes("Graywolf's channel named radio1 is deleted."), "with full wiring, Release says Graywolf's channel for the radio is deleted");
  answer = args => {
    if (args[1] === "release") return Promise.reject({ exit_status: 3, message: "2026-10-06 12:00:00 [INFO] working\n2026-10-06 12:00:01 [ERROR] no such radio: radio1" });
    if (args[0].endsWith("dxberry-status")) return Promise.resolve(JSON.stringify(status));
    return Promise.resolve("[]");
  };
  confirmD.returnValue = "ok"; confirmD.close();
  ok(text().includes("Releasing…"), "the radio shows a busy badge while the command runs");
  ok(btnByKey("radio:radio1:edit").disabled, "its buttons are disabled meanwhile");
  await flush(); await flush(); await flush();
  const rel = calls.filter(c => c.args && c.args[1] === "release").pop();
  ok(rel && rel.args.join(" ") === "/opt/dxberry/bin/dxberry-radio release radio1 --json" && rel.opts.superuser === "require", "release runs as superuser with --json");
  const notes = doc.getElementById("notices").textContent;
  ok(notes.includes("Releasing radio1 from Graywolf failed. Exit 3: there is no such radio or application. no such radio: radio1"), "failure notice: " + notes);
  ok(!text().includes("Releasing…") && !btnByKey("radio:radio1:edit").disabled, "busy state cleared after the failure");

  // Add from the DigiRig candidate
  answer = args => {
    if (args[1] === "models") return Promise.resolve(JSON.stringify(models));
    if (args[1] === "add") return Promise.resolve(JSON.stringify({ radios: {}, warnings: ["radio: ptt_type RTS needs a serial pin; set to NONE"] }));
    if (args[0].endsWith("dxberry-status")) return Promise.resolve(JSON.stringify(status));
    return Promise.resolve("{}");
  };
  btnByKey("cand:usb-0:1.3:add").click();
  await flush(); await flush();
  ok(radioD.open, "Add opens the form");
  const $ = id => doc.getElementById(id);
  ok($("radio-dialog-title").textContent === "Add DigiRig Mobile", "form title");
  ok($("rf-name").value === "radio2", "next free name suggested: " + $("rf-name").value);
  ok($("rf-audio").value === "usb-0:1.3:1.0", "sound card preselected from the candidate");
  ok($("rf-cat").value === "none", "no CAT preselected (separate device)");
  ok($("radio-fields").textContent.includes("separate USB device"), "hint for the DigiRig's separate CAT port");
  const audioField = $("rf-audio").closest(".field").textContent;
  ok(audioField.includes("Graywolf has channels made by hand (VHF APRS).") && audioField.includes("renames it to the radio's name in capitals at the next replug or reboot"),
    "Add cautions that pinning a sound card renames it under Graywolf's hand-made channel: " + audioField);
  ok($("rf-hid").value === "", "HID automatic on add");
  ok($("rf-model").value === "1" && $("rf-model").options.length === 4, "model list loaded, default 1");
  $("rf-model-filter").value = "icom"; $("rf-model-filter").fire("input");
  ok($("rf-model").options.length === 3 && $("rf-model").value === "1", "search narrows the list and keeps the chosen model: " + $("rf-model").options.map(o => o.textContent).join("|"));
  let prevented = false;
  $("rf-model-filter").fire("keydown", { key: "Enter", preventDefault() { prevented = true; } });
  // the list reads Hamlib Dummy (kept, but not a match), Icom IC-705, Icom IC-7300: Enter takes the first match
  ok(prevented && $("rf-model").value === "3085", "Enter in the search picks the first model it finds instead of submitting the form: " + $("rf-model").value);
  ok($("rf-gpio").closest(".field").hidden === true && $("rf-ptt-type").closest(".field").hidden === false, "GPIO hidden, PTT type shown for rigctld");
  $("rf-ptt").value = "gpio"; $("rf-ptt").fire("change");
  ok($("rf-gpio").closest(".field").hidden === false && $("rf-ptt-type").closest(".field").hidden === true, "GPIO shown for gpio PTT");
  rform.fire("submit");
  ok($("radio-form-error").hidden === false && $("radio-form-error").textContent.includes("GPIO line"), "missing GPIO line is caught in the form");
  $("rf-ptt").value = "rigctld"; $("rf-ptt").fire("change");
  $("rf-cat").value = "usb-0:1.4:1.0"; $("rf-label").value = "IC-7300 bench";
  $("rf-model").value = "3073";
  rform.fire("submit");
  ok($("radio-save").disabled === true, "Save disabled while saving");
  await flush(); await flush(); await flush();
  const add = calls.filter(c => c.args && c.args[1] === "add").pop();
  ok(add && add.args.join(" ") === "/opt/dxberry/bin/dxberry-radio add radio2 --audio usb-0:1.3:1.0 --cat usb-0:1.4:1.0 --model 3073 --baud 57600 --ptt rigctld --ptt-type RTS --wiring full --label IC-7300 bench --json",
    "add arguments: " + (add && add.args.join(" ")));
  ok(!radioD.open, "dialog closes on success");
  const notes2 = doc.getElementById("notices").textContent;
  ok(notes2.includes("radio2 added.") && notes2.includes("radio2: radio: ptt_type RTS needs a serial pin"), "success and warning notices: " + notes2);

  // Edit: only what changed
  btnByKey("radio:radio1:edit").click();
  await flush(); await flush();
  ok(radioD.open && $("radio-dialog-title").textContent === "Edit radio1", "Edit opens the form");
  ok($("rf-name") === null, "no Name field when editing");
  ok(!$("rf-audio").closest(".field").textContent.includes("made by hand"), "Edit carries no renaming caution");
  ok($("rf-audio").value === "" && $("rf-model").value === "3073", "pins default to keep; model is the radio's");
  answer = args => {
    if (args[1] === "set") return Promise.reject({ exit_status: 2, message: "2026-10-06 12:00:00 [ERROR] invalid radio record:\n  radio1: bad label" });
    if (args[0].endsWith("dxberry-status")) return Promise.resolve(JSON.stringify(status));
    return Promise.resolve("{}");
  };
  $("rf-label").value = "New label";
  rform.fire("submit");
  await flush(); await flush(); await flush();
  const set = calls.filter(c => c.args && c.args[1] === "set").pop();
  ok(set && set.args.join(" ") === "/opt/dxberry/bin/dxberry-radio set radio1 --label New label --json", "set sends only the label: " + (set && set.args.join(" ")));
  ok(radioD.open && $("radio-form-error").textContent === "Exit 2: the request was not valid. invalid radio record: radio1: bad label", "refusal shown in the open form: " + $("radio-form-error").textContent);
  doc.getElementById("radio-cancel").click();
  ok(!radioD.open, "Cancel closes the form");
  // nothing changed path
  btnByKey("radio:radio1:edit").click(); await flush(); await flush();
  rform.fire("submit");
  ok(!radioD.open && doc.getElementById("notices").textContent.includes("radio1: nothing changed."), "an unchanged Edit just closes");

  // a refresh while the form is open leaves the form alone
  btnByKey("cand:usb-0:1.4:add").click(); await flush(); await flush();
  $("rf-label").value = "typed";
  doc.getElementById("refresh").click(); await flush(); await flush();
  ok(radioD.open && $("rf-label").value === "typed", "the 10 s refresh does not touch the open form");
  ok($("rf-audio").value === "none" && $("rf-cat").value === "usb-0:1.4:1.0", "serial-only candidate: CAT preselected, no sound card");
  // the profile says RTS, but only rigctld keys by a serial line: anything else is added with NONE
  $("rf-ptt").value = "vox"; $("rf-ptt").fire("change");
  rform.fire("submit");
  await flush(); await flush(); await flush();
  const add2 = calls.filter(c => c.args && c.args[1] === "add").pop();
  ok(add2 && add2.args.join(" ").includes("--ptt vox --ptt-type NONE --wiring full --label typed"), "a VOX radio is added with --ptt-type NONE: " + (add2 && add2.args.join(" ")));

  // Give to: an unowned radio with hand-made channels asks first
  status.radios.radios.radio1.owner = "";
  doc.getElementById("refresh").click(); await flush(); await flush();
  btnByKey("radio:radio1:give:graywolf").click();
  ok(confirmD.open && confirmD.querySelector("p").textContent.includes("made by hand: VHF APRS"), "Give to Graywolf warns about hand-made channels");
  ok(confirmD.querySelector("p").textContent.includes("It gets a new channel named radio1"), "with full wiring, Give says Graywolf gets a channel for the radio");
  confirmD.returnValue = "cancel"; confirmD.close();
  ok(!calls.some(c => c.args && c.args[1] === "claim"), "Cancel runs nothing");
  ok(aria("radio:radio1:give:graywolf") === "Give radio1 to Graywolf", "Give names the radio and the application for screen readers");

  // Graywolf not answering: its hand-made channels cannot be checked, and Give says so first
  status.graywolf.api_ok = false;
  doc.getElementById("refresh").click(); await flush(); await flush();
  btnByKey("radio:radio1:give:graywolf").click();
  ok(confirmD.open && confirmD.querySelector("p").textContent.includes("Graywolf is not answering, so its channels made by hand could not be checked."),
    "Give to Graywolf says when Graywolf could not be asked about hand-made channels: " + confirmD.querySelector("p").textContent);
  confirmD.returnValue = "cancel"; confirmD.close();
  status.graywolf.api_ok = true;

  // names wiring: DXBerry makes no Graywolf channel, so neither Release nor Give talks about one
  status.radios.radios.radio1.wiring = "names";
  status.radios.radios.radio1.owner = "graywolf";
  doc.getElementById("refresh").click(); await flush(); await flush();
  btnByKey("radio:radio1:release").click();
  const ntext = confirmD.querySelector("p").textContent;
  ok(confirmD.open && !ntext.includes("is deleted") && ntext.includes("stops altogether"), "Release of a names-only radio promises no channel deletion: " + ntext);
  confirmD.returnValue = "cancel"; confirmD.close();
  status.radios.radios.radio1.owner = "";
  doc.getElementById("refresh").click(); await flush(); await flush();
  btnByKey("radio:radio1:give:graywolf").click();
  ok(!confirmD.open, "Give a names-only radio to Graywolf: no channel is made, so nothing to caution about");
  await flush(); await flush(); await flush();
  const claim = calls.filter(c => c.args && c.args[1] === "claim").pop();
  ok(claim && claim.args.join(" ") === "/opt/dxberry/bin/dxberry-radio claim radio1 graywolf --json", "Give runs claim: " + (claim && claim.args.join(" ")));
  status.radios.radios.radio1.wiring = "full";

  // unplugged radio: Give to disabled
  status.radios.radios.radio1.present = false;
  doc.getElementById("refresh").click(); await flush(); await flush();
  ok(btnByKey("radio:radio1:give:graywolf").disabled && text().includes("unplugged"), "Give to disabled while unplugged");

  // ---- the Settings section (settings.js) --------------------------------------------------
  const sbtn = k => doc.querySelectorAll("[data-key]").find(e => e.dataset.key === k);
  const stext = () => settingsSec.textContent;
  ok(stext().includes("Hostname") && stext().includes("shackpi"), "Settings shows the hostname");
  ok(stext().includes("Address") && stext().includes("10.0.0.90/24 (fixed)"), "Settings shows the current address");
  ok(stext().includes("WiFi") && stext().includes("Shack Net (US), password set"), "Settings shows the WiFi network");
  ok(stext().includes("Login password") && stext().includes("set"), "Settings shows the login password is set");
  ok(!stext().includes("<applied>"), "Settings never shows the raw <applied> marker");
  // network settings switched off: the values show, but no dialog is offered, and a note says how
  ok(!sbtn("cfg:network") && !stext().includes("Change network settings"), "no Change network settings while network settings are off: " + stext());
  ok(stext().includes("Network settings change in dxberry.txt for now: edit it, run sudo dxberry-provision, then restart the Pi."),
    "a note says where network settings change for now: " + stext());
  ok(!!sbtn("cfg:system"), "system settings are still offered while network settings are off");

  // From here on, dxberry-config's own answers drive the section: a successful network save makes
  // its job pending (Keep/Undo), CONSOLE=off is refused by the validator.
  let jobAnswer = { now: 1000, pending: false, revert_at: null, reverted_at: null, reboot_required: false, job: null };
  answer = args => {
    if (!args[0].endsWith("dxberry-config")) return Promise.reject({ problem: "not-found" });
    const cmd = args[1];
    if (cmd === "get") return Promise.resolve(cfgGetAnswer());
    if (cmd === "job") return Promise.resolve(JSON.stringify(jobAnswer));
    if (cmd === "set") {
      if (args.includes("CONSOLE=off")) {
        return Promise.reject({ exit_status: 4,
          message: "dxberry.txt would not be valid, so nothing was written:\n  line 3: GATEWAY is not inside 10.0.0.90/24" });
      }
      jobAnswer = { now: 1000, pending: true, revert_at: 1090, reverted_at: null, reboot_required: false,
        job: { unit: "dxberry-job-config-1", state: "finished", exit: 0, changed: ["WIFI_SSID"], network: true, lines: [] } };
      return Promise.resolve('{"ok":true,"changed":["WIFI_SSID"],"job":"dxberry-job-config-1","network":true}');
    }
    if (cmd === "confirm") {
      jobAnswer = Object.assign({}, jobAnswer, { pending: false, revert_at: null });
      return Promise.resolve('{"ok":true,"kept":true}');
    }
    return Promise.reject({ problem: "not-found" });
  };

  // network settings switched on (DXB_CONFIG_NETWORK=1): the dialog is offered and the note goes
  cfgNetworkEditable = true;
  vm.runInContext("cfgLoadValues()", ctx);
  await flush(); await flush();
  ok(!!sbtn("cfg:network") && !stext().includes("dxberry.txt for now"), "Change network settings is offered once network settings are on");

  // Change network settings: opens with the current address; DHCP hides the address fields.
  sbtn("cfg:network").click();
  await flush(); await flush();
  ok(settingsD.open, "Change network settings opens the dialog");
  ok($("cf-STATIC_IP").value === "10.0.0.90/24", "the dialog opens with the current address");
  $("cf-mode").value = "dhcp"; $("cf-mode").fire("change");
  ok($("cf-STATIC_IP").closest(".field").hidden === true, "switching to DHCP hides the address fields");
  $("cf-mode").value = "static"; $("cf-mode").fire("change");
  ok($("cf-STATIC_IP").closest(".field").hidden === false, "switching back to a fixed address shows them again");

  // Save with a new WiFi password: only the password changed, and it travels on stdin, never argv.
  $("cf-WIFI_PASSWORD").value = "new wifi pass";
  sform.fire("submit");
  await flush(); await flush(); await flush();
  const setCall = calls.filter(c => c.args && c.args[0].endsWith("dxberry-config") && c.args[1] === "set").pop();
  ok(setCall && setCall.args.join(" ") === "/opt/dxberry/bin/dxberry-config set --stdin --json",
    "Save sends only --stdin, no plain KEY=VALUE args: " + (setCall && setCall.args.join(" ")));
  ok(setCall && !setCall.args.some(a => a.includes("new wifi pass")), "the password never appears in the arguments");
  ok(setCall && setCall.input === "WIFI_PASSWORD=new wifi pass\n", "the password is sent only on standard input: " + (setCall && JSON.stringify(setCall.input)));
  ok(!settingsD.open, "the dialog closes on a successful save");

  // The change is now pending: Keep these settings / Undo now, with a countdown near 1:30.
  ok(stext().includes("Keep these settings") && stext().includes("Undo now"), "a pending network change offers Keep and Undo: " + stext());
  const left = (() => { const m = /in (\d+):(\d\d) /.exec(stext()); return m ? Number(m[1]) * 60 + Number(m[2]) : null; })();
  ok(left !== null && left >= 85 && left <= 90, "the countdown is near 1:30: " + stext());

  // Keep these settings runs dxberry-config confirm --json.
  sbtn("cfg:keep").click();
  await flush(); await flush();
  const confirmCall = calls.filter(c => c.args && c.args[1] === "confirm").pop();
  ok(confirmCall && confirmCall.args.join(" ") === "/opt/dxberry/bin/dxberry-config confirm --json",
    "Keep these settings runs dxberry-config confirm --json: " + (confirmCall && confirmCall.args.join(" ")));
  ok(doc.getElementById("notices").textContent.includes("Network settings kept."), "a kept change says so");

  // Change system settings: turning the console off asks first, naming what it does.
  sbtn("cfg:system").click();
  await flush(); await flush();
  ok(settingsD.open && $("settings-dialog-title").textContent === "System settings", "Change system settings opens the dialog");
  $("cf-CONSOLE").value = "off";
  sform.fire("submit");
  ok(confirmD.open && confirmD.querySelector("p").textContent.includes("CONSOLE=off turns this console off"),
    "turning the console off asks first and names it: " + confirmD.querySelector("p").textContent);

  // A refused save (the validator rejected it) shows the exit code's meaning and the validator's
  // own line, in the still-open dialog.
  confirmD.returnValue = "ok"; confirmD.close();
  await flush(); await flush(); await flush();
  ok(settingsD.open, "a refused save leaves the dialog open");
  const serrText = $("settings-form-error").textContent;
  ok(serrText.includes("the settings would not be valid") && serrText.includes("GATEWAY is not inside 10.0.0.90/24"),
    "a refused save shows the exit code's meaning and the validator's line: " + serrText);

  // ---- the Updates section (updates.js) ----------------------------------------------------
  const utext = () => updatesSec.textContent;
  const ubtn = k => updatesSec.querySelectorAll("[data-key]").find(e => e.dataset.key === k);
  ok(utext().includes("0.14.13") && utext().includes("0.14.14"), "Updates shows Graywolf's installed and latest versions: " + utext());
  ok(utext().includes("0.3.0-rc3") && utext().includes("0.3.0-rc4") && utext().includes("(pre-release)"),
    "Updates shows DXBerry's installed and latest, marked pre-release: " + utext());
  ok(utext().includes("2 packages to upgrade"), "Updates shows the system package count: " + utext());
  ok(utext().includes("0.3.0-rc2") && utext().includes("is kept"), "Updates shows the rollback that is kept: " + utext());
  ok(!!ubtn("upd:graywolf") && !!ubtn("upd:dxberry") && !!ubtn("upd:system") && !!ubtn("upd:rollback") && !!ubtn("upd:check"),
    "the update, install, rollback and check-now buttons are offered");
  const preBox = $("upd-prereleases");
  ok(!!preBox && Object.prototype.hasOwnProperty.call(preBox.attributes, "checked"),
    "the pre-release switch starts on, matching DXBerry's include_prereleases");

  // Update Graywolf: asks first, warning APRS stops for a moment; OK runs dxberry-update graywolf --json.
  ubtn("upd:graywolf").click();
  ok(confirmD.open && confirmD.querySelector("h2").textContent === "Update Graywolf?", "Update Graywolf asks first");
  const gwConfirmText = confirmD.querySelector("p").textContent;
  ok(gwConfirmText.includes("APRS") && gwConfirmText.includes("stop for a moment"), "the confirmation says APRS stops for a moment: " + gwConfirmText);
  let uJob = { now: 1700000100, reboot_required: false,
    job: { unit: "dxberry-job-update-graywolf-1700000100", kind: "graywolf", started: 1700000100, state: "running", exit: null, finished: null,
      lines: ["downloading graywolf_0.14.14_arm64.deb", "installing"] } };
  answer = args => {
    if (!args[0].endsWith("dxberry-update")) return Promise.reject({ problem: "not-found" });
    if (args[1] === "graywolf") return Promise.resolve(JSON.stringify({ ok: true, job: uJob.job.unit, kind: "graywolf" }));
    if (args[1] === "job") return Promise.resolve(JSON.stringify(uJob));
    if (args[1] === "check") return Promise.resolve(JSON.stringify(updCheckAnswer));
    return Promise.reject({ problem: "not-found" });
  };
  confirmD.returnValue = "ok"; confirmD.close();
  await flush(); await flush(); await flush();
  const gwStart = calls.filter(c => c.args && c.args[0].endsWith("dxberry-update") && c.args[1] === "graywolf").pop();
  ok(gwStart && gwStart.args.join(" ") === "/opt/dxberry/bin/dxberry-update graywolf --json",
    "OK runs dxberry-update graywolf --json: " + (gwStart && gwStart.args.join(" ")));

  // while the job runs: its output shows under Updates, and the update buttons are disabled
  ok(utext().includes("Running: graywolf update"), "a running notice names what is updating: " + utext());
  ok(utext().includes("downloading graywolf_0.14.14_arm64.deb"), "the job's output shows under Updates: " + utext());
  ok(ubtn("upd:graywolf").disabled && ubtn("upd:system").disabled && ubtn("upd:check").disabled, "the update buttons are disabled while the job runs");

  // the job finishes with exit 0: a notice says so, and the check reloads (without forcing a refresh)
  uJob = { now: 1700000200, reboot_required: false,
    job: { unit: uJob.job.unit, kind: "graywolf", started: 1700000100, state: "finished", exit: 0, finished: 1700000200,
      lines: ["downloading graywolf_0.14.14_arm64.deb", "installing", "finished with exit 0"] } };
  vm.runInContext("updLoadJob()", ctx);
  await flush(); await flush(); await flush();
  ok(doc.getElementById("notices").textContent.includes("Graywolf update finished."),
    "a finished job notices it: " + doc.getElementById("notices").textContent);
  const checkCalls = calls.filter(c => c.args && c.args[0].endsWith("dxberry-update") && c.args[1] === "check");
  ok(checkCalls.length > 0 && !checkCalls[checkCalls.length - 1].args.includes("--refresh"), "the finished job reloads the check (not forced)");

  // Include pre-releases: switching it off runs dxberry-update prereleases off --json and reloads the check
  answer = args => {
    if (!args[0].endsWith("dxberry-update")) return Promise.reject({ problem: "not-found" });
    if (args[1] === "prereleases") return Promise.resolve(JSON.stringify({ ok: true, include_prereleases: args[2] === "on" }));
    if (args[1] === "check") return Promise.resolve(JSON.stringify(updCheckAnswer));
    if (args[1] === "job") return Promise.resolve(JSON.stringify(uJob));
    return Promise.reject({ problem: "not-found" });
  };
  const preBox2 = $("upd-prereleases");
  preBox2.checked = false;
  preBox2.fire("change");
  await flush(); await flush(); await flush();
  const preCall = calls.filter(c => c.args && c.args[0].endsWith("dxberry-update") && c.args[1] === "prereleases").pop();
  ok(preCall && preCall.args.join(" ") === "/opt/dxberry/bin/dxberry-update prereleases off --json",
    "unchecking runs dxberry-update prereleases off --json: " + (preCall && preCall.args.join(" ")));
  const checkAfterPre = calls.filter(c => c.args && c.args[0].endsWith("dxberry-update") && c.args[1] === "check").pop();
  ok(checkAfterPre && checkAfterPre.args.includes("--refresh"), "switching pre-releases reloads the check with --refresh: " + (checkAfterPre && checkAfterPre.args.join(" ")));

  // Check now: runs check --refresh --json directly, no confirmation
  answer = args => {
    if (!args[0].endsWith("dxberry-update")) return Promise.reject({ problem: "not-found" });
    if (args[1] === "check") return Promise.resolve(JSON.stringify(updCheckAnswer));
    if (args[1] === "job") return Promise.resolve(JSON.stringify(uJob));
    return Promise.reject({ problem: "not-found" });
  };
  ubtn("upd:check").click();
  await flush(); await flush(); await flush();
  const checkNow = calls.filter(c => c.args && c.args[0].endsWith("dxberry-update") && c.args[1] === "check").pop();
  ok(checkNow && checkNow.args.join(" ") === "/opt/dxberry/bin/dxberry-update check --refresh --json",
    "Check now runs check --refresh --json: " + (checkNow && checkNow.args.join(" ")));

  // Roll back: asks first, naming the kept version
  ubtn("upd:rollback").click();
  ok(confirmD.open && confirmD.querySelector("h2").textContent === "Roll back DXBerry?", "Roll back asks first");
  let rbText = confirmD.querySelector("p").textContent;
  ok(rbText.includes("0.3.0-rc2") && !rbText.includes("no Updates screen"),
    "Roll back names the kept version; no warning while that tree still has dxberry-update: " + rbText);
  confirmD.returnValue = "cancel"; confirmD.close();

  // Roll back to a tree with no Updates command of its own: the confirmation adds the warning
  updCheckAnswer = Object.assign({}, updCheckAnswer, { rollback: { available: true, version: "0.3.0-rc2", updates: false } });
  vm.runInContext("updLoadCheck(true)", ctx);
  await flush(); await flush(); await flush();
  ubtn("upd:rollback").click();
  ok(confirmD.open, "Roll back still asks first once the kept tree has no Updates screen");
  rbText = confirmD.querySelector("p").textContent;
  ok(rbText.includes("0.3.0-rc2 has no Updates screen") && rbText.includes("flashing a new image or copying DXBerry by hand"),
    "the confirmation warns that an older kept tree has no Updates screen: " + rbText);
  confirmD.returnValue = "cancel"; confirmD.close();

  // the About card shows "update available" once release.update says so
  status.release.update = { graywolf: false, dxberry: true, system: 0, checked_at: 1700000000 };
  answer = args => {
    if (args[0].endsWith("dxberry-status")) return Promise.resolve(JSON.stringify(status));
    return Promise.reject({ problem: "not-found" });
  };
  doc.getElementById("refresh").click();
  await flush(); await flush(); await flush();
  ok(text().includes("update available"), "the About card shows an update-available badge once release.update says so: " + text());

  console.log(failures ? `${failures} failure(s)` : "all ok");
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log("CRASH " + (e && e.stack || e)); process.exit(2); });
