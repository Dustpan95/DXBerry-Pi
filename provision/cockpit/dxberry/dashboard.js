/* DXBerry page, the station dashboard (applications spec section 5.1): who is on the air, one card
 * per radio with the program it runs, the recent contacts and the station's links. Loaded after
 * dxberry.js, settings.js and updates.js and shares their global scope, so every top-level name here
 * starts with dash or DASH_ (plus the one state object, dashboard). It draws from the last
 * dxberry-status report; its actions are dxberry-app start|stop and dxberry.js's claim and release. */
"use strict";

const DASH_SCREEN = "/cockpit/@localhost/dxberry/screen.html#";
// a program's state on a radio, in words, with its badge
const DASH_STATES = { running: ["Running", "good"], starting: ["Starting", "warn"], stopped: ["Stopped", "warn"], failed: ["Failed", "bad"],
  unplugged: ["Unplugged", "warn"] };
// the update jobs that stop or change a program (its radios wait for them)
const DASH_APP_JOBS = ["app", "app-install", "app-remove"];
const DASH_LOGBOOKS = { wavelog: "Wavelog", cloudlog: "Cloudlog" };

// skew: the Pi's clock minus the browser's (ms), taken when a new report arrives (generated is that
// report's time, to the millisecond); timer: what ticks the two clocks - first a timeout to the
// Pi's next second, then one 1 s interval, made once and kept across redraws (it ticks whichever
// clock elements are current); utc/local: those elements; switching: the radio the Switch program
// dialog is open for.
const dashboard = { skew: 0, generated: null, timer: null, utc: null, local: null, switching: null };

// ---- reading the report ---------------------------------------------------------------------
function dashPrograms(s) {
  return s && s.apps && s.apps.ok && Array.isArray(s.apps.programs) ? s.apps.programs : [];
}

function dashProgram(s, name) {
  return dashPrograms(s).find(p => p.name === name) || null;
}

// dashState S NAME X: the state of the program on radio NAME (running, starting, stopped, failed,
// unplugged), or null when it cannot be told.
function dashState(s, n, x) {
  if (!x.owner) return null;
  if (!x.present) return "unplugged";
  const p = dashProgram(s, x.owner);
  const here = p && (p.sessions || []).find(q => q.radio === n);
  if (here) return here.state;
  if (x.owner === "graywolf" && s.graywolf && s.graywolf.ok) {
    const a = s.graywolf.active;
    return a === "active" ? "running" : a === "failed" ? "failed" : a === "activating" ? "starting" : "stopped";
  }
  return null;
}

// dashJobFor APP: the running update job that installs, updates or removes APP, or null.
function dashJobFor(app) {
  const j = updates.job && updates.job.job;
  return j && j.state === "running" && DASH_APP_JOBS.includes(j.kind) && j.name === app ? j : null;
}

function dashLogbookUrl(lb) {
  if (/^http:\/\/(127\.0\.0\.1|localhost):8083\/?$/.test(lb.url || "")) {
    return (lb.local && lb.local.web) || `https://${location.hostname}:8082/`;
  }
  return /^https:\/\//.test(lb.url || "") ? lb.url : null;
}

function dashLogbookName(lb) {
  if (/^http:\/\/(127\.0\.0\.1|localhost):8083\/?$/.test(lb.url || "")) return "this Pi's Wavelog";
  const host = /^https?:\/\/([^/]+)/.exec(lb.url || "");
  return `${DASH_LOGBOOKS[lb.type] || "A logbook"}${host ? " at " + host[1] : ""}`;
}

const dashPlural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// ---- the station strip ------------------------------------------------------------------------
function dashTick() {
  const d = new Date(Date.now() + dashboard.skew);
  const p = v => String(v).padStart(2, "0");
  if (dashboard.utc) dashboard.utc.textContent = `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}`;
  if (dashboard.local) dashboard.local.textContent = `${p(d.getHours())}:${p(d.getMinutes())}`;
}

function dashStrip(s) {
  const st = s.station && s.station.ok ? s.station : null;
  const call = st ? st.callsign : "";
  const radios = s.radios && s.radios.ok ? Object.keys(s.radios.radios || {}).length : null;
  const problems = Object.keys(s).filter(k => s[k] && typeof s[k] === "object" && s[k].ok === false);
  const facts = [
    s.release && s.release.ok && s.release.dxberry ? el("span", {}, `DXBerry ${s.release.dxberry}`) : null,
    radios === null ? null : el("span", {}, dashPlural(radios, "radio", "radios")),
    problems.length ? badge(dashPlural(problems.length, "problem", "problems"), "bad") : badge("All parts OK", "good"),
  ];
  dashboard.utc = el("span", { class: "dash-clock", id: "dash-utc" });
  dashboard.local = el("span", { class: "dash-clock", id: "dash-local" });
  return el("div", { class: "card dash-strip" },
    el("div", { class: "dash-welcome" },
      el("h2", { id: "dashboard-title" }, call ? `Welcome, ${call}` : "Welcome"),
      call ? null : el("p", { class: "muted hint" }, "Set CALLSIGN in dxberry.txt to see it here."),
      el("p", { class: "dash-facts" }, facts.filter(Boolean).map((f, i) => (i ? [" · ", f] : f))),
      problems.length ? el("p", { class: "muted hint" }, "Some station details could not be read.") : null,
      st && st.grid ? el("p", { class: "muted hint" }, `Grid ${st.grid}`) : null),
    el("div", { class: "dash-clocks" },
      el("div", {}, el("span", { class: "muted" }, "UTC"), dashboard.utc),
      el("div", {}, el("span", { class: "muted" }, "Local"), dashboard.local)));
}

// ---- one card per radio -----------------------------------------------------------------------
function dashGraywolfLine(g, n) {
  if (!g || !g.ok || !g.api_ok) return (g && g.api_error) || DASH;
  const c = (g.channels || []).find(ch => ch.name === n);
  const bits = [];
  if (c) bits.push(`Heard ${num(c.rx_frames)}`, `sent ${num(c.tx_frames)}`);
  if (g.igate) bits.push(`${num(g.igate.rf_to_is_gated)} gated to APRS-IS`);
  return bits.join(" · ") || DASH;
}

function dashOpenLink(s, n, x, label) {
  const a = (href, text, rel) => el("a", { class: "btn small primary", href, target: "_blank", rel, "data-key": `dash:${n}:open` }, text);
  if (x.owner === "graywolf") {
    return a(`http://${location.hostname}:${(s.graywolf && s.graywolf.web_port) || 8080}/`, "Open Graywolf", "noopener noreferrer");
  }
  const p = dashProgram(s, x.owner);
  const o = (p && p.open) || { kind: "screen" };
  if (o.kind === "url") {
    const path = o.path === undefined || o.path === null ? "/" : String(o.path);
    // only a port and a plain path go into the address; anything else gets no link
    if (!/^\d{1,5}$/.test(String(o.port)) || !/^\/[A-Za-z0-9._~/-]*$/.test(path)) return null;
    return a(`https://${location.hostname}:${o.port}${path}`, `Open ${label}`, "noopener");
  }
  if (!PROGRAM_RE.test(x.owner) || !NAME_RE.test(n)) return null;
  return a(DASH_SCREEN + `${x.owner}-${n}`, `Open ${label}`, "noopener");
}

// dashNamesOk RADIO [PROGRAM]: the names are fit to go into a command's arguments; otherwise a
// notice says so and nothing runs.
function dashNamesOk(n, app) {
  if (NAME_RE.test(n) && (app === undefined || PROGRAM_RE.test(app))) return true;
  notice(`${n}${app === undefined ? "" : " / " + app} is not a valid radio or program name, so nothing was changed.`, "bad");
  return false;
}

// dashAppAction NAME VERB LABEL: dxberry-app start|stop for radio NAME; the radio stays the program's.
function dashAppAction(n, verb, label) {
  if (!dashNamesOk(n)) return;
  pending[n] = verb === "stop" ? `Stopping ${label}` : `Starting ${label}`;
  show();
  run([APP, verb, n, "--json"]).then(() => {
    delete pending[n];
    notice(verb === "stop" ? `${label} stopped on ${n}.` : `${label} started on ${n}.`, "good");
    refresh(true);
  }, ex => {
    delete pending[n];
    // 9: the program did not start (its own reason is under Services)
    if (verb === "start" && ex && ex.exit_status === 9) notice(`${label} did not start on ${n}; see Services.`, "bad");
    else failure(verb === "stop" ? `Stopping ${label} on ${n}` : `Starting ${label} on ${n}`, ex, APP_EXITS);
    show();
    refresh(true);
  });
}

function dashRadioCard(s, n, x) {
  const r = s.radios;
  const label = x.owner ? appLabel(r, x.owner) : "";
  const job = x.owner ? dashJobFor(x.owner) : null;
  const busyText = pending[n] || (job ? `${label} is being ${job.kind === "app-remove" ? "removed" : "updated"}` : null);
  const why = busyText ? "Wait for the change shown on this card to finish" : null;
  const state = dashState(s, n, x);
  const [stText, stKind] = DASH_STATES[state] || [state || DASH, "muted"];
  const rows = [["Connected through", parts(x)]];
  if (x.cat) rows.push(["Frequency", freq(x.freq, x.mode)]);
  rows.push(["Program", x.owner ? [label, " ", badge(stText, stKind)] : "No program"]);
  if (x.owner === "graywolf") rows.push(["APRS", dashGraywolfLine(s.graywolf, n)]);
  const acts = [];
  // no Open while a job installs, updates or removes the program
  if (x.owner && state === "running" && !job) acts.push(dashOpenLink(s, n, x, label));
  const flrig = dashProgram(s, "flrig");
  if (x.rig && x.rig.control === "flrig" && flrig && flrig.installed && NAME_RE.test(n)) {
    acts.push(el("a", { class: "btn small", href: DASH_SCREEN + `flrig-${n}`, target: "_blank", rel: "noopener", "data-key": `dash:${n}:flrig` }, "Open flrig"));
  }
  if (x.owner) {
    acts.push(btn("Switch program", () => dashSwitchOpen(n), { key: `dash:${n}:switch`, aria: `Switch the program on ${n}`, disabled: !!busyText, title: why }));
    if (perRadioProgram(x.owner)) {
      if (state === "running" || state === "starting") {
        acts.push(btn("Stop", () => dashAppAction(n, "stop", label), { key: `dash:${n}:stop`, aria: `Stop ${label} on ${n}`, disabled: !!busyText, title: why }));
      } else if (state) {
        acts.push(btn("Start", () => dashAppAction(n, "start", label), { key: `dash:${n}:start`, aria: `Start ${label} on ${n}`,
          disabled: !!busyText || !x.present, title: why || (x.present ? null : "Plug the radio in first") }));
      }
    }
  } else {
    acts.push(btn("Choose a program", () => dashSwitchOpen(n), { cls: "primary", key: `dash:${n}:choose`, aria: `Choose a program for ${n}`, disabled: !!busyText, title: why }));
  }
  acts.push(btn("Radio settings", () => openRadioForm(n), { key: `dash:${n}:settings`, aria: `Settings of ${n}`, disabled: !!busyText, title: why }));
  // the radio's settings changed while the program ran: it takes them at its next start (ruling 7)
  const prog = x.owner ? dashProgram(s, x.owner) : null;
  const here = prog ? (prog.sessions || []).find(q => q.radio === n) : null;
  const rewire = state === "running" && here && here.rewire_pending === true;
  return el("section", { class: "card dash-radio", "data-radio": n, "aria-label": `Radio ${n}` },
    el("h3", {}, x.label || n, x.label ? el("span", { class: "muted" }, ` ${n}`) : null),
    busyText ? el("p", {}, badge(busyText + "…", "warn")) : null,
    rewire ? el("p", { class: "hint" }, badge(`Restart ${label} to use the new radio settings`, "warn")) : null,
    kv(rows),
    el("div", { class: "actions" }, acts));
}

// ---- the Switch program dialog ------------------------------------------------------------------
function dashSwitchOpen(n) {
  const r = last.radios;
  const x = r.radios[n];
  const state = dashState(last, n, x);
  dashboard.switching = n;
  const opts = [["", "No program"]];
  for (const a of r.apps || []) {
    if (a.installed === false) continue;
    let text = a.label;
    if (a.name === x.owner) text += state === "running" || state === "starting" ? " (running now)" : " (chosen now)";
    const p = dashProgram(last, a.name);
    const elsewhere = p && p.single_copy ? (p.sessions || []).map(q => q.radio).filter(q => q !== n) : [];
    if (elsewhere.length) text += ` (on ${elsewhere.join(", ")} now; it runs on one radio at a time)`;
    opts.push([a.name, text]);
  }
  const sel = choice("sw-app", opts, x.owner || "");
  for (const o of sel.options) {
    const p = dashProgram(last, o.getAttribute("value"));
    if (p && p.single_copy && (p.sessions || []).some(q => q.radio !== n)) o.setAttribute("disabled", "");
  }
  document.getElementById("switch-dialog-title").textContent = x.owner ? `Switch the program on ${n}` : `Choose a program for ${n}`;
  document.getElementById("switch-fields").replaceChildren(
    field("Program", "sw-app", sel, "What stops first is said before anything changes."),
    el("p", {}, btn("Install more…", dashInstallMore, { cls: "link", key: "dash:switch:more" })));
  document.getElementById("switch-dialog").showModal();
}

// dashToPrograms: the page scrolls to the Programs section (Install more…, Set up the logbook).
function dashToPrograms() {
  const p = document.getElementById("programs");
  if (p && p.scrollIntoView) p.scrollIntoView({ behavior: "smooth", block: "start" });
}

function dashInstallMore() {
  dashboard.switching = null;
  document.getElementById("switch-dialog").close();
  dashToPrograms();
}

function dashSwitchSave(e) {
  e.preventDefault();
  const n = dashboard.switching;
  const value = document.getElementById("sw-app").value;
  dashboard.switching = null;
  document.getElementById("switch-dialog").close();
  const x = last && last.radios && last.radios.radios[n];
  if (!x) return;
  if (value === (x.owner || "")) { notice(`${n}: nothing changed.`, "good"); return; }
  if (!dashNamesOk(n, value || undefined)) return;
  if (!value) { release(n); return; }
  const app = (last.radios.apps || []).find(a => a.name === value);
  if (app) giveTo(n, app);
}

// ---- recent contacts and the station box --------------------------------------------------------
function dashTime(t) {
  const m = /^(\d{4}-\d\d-\d\d)T(\d\d:\d\d)/.exec(t || "");
  return m ? `${m[1]} ${m[2]}` : t || DASH;
}

function dashRecent(s) {
  const kids = [el("h2", {}, "Recent contacts")];
  const a = s.apps;
  const lb = a && a.ok ? a.logbook || {} : null;
  const rec = a && a.ok ? a.recent || {} : null;
  if (!a || !a.ok) kids.push(el("p", { class: "error" }, (a && a.error) || "The logbook could not be read."));
  else if (lb.error) kids.push(el("p", { class: "error" }, lb.error));
  else if (!lb.configured) {
    kids.push(el("p", { class: "muted" }, "No logbook yet. Set one up under Programs → Logbook."),
      el("div", { class: "actions" }, btn("Set up the logbook", dashToPrograms, { cls: "primary", key: "dash:logbook:setup" })));
  }
  else if (rec.ok !== true) kids.push(el("p", { class: "error" }, rec.error || "The recent contacts could not be read."));
  else if (!(rec.contacts || []).length) kids.push(el("p", { class: "muted" }, "No contacts in the logbook yet."));
  else {
    kids.push(table(["UTC", "Call", "Band", "Mode", "Sent", "Rcvd", "Grid"],
      rec.contacts.map(c => [dashTime(c.time_utc), c.call, c.band, c.mode, c.rst_sent, c.rst_rcvd, c.grid])));
  }
  const href = lb && lb.configured ? dashLogbookUrl(lb) : null;
  if (href) kids.push(el("div", { class: "actions" }, el("a", { class: "btn small", href, target: "_blank", rel: "noopener", "data-key": "dash:logbook" }, "Open the logbook")));
  return el("section", { class: "card dash-recent" }, kids);
}

function dashLogbookState(s) {
  const a = s.apps;
  if (!a || !a.ok) return DASH;
  const lb = a.logbook || {};
  if (lb.error) return lb.error;
  const bits = [lb.configured ? dashLogbookName(lb) : badge("not set", "muted")];
  if (lb.queue > 0) bits.push(" ", badge(`${dashPlural(lb.queue, "contact", "contacts")} waiting to be sent`, "warn"));
  if (lb.refused > 0) bits.push(" ", badge(`${dashPlural(lb.refused, "contact", "contacts")} refused by the logbook`, "bad"));
  return bits;
}

function dashStationBox(s) {
  const g = s.graywolf;
  const ig = g && g.ok && g.api_ok ? g.igate : null;
  const n = s.network;
  const how = n && n.ok ? ({ ETH: "Ethernet", WIFI: "WiFi", NONE: "no connection" }[n.state] || n.state) : null;
  return el("section", { class: "card dash-box" }, el("h2", {}, "Station"), kv([
    ["Logbook", dashLogbookState(s)],
    ["APRS-IS", ig ? [badge(ig.connected ? "connected" : "not connected", ig.connected ? "good" : "warn"), " ", ig.server] : (g && g.ok && g.api_error) || DASH],
    ["GPS", s.radios && s.radios.ok ? gps(s.radios.gps) : DASH],
    ["Network", how ? [badge(how, n.state === "ETH" || n.state === "WIFI" ? "good" : "bad"), n.address ? ` ${n.address}` : ""] : DASH],
  ]));
}

// ---- drawing ------------------------------------------------------------------------------------
// dashRender S: the dashboard from report S. The clocks' offset is taken once per new report; the
// one 1 s interval is replaced with the nodes it ticks.
function dashRender(s) {
  const sec = document.getElementById("dashboard");
  if (!sec || !s) return;
  const a = document.activeElement;
  const focused = a && a.dataset ? a.dataset.key : undefined;
  if (s.generated !== dashboard.generated) {
    dashboard.generated = s.generated;
    const t = Date.parse(s.generated);
    dashboard.skew = Number.isNaN(t) ? 0 : t - Date.now();
  }
  const radios = s.radios && s.radios.ok ? s.radios.radios || {} : {};
  const names = Object.keys(radios);
  sec.replaceChildren(
    dashStrip(s),
    names.length ? el("div", { class: "dash-radios" }, names.map(n => dashRadioCard(s, n, radios[n])))
      : el("p", { class: "card muted" }, s.radios && s.radios.ok ? "No radios set up yet: add one under Radios below." : "The radios could not be read; see the Radios card below."),
    el("div", { class: "dash-bottom" }, dashRecent(s), dashStationBox(s)));
  dashTick();
  if (dashboard.timer === null) {
    // the first tick lands on the Pi's next whole second, then one tick a second
    dashboard.timer = setTimeout(() => {
      dashTick();
      dashboard.timer = setInterval(dashTick, 1000);
    }, 1000 - (((Date.now() + dashboard.skew) % 1000) + 1000) % 1000);
  }
  if (focused) {
    const again = [...sec.querySelectorAll("[data-key]")].find(e => e.dataset.key === focused);
    if (again) again.focus();
  }
}

function dashInit() {
  document.getElementById("switch-form").addEventListener("submit", dashSwitchSave);
  document.getElementById("switch-cancel").addEventListener("click", () => { dashboard.switching = null; document.getElementById("switch-dialog").close(); });
  // a job starting or ending changes which radios wait for it
  updWatchers.push(() => { if (last) dashRender(last); });
}

dashInit();
