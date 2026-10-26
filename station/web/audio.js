/* Browser audio for a radio: one WebRTC peer connection with the station per tab, signalled
 * over /api/audio/<radio>/signal. The radio's audio plays in an <audio> element that is started
 * inside the operator's tap; the microphone is asked for only on the first PTT press, goes out
 * only while PTT is held, and the radio's audio is turned down by 20 dB meanwhile.
 * window.dxbAudio: listen(radio), openMic(), setTX(on), stop(). listen and openMic resolve with
 * {ok:true} or {ok:false, reason, fix}, like every station intent. */
"use strict";

(() => {
  const NAME = /^[a-z][a-z0-9-]{0,31}$/;
  const DUCK_DB = 20;
  const DUCK = Math.pow(10, -DUCK_DB / 20); // the element's volume while transmitting
  const LOST_TEXT = "audio lost, tap to reconnect";
  // the radio wants the microphone as it is: no processing meant for a phone call
  const MIC = { audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false } };
  const UNREACHABLE = { ok: false, reason: "The station could not be reached.", fix: "check the connection and try again" };

  let cur = null; // the live connection
  let lastRadio = null; // what the reconnect chip reconnects to
  let mic = null; // the microphone track, once allowed
  let micAsk = null;
  let tx = false;
  let chip = null;

  function lostChip() {
    if (chip) return chip;
    chip = document.createElement("button");
    chip.type = "button";
    chip.className = "chip audio-lost";
    chip.hidden = true;
    chip.append(document.createTextNode(LOST_TEXT));
    chip.addEventListener("click", reconnect);
    const link = document.getElementById("link");
    if (link && link.parentNode) link.parentNode.insertBefore(chip, link.nextSibling);
    else document.body.append(chip);
    return chip;
  }

  function showLost(on) { lostChip().hidden = !on; }

  function setVolume() { if (cur && cur.el) cur.el.volume = tx ? DUCK : 1; }

  function send(c, m) { if (c.open) c.ws.send(JSON.stringify(m)); }

  function settle(c, result) {
    if (c.settled) return;
    c.settled = true;
    c.resolve(result);
  }

  // teardown ends one connection: the station is told, the socket, peer and element go
  function teardown(c) {
    if (c.down) return;
    c.down = true;
    if (cur === c) cur = null;
    try { send(c, { type: "close" }); } catch (e) { /* the socket is already gone */ }
    try { c.ws.close(); } catch (e) { /* idem */ }
    try { c.pc.close(); } catch (e) { /* idem */ }
    if (c.el) {
      try { c.el.pause(); } catch (e) { /* not playing */ }
      c.el.srcObject = null;
      c.el.remove();
    }
  }

  function lost(c) {
    if (cur !== c) return;
    lastRadio = c.radio;
    teardown(c);
    settle(c, { ok: false, reason: "audio lost", fix: "tap to reconnect" });
    showLost(true);
  }

  function reconnect() {
    showLost(false);
    if (lastRadio) listen(lastRadio);
  }

  function onMessage(c, ev) {
    if (cur !== c) return;
    let m;
    try { m = JSON.parse(ev.data); } catch (e) { return; }
    switch (m && m.type) {
      case "answer":
        c.pc.setRemoteDescription({ type: "answer", sdp: String(m.sdp) }).then(
          () => settle(c, { ok: true }),
          () => { settle(c, { ok: false, reason: "The station's audio answer could not be used.", fix: "tap to reconnect" }); lost(c); });
        break;
      case "ice":
        if (m.candidate) c.pc.addIceCandidate(m.candidate).catch(() => {});
        break;
      case "refused":
        settle(c, { ok: false, reason: String(m.reason || "no browser audio"), fix: String(m.fix || "") });
        lastRadio = null;
        teardown(c);
        break;
      case "error":
        settle(c, { ok: false, reason: String(m.reason || "no browser audio"), fix: String(m.fix || "tap to reconnect") });
        lost(c);
        break;
      case "audio-lost":
        lost(c);
        break;
      default:
    }
  }

  // listen RADIO: hear the radio in this tab. Call it from a tap: the element starts playing then.
  function listen(radio) {
    if (cur) teardown(cur);
    showLost(false);
    if (!NAME.test(String(radio))) return Promise.resolve({ ok: false, reason: "no such radio", fix: "open the radio from the dashboard" });
    lastRadio = radio;
    const c = { radio, queue: [], offered: false, open: false, settled: false, down: false };
    cur = c;
    // the element first, inside the user's gesture, so the browser lets it play the track later
    c.stream = new MediaStream();
    c.el = document.createElement("audio");
    c.el.autoplay = true;
    c.el.hidden = true;
    c.el.srcObject = c.stream;
    document.body.append(c.el);
    const played = c.el.play();
    if (played && played.catch) played.catch(() => {});
    setVolume();

    c.pc = new RTCPeerConnection({ iceServers: [] });
    c.tr = c.pc.addTransceiver("audio", { direction: "sendrecv" });
    if (mic) c.tr.sender.replaceTrack(mic);
    c.pc.ontrack = (ev) => { if (cur === c) c.stream.addTrack(ev.track); };
    c.pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      const cand = ev.candidate.toJSON ? ev.candidate.toJSON() : ev.candidate;
      if (c.offered) send(c, { type: "ice", candidate: cand });
      else c.queue.push(cand);
    };
    c.pc.onconnectionstatechange = () => { if (c.pc.connectionState === "failed") lost(c); };

    return new Promise((resolve) => {
      c.resolve = resolve;
      try {
        c.ws = dxb.ws("/api/audio/" + encodeURIComponent(radio) + "/signal?tab=" + encodeURIComponent(dxb.tab()));
      } catch (e) {
        settle(c, UNREACHABLE);
        teardown(c);
        return;
      }
      c.ws.onopen = () => {
        c.open = true;
        c.pc.createOffer().then((o) => c.pc.setLocalDescription(o)).then(() => {
          send(c, { type: "offer", sdp: c.pc.localDescription.sdp });
          c.offered = true;
          for (const cand of c.queue) send(c, { type: "ice", candidate: cand });
          c.queue = [];
        }).catch(() => {
          settle(c, { ok: false, reason: "This browser could not start audio.", fix: "reload the page and try again" });
          teardown(c);
        });
      };
      c.ws.onmessage = (ev) => onMessage(c, ev);
      c.ws.onclose = () => {
        c.open = false;
        if (cur !== c) return;
        if (c.settled) lost(c);
        else { settle(c, UNREACHABLE); teardown(c); }
      };
    });
  }

  // openMic: ask for the microphone (the first PTT press); later calls reuse it
  function openMic() {
    if (micAsk) return micAsk;
    if (typeof navigator === "undefined" || !navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return Promise.resolve({ ok: false, reason: "This browser has no microphone access here.", fix: "open the station over https" });
    }
    micAsk = navigator.mediaDevices.getUserMedia(MIC).then((stream) => {
      mic = stream.getAudioTracks()[0];
      mic.enabled = tx;
      if (cur && cur.tr) cur.tr.sender.replaceTrack(mic);
      return { ok: true };
    }, () => {
      micAsk = null;
      return { ok: false, reason: "The microphone was not allowed.", fix: "allow the microphone for this page in the browser's settings" };
    });
    return micAsk;
  }

  // setTX ON: the microphone goes out and the radio's audio ducks by 20 dB while on
  function setTX(on) {
    tx = !!on;
    if (mic) mic.enabled = tx;
    setVolume();
  }

  // stop: no more audio in this tab, the microphone released
  function stop() {
    if (cur) teardown(cur);
    lastRadio = null;
    showLost(false);
    tx = false;
    if (mic) mic.stop();
    mic = null;
    micAsk = null;
  }

  window.dxbAudio = { listen, openMic, setTX, stop, DUCK_DB, DUCK };
})();
