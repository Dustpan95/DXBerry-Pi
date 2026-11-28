// Watch the same rigctld poller as the VFO page. This window never claims a radio.
"use strict";
(() => {
  const radio = location.hash.slice(1);
  const el = id => document.getElementById(id);
  const number = (v, places, unit) => v.toFixed(places) + unit;
  const fields = [
    ["VD_METER", "Supply voltage", v => number(v, 1, " V")],
    ["ID_METER", "PA current", v => number(v, 1, " A")],
    ["TEMP_METER", "Temperature", v => number(v, 1, " °C")],
    ["SWR", "SWR", v => v >= 1 ? number(v, 2, ":1") : "—", "tx"],
    ["RFPOWER_METER_WATTS", "RF output", v => number(v, 1, " W"), "tx"],
    ["ALC", "ALC", v => number(v, 2, ""), "tx"],
    ["STRENGTH", "Signal", v => v >= 0 ? (v === 0 ? "S9" : "S9 +" + Math.round(v) + " dB") : "S" + Math.max(0, Math.round(9 + v / 6)), "rx"],
    ["RFPOWER", "Power setting", v => Math.round(100 * v) + "%"],
    ["COMP_METER", "Compression", v => number(v, 1, " dB"), "tx"],
  ];
  const rows = new Map();
  for (const [key, label] of fields) {
    const row = document.createElement("div"), term = document.createElement("dt"), value = document.createElement("dd");
    term.textContent = label;
    row.append(term, value);
    row.hidden = true;
    el("meters").append(row);
    rows.set(key, {row, value});
  }
  let caps, state, stamp, freshAt, metaPending = false, generation = 0, knownMissing = false;
  const write = (node, value) => { if (node.textContent !== value) node.textContent = value; };

  function paint() {
    const timely = freshAt !== undefined && performance.now() - freshAt < 10000;
    const live = Boolean(dxb.events.open && timely && state?.connected && state.answering && !knownMissing);
    const keyed = live && caps?.CanGetPTT && state.ptt_known && !state.optimistic?.ptt ? state.ptt : undefined;
    const phase = knownMissing ? "Radio unavailable" : !dxb.events.open ? "Connecting…"
      : !state ? "Waiting for radio" : !state.connected || !state.answering ? "Radio offline"
      : !timely ? "Waiting for a fresh reading" : keyed === true ? "TX · Transmitting" : keyed === false ? "RX · Receiving" : "Monitoring";
    write(el("phase"), phase);
    el("phase").dataset.state = keyed === true ? "tx" : keyed === false ? "rx" : "";
    const freq = live && !state.optimistic?.freq && state.freq > 0 ? (state.freq / 1e6).toFixed(6) : "—";
    write(el("frequency"), freq);
    const tuning = live ? [
      caps?.CanGetVFO && !state.optimistic?.vfo && state.vfo?.replace(/^VFO([AB])$/, "VFO $1"),
      !state.optimistic?.mode && state.mode,
      !state.optimistic?.mode && Number(state.passband) > 0 && state.passband + " Hz",
      caps?.CanGetSplit && state.split && !state.optimistic?.split && "Split",
    ].filter(Boolean).join(" · ") : "";
    write(el("tuning"), tuning || "—");
    const unsupported = [];
    for (const [key, label, format, period] of fields) {
      const {row, value} = rows.get(key);
      const supported = caps?.ReadableMeters?.includes(key);
      row.hidden = !supported;
      if (!supported) {
        if (caps && ["VD_METER", "TEMP_METER", "SWR"].includes(key)) unsupported.push(label.toLowerCase());
        continue;
      }
      const v = state?.meters?.[key] ?? state?.levels?.[key];
      const inPeriod = !period || (period === "tx" ? keyed === true : keyed === false);
      write(value, live && inPeriod && Number.isFinite(v) && !state.optimistic?.[key] ? format(v) : "—");
    }
    const notes = [];
    if (live && keyed === false && caps?.ReadableMeters?.includes("SWR")) notes.push("SWR and transmit meters appear during TX.");
    if (unsupported.length) notes.push("CAT does not report " + unsupported.join(", ") + ".");
    write(el("meter-note"), notes.join(" "));
  }

  if (!/^[a-z][a-z0-9]{0,11}$/.test(radio)) {
    write(el("phase"), "Open Radio status from a program window.");
    return;
  }
  const detach = el("detach");
  detach.href = location.href;
  detach.hidden = window.parent === window;
  detach.addEventListener("click", e => {
    if (e.button || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    window.open(detach.href, "_blank", "popup,noopener,width=380,height=540");
  });
  async function metadata() {
    if (metaPending || caps || knownMissing || !dxb.events.open) return;
    const run = generation;
    metaPending = true;
    try {
      const view = await dxb.api("GET", "/api/radio/" + radio, undefined, {timeout: 4000});
      if (run !== generation || !view?.radio || view.radio.name !== radio) return;
      caps = view.caps;
      const name = view.radio.label || caps?.ModelName || radio;
      write(el("radio-name"), name);
      document.title = name + " · Radio status";
      paint();
    } finally { if (run === generation) metaPending = false; }
  }
  function reset() {
    generation++;
    metaPending = false;
    caps = state = stamp = freshAt = undefined;
    paint();
  }
  function watch() {
    if (!dxb.events.open || knownMissing) return;
    // The poller publishes on change. Request its cached snapshot periodically
    // so an unchanged frequency can still be distinguished from a stalled poller.
    dxb.events.send({type: "watch", radio, level: "page"});
    metadata();
  }
  dxb.events.on("rig", message => {
    if (message.radio !== radio || !message.data || knownMissing) return;
    state = message.data;
    if (stamp !== state.updated_at) {
      // A first snapshot may predate this connection. Wait for a new poll.
      if (stamp && Number.isFinite(Date.parse(state.updated_at))) freshAt = performance.now();
      stamp = state.updated_at;
    }
    paint();
  });
  dxb.events.on("radios", message => {
    if (!Array.isArray(message.data)) return;
    const missing = !message.data.some(r => r.name === radio);
    if (missing !== knownMissing) {
      knownMissing = missing;
      reset();
      watch();
    }
  });
  dxb.events.onOpen(() => { knownMissing = false; reset(); watch(); });
  dxb.events.onClose(reset);
  dxb.events.start();
  setInterval(() => { if (!document.hidden) { watch(); paint(); } }, 3000);
})();
