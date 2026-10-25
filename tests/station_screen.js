// station_screen.js: drives the program tab (station/web/screen.js) in a fake browser - a minimal DOM
// shaped like screen.html, a fake dxb (run answers like dxberry-app screen, ws records the path) and a
// stub RFB class standing in for noVNC's. Plain node, no packages.
// Run: node tests/station_screen.js station/web/screen.js
// Prints one ok/FAIL line per check; exits 1 when a check failed, 2 on a crash.
"use strict";
const fs = require("fs");
const vm = require("vm");

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
  (async () => {
    const src = fs.readFileSync(process.argv[2], "utf8");
    const importLine = /^import RFB from "\.\/vendor\/novnc\/core\/rfb\.js";$/m;
    ok(importLine.test(src), "screen.js imports RFB from ./vendor/novnc/core/rfb.js");
    const code = src.replace(importLine, "const RFB = globalThis.FakeRFB;");
    const flush2 = () => new Promise(r => setImmediate(r));
    const run = async (hash, info, fail) => {
      doc.body.replaceChildren();
      for (const [tag, id, cls] of [["strong", "program"], ["span", "radio"], ["span", "state"], ["button", "reconnect", "btn"], ["a", "back"], ["main", "screen"]]) doc.body.append(mk(tag, id, cls));
      const spawns = []; const rfbs = []; const sockets = [];
      class FakeRFB {
        constructor(target, url, opts) { this.target = target; this.url = url; this.opts = opts; this.listeners = {}; rfbs.push(this); }
        addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); }
        emit(t) { (this.listeners[t] || []).forEach(f => f({ detail: {} })); }
        // like noVNC's own, a disconnect asked for tells its listeners at once
        disconnect() { this.gone = true; this.emit("disconnect"); }
      }
      const sctx = {
        document: doc, console, FakeRFB, decodeURIComponent, Error, JSON,
        location: { hash, host: "pi" },
        dxb: {
          run(args, input) { spawns.push({ args, input }); return fail ? Promise.reject(fail) : Promise.resolve(JSON.stringify(info)); },
          ws(path) { const w = { path }; sockets.push(w); return w; },
        },
      };
      sctx.globalThis = sctx;
      vm.createContext(sctx);
      vm.runInContext(code, sctx);
      await flush2(); await flush2(); await flush2();
      return { spawns, rfbs, sockets, text: () => doc.body.textContent, el: id => doc.getElementById(id), sctx };
    };
    const base = { session: "wsjtx-radio2", app: "wsjtx", radio: "radio2", label: "WSJT-X", radio_label: "radio2", open: { kind: "screen" }, access: true };

    let r = await run("#wsjtx-radio2", { ...base, state: "stopped", socket: null });
    ok(r.text().includes("WSJT-X is not running on radio2. Start it from the station page."), "stopped: says so: " + r.text());
    ok(r.rfbs.length === 0, "stopped: no RFB is created");
    ok(r.spawns.length === 1 && JSON.stringify(r.spawns[0].args) === JSON.stringify(["/opt/dxberry/bin/dxberry-app", "screen", "wsjtx-radio2", "--json"]), "stopped: asked dxberry-app screen");
    ok(r.text().includes("Go to the station page"), "stopped: links to the station page");

    const runningInfo = { ...base, state: "running", socket: "/run/dxberry/screens/wsjtx-radio2.sock" };
    r = await run("#wsjtx-radio2", runningInfo);
    ok(r.rfbs.length === 1, "running: one RFB is created");
    const u = r.rfbs[0] ? r.rfbs[0].url : null;
    ok(r.sockets.length === 1 && r.sockets[0].path === "/api/vnc/wsjtx-radio2" && u === r.sockets[0], "running: RFB gets dxb.ws('/api/vnc/wsjtx-radio2'): " + JSON.stringify(r.sockets));
    ok(r.rfbs[0] && r.rfbs[0].opts.shared === true && r.rfbs[0].scaleViewport === false && r.rfbs[0].resizeSession === true, "running: shared, no viewport scaling, session resizes");
    ok(r.rfbs[0] && r.rfbs[0].target === r.el("screen"), "running: RFB draws into #screen");
    ok(r.el("state").textContent === "Connecting", "running: shows Connecting: " + r.el("state").textContent);
    r.rfbs[0].emit("connect");
    ok(r.el("state").textContent === "Connected", "shows Connected");
    r.rfbs[0].emit("disconnect");
    ok(r.el("state").textContent === "Disconnected" && !r.text().includes("Log out of the console"), "shows Disconnected after a good session");
    ok(r.sctx.document.getElementById("program").textContent === "WSJT-X" && r.el("radio").textContent === "radio2", "bar names program and radio");
    ok(doc.title === "WSJT-X \u00b7 radio2", "tab title is program and radio: " + doc.title);
    r.el("reconnect").click();
    await flush2(); await flush2(); await flush2();
    ok(r.spawns.length === 2 && r.rfbs.length === 2 && r.rfbs[1].url !== u && r.sockets.length === 2 && r.sockets[1].path === "/api/vnc/wsjtx-radio2", "Reconnect re-runs the whole sequence (spawn + new RFB)");
    // closed before ever connecting: the program tab asks again, and says "log out and in" only
    // when this login has no access to program screens
    r.rfbs[1].emit("disconnect");
    await flush2(); await flush2();
    ok(r.spawns.length === 3 && r.text().includes("The program window closed. Reconnect?") && !r.text().includes("Log out of the console"),
      "an early close with access: the window closed, reconnect: " + r.text());
    r.el("reconnect").click();
    await flush2(); await flush2(); await flush2();
    runningInfo.access = false;
    r.rfbs[2].emit("disconnect");
    await flush2(); await flush2();
    ok(r.text().includes("This login cannot open the program's screen yet. Log out of the console and back in once, then reload this tab."), "refused channel: log out and in: " + r.text());
    runningInfo.access = true;
    // Reconnect while the old screen is still connecting: the old one's close belongs to the old try
    r = await run("#wsjtx-radio2", runningInfo);
    r.el("reconnect").click();
    await flush2(); await flush2(); await flush2();
    ok(r.spawns.length === 2 && r.rfbs.length === 2 && r.rfbs[0].gone && r.el("state").textContent === "Connecting",
      "Reconnect drops the old screen without asking again for it: " + r.spawns.length + " questions, " + r.el("state").textContent);

    r = await run("#wsjtx-radio2", { session: "wsjtx-radio2", access: false });
    ok(r.rfbs.length === 0 && r.text().includes("Log out of the console and back in once, then reload this tab."), "no dxbscreen access: the log out/in message, no RFB");

    r = await run("#%E0%A4%A", null);
    ok(r.text().includes("This link is not a program window") && r.spawns.length === 0, "a malformed link: not a program window: " + r.text());

    r = await run("#../etc", null);
    ok(r.text().includes("No program is named here.") && r.spawns.length === 0 && r.rfbs.length === 0 && r.sockets.length === 0, "bad hash: named nowhere, nothing spawned: " + r.text());

    r = await run("#wsjtx-radio9", null, { exit_status: 3, message: "no such session" });
    ok(r.text().includes("No program is named here.") && r.rfbs.length === 0, "unknown session (exit 3): named nowhere");

    console.log(failures ? `${failures} failure(s)` : "all ok");
    process.exit(failures ? 1 : 0);
  })().catch(e => { console.log("CRASH " + (e && e.stack || e)); process.exit(2); });
