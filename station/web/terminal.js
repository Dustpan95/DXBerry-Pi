// terminal.js: the terminal page. xterm.js attached to the station's own authenticated WebSocket
// (/api/term), which runs a login shell on a pty. Keystrokes go out as binary frames, the window
// size as a {"resize":{"cols":C,"rows":R}} text frame, output comes back as binary frames.
(function () {
  const TRM_ENDED = "Session ended";
  const el = id => document.getElementById(id);
  const enc = new TextEncoder();

  const term = new Terminal({ cursorBlink: true, fontFamily: "ui-monospace, Menlo, Consolas, monospace", fontSize: 14 });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(el("term"));
  let ws = null;
  let live = false;

  function sendSize() {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
    }
  }

  function refit() {
    try { fit.fit(); } catch (e) { /* the page has no size yet */ }
  }

  function ended() {
    if (!live) return;
    live = false;
    el("state").textContent = "Ended";
    el("close").disabled = true;
    term.writeln("");
    term.writeln("\x1b[2m" + TRM_ENDED + "\x1b[0m");
  }

  function connect() {
    refit();
    el("state").textContent = "Connecting";
    ws = dxb.ws("/api/term");
    ws.binaryType = "arraybuffer";
    ws.addEventListener("open", () => {
      live = true;
      el("state").textContent = "Connected";
      sendSize();
      term.focus();
    });
    ws.addEventListener("message", ev => {
      if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data));
    });
    ws.addEventListener("close", ended);
    ws.addEventListener("error", () => { if (!live) { live = true; ended(); } });
  }

  term.onData(text => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(enc.encode(text));
  });
  term.onBinary(text => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(Uint8Array.from(text, ch => ch.charCodeAt(0)));
  });
  term.onResize(sendSize);
  window.addEventListener("resize", refit);
  el("close").addEventListener("click", () => { if (ws) ws.close(1000); });
  connect();
})();
