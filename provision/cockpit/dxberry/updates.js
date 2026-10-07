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
const UPDATE_POLL_MS = 2000;

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

// check: dxberry-update check's answer (or {failed: text}); job: dxberry-update job's answer.
const updates = { check: null, job: null, poll: null, logOpen: false, checking: false };

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

function updLoadJob() {
  clearTimeout(updates.poll);
  updRun(["job"]).then(out => {
    const j = updParse(out);
    if (!j) return;
    const wasRunning = !!(updates.job && updates.job.job && updates.job.job.state === "running");
    updates.job = j;
    updRender();
    if (j.job && j.job.state === "running") updates.poll = setTimeout(updLoadJob, UPDATE_POLL_MS);
    else if (wasRunning) updFinished(j.job);
  }, () => { updates.poll = setTimeout(updLoadJob, UPDATE_POLL_MS * 5); });
}

function updFinished(job) {
  const what = { graywolf: "Graywolf update", dxberry: "DXBerry update", rollback: "Rollback", system: "System update" }[job.kind] || "Update";
  if (job.exit === 0) notice(`${what} finished.`, "good");
  else if (job.exit === 8) notice(`${what}: installed, but a later step failed; see its output under Updates.`, "bad");
  else if (job.exit === 6) notice(`${what} failed; nothing was changed. See its output under Updates.`, "bad");
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
    : `${d.installed}, the newest${d.include_prereleases ? "" : " full release"}`]);
  const s = c.system || {};
  rows.push(["System", s.error ? s.error : s.count ? [badge("update", "warn"), ` ${s.count} package${s.count === 1 ? "" : "s"} to upgrade`] : "up to date"]);
  if (c.rollback && c.rollback.available) rows.push(["Rollback", `DXBerry ${c.rollback.version} is kept`]);
  rows.push(["Checked", c.checked_at ? new Date(c.checked_at * 1000).toLocaleString("en-US") + (c.cached ? " (from the last check)" : "") : DASH]);
  return rows;
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
  if (c.rollback && c.rollback.available) acts.push(btn(`Roll back to ${c.rollback.version}`, () => updStart("rollback", "Roll back DXBerry?",
    `DXBerry goes back to ${c.rollback.version} and the setup runs again; the current version is kept so you can return to it.` +
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
    ex => { failure("Changing the pre-release setting", ex, UPDATE_EXITS); updRender(); }));
  return el("label", { class: "upd-switch", for: "upd-prereleases" }, box, " Include DXBerry pre-releases");
}

function updJobBlock() {
  const j = updates.job;
  if (!j) return [];
  const out = [];
  const job = j.job;
  if (job && job.state === "running") out.push(el("div", { class: "notice warn" }, el("span", {}, `Running: ${job.kind} update…`)));
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
