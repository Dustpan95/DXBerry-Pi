/* DXBerry page, updates (console spec section 11): what is installed against what is out, and the
 * jobs that update Graywolf, DXBerry and the system packages. Loaded after dxberry.js and
 * settings.js and shares their global scope, so every top-level name here starts with upd or
 * UPDATE (plus the one state object, updates). Every reading and action is dxberry-update. */
"use strict";

const UPDATE = "/opt/dxberry/bin/dxberry-update";
// dxberry-update's exit codes in words (console spec section 11).
const UPDATE_EXITS = {
  1: "this needs administrative access",
  2: "the request was not valid",
  5: "an update or a settings change is still running",
  6: "the update failed, so nothing was changed",
  8: "the update was installed, but a later step failed",
};
// the pre-release switch's own exit 6: the setting, not an update, could not be saved
const UPDATE_SETTING_EXITS = Object.assign({}, UPDATE_EXITS, { 6: "the setting could not be saved, so it is unchanged" });
const UPDATE_POLL_MS = 2000;
// what a running job is doing, by kind
const UPDATE_RUNNING = { graywolf: "Updating Graywolf…", dxberry: "Updating DXBerry…", rollback: "Rolling back…", system: "Updating system packages…" };

/* update-fields-begin: what the page reads from dxberry-update's --json answers, as COMMAND:PATH.
 * tests/test_page.sh checks each one against the command's real output. */
const UPDATE_FIELDS = [
  "check:checked_at", "check:cached", "check:reboot_required",
  "check:graywolf.installed", "check:graywolf.latest", "check:graywolf.pinned", "check:graywolf.update",
  "check:dxberry.installed", "check:dxberry.latest", "check:dxberry.prerelease", "check:dxberry.update",
  "check:dxberry.include_prereleases",
  "check:system.count", "check:system.packages",
  "check:rollback.available", "check:rollback.version", "check:rollback.updates",
  "start:job", "start:kind",
  "job:now", "job:reboot_required", "job:job.unit", "job:job.kind", "job:job.state", "job:job.exit", "job:job.lines",
  "prereleases:include_prereleases",
];
/* update-fields-end */

// check: dxberry-update check's answer (or {failed: text}); job: dxberry-update job's answer;
// jobError: why the last job poll could not be read (null once one is).
const updates = { check: null, job: null, jobError: null, poll: null, logOpen: false, checking: false };

function updRun(args) {
  return run([UPDATE, ...args, "--json"]);
}

function updParse(out) {
  try { return JSON.parse(out); } catch (e) { return null; }
}

function updLoadCheck(refresh) {
  updates.checking = true;
  updRender();
  updRun(refresh ? ["check", "--refresh"] : ["check"]).then(out => {
    updates.checking = false;
    updates.check = updParse(out) || { failed: "dxberry-update did not return a check." };
    updRender();
  }, ex => {
    updates.checking = false;
    updates.check = { failed: problemText(ex, UPDATE_EXITS) };
    updRender();
  });
}

// updJobFailed TEXT: a job poll that failed or did not parse - said under Updates, and asked again
// at the slower pace.
function updJobFailed(text) {
  updates.jobError = text;
  updRender();
  updates.poll = setTimeout(updLoadJob, UPDATE_POLL_MS * 5);
}

function updLoadJob() {
  clearTimeout(updates.poll);
  updRun(["job"]).then(out => {
    const j = updParse(out);
    if (!j || typeof j !== "object") {
      updJobFailed("The update job's state could not be read (dxberry-update's answer did not parse); trying again.");
      return;
    }
    const wasRunning = !!(updates.job && updates.job.job && updates.job.job.state === "running");
    updates.job = j;
    updates.jobError = null;
    updRender();
    if (j.job && j.job.state === "running") updates.poll = setTimeout(updLoadJob, UPDATE_POLL_MS);
    else if (wasRunning) updFinished(j.job);
  }, ex => updJobFailed(`The update job's state could not be read (${problemText(ex, UPDATE_EXITS)}); trying again.`));
}

function updFinished(job) {
  const what = { graywolf: "Graywolf update", dxberry: "DXBerry update", rollback: "Rollback", system: "System update" }[job.kind] || "Update";
  if (job.exit === 0) notice(`${what} finished.`, "good");
  else if (job.exit === 8) notice(`${what}: installed, but a later step failed; see its output under Updates.`, "bad");
  else if (job.exit === 6) notice(`${what} failed. ${job.kind === "rollback" ? "Nothing was rolled back" : "Nothing changed"}; see its output under Updates.`, "bad");
  else notice(`${what} ended without a result; see its output under Updates.`, "bad");
  updLoadCheck(false);
}

function updBusy() {
  const j = updates.job;
  return !!(j && j.job && j.job.state === "running");
}

function updStart(kind, title, text, label) {
  confirmThen(title, text, label, () => updRun([kind]).then(out => {
    const r = updParse(out) || {};
    notice(`Started: ${r.kind || kind}.`, "good");
    updLoadJob();
  }, ex => failure(title.replace(/\?$/, ""), ex, UPDATE_EXITS)));
}

function updRows(c) {
  const rows = [];
  const g = c.graywolf || {};
  rows.push(["Graywolf", g.error ? g.error : g.pinned ? `${g.installed} (pinned to ${g.pinned} in dxberry.txt)`
    : g.update ? [badge("update", "warn"), ` ${g.installed} → ${g.latest}`] : `${g.installed || "not installed"}, the newest`]);
  const d = c.dxberry || {};
  rows.push(["DXBerry", d.error ? d.error : d.update ? [badge("update", "warn"), ` ${d.installed} → ${d.latest}${d.prerelease ? " (pre-release)" : ""}`]
    : `${d.installed}, ${updNewest(d)}`]);
  const s = c.system || {};
  rows.push(["System", s.error ? s.error : s.count ? [badge("update", "warn"), ` ${s.count} package${s.count === 1 ? "" : "s"} to upgrade`] : "up to date"]);
  if (c.rollback && c.rollback.available) rows.push(["Rollback", `DXBerry ${c.rollback.version} is kept`]);
  rows.push(["Checked", c.checked_at ? new Date(c.checked_at * 1000).toLocaleString("en-US") + (c.cached ? " (from the last check)" : "") : DASH]);
  return rows;
}

// updNewest D: the DXBerry row's words when nothing is newer. A pre-release is never called "the
// newest full release": not the newest one found (latest is a pre-release), nor an installed
// pre-release while pre-releases are off (a newer one would not have been looked for).
function updNewest(d) {
  if (d.prerelease) return "the newest pre-release";
  if (d.include_prereleases) return "the newest";
  return /-/.test(String(d.installed || "")) ? "no newer full release" : "the newest full release";
}

function updActions(c) {
  const busy = updBusy() || updates.checking;
  const g = c.graywolf || {}, d = c.dxberry || {}, s = c.system || {};
  const acts = [];
  if (g.update) acts.push(btn(`Update Graywolf to ${g.latest}`, () => updStart("graywolf", "Update Graywolf?",
    `Graywolf ${g.installed} is replaced with ${g.latest} and restarted: APRS, the iGate and the digipeater stop for a moment.`, "Update"),
    { cls: "primary", key: "upd:graywolf", disabled: busy }));
  if (d.update) acts.push(btn(`Update DXBerry to ${d.latest}`, () => updStart("dxberry", "Update DXBerry?",
    `DXBerry ${d.installed} is replaced with ${d.latest} and the setup runs again; this page may reload. ${d.installed} is kept for a rollback.`, "Update"),
    { cls: "primary", key: "upd:dxberry", disabled: busy }));
  if (s.count) acts.push(btn(`Install ${s.count} system update${s.count === 1 ? "" : "s"}`, () => updStart("system", "Install the system updates?",
    "apt-get upgrades the Debian packages without asking. This can take several minutes; if Cockpit itself is upgraded, this page disconnects and you log in again.", "Install"),
    { key: "upd:system", disabled: busy }));
  // after a rollback, the kept tree is the NEWER one: the words never say which way this goes
  if (c.rollback && c.rollback.available) acts.push(btn(`Roll back to ${c.rollback.version}`, () => updStart("rollback", "Roll back DXBerry?",
    `Switch to DXBerry ${c.rollback.version}, the version that was installed before the last update or roll back? The setup runs again; the current version is kept so you can return to it.` +
      (c.rollback.updates ? "" : ` DXBerry ${c.rollback.version} has no Updates screen: after this rollback, updating again means flashing a new image or copying DXBerry by hand.`),
    "Roll back"),
    { cls: "danger", key: "upd:rollback", disabled: busy }));
  acts.push(btn(updates.checking ? "Checking…" : "Check now", () => updLoadCheck(true), { key: "upd:check", disabled: busy }));
  return acts;
}

function updPrerelease(c) {
  const on = !!(c.dxberry && c.dxberry.include_prereleases);
  const box = el("input", { type: "checkbox", id: "upd-prereleases", checked: on, disabled: updBusy() });
  box.addEventListener("change", () => updRun(["prereleases", box.checked ? "on" : "off"]).then(() => updLoadCheck(true),
    ex => { failure("Changing the pre-release setting", ex, UPDATE_SETTING_EXITS); updRender(); }));
  return el("label", { class: "upd-switch", for: "upd-prereleases" }, box, " Include DXBerry pre-releases");
}

function updJobBlock() {
  const out = [];
  if (updates.jobError) out.push(el("p", { class: "error" }, updates.jobError));
  const j = updates.job;
  if (!j) return out;
  const job = j.job;
  if (job && job.state === "running") out.push(el("div", { class: "notice warn" }, el("span", {}, UPDATE_RUNNING[job.kind] || "Updating…")));
  if (j.reboot_required && !(job && job.state === "running")) {
    out.push(el("div", { class: "notice warn" }, el("span", {}, "Restart the Pi to finish the updates."),
      btn("Restart the Pi", () => power("reboot.target"), { cls: "danger", key: "upd:reboot" })));
  }
  if (job && job.lines && job.lines.length) {
    const state = job.state === "running" ? "running" : job.exit === 0 ? "done" : job.exit === 8 ? "installed with a failed step"
      : job.exit === 6 ? "failed, nothing changed" : "ended without a result";
    out.push(el("details", { class: "job-log", open: updates.logOpen, ontoggle: e => { updates.logOpen = e.target.open; } },
      el("summary", {}, `Output of the last update (${job.kind}, ${state})`),
      el("pre", { class: "log" }, job.lines.join("\n"))));
  }
  return out;
}

function updRender() {
  const sec = document.getElementById("updates");
  if (!sec) return;
  const a = document.activeElement;
  const focused = a && a.dataset ? a.dataset.key : undefined;
  const c = updates.check;
  const kids = [el("h2", { id: "updates-title" }, "Updates"), ...updJobBlock()];
  if (!c) kids.push(el("p", { class: "muted" }, "Checking for updates…"));
  else if (c.failed) kids.push(el("p", { class: "error" }, c.failed), el("div", { class: "actions" }, btn("Check now", () => updLoadCheck(true), { key: "upd:check" })));
  else kids.push(kv(updRows(c)), updPrerelease(c), el("div", { class: "actions" }, updActions(c)));
  sec.replaceChildren(...kids);
  if (focused) {
    const again = [...sec.querySelectorAll("[data-key]")].find(e => e.dataset.key === focused);
    if (again) again.focus();
  }
}

function updInit() {
  updRender();
  updLoadCheck(false);
  updLoadJob();
}

updInit();
