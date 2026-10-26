/* Browser audio for a radio: one WebRTC peer connection with the station per tab, signalled
 * over /api/audio/<radio>/signal. The radio's audio plays through Web Audio (source > analyser >
 * gain > speakers) on an AudioContext made inside the operator's tap; a muted <audio> element
 * carries the remote track too, since some browsers deliver a WebRTC track to Web Audio only
 * while an element plays it. The microphone is asked for once (the Mic button or the first PTT
 * press), goes out only while PTT is held, and the gain turns the radio down by 20 dB meanwhile
 * (iPhones ignore an element's volume, so the gain is what ducks; without Web Audio the
 * element's volume does). window.dxbAudio: listen(radio), openMic(), setTX(on), stop(), level()
 * (the received RMS level, 0..1, or null), micLabel() (the microphone's name, or null when there
 * is none) and onChange(fn) (fn({state, radio}) with state connecting, on, lost or idle). listen
 * and openMic resolve with {ok:true} or {ok:false, reason, fix}, like every station intent. */
"use strict";

(() => {
  const NAME = /^[a-z][a-z0-9-]{0,31}$/;
  const DUCK_DB = 20;
  const DUCK = Math.pow(10, -DUCK_DB / 20); // the gain while transmitting: 20 dB down is a tenth
  const LOST_TEXT = "audio lost, tap to reconnect";
  // the radio wants the microphone as it is: no processing meant for a phone call
  const MIC = { audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false } };
  const UNREACHABLE = { ok: false, reason: "The station could not be reached.", fix: "check the connection and try again" };
  const STOPPED = { ok: false, reason: "Audio was stopped.", fix: "tap Listen again" };

  let cur = null; // the live connection
  let lastRadio = null; // what the reconnect chip reconnects to
  let mic = null; // the microphone track, once allowed
  let micAsk = null;
  let micGen = 0; // stop() moves it on, so a prompt still open when audio stopped is let go
  let tx = false;
  let chip = null;
  let actx = null; // the tab's one AudioContext, made inside the first tap and kept
  let state = "idle";
  const watchers = [];

  // change STATE RADIO: tell the page (a repeated idle is not news)
  function change(s, radio) {
    if (s === "idle" && state === "idle") return;
    state = s;
    for (const f of watchers) { try { f({ state: s, radio: radio || null }); } catch (e) { /* the page's own trouble */ } }
  }

  // audioContext: the tab's AudioContext, made or woken now (inside a tap), or null without Web Audio
  function audioContext() {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return null;
    if (!actx || actx.state === "closed") {
      try { actx = new AC(); } catch (e) { actx = null; return null; }
    }
    if (actx.state !== "running" && actx.resume) {
      const p = actx.resume();
      if (p && p.catch) p.catch(() => {});
    }
    return actx;
  }

  // wake: a browser that would not start the sound outside a tap gets it on the next tap (a PTT
  // press starts the audio on pointerdown, which phones do not count as a gesture)
  function wake() {
    if (!cur) return;
    if (actx && actx.state !== "running" && actx.state !== "closed" && actx.resume) {
      const p = actx.resume();
      if (p && p.catch) p.catch(() => {});
    }
    if (cur.el && cur.el.paused) {
      const p = cur.el.play();
      if (p && p.catch) p.catch(() => {});
    }
  }
  if (typeof document !== "undefined" && document.addEventListener) {
    for (const t of ["pointerup", "touchend", "keydown", "click"]) document.addEventListener(t, wake, true);
  }

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

  // duck: the radio 20 dB down while transmitting, through the gain (or the element's volume
  // where there is no Web Audio)
  function duck() {
    if (!cur) return;
    if (cur.gain) cur.gain.gain.value = tx ? DUCK : 1;
    else if (cur.el) cur.el.volume = tx ? DUCK : 1;
  }

  function send(c, m) { if (c.open) c.ws.send(JSON.stringify(m)); }

  function settle(c, result) {
    if (c.settled) return;
    c.settled = true;
    c.resolve(result);
  }

  // teardown ends one connection: the station is told, the socket, peer, audio nodes and element
  // go, and a listen still waiting for its answer is told it was stopped
  function teardown(c) {
    if (c.down) return;
    c.down = true;
    if (cur === c) cur = null;
    settle(c, STOPPED);
    try { send(c, { type: "close" }); } catch (e) { /* the socket is already gone */ }
    try { c.ws.close(); } catch (e) { /* idem */ }
    try { c.pc.close(); } catch (e) { /* idem */ }
    for (const n of [c.src, c.analyser, c.gain]) {
      if (n) { try { n.disconnect(); } catch (e) { /* not connected */ } }
    }
    if (c.el) {
      try { c.el.pause(); } catch (e) { /* not playing */ }
      c.el.srcObject = null;
      c.el.remove();
    }
  }

  function lost(c) {
    if (cur !== c) return;
    lastRadio = c.radio;
    settle(c, { ok: false, reason: "audio lost", fix: "tap to reconnect" });
    teardown(c);
    showLost(true);
    change("lost", c.radio);
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
          () => { if (cur !== c) return; settle(c, { ok: true }); change("on", c.radio); },
          () => { settle(c, { ok: false, reason: "The station's audio answer could not be used.", fix: "tap to reconnect" }); lost(c); });
        break;
      case "ice":
        if (m.candidate) c.pc.addIceCandidate(m.candidate).catch(() => {});
        break;
      case "refused":
        settle(c, { ok: false, reason: String(m.reason || "no browser audio"), fix: String(m.fix || "") });
        lastRadio = null;
        teardown(c);
        change("idle", c.radio);
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

  // listen RADIO: hear the radio in this tab. Call it from a tap: the audio context and the
  // element start then.
  function listen(radio) {
    if (cur) teardown(cur);
    showLost(false);
    if (!NAME.test(String(radio))) return Promise.resolve({ ok: false, reason: "no such radio", fix: "open the radio from the dashboard" });
    if (!window.RTCPeerConnection) {
      change("idle", radio);
      return Promise.resolve({ ok: false, reason: "This browser cannot play the station's audio.", fix: "open the page in a current browser" });
    }
    lastRadio = radio;
    const c = { radio, queue: [], offered: false, open: false, settled: false, down: false, src: null, analyser: null, gain: null };
    cur = c;
    change("connecting", radio);
    // the audio graph and the element first, inside the user's gesture, so the browser lets them
    // play the track later
    const ac = audioContext();
    if (ac) {
      try {
        c.gain = ac.createGain();
        c.analyser = ac.createAnalyser();
        c.analyser.fftSize = 1024;
        c.analyser.connect(c.gain);
        c.gain.connect(ac.destination);
      } catch (e) {
        c.gain = c.analyser = null;
      }
    }
    c.stream = new MediaStream();
    c.el = document.createElement("audio");
    c.el.autoplay = true;
    c.el.hidden = true;
    // with the gain in the path the element only carries the track; it must not play it twice
    c.el.muted = !!c.gain;
    c.el.srcObject = c.stream;
    document.body.append(c.el);
    const played = c.el.play();
    if (played && played.catch) played.catch(() => {});
    duck();

    c.pc = new RTCPeerConnection({ iceServers: [] });
    c.tr = c.pc.addTransceiver("audio", { direction: "sendrecv" });
    if (mic) c.tr.sender.replaceTrack(mic);
    c.pc.ontrack = (ev) => {
      if (cur !== c) return;
      c.stream.addTrack(ev.track);
      if (c.gain && !c.src) {
        try {
          c.src = actx.createMediaStreamSource(c.stream);
          c.src.connect(c.analyser);
        } catch (e) {
          // this browser will not take the track into Web Audio: the element plays it instead
          c.src = null;
          try { c.gain.disconnect(); } catch (x) { /* not connected */ }
          c.gain = c.analyser = null;
          c.el.muted = false;
          duck();
        }
      }
    };
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
        change("idle", radio);
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
          if (cur !== c) return;
          settle(c, { ok: false, reason: "This browser could not start audio.", fix: "reload the page and try again" });
          teardown(c);
          change("idle", radio);
        });
      };
      c.ws.onmessage = (ev) => onMessage(c, ev);
      c.ws.onclose = () => {
        c.open = false;
        if (cur !== c) return;
        if (c.settled) lost(c);
        else { settle(c, UNREACHABLE); teardown(c); change("idle", radio); }
      };
    });
  }

  // openMic: ask for the microphone (the first PTT press); later calls reuse it
  function openMic() {
    if (micAsk) return micAsk;
    if (typeof navigator === "undefined" || !navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return Promise.resolve({ ok: false, reason: "This browser has no microphone access here.", fix: "open the station over https" });
    }
    const gen = ++micGen;
    micAsk = navigator.mediaDevices.getUserMedia(MIC).then((stream) => {
      if (gen !== micGen) { // stopped while the prompt was open: do not keep the microphone
        for (const t of stream.getAudioTracks()) t.stop();
        return { ok: false, reason: "Audio was stopped.", fix: "tap Listen again" };
      }
      mic = stream.getAudioTracks()[0];
      mic.enabled = tx;
      if (cur && cur.tr) cur.tr.sender.replaceTrack(mic);
      return { ok: true };
    }, () => {
      if (gen === micGen) micAsk = null;
      return { ok: false, reason: "The microphone was not allowed.", fix: "allow the microphone for this page in the browser's settings" };
    });
    return micAsk;
  }

  // setTX ON: the microphone goes out and the radio's audio ducks by 20 dB while on
  function setTX(on) {
    tx = !!on;
    if (mic) mic.enabled = tx;
    duck();
  }

  // stop: no more audio in this tab, the microphone released, the audio context asleep
  function stop() {
    const radio = cur ? cur.radio : lastRadio;
    if (cur) teardown(cur);
    lastRadio = null;
    showLost(false);
    tx = false;
    if (mic) mic.stop();
    mic = null;
    micAsk = null;
    micGen++;
    if (actx && actx.state === "running" && actx.suspend) {
      const p = actx.suspend();
      if (p && p.catch) p.catch(() => {});
    }
    change("idle", radio);
  }

  // level: how loud the radio's audio is (RMS, 0..1) as it arrives, before the duck; null while
  // there is nothing to measure (no track yet, or no Web Audio). The float samples are read where
  // the browser has them: the byte form floors each sample, so even a quiet noise floor would read
  // about -45 dBFS there.
  function level() {
    const c = cur;
    if (!c || !c.src || !c.analyser) return null;
    const a = c.analyser;
    const n = a.fftSize;
    let sum = 0;
    if (typeof a.getFloatTimeDomainData === "function" && typeof Float32Array === "function") {
      const buf = new Float32Array(n);
      a.getFloatTimeDomainData(buf);
      for (let i = 0; i < n; i++) sum += buf[i] * buf[i];
    } else {
      const buf = new Uint8Array(n);
      a.getByteTimeDomainData(buf);
      for (let i = 0; i < n; i++) {
        const x = (buf[i] - 128) / 128;
        sum += x * x;
      }
    }
    return Math.sqrt(sum / n);
  }

  // micLabel: the name of the microphone in use, as the browser gives it; null when none is open
  const micLabel = () => (mic ? String(mic.label || "") : null);

  // onChange FN: FN({state, radio}) whenever the audio connects, plays, is lost or stops
  function onChange(fn) { if (typeof fn === "function") watchers.push(fn); }

  window.dxbAudio = { listen, openMic, setTX, stop, level, micLabel, onChange, DUCK_DB, DUCK };
})();
