// screen.js: the program tab. Opens one program's window (a VNC screen that listens only on a unix
// socket) with noVNC over the station's own authenticated WebSocket relay (/api/vnc/SESSION).
import RFB from "./vendor/novnc/core/rfb.js";

import keysyms from "./vendor/novnc/core/input/keysymdef.js";
import { releaseCapture } from "./vendor/novnc/core/util/events.js";

const SCR_APP = "/opt/dxberry/bin/dxberry-app";
const SCR_SESSION = /^[a-z][a-z0-9_]{0,15}-[a-z][a-z0-9]{0,11}$/;
const SCR_STATION = "/";
const SCR_NO_ACCESS = "This login cannot open the program's screen yet. Log out of the console and back in once, then reload this tab.";
const SCR_CLOSED = "The program window closed. Reconnect?";

const scrEl = id => document.getElementById(id);
const scr = { session: "", rfb: null, run: 0 };

const mobile = matchMedia("(any-pointer: coarse), (max-width: 700px)").matches;
const pad = scrEl("trackpad");
// Keep noVNC's display and its inverse pointer-coordinate mapping at the same scale.
class ViewerRFB extends RFB {
  _updateScale() {
    if (!this.manualScale) return super._updateScale();
    this._display.scale = this.manualScale;
    this._fixScrollbars();
  }
}
let zoomScale = null;
let scrollPosition = null;
let pinch = null;
let waitForLift = false;
let active = false;
let gesture = null;
const position = { x: 0.5, y: 0.5 }; // Fractions of the framebuffer, preserved across rotation.

function surface() { return scrEl("screen").querySelector("canvas"); }

function cursorPoint() {
  const rect = surface().getBoundingClientRect();
  return { clientX: rect.left + position.x * rect.width, clientY: rect.top + position.y * rect.height };
}

function drawPointer() {
  const marker = scrEl("pointer");
  marker.hidden = !active || !pad.checked || !surface();
  if (marker.hidden) return;
  const origin = scrEl("screen-area").getBoundingClientRect();
  const point = cursorPoint();
  marker.style.left = (point.clientX - origin.left) + "px";
  marker.style.top = (point.clientY - origin.top) + "px";
}

function mouse(type, buttons) {
  surface().dispatchEvent(new MouseEvent(type, { ...cursorPoint(), bubbles: true, cancelable: true, button: 0, buttons }));
}

function endGesture() {
  if (!gesture) return;
  clearTimeout(gesture.timer);
  if (gesture.down) {
    if (surface()) mouse("mouseup", 0);
    // Synthetic mouseup goes to the canvas, bypassing noVNC's window capture proxy.
    releaseCapture();
  }
  gesture = null;
}

function controls(on) {
  endGesture();
  active = on;
  pinch = null;
  waitForLift = false;
  scrEl("keyboard").hidden = !on || !mobile;
  if (!on) closeKeyboard();
  scrEl("zoom").hidden = !on || !mobile;
  scrEl("pad-label").hidden = !on || !mobile;
  scrEl("pad-help").hidden = !on || !pad.checked;
  scrEl("screen-area").classList.toggle("trackpad", on && pad.checked);
  drawPointer();
}

function followPointer() {
  const canvas = surface();
  if (!active || !canvas) return;
  const view = canvas.parentElement;
  const bounds = view.getBoundingClientRect(), point = cursorPoint();
  if (point.clientX < bounds.left || point.clientX >= bounds.right)
    view.scrollLeft += point.clientX - (bounds.left + bounds.width / 2);
  if (point.clientY < bounds.top || point.clientY >= bounds.bottom)
    view.scrollTop += point.clientY - (bounds.top + bounds.height / 2);
  drawPointer();
}

function scaleAt(scale, x, y, fx, fy) {
  const canvas = surface(), view = canvas.parentElement;
  const fit = Math.min(view.clientWidth / canvas.width, view.clientHeight / canvas.height);
  zoomScale = scale <= fit + 0.001 ? null : Math.min(Math.max(4, fit), scale);
  scr.rfb.manualScale = zoomScale;
  scr.rfb.scaleViewport = zoomScale === null;
  const rect = canvas.getBoundingClientRect();
  view.scrollLeft += rect.left + fx * rect.width - x;
  view.scrollTop += rect.top + fy * rect.height - y;
  scrEl("zoom-fit").textContent = zoomScale ? Math.round(zoomScale * 100) + "%" : "Fit";
  drawPointer();
}

function zoomStep(factor) {
  if (!active || !surface()) return;
  const canvas = surface(), rect = canvas.getBoundingClientRect();
  const view = canvas.parentElement.getBoundingClientRect();
  const x = view.left + view.width / 2, y = view.top + view.height / 2;
  scaleAt(rect.width / canvas.width * factor, x, y, (x - rect.left) / rect.width, (y - rect.top) / rect.height);
}
scrEl("zoom-in").addEventListener("click", () => zoomStep(1.5));
scrEl("zoom-out").addEventListener("click", () => zoomStep(1 / 1.5));
scrEl("zoom-fit").addEventListener("click", () => zoomStep(0));

function trackTouch(e) {
  const canvas = surface();
  if (!active || !mobile || !canvas) return;
  e.stopPropagation();
  e.preventDefault();
  const touches = e.touches, rect = canvas.getBoundingClientRect();
  if (!rect.width || !rect.height) return;
  if (touches.length >= 2 && e.type !== "touchcancel") {
    endGesture();
    waitForLift = true;
    const [a,b] = touches;
    const x = (a.clientX + b.clientX) / 2, y = (a.clientY + b.clientY) / 2;
    const distance = Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
    if (!pinch || e.type === "touchstart" || e.type === "touchend") {
      pinch = { distance: Math.max(1, distance), scale: rect.width / canvas.width,
        fx: (x - rect.left) / rect.width, fy: (y - rect.top) / rect.height };
    } else {
      scaleAt(pinch.scale * distance / pinch.distance, x, y, pinch.fx, pinch.fy);
    }
    return;
  }
  if (waitForLift || e.type === "touchcancel") {
    endGesture();
    pinch = null;
    waitForLift = touches.length > 0;
    return;
  }
  const t = touches[0];
  const clamp = (v, size) => Math.max(0, Math.min(1 - 1 / size, v));
  if (e.type === "touchstart") {
    if (!pad.checked) {
      position.x = clamp((t.clientX - rect.left) / rect.width, canvas.width);
      position.y = clamp((t.clientY - rect.top) / rect.height, canvas.height);
      mouse("mousemove", 0);
    }
    gesture = { id: t.identifier, x: t.clientX, y: t.clientY, travel: 0, down: false };
    gesture.timer = setTimeout(() => { gesture.down = true; mouse("mousedown", 1); }, 450);
    return;
  }
  if (!gesture) return;
  if (e.type === "touchend") {
    if (!gesture.down && gesture.travel < 5) { mouse("mousedown", 1); gesture.down = true; }
    endGesture();
    return;
  }
  if (!t || t.identifier !== gesture.id) return;
  const dx = t.clientX - gesture.x, dy = t.clientY - gesture.y;
  gesture.travel += Math.hypot(dx, dy);
  if (gesture.travel >= 5) clearTimeout(gesture.timer);
  gesture.x = t.clientX; gesture.y = t.clientY;
  position.x = clamp(position.x + dx / rect.width, canvas.width);
  position.y = clamp(position.y + dy / rect.height, canvas.height);
  if (pad.checked) followPointer();
  mouse("mousemove", gesture.down ? 1 : 0);
}

pad.checked = mobile;
pad.addEventListener("change", () => controls(active));

const keyInput = scrEl("key-input");
let compositionCommit = null;
const anchor = "\u200b"; // iOS emits backward deletion only if there is text before the caret.
const specialKeys = { Backspace: 0xff08, Delete: 0xffff, Enter: 0xff0d, Tab: 0xff09, Escape: 0xff1b,
  ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54 };
function resetInput() {
  keyInput.value = anchor;
  keyInput.setSelectionRange(1, 1);
}
function closeKeyboard() {
  clearTimeout(compositionCommit);
  scrEl("typing").hidden = true;
  scrEl("keyboard").setAttribute("aria-expanded", "false");
  if (document.activeElement === keyInput) keyInput.blur();
}
scrEl("keyboard-done").addEventListener("click", closeKeyboard);
scrEl("keyboard").addEventListener("click", () => {
  if (!active) return;
  if (!scrEl("typing").hidden) { closeKeyboard(); return; }
  scrEl("typing").hidden = false;
  scrEl("keyboard").setAttribute("aria-expanded", "true");
  resetInput();
  keyInput.focus(); // iOS requires focus inside the operator's tap handler.
});
keyInput.addEventListener("keydown", e => {
  if (!active || e.isComposing || !Object.hasOwn(specialKeys, e.key)) return;
  e.preventDefault();
  scr.rfb.sendKey(specialKeys[e.key], e.code || e.key);
});
function forwardInput() {
  if (!active) return;
  const value = keyInput.value;
  if (!value) scr.rfb.sendKey(specialKeys.Backspace, "Backspace");
  for (const c of value.startsWith(anchor) ? value.slice(1) : value) {
    if (c === "\n") scr.rfb.sendKey(specialKeys.Enter, "Enter");
    else scr.rfb.sendKey(keysyms.lookup(c.codePointAt(0)));
  }
  resetInput();
}
keyInput.addEventListener("input", e => {
  if (e.isComposing) return;
  clearTimeout(compositionCommit);
  forwardInput();
});
keyInput.addEventListener("compositionend", () => {
  // Some keyboards deliver the final input after compositionend.
  clearTimeout(compositionCommit);
  compositionCommit = setTimeout(forwardInput, 0);
});
if (window.visualViewport) {
  const sizeViewer = () => document.body.style.setProperty("--viewer-height", window.visualViewport.height + "px");
  window.visualViewport.addEventListener("resize", sizeViewer);
  sizeViewer();
}
for (const type of ["touchstart", "touchmove", "touchend", "touchcancel"])
  scrEl("screen").addEventListener(type, trackTouch, { capture: true, passive: false });
window.addEventListener("blur", endGesture);
window.addEventListener("resize", () => requestAnimationFrame(drawPointer));

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
    const view = surface()?.parentElement;
    if (active && view) scrollPosition = { x: view.scrollLeft, y: view.scrollTop };
    controls(false);
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
  const rfb = new ViewerRFB(scrEl("screen"), scrChannel(), { shared: true });
  rfb.manualScale = zoomScale;
  rfb.scaleViewport = mobile && zoomScale === null;
  rfb.resizeSession = !mobile;
  scr.rfb = rfb;
  let refused = false;
  rfb.addEventListener("connect", () => {
    if (run !== scr.run) return;
    const view = surface()?.parentElement;
    if (view && scrollPosition && zoomScale) {
      view.scrollLeft = scrollPosition.x;
      view.scrollTop = scrollPosition.y;
    }
    connected = true;
    retryDelay = 1000;
    controls(true);
    scrEl("state").textContent = "Connected";
  });
  rfb.addEventListener("credentialsrequired", () => {
    if (run !== scr.run) return;
    refused = true;
    rfb.disconnect();
    scrSay("The program's screen asked for a password, which this page cannot give.");
  });
  rfb.addEventListener("disconnect", () => {
    if (run !== scr.run) return;
    controls(false);
    scrEl("state").textContent = "Disconnected";
    if (refused) return;
    if (!connected) scrRecheck(run);
    else scheduleRetry();
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
    if (info && info.access !== false && ["running", "starting"].includes(info.state)) scheduleRetry();
  }, () => {
    if (run !== scr.run) return;
    scrSay(SCR_CLOSED);
    scheduleRetry();
  });
}

// Removing the iframe releases its read-only watch while leaving VNC connected.
const hudButton = scrEl("hud-toggle");
const hudDock = scrEl("hud-dock");
let hudRadio = "";
let hudWanted = false;
try { hudWanted = localStorage.getItem("dxb.screen.radioStatus") === "open"; } catch (_) { /* storage unavailable */ }

function showHud(radio) {
  hudRadio = radio;
  hudButton.hidden = !radio;
  const open = Boolean(radio && hudWanted);
  hudButton.setAttribute("aria-expanded", String(open));
  hudDock.hidden = !open;
  if (open) {
    const frame = document.createElement("iframe");
    frame.title = "Radio status";
    frame.src = "/radio-monitor#" + encodeURIComponent(radio);
    hudDock.replaceChildren(frame);
  } else {
    hudDock.replaceChildren();
  }
}
hudButton.addEventListener("click", () => {
  hudWanted = !hudWanted;
  try { localStorage.setItem("dxb.screen.radioStatus", hudWanted ? "open" : "closed"); } catch (_) { /* optional */ }
  showHud(hudRadio);
  requestAnimationFrame(drawPointer);
});

function scrShow(info, run) {
  const label = info.label || info.app || scr.session;
  const radio = info.radio_label || info.radio || "";
  scrEl("program").textContent = label;
  scrEl("radio").textContent = radio;
  document.title = label + " · " + radio;
  if (info.state === "running" && info.socket) {
    showHud(/^[a-z][a-z0-9]{0,11}$/.test(info.radio) ? info.radio : "");
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

let suspended = false, retryTimer = null, retryDelay = 1000;
function scheduleRetry() {
  if (suspended || retryTimer !== null) return;
  retryTimer = setTimeout(scrStart, retryDelay);
  retryDelay = Math.min(15000, retryDelay * 2);
}
function suspend() {
  if (suspended) return;
  suspended = true;
  scr.run++;
  clearTimeout(retryTimer);
  retryTimer = null;
  scrDrop();
}
function resume() {
  if (!suspended || document.hidden) return;
  suspended = false;
  if (SCR_SESSION.test(scr.session)) scrStart();
}
document.addEventListener("visibilitychange", () => document.hidden ? suspend() : resume());
window.addEventListener("pagehide", suspend);
window.addEventListener("pageshow", resume);
window.addEventListener("online", () => { if (retryTimer !== null && !suspended) scrStart(); });

function scrStart() {
  clearTimeout(retryTimer);
  retryTimer = null;
  if (suspended || document.hidden) { suspend(); return; }
  // a new try first, so whatever the old screen says as it closes belongs to the old one
  scr.run += 1;
  const run = scr.run;
  scrDrop();
  showHud("");
  scrEl("state").textContent = "Reading";
  dxb.run([SCR_APP, "screen", scr.session, "--json"]).then(text => {
    if (run !== scr.run) return;
    let info;
    try { info = JSON.parse(text); } catch (e) { scrEl("state").textContent = "Error"; scrSay("The station gave an answer this page cannot read."); return; }
    if (info.access === false) { scrEl("state").textContent = "No access"; scrSay(SCR_NO_ACCESS); return; }
    scrShow(info, run);
    if (info.state === "starting") scheduleRetry();
  }, err => {
    if (run !== scr.run) return;
    scrEl("state").textContent = "Error";
    if (err && err.exit_status === 3) scrSay("No program is named here.");
    else if (err && err.problem === "unreachable") { scrSay("Waiting for the station connection…"); scheduleRetry(); }
    else scrSay("Could not read the program: " + ((err && (err.message || err.problem)) || "unknown error"), true);
  });
}

let scrLinkOk = true;
try { scr.session = decodeURIComponent(location.hash.replace(/^#/, "")); } catch (e) { scr.session = ""; scrLinkOk = false; }
scrEl("reconnect").addEventListener("click", () => { if (SCR_SESSION.test(scr.session)) scrStart(); });
if (SCR_SESSION.test(scr.session)) scrStart();
else { scrEl("state").textContent = "Error"; scrSay(scrLinkOk ? "No program is named here." : "This link is not a program window.", !scrLinkOk); }
