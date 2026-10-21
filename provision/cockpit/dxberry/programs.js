/* DXBerry page, Programs (applications spec section 5.2): every program DXBerry can install, with
 * Install, Update and Remove, and the logbook the programs log to. Loaded after dxberry.js,
 * settings.js, updates.js and dashboard.js and shares their global scope, so every top-level name
 * here starts with prog or PROG_ (plus the one state object, programs). Installs and removals are
 * update jobs (dxberry-app install|remove, followed through updates.js); the logbook is
 * dxberry-logbook, its address and key always on standard input. */
"use strict";

const PROG_LOGBOOK = "/opt/dxberry/bin/dxberry-logbook";
// dxberry-logbook's exit codes in words
const PROG_LOGBOOK_EXITS = {
  1: "this needs administrative access",
  2: "the setting is not valid",
  6: "the logbook did not answer, or refused",
};
const PROG_KINDS = { radio: "Uses a radio", rigcontrol: "Rig control for a radio", service: "Runs on its own" };
const PROG_SOURCES = { debian: "From Debian", upstream: "From its authors", builtin: "Part of DXBerry" };
// what a program's row says about it besides its description
const PROG_NOTES = { qsstv: "Log SSTV contacts by hand in the logbook." };
const PROG_DOING = { "app-install": "Installing…", "app-remove": "Removing…", app: "Updating…" };
const PROG_MEM_MIN = 2 * 1024 * 1024 * 1024;
const PROG_LOCAL_RE = /^http:\/\/(127\.0\.0\.1|localhost):8083\/?$/;

// starting: an install, update or removal asked for, its job not seen yet; form: the open logbook
// dialog ({saving}).
const programs = { starting: false, form: null };

function progBusy() {
  return programs.starting || updBusy();
}

// ---- one row per program ----------------------------------------------------------------------
// progStart VERB P: dxberry-app install|remove, or dxberry-update app (an update), as an update job
// whose output shows under Updates.
function progStart(verb, p) {
  if (!PROGRAM_RE.test(p.name)) { notice(`${p.name} is not a valid program name, so nothing was changed.`, "bad"); return; }
  const args = verb === "update" ? [UPDATE, "app", p.name, "--json"] : [APP, verb, p.name, "--json"];
  const what = { install: "Installing", remove: "Removing", update: "Updating" }[verb];
  programs.starting = true;
  progRender(last);
  run(args).then(() => {
    programs.starting = false;
    notice(`${what} ${p.label}… Its progress shows under Updates.`, "good");
    updLoadJob();
  }, ex => {
    programs.starting = false;
    failure(`${what} ${p.label}`, ex, verb === "update" ? UPDATE_EXITS : APP_EXITS);
    progRender(last);
  });
}

function progRemove(p) {
  confirmThen(`Remove ${p.label}?`, `${p.label} is removed; its settings and logs stay, so a reinstall picks up where you left off.`, "Remove",
    () => progStart("remove", p));
}

// progRigControlUsers NAME: the radios whose rig control is program NAME (flrig).
function progRigControlUsers(name) {
  const all = last && last.radios && last.radios.ok && last.radios.radios ? last.radios.radios : {};
  return Object.keys(all).filter(n => all[n] && all[n].rig && all[n].rig.control === name);
}

function progRow(p, busy) {
  const job = updates.job && updates.job.job;
  const doing = job && job.state === "running" && job.name === p.name ? PROG_DOING[job.kind] : null;
  const radios = (p.sessions || []).map(q => q.radio);
  const rigUsers = progRigControlUsers(p.name);
  const why = busy ? "Wait for the install, update or removal under Updates to finish" : null;
  const check = updates.check && updates.check.apps ? updates.check.apps[p.name] : null;
  const notes = [];
  if (p.single_copy) notes.push("Runs on one radio at a time.");
  if (PROG_NOTES[p.name]) notes.push(PROG_NOTES[p.name]);
  const acts = [];
  if (p.source !== "builtin") {
    if (!p.installed) {
      acts.push(btn("Install", () => progStart("install", p), { cls: "primary", key: `prog:${p.name}:install`, aria: `Install ${p.label}`, disabled: busy, title: why }));
    } else {
      if (check && check.update) {
        acts.push(btn(`Update to ${check.latest}`, () => progStart("update", p), { cls: "primary", key: `prog:${p.name}:update`, aria: `Update ${p.label}`, disabled: busy, title: why }));
      }
      // a radio that has the program keeps it, running or stopped, so removal waits for a switch;
      // one whose rig control is the program (flrig) waits for its rig control to go back to Hamlib
      const running = radios.length ? `${radios.join(", ")} ${radios.length === 1 ? "uses" : "use"} it` : null;
      const rigWhy = rigUsers.length ? (rigUsers.length === 1 ? `${rigUsers[0]} uses ${p.label} for rig control; set its rig control to Hamlib first`
        : `${rigUsers.join(", ")} use ${p.label} for rig control; set their rig control to Hamlib first`) : null;
      acts.push(btn("Remove", () => progRemove(p), { cls: "danger", key: `prog:${p.name}:remove`, aria: `Remove ${p.label}`,
        disabled: busy || !!running || !!rigWhy, title: why || (running ? `${running}; switch ${radios.join(", ")} to another program first` : rigWhy) }));
    }
  }
  return el("div", { class: "prog-row", "data-program": p.name },
    el("div", { class: "radio-head" }, el("strong", {}, p.label), " ",
      badge(p.installed ? `Installed${p.version ? " " + p.version : ""}` : "Not installed", p.installed ? "good" : "muted"),
      doing ? [" ", badge(doing, "warn")] : null),
    el("p", {}, p.description),
    el("p", { class: "muted hint" }, [PROG_KINDS[p.kind] || p.kind, PROG_SOURCES[p.source] || p.source].join(" · ")),
    notes.length ? el("p", { class: "hint" }, notes.join(" ")) : null,
    p.installed && radios.length && p.source !== "builtin" ? el("p", { class: "muted hint" }, `${radios.join(", ")} ${radios.length === 1 ? "uses" : "use"} it.`) : null,
    p.installed && rigUsers.length ? el("p", { class: "muted hint" }, `${rigUsers.join(", ")} ${rigUsers.length === 1 ? "uses" : "use"} it for rig control.`) : null,
    p.installed && p.setup_note ? el("p", { class: "hint prog-note" }, p.setup_note) : null,
    // the logbook keys the operator changed in the program, which the logbook no longer sets there
    (Array.isArray(p.kept) ? p.kept : []).map(k => el("p", { class: "hint" }, progKept(k))),
    acts.length ? el("div", { class: "actions" }, acts) : null);
}

// ---- the logbook --------------------------------------------------------------------------------
function progLogbookRow(s, busy) {
  const lb = s.apps.logbook || {};
  const local = lb.local || {};
  const wavelog = Array.isArray(s.apps.programs) ? s.apps.programs.find(p => p.name === "wavelog") : null;
  const why = busy ? "Wait for the install, update or removal under Updates to finish" : null;
  const kids = [el("div", { class: "radio-head" }, el("strong", {}, "Logbook"), " ",
    lb.error ? badge("could not be read", "bad") : lb.configured ? badge(dashLogbookName(lb), "good") : badge("not set", "muted"))];
  if (lb.error) kids.push(el("p", { class: "error" }, lb.error));
  kids.push(el("p", {}, "Where WSJT-X, JS8Call and fldigi log their contacts: your own Wavelog or Cloudlog, or a Wavelog on this Pi."));
  if (lb.queue > 0) kids.push(el("p", { class: "hint" }, `${dashPlural(lb.queue, "contact", "contacts")} waiting to be sent; they go as soon as the logbook answers.`));
  if (lb.refused > 0) {
    kids.push(el("p", { class: "hint" }, `The logbook refused ${dashPlural(lb.refused, "contact", "contacts")} (kept on the Pi)${lb.refused_reason ? ": " + lb.refused_reason : "."}`));
  }
  const acts = [btn("Use my own…", () => progLogbookOpen(null), { key: "prog:logbook:own", disabled: busy, title: why })];
  if (local.installed) {
    if (!PROG_LOCAL_RE.test(lb.url || "")) {
      acts.push(btn("Use this Pi's Wavelog…", () => progLogbookOpen(local.url), { key: "prog:logbook:local", disabled: busy, title: why }));
    }
  } else if (wavelog && !wavelog.installed) {
    acts.push(btn("Install on this Pi", () => progStart("install", wavelog), { key: "prog:logbook:install", disabled: busy, title: why }));
  }
  const href = lb.configured ? dashLogbookUrl(lb) : null;
  if (href) acts.push(el("a", { class: "btn small", href, target: "_blank", rel: "noopener", "data-key": "prog:logbook:open" }, "Open the logbook"));
  if (lb.configured) acts.push(btn("Forget", progLogbookForget, { cls: "danger", key: "prog:logbook:forget", disabled: busy, title: why }));
  kids.push(el("div", { class: "actions" }, acts));
  return el("div", { class: "prog-row prog-logbook", "data-program": "logbook" }, kids);
}

function progInput(id, value, attrs) {
  return el("input", Object.assign({ id, type: "text", value: value || "", autocomplete: "off", spellcheck: "false" }, attrs || {}));
}

// progLogbookOpen URL: the logbook dialog, its address URL (this Pi's Wavelog) or the stored one.
function progLogbookOpen(url) {
  const lb = (last && last.apps && last.apps.logbook) || {};
  const keySet = lb.key === "set";
  programs.form = { saving: false, testing: false };
  document.getElementById("logbook-dialog-title").textContent = "Logbook";
  progSetError("");
  progSetResult("");
  progSetSaving(false);
  progSetTesting(false);
  document.getElementById("logbook-fields").replaceChildren(
    field("Address", "lb-url", progInput("lb-url", url || lb.url || "", { placeholder: "https://log.example.org/" }),
      "Your Wavelog or Cloudlog, starting with https://. This Pi's own logbook is offered as a choice once it is installed."),
    field("API key", "lb-key", progInput("lb-key", "", { type: "password", autocomplete: "new-password", placeholder: keySet ? "Leave empty to keep the stored key" : "" }),
      keySet ? "Leave empty to keep the stored key." : "Made in the logbook, under your account's API keys; it needs to read and write."),
    field("Station ID", "lb-station", progInput("lb-station", lb.station_id || "1", { inputmode: "numeric" }),
      "The number of your station location in the logbook; 1 when it has only one."),
    field("Public slug", "lb-slug", progInput("lb-slug", lb.slug || ""),
      "Cloudlog only: your logbook's public slug, so the recent contacts show here. Leave it empty for Wavelog."));
  document.getElementById("logbook-dialog").showModal();
}

function progSetError(text) {
  const p = document.getElementById("logbook-form-error");
  p.textContent = text;
  p.hidden = !text;
}

function progSetResult(text) {
  const p = document.getElementById("logbook-result");
  p.textContent = text;
  p.hidden = !text;
}

function progSetTesting(on) {
  const b = document.getElementById("logbook-test");
  b.disabled = on;
  b.textContent = on ? "Testing…" : "Test";
}

function progSetSaving(on) {
  const b = document.getElementById("logbook-save");
  b.disabled = on;
  document.getElementById("logbook-test").disabled = on;
  b.textContent = on ? "Saving…" : "Save";
}

// progLogbookStdin: the dialog's fields as dxberry-logbook's standard input. An empty key field
// sends no key, so the stored one is kept.
function progLogbookStdin() {
  const v = id => { const e = document.getElementById(id); return e ? e.value.trim() : ""; };
  const o = { url: v("lb-url"), station_id: v("lb-station"), slug: v("lb-slug") };
  const key = v("lb-key");
  return JSON.stringify(key ? { url: o.url, key, station_id: o.station_id, slug: o.slug } : o);
}

// progProblem EX DATA: why a logbook command failed - its JSON answer's error when Cockpit hands
// over the output, else its standard error and exit code.
function progProblem(ex, data) {
  try {
    const j = JSON.parse(data);
    if (j && j.error) return j.error;
  } catch (e) { /* no answer to read: the error text below */ }
  return problemText(ex, PROG_LOGBOOK_EXITS);
}

// progKept LINE: dxberry-logbook's "radio2: CLOUDLOG_STATION_ID (changed in fldigi)" in words.
function progKept(line) {
  const m = /^(.+?): (\S+) \(changed in (.+)\)$/.exec(line);
  return m ? `${m[1]}: ${m[3]} keeps its own ${m[2]}` : line;
}

// progFollowsLater RADIO: a program running on RADIO when the logbook changed follows it at its
// next start, in words.
function progFollowsLater(r) {
  const x = last && last.radios && last.radios.radios ? last.radios.radios[r] : null;
  const label = x && x.owner ? appLabel(last.radios, x.owner) : "The program";
  return `${label} on ${r} is running, so it follows the new logbook when it next starts.`;
}

function progLogbookTest() {
  const f = programs.form;
  if (!f || f.saving || f.testing) return;
  f.testing = true;
  progSetTesting(true);
  progSetError("");
  progSetResult("Asking the logbook…");
  const done = () => { f.testing = false; if (programs.form === f) progSetTesting(false); };
  run([PROG_LOGBOOK, "test", "--json"], progLogbookStdin()).then(out => {
    let j = null;
    try { j = JSON.parse(out); } catch (e) { j = null; }
    done();
    if (programs.form !== f) return;
    if (j && j.ok) progSetResult(`${DASH_LOGBOOKS[j.type] || j.type}${j.version ? " " + j.version : ""} answers.`);
    else progSetResult(`Not a logbook: ${(j && j.error) || "no answer could be read"}`);
  }, (ex, data) => {
    done();
    if (programs.form !== f) return;
    progSetResult("");
    progSetError(progProblem(ex, data));
  });
}

function progLogbookSave(e) {
  e.preventDefault();
  const f = programs.form;
  if (!f || f.saving || f.testing) return;
  const d = document.getElementById("logbook-dialog");
  f.saving = true;
  progSetError("");
  progSetResult("");
  progSetSaving(true);
  run([PROG_LOGBOOK, "set", "--json"], progLogbookStdin()).then(out => {
    f.saving = false;
    if (programs.form === f) { progSetSaving(false); programs.form = null; if (d.open) d.close(); }
    let j = {};
    try { j = JSON.parse(out) || {}; } catch (ex) { j = {}; }
    notice(`Logbook saved${j.type ? `: ${DASH_LOGBOOKS[j.type] || j.type}` : ""}. Contacts go there from now on.`, "good");
    for (const k of j.kept || []) notice(progKept(k), "warn");
    for (const r of j.following_later || []) notice(progFollowsLater(r), "warn");
    refresh(true);
  }, (ex, data) => {
    f.saving = false;
    if (programs.form === f && d.open) { progSetSaving(false); progSetError(progProblem(ex, data)); }
    else failure("Saving the logbook", ex, PROG_LOGBOOK_EXITS);
  });
}

function progLogbookForget() {
  confirmThen("Forget the logbook?", "DXBerry stops sending contacts to it. Contacts not sent yet stay on the Pi until a logbook is set again.", "Forget",
    () => run([PROG_LOGBOOK, "set", "--json"], JSON.stringify({ clear: true })).then(() => {
      notice("Logbook forgotten.", "good");
      refresh(true);
    }, ex => failure("Forgetting the logbook", ex, PROG_LOGBOOK_EXITS)));
}

// ---- drawing ------------------------------------------------------------------------------------
function progRender(s) {
  const sec = document.getElementById("programs");
  if (!sec || !s) return;
  const a = document.activeElement;
  const focused = a && a.dataset ? a.dataset.key : undefined;
  const kids = [el("h2", { id: "programs-title" }, "Programs")];
  const apps = s.apps;
  if (!apps || !apps.ok) {
    kids.push(el("p", { class: "error" }, (apps && apps.error) || "No data."));
  } else {
    const busy = progBusy();
    if (s.pi && s.pi.ok && typeof s.pi.mem_total === "number" && s.pi.mem_total < PROG_MEM_MIN) {
      kids.push(el("div", { class: "notice warn" }, el("span", {}, "This Pi has less than 2 GB of memory. One program at a time works best.")));
    }
    if (busy) kids.push(el("p", { class: "muted" }, "An install, update or removal is running; its output shows under Updates."));
    if (!Array.isArray(apps.programs)) kids.push(el("p", { class: "error" }, (apps.programs && apps.programs.error) || "The programs could not be read."));
    else kids.push(...apps.programs.map(p => progRow(p, busy)));
    kids.push(progLogbookRow(s, busy));
  }
  sec.replaceChildren(...kids);
  if (focused) {
    const again = [...sec.querySelectorAll("[data-key]")].find(e => e.dataset.key === focused);
    if (again) again.focus();
  }
}

function progInit() {
  document.getElementById("logbook-form").addEventListener("submit", progLogbookSave);
  document.getElementById("logbook-test").addEventListener("click", progLogbookTest);
  document.getElementById("logbook-cancel").addEventListener("click", () => { programs.form = null; document.getElementById("logbook-dialog").close(); });
  // a job starting or ending changes the rows' buttons and their Installing…/Removing…
  updWatchers.push(() => { if (last) progRender(last); });
}

progInit();
