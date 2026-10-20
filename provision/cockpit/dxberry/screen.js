// screen.js: the program tab. Opens one program's window (a VNC screen that listens only on a unix
// socket) with noVNC over Cockpit's own authenticated external channel. Runs as the logged-in user,
// without administrative access: dxberry-app screen only reads unit state and a group-readable file.
import RFB from "./novnc/core/rfb.js";

const SCR_APP = "/opt/dxberry/bin/dxberry-app";
const SCR_SESSION = /^[a-z][a-z0-9_]{0,15}-[a-z][a-z0-9]{0,11}$/;
const SCR_STATION = "/cockpit/@localhost/dxberry/index.html";
const SCR_NO_ACCESS = "This login cannot open the program's screen yet. Log out of the console and back in once, then reload this tab.";

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

function scrTransportReady() {
  return new Promise(resolve => cockpit.transport.wait(resolve));
}

function scrUrl(socket) {
  const query = btoa(JSON.stringify({ payload: "stream", binary: "raw", unix: socket }));
  return "wss://" + location.host + "/cockpit/channel/" + cockpit.transport.csrf_token + "?" + query;
}

function scrConnect(socket, run) {
  scrEl("state").textContent = "Connecting";
  scrTransportReady().then(() => {
    if (run !== scr.run) return;
    let connected = false;
    const rfb = new RFB(scrEl("screen"), scrUrl(socket), { shared: true });
    rfb.scaleViewport = false;
    rfb.resizeSession = true;
    scr.rfb = rfb;
    rfb.addEventListener("connect", () => { connected = true; scrEl("state").textContent = "Connected"; });
    rfb.addEventListener("credentialsrequired", () => { scrEl("state").textContent = "Disconnected"; scrSay("The program's screen asked for a password, which this page cannot give."); });
    rfb.addEventListener("disconnect", () => {
      if (run !== scr.run) return;
      scrEl("state").textContent = "Disconnected";
      if (!connected) scrSay(SCR_NO_ACCESS);
    });
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
    scrConnect(info.socket, run);
    return;
  }
  scrEl("state").textContent = info.state === "running" ? "No screen" : info.state;
  if (info.state === "running") scrSay(label + " on " + radio + " does not show a window here.", true);
  else if (info.state === "starting") scrSay(label + " is starting on " + radio + ". Press Reconnect in a moment.", true);
  else if (info.state === "failed") scrSay(label + " on " + radio + " stopped with an error. Start it again from the station page.", true);
  else scrSay(label + " is not running on " + radio + ". Start it from the station page.", true);
}

function scrStart() {
  scrDrop();
  scr.run += 1;
  const run = scr.run;
  scrEl("state").textContent = "Reading";
  cockpit.spawn([SCR_APP, "screen", scr.session, "--json"], { err: "message" }).then(text => {
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

scr.session = decodeURIComponent(location.hash.replace(/^#/, ""));
scrEl("reconnect").addEventListener("click", () => { if (SCR_SESSION.test(scr.session)) scrStart(); });
if (SCR_SESSION.test(scr.session)) scrStart();
else { scrEl("state").textContent = "Error"; scrSay("No program is named here."); }
