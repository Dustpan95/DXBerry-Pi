/* DXBerry page, settings (console spec section 10): the Pi's own settings in dxberry.txt, the
 * progress of a settings change, and the network safety net. Loaded after dxberry.js and shares its
 * global scope, so every top-level name here starts with cfg or CONFIG (plus the one state object,
 * settings). Every reading and change is dxberry-config, run as superuser through run(). */
"use strict";

const CONFIG = "/opt/dxberry/bin/dxberry-config";
// dxberry-config's exit codes in words (console spec section 10.2).
const CONFIG_EXITS = {
  1: "this needs administrative access",
  2: "the request was not valid",
  4: "the settings would not be valid, so nothing was written",
  5: "a settings change is still being applied, or the last network change waits to be kept or undone",
  6: "writing the settings failed, so nothing was changed",
  8: "the settings were written, but the setup run reported failed steps",
};
const CONFIG_POLL_MS = 2000;
const CONFIG_GPS_BAUDS = ["4800", "9600", "19200", "38400", "57600", "115200"];

/* config-fields-begin: what the page reads from dxberry-config's --json answers, as COMMAND:PATH.
 * tests/test_page.sh checks each one against the command's real output. */
const CONFIG_FIELDS = [
  "get:valid", "get:errors", "get:network_editable", "get:keys.HOSTNAME.value", "get:keys.HOSTNAME.effective", "get:keys.STATIC_IP.value",
  "get:keys.STATIC_IP.effective", "get:keys.GATEWAY.value", "get:keys.GATEWAY.effective", "get:keys.DNS.value",
  "get:keys.DNS.effective", "get:keys.WIFI_SSID.value", "get:keys.WIFI_PASSWORD.set", "get:keys.WIFI_COUNTRY.value",
  "get:keys.TIMEZONE.value", "get:keys.TIMEZONE.effective", "get:keys.PASSWORD.set", "get:keys.SSH_PUBKEY.value",
  "get:keys.GPS_DEVICE.value", "get:keys.GPS_DEVICE.effective", "get:keys.GPS_BAUD.value", "get:keys.GPS_BAUD.effective",
  "get:keys.GPS_PPS.value", "get:keys.POSITION_LOG.value", "get:keys.POSITION_LOG.effective", "get:keys.CONSOLE.value",
  "get:keys.CONSOLE.effective",
  "set:changed", "set:job", "set:network",
  "job:now", "job:pending", "job:revert_at", "job:reverted_at", "job:reboot_required",
  "job:job.unit", "job:job.state", "job:job.exit", "job:job.changed", "job:job.network", "job:job.lines",
];
/* config-fields-end */

// values: dxberry-config get's answer (or {failed: text}); job: dxberry-config job's answer and when
// it arrived (fetchedAt, for the countdown); form: the open dialog; logOpen: the output disclosure.
const settings = { values: null, job: null, fetchedAt: 0, form: null, poll: null, logOpen: false, undoing: false };

function cfgRun(args, input) {
  return run([CONFIG, ...args, "--json"], input);
}

function cfgParse(out) {
  try { return JSON.parse(out); } catch (e) { return null; }
}

function cfgLoadValues() {
  cfgRun(["get"]).then(out => {
    settings.values = cfgParse(out) || { failed: "dxberry-config did not return the settings." };
    cfgRender();
  }, ex => {
    settings.values = { failed: problemText(ex, CONFIG_EXITS) };
    cfgRender();
  });
}

// cfgLoadJob: the last settings change and the undo; polls while the change runs.
function cfgLoadJob() {
  clearTimeout(settings.poll);
  cfgRun(["job"]).then(out => {
    const j = cfgParse(out);
    if (!j) return;
    const wasRunning = !!(settings.job && settings.job.job && settings.job.job.state === "running");
    settings.job = j;
    settings.fetchedAt = Date.now();
    settings.undoing = false;
    cfgRender();
    const running = !!(j.job && j.job.state === "running");
    if (running) settings.poll = setTimeout(cfgLoadJob, CONFIG_POLL_MS);
    else if (wasRunning) cfgFinished(j);
  }, () => { settings.poll = setTimeout(cfgLoadJob, CONFIG_POLL_MS * 5); });
}

function cfgFinished(j) {
  const job = j.job;
  if (job && job.exit === 0) notice(j.pending ? "Network settings applied. Keep them below, or they are undone." : "Settings applied.", "good");
  else if (job && job.exit === 8) notice("The settings were written, but the setup run reported failed steps; see its output under Settings.", "bad");
  else notice("The settings change ended without a result; see its output under Settings.", "bad");
  cfgLoadValues();
}

function cfgBusy() {
  const j = settings.job;
  return !!(j && (j.pending || (j.job && j.job.state === "running")));
}

function cfgShown(keys, k) {
  const x = keys[k];
  if (!x) return "";
  if (x.secret) return x.set ? "set" : "not set";
  return x.effective || x.value || "";
}

function cfgNetworkRows(keys) {
  const ip = cfgShown(keys, "STATIC_IP");
  const ssid = cfgShown(keys, "WIFI_SSID");
  return [
    ["Address", ip ? `${ip} (fixed)` : "from your router (DHCP)"],
    ["Gateway", ip ? cfgShown(keys, "GATEWAY") || DASH : DASH],
    ["DNS", ip ? cfgShown(keys, "DNS") || DASH : DASH],
    ["WiFi", ssid ? `${ssid} (${cfgShown(keys, "WIFI_COUNTRY") || "no country"}), password ${cfgShown(keys, "WIFI_PASSWORD")}` : "off"],
  ];
}

function cfgSystemRows(keys) {
  const gps = cfgShown(keys, "GPS_DEVICE");
  const key = cfgShown(keys, "SSH_PUBKEY").split(" ");
  const pps = cfgShown(keys, "GPS_PPS");
  return [
    ["Hostname", cfgShown(keys, "HOSTNAME")],
    ["Time zone", cfgShown(keys, "TIMEZONE")],
    ["Login password", cfgShown(keys, "PASSWORD")],
    ["SSH key", key[0] ? [key[0], key.slice(2).join(" ")].filter(Boolean).join(" … ") : "none"],
    ["GPS", gps === "none" ? "off" : `${gps}, ${cfgShown(keys, "GPS_BAUD")} baud${pps ? `, PPS on GPIO ${pps}` : ""}`],
    ["Position log", cfgShown(keys, "POSITION_LOG") === "on" ? "on, in RAM" : "off"],
    ["Console", cfgShown(keys, "CONSOLE")],
  ];
}

function cfgSecondsLeft(j) {
  if (!j || typeof j.revert_at !== "number") return null;
  return Math.round(j.revert_at - j.now - (Date.now() - settings.fetchedAt) / 1000);
}

function cfgCountdownText(left) {
  if (left === null) return "It is undone at the next restart unless you keep it.";
  if (left <= 0) return "Undoing it now…";
  return `It undoes itself in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")} unless you keep it.`;
}

// cfgNewAddress: where to keep the change from, when the network change moves the Pi.
function cfgNewAddress() {
  const v = settings.values;
  if (!v || !v.keys) return null;
  const ip = (cfgShown(v.keys, "STATIC_IP").split("/")[0]) || "";
  if (ip && ip !== location.hostname) {
    return [`The Pi's address is now ${ip}. Open `, el("a", { href: `https://${ip}/` }, `https://${ip}/`), ", log in, and keep the settings from there."];
  }
  if (!ip && /^[0-9.]+$/.test(location.hostname)) {
    return ["The Pi now gets its address from your router. Find it in the router's list of devices, open it, log in, and keep the settings from there."];
  }
  return null;
}

function cfgKeepBanner(j) {
  const addr = cfgNewAddress();
  return el("div", { class: "notice warn keep" },
    el("div", {},
      el("strong", {}, "Keep these network settings?"), " ",
      el("span", { id: "cfg-countdown" }, cfgCountdownText(cfgSecondsLeft(j))),
      addr ? el("p", {}, addr) : null),
    el("div", { class: "actions" },
      btn("Keep these settings", cfgConfirm, { cls: "primary", key: "cfg:keep" }),
      btn("Undo now", cfgUndo, { key: "cfg:undo" })));
}

function cfgJobState(job) {
  if (job.state === "running") return "running";
  if (job.exit === 0) return "done";
  if (job.exit === 8) return "finished with failed steps";
  return "ended without a result";
}

function cfgJobBlock() {
  const j = settings.job;
  if (!j) return [];
  const job = j.job;
  const running = !!(job && job.state === "running");
  const out = [];
  if (running) out.push(el("div", { class: "notice warn" }, el("span", {}, `Applying ${job.changed.join(", ")}…`)));
  if (j.pending && !running) out.push(cfgKeepBanner(j));
  if (j.reboot_required && !running) {
    out.push(el("div", { class: "notice warn" }, el("span", {}, "Restart the Pi to finish this change."),
      btn("Restart the Pi", () => power("reboot.target"), { cls: "danger", key: "cfg:reboot" })));
  }
  if (typeof j.reverted_at === "number" && j.now - j.reverted_at < 3600) {
    out.push(el("p", { class: "muted" }, `The last network change was undone at ${new Date(j.reverted_at * 1000).toLocaleTimeString("en-US")}.`));
  }
  if (job && job.lines && job.lines.length) {
    out.push(el("details", { class: "job-log", open: settings.logOpen, ontoggle: e => { settings.logOpen = e.target.open; } },
      el("summary", {}, `Output of the last settings change (${cfgJobState(job)})`),
      el("pre", { class: "log" }, job.lines.join("\n"))));
  }
  return out;
}

function cfgRender() {
  const sec = document.getElementById("settings");
  if (!sec) return;
  const a = document.activeElement;
  const focused = a && a.dataset ? a.dataset.key : undefined;
  const v = settings.values;
  const kids = [el("h2", { id: "settings-title" }, "Settings"), ...cfgJobBlock()];
  if (!v) {
    kids.push(el("p", { class: "muted" }, "Reading the settings…"));
  } else if (v.failed) {
    kids.push(el("p", { class: "error" }, v.failed));
  } else {
    if (!v.valid) kids.push(el("p", { class: "error" }, "dxberry.txt has errors: " + v.errors.join(" ")));
    const busy = cfgBusy();
    const why = busy ? "Wait for the change above to finish, or keep or undo it" : null;
    // network settings from the console wait for a tested live re-apply (console spec 10.3):
    // dxberry-config get says whether set takes them
    const netOk = v.network_editable === true;
    kids.push(el("h3", {}, "Network"), kv(cfgNetworkRows(v.keys)));
    if (!netOk) {
      kids.push(el("p", { class: "muted" }, "Network settings change in dxberry.txt for now: edit it, run sudo dxberry-provision, then restart the Pi."));
    }
    kids.push(el("h3", {}, "System"), kv(cfgSystemRows(v.keys)),
      el("div", { class: "actions" },
        netOk ? btn("Change network settings", () => cfgOpen("network"), { key: "cfg:network", disabled: busy, title: why }) : null,
        btn("Change system settings", () => cfgOpen("system"), { key: "cfg:system", disabled: busy, title: why }),
        el("a", { class: "btn small link", href: `http://${location.hostname}:8080/`, target: "_blank", rel: "noopener noreferrer", "data-key": "cfg:graywolf" },
          "Station, beacon and iGate settings: Graywolf's page")));
  }
  sec.replaceChildren(...kids);
  if (focused) {
    const again = [...sec.querySelectorAll("[data-key]")].find(e => e.dataset.key === focused);
    if (again) again.focus();
  }
}

// cfgTick: once a second, only the countdown text changes (a full redraw would steal focus).
function cfgTick() {
  const j = settings.job;
  if (!j || !j.pending) return;
  const left = cfgSecondsLeft(j);
  const span = document.getElementById("cfg-countdown");
  if (span) span.textContent = cfgCountdownText(left);
  if (left !== null && left <= 0 && !settings.undoing) {
    settings.undoing = true;
    settings.poll = setTimeout(cfgLoadJob, 5000);
  }
}

// cfgConfirm: keep the waiting network change. "kept" tells whether there was one to keep -
// confirm --json can answer {kept:false} when the undo already ran (or another tab beat it).
function cfgConfirm() {
  cfgRun(["confirm"]).then(out => {
    const r = cfgParse(out) || {};
    if (r.kept) notice("Network settings kept.", "good");
    else notice("No network change was waiting; it may already have been undone.", "warn");
    cfgLoadJob();
  }, ex => failure("Keeping the network settings", ex, CONFIG_EXITS));
}

function cfgUndo() {
  confirmThen("Undo the network change?",
    "The Pi goes back to its previous network settings now. If its address changes back, reconnect at the old address.", "Undo now",
    () => cfgRun(["revert"]).then(() => { notice("Network change undone.", "good"); cfgLoadJob(); cfgLoadValues(); },
      ex => failure("Undoing the network change", ex, CONFIG_EXITS)));
}

// ---- the settings dialogs ------------------------------------------------------------------
function cfgInput(id, value, attrs) {
  return el("input", Object.assign({ id, type: "text", value: value || "", autocomplete: "off", spellcheck: "false" }, attrs || {}));
}

function cfgNetworkFields(keys) {
  const raw = k => (keys[k] && !keys[k].secret ? keys[k].value : "");
  return [
    field("Address", "cf-mode", choice("cf-mode", [["dhcp", "From my router (DHCP)"], ["static", "Fixed address"]], raw("STATIC_IP") ? "static" : "dhcp")),
    field("Fixed address", "cf-STATIC_IP", cfgInput("cf-STATIC_IP", raw("STATIC_IP"), { placeholder: "192.168.1.90/24" }),
      "With its prefix length; /24 when left out."),
    field("Gateway", "cf-GATEWAY", cfgInput("cf-GATEWAY", raw("GATEWAY"), { placeholder: "192.168.1.1" })),
    field("DNS servers", "cf-DNS", cfgInput("cf-DNS", raw("DNS"), { placeholder: keys.DNS ? keys.DNS.effective : "" }),
      "Space-separated; the gateway when empty."),
    field("WiFi network (SSID)", "cf-WIFI_SSID", cfgInput("cf-WIFI_SSID", raw("WIFI_SSID")), "Empty turns WiFi off."),
    field("WiFi password", "cf-WIFI_PASSWORD", cfgInput("cf-WIFI_PASSWORD", "",
      { type: "password", autocomplete: "new-password", placeholder: keys.WIFI_PASSWORD && keys.WIFI_PASSWORD.set ? "Unchanged" : "" })),
    field("WiFi country", "cf-WIFI_COUNTRY", cfgInput("cf-WIFI_COUNTRY", raw("WIFI_COUNTRY"), { maxlength: "2", placeholder: "US" }),
      "Two letters, for the radio rules of your country."),
    el("p", { class: "hint muted" }, "Saving applies the change, then undoes it two minutes later unless you click Keep these settings — from the new address if the Pi's address changes."),
  ];
}

function cfgSystemFields(keys) {
  const raw = k => (keys[k] && !keys[k].secret ? keys[k].value : "");
  const eff = k => (keys[k] ? keys[k].effective : "");
  const dev = raw("GPS_DEVICE");
  const isPath = dev.startsWith("/dev/");
  const dflt = (k, choices) => [["", `Default (${eff(k)})`]].concat(choices);
  return [
    field("Hostname", "cf-HOSTNAME", cfgInput("cf-HOSTNAME", raw("HOSTNAME"), { placeholder: eff("HOSTNAME") })),
    field("Time zone", "cf-TIMEZONE", cfgInput("cf-TIMEZONE", raw("TIMEZONE"), { placeholder: eff("TIMEZONE") }), "For example America/Chicago."),
    field("New login password", "cf-PASSWORD", cfgInput("cf-PASSWORD", "", { type: "password", autocomplete: "new-password", placeholder: "Unchanged" }),
      "Also the console login (dietpi) and root's password."),
    field("New login password again", "cf-PASSWORD2", cfgInput("cf-PASSWORD2", "", { type: "password", autocomplete: "new-password" })),
    field("SSH public key", "cf-SSH_PUBKEY", cfgInput("cf-SSH_PUBKEY", raw("SSH_PUBKEY"), { placeholder: "ssh-ed25519 AAAA… you@laptop" }),
      "Added for root and dietpi. Removing it here does not remove it from the Pi."),
    field("GPS", "cf-GPS_DEVICE", choice("cf-GPS_DEVICE", dflt("GPS_DEVICE", [["auto", "Find it (USB or serial pins)"], ["none", "No GPS"],
      ["uart", "The Pi's serial pins"], ["path", "A device path…"]]), isPath ? "path" : dev)),
    field("GPS device path", "cf-GPS_PATH", cfgInput("cf-GPS_PATH", isPath ? dev : "", { placeholder: "/dev/ttyACM0" })),
    field("GPS baud rate", "cf-GPS_BAUD", choice("cf-GPS_BAUD", dflt("GPS_BAUD", CONFIG_GPS_BAUDS.map(b => [b, b])), raw("GPS_BAUD"))),
    field("GPS PPS pin", "cf-GPS_PPS", cfgInput("cf-GPS_PPS", raw("GPS_PPS"), { placeholder: "none" }), "The BCM GPIO number of a 1PPS signal; empty for none."),
    field("Position log", "cf-POSITION_LOG", choice("cf-POSITION_LOG", dflt("POSITION_LOG", [["on", "On, in RAM"], ["off", "Off"]]), raw("POSITION_LOG"))),
    field("Console", "cf-CONSOLE", choice("cf-CONSOLE", dflt("CONSOLE", [["on", "On"], ["off", "Off"]]), raw("CONSOLE")), "Off turns this console off."),
  ];
}

function cfgSync() {
  const hide = (id, yes) => { const e = document.getElementById(id); if (e) e.closest(".field").hidden = yes; };
  const mode = document.getElementById("cf-mode");
  if (mode) ["cf-STATIC_IP", "cf-GATEWAY", "cf-DNS"].forEach(id => hide(id, mode.value !== "static"));
  const gps = document.getElementById("cf-GPS_DEVICE");
  if (gps) hide("cf-GPS_PATH", gps.value !== "path");
}

function cfgSetError(text) {
  const p = document.getElementById("settings-form-error");
  p.textContent = text;
  p.hidden = !text;
}

function cfgSetSaving(on) {
  const b = document.getElementById("settings-save");
  b.disabled = on;
  b.textContent = on ? "Saving…" : "Save";
}

function cfgOpen(section) {
  const v = settings.values;
  if (!v || v.failed || cfgBusy()) return;
  if (section === "network" && v.network_editable !== true) return;
  settings.form = { section, saving: false };
  document.getElementById("settings-dialog-title").textContent = section === "network" ? "Network settings" : "System settings";
  cfgSetError("");
  cfgSetSaving(false);
  document.getElementById("settings-fields").replaceChildren(...(section === "network" ? cfgNetworkFields(v.keys) : cfgSystemFields(v.keys)));
  ["cf-mode", "cf-GPS_DEVICE"].forEach(id => { const e = document.getElementById(id); if (e) e.addEventListener("change", cfgSync); });
  cfgSync();
  document.getElementById("settings-dialog").showModal();
}

// cfgFormArgs: dxberry-config set's arguments for the open dialog - only what changed, passwords on
// stdin (with --stdin) - or {error}. warn lists what the operator must hear before saving.
function cfgFormArgs() {
  const keys = settings.values.keys;
  const v = id => { const e = document.getElementById(id); return e ? e.value.trim() : ""; };
  const raw = id => { const e = document.getElementById(id); return e ? e.value : ""; };
  const was = k => (keys[k] && !keys[k].secret ? keys[k].value : "");
  const args = [], secrets = [], warn = [];
  const put = (k, value) => { if (value !== was(k)) args.push(`${k}=${value}`); };
  if (settings.form.section === "network") {
    if (v("cf-mode") === "static") {
      if (!v("cf-STATIC_IP")) return { error: "Give the fixed address, for example 192.168.1.90/24." };
      put("STATIC_IP", v("cf-STATIC_IP"));
      put("GATEWAY", v("cf-GATEWAY"));
      put("DNS", v("cf-DNS"));
    } else {
      put("STATIC_IP", "");
      put("GATEWAY", "");
      put("DNS", "");
    }
    // a WiFi password with no network is never used, so it would stay unscrubbed in dxberry.txt
    if (raw("cf-WIFI_PASSWORD") && !v("cf-WIFI_SSID")) return { error: "A WiFi password needs a WiFi network name (SSID)." };
    put("WIFI_SSID", v("cf-WIFI_SSID"));
    put("WIFI_COUNTRY", v("cf-WIFI_COUNTRY").toUpperCase());
    if (raw("cf-WIFI_PASSWORD")) secrets.push(`WIFI_PASSWORD=${raw("cf-WIFI_PASSWORD")}`);
  } else {
    put("HOSTNAME", v("cf-HOSTNAME"));
    put("TIMEZONE", v("cf-TIMEZONE"));
    if (raw("cf-PASSWORD") || raw("cf-PASSWORD2")) {
      if (raw("cf-PASSWORD") !== raw("cf-PASSWORD2")) return { error: "The two passwords differ." };
      secrets.push(`PASSWORD=${raw("cf-PASSWORD")}`);
      warn.push("The new password is also the console login (dietpi) and root's password.");
    }
    put("SSH_PUBKEY", v("cf-SSH_PUBKEY"));
    put("GPS_DEVICE", v("cf-GPS_DEVICE") === "path" ? v("cf-GPS_PATH") : v("cf-GPS_DEVICE"));
    put("GPS_BAUD", v("cf-GPS_BAUD"));
    put("GPS_PPS", v("cf-GPS_PPS"));
    put("POSITION_LOG", v("cf-POSITION_LOG"));
    put("CONSOLE", v("cf-CONSOLE"));
    if (v("cf-CONSOLE") === "off" && was("CONSOLE") !== "off") {
      warn.push("CONSOLE=off turns this console off as soon as the change is applied. Turn it back on over SSH: sudo dxberry-config set CONSOLE=on.");
    }
  }
  if (secrets.some(s => /[\u0000-\u001f\u007f]/.test(s))) return { error: "A password cannot contain a line break or another control character." };
  // dxberry.txt's parser trims spaces around a value and strips wrapping double quotes
  const changedByFile = s => { const p = s.slice(s.indexOf("=") + 1); return /^\s|\s$/.test(p) || (p.length >= 2 && p.startsWith('"') && p.endsWith('"')); };
  if (secrets.some(changedByFile)) return { error: "A password cannot start or end with a space or be wrapped in double quotes (dxberry.txt would change it)." };
  if (!args.length && !secrets.length) return { args: null, stdin: "", warn };
  return { args: ["set", ...args, ...(secrets.length ? ["--stdin"] : [])], stdin: secrets.map(s => s + "\n").join(""), warn };
}

function cfgSave(e) {
  e.preventDefault();
  const f = settings.form;
  if (!f || f.saving) return;
  const d = document.getElementById("settings-dialog");
  const r = cfgFormArgs();
  if (r.error) { cfgSetError(r.error); return; }
  if (!r.args) { settings.form = null; d.close(); notice("Nothing changed.", "good"); return; }
  const go = () => {
    f.saving = true;
    cfgSetError("");
    cfgSetSaving(true);
    cfgRun(r.args, r.stdin).then(out => {
      f.saving = false;
      if (settings.form === f) { cfgSetSaving(false); settings.form = null; if (d.open) d.close(); }
      const res = cfgParse(out) || {};
      notice(res.job ? "Saved. Applying the change…" : "Nothing changed.", "good");
      cfgLoadJob();
      cfgLoadValues();
    }, ex => {
      f.saving = false;
      if (settings.form === f && d.open) { cfgSetSaving(false); cfgSetError(problemText(ex, CONFIG_EXITS)); }
      else failure("Saving the settings", ex, CONFIG_EXITS);
    });
  };
  if (r.warn.length) confirmThen("Save these settings?", r.warn.join(" "), "Save", go);
  else go();
}

function cfgInit() {
  document.getElementById("settings-form").addEventListener("submit", cfgSave);
  document.getElementById("settings-cancel").addEventListener("click", () => {
    settings.form = null;
    document.getElementById("settings-dialog").close();
  });
  setInterval(cfgTick, 1000);
  cfgRender();
  cfgLoadValues();
  cfgLoadJob();
}

cfgInit();
