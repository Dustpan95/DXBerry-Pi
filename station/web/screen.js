// screen.js: the program tab. Opens one program's window (a VNC screen that listens only on a unix
// socket) with noVNC over the station's own authenticated WebSocket relay (/api/vnc/SESSION).
import RFB from "./vendor/novnc/core/rfb.js";

const SCR_APP = "/opt/dxberry/bin/dxberry-app";
const SCR_SESSION = /^[a-z][a-z0-9_]{0,15}-[a-z][a-z0-9]{0,11}$/;
const SCR_STATION = "/";
const SCR_NO_ACCESS = "This login cannot open the program's screen yet. Log out of the console and back in once, then reload this tab.";
const SCR_CLOSED = "The program window closed. Reconnect?";

const scrEl = id => document.getElementById(id);
const scr = { session: "", rfb: null, run: 0 };

function scrSay(text, withLink) {
  const p = document.createElement("p");
  p.className = "screen-note";
  p.append(text);
  if (withLink) {
    const a = document.createElement("a");
    a.setAttribute("href", SCR_STATION);
    a.append("Go to the station page");
    p.append(" ", a);
  }
  scrEl("screen").replaceChildren(p);
}

function scrDrop() {
  if (scr.rfb) {
    try { scr.rfb.disconnect(); } catch (e) { /* already gone */ }
    scr.rfb = null;
  }
}

function scrChannel() {
  if (!SCR_SESSION.test(scr.session)) throw new Error("not a program window");
  return dxb.ws("/api/vnc/" + scr.session);
}

function scrConnect(run) {
  scrEl("state").textContent = "Connecting";
  if (run !== scr.run) return;
  let connected = false;
  const rfb = new RFB(scrEl("screen"), scrChannel(), { shared: true });
  rfb.scaleViewport = false;
  rfb.resizeSession = true;
  scr.rfb = rfb;
  rfb.addEventListener("connect", () => { connected = true; scrEl("state").textContent = "Connected"; });
  rfb.addEventListener("credentialsrequired", () => { scrEl("state").textContent = "Disconnected"; scrSay("The program's screen asked for a password, which this page cannot give."); });
  rfb.addEventListener("disconnect", () => {
    if (run !== scr.run) return;
    scrEl("state").textContent = "Disconnected";
    if (!connected) scrRecheck(run);
  });
}

// scrRecheck RUN: the window closed before it ever showed. Only a login without access to program
// screens is told to log out and in; otherwise the window simply closed (the program ended, or the
// screen was not ready yet).
function scrRecheck(run) {
  dxb.run([SCR_APP, "screen", scr.session, "--json"]).then(text => {
    if (run !== scr.run) return;
    let info = null;
    try { info = JSON.parse(text); } catch (e) { info = null; }
    scrSay(info && info.access === false ? SCR_NO_ACCESS : SCR_CLOSED);
  }, () => {
    if (run !== scr.run) return;
    scrSay(SCR_CLOSED);
  });
}

function scrShow(info, run) {
  const label = info.label || info.app || scr.session;
  const radio = info.radio_label || info.radio || "";
  scrEl("program").textContent = label;
  scrEl("radio").textContent = radio;
  document.title = label + " · " + radio;
  if (info.state === "running" && info.socket) {
    scrEl("screen").replaceChildren();
    scrConnect(run);
    return;
  }
  scrEl("state").textContent = info.state === "running" ? "No screen" : info.state;
  if (info.state === "running") scrSay(label + " on " + radio + " does not show a window here.", true);
  else if (info.state === "starting") scrSay(label + " is starting on " + radio + ". Press Reconnect in a moment.", true);
  else if (info.state === "failed") scrSay(label + " on " + radio + " stopped with an error. Start it again from the station page.", true);
  else scrSay(label + " is not running on " + radio + ". Start it from the station page.", true);
}

function scrStart() {
  // a new try first, so whatever the old screen says as it closes belongs to the old one
  scr.run += 1;
  const run = scr.run;
  scrDrop();
  scrEl("state").textContent = "Reading";
  dxb.run([SCR_APP, "screen", scr.session, "--json"]).then(text => {
    if (run !== scr.run) return;
    let info;
    try { info = JSON.parse(text); } catch (e) { scrEl("state").textContent = "Error"; scrSay("The station gave an answer this page cannot read."); return; }
    if (info.access === false) { scrEl("state").textContent = "No access"; scrSay(SCR_NO_ACCESS); return; }
    scrShow(info, run);
  }, err => {
    if (run !== scr.run) return;
    scrEl("state").textContent = "Error";
    if (err && err.exit_status === 3) scrSay("No program is named here.");
    else scrSay("Could not read the program: " + ((err && (err.message || err.problem)) || "unknown error"), true);
  });
}

let scrLinkOk = true;
try { scr.session = decodeURIComponent(location.hash.replace(/^#/, "")); } catch (e) { scr.session = ""; scrLinkOk = false; }
scrEl("reconnect").addEventListener("click", () => { if (SCR_SESSION.test(scr.session)) scrStart(); });
if (SCR_SESSION.test(scr.session)) scrStart();
else { scrEl("state").textContent = "Error"; scrSay(scrLinkOk ? "No program is named here." : "This link is not a program window.", !scrLinkOk); }
