/* DXBerry page, the VFO view (/radio/<name>): the live front panel of one radio. Loaded last. The
 * pure logic (formats, step math, bands, which sections a radio gets, the owner line) is on
 * window.dxbVFO so the node tests can reach it; the rest draws the page. Every command goes
 * through dxb.api (app.js). Text from the station goes on the page as text nodes only. */
"use strict";

(() => {
  // ---- pure logic ----------------------------------------------------------------------------
  // The amateur bands, edges in Hz (US allocations). The image is generic, so a band is named by
  // its wavelength only.
  const BANDS = [
    ["160 m", 1800000, 2000000], ["80 m", 3500000, 4000000], ["60 m", 5330500, 5406400],
    ["40 m", 7000000, 7300000], ["30 m", 10100000, 10150000], ["20 m", 14000000, 14350000],
    ["17 m", 18068000, 18168000], ["15 m", 21000000, 21450000], ["12 m", 24890000, 24990000],
    ["10 m", 28000000, 29700000], ["6 m", 50000000, 54000000], ["2 m", 144000000, 148000000],
    ["1.25 m", 222000000, 225000000], ["70 cm", 420000000, 450000000], ["33 cm", 902000000, 928000000],
    ["23 cm", 1240000000, 1300000000],
  ].map(([name, lo, hi]) => ({ name, lo, hi }));

  const STEPS = [10, 100, 1000, 10000, 100000, 1000000];
  const STEP_NAMES = { 10: "10 Hz", 100: "100 Hz", 1000: "1 kHz", 10000: "10 kHz", 100000: "100 kHz", 1000000: "1 MHz" };
  const NAME_RE = /^[a-z][a-z0-9-]{0,31}$/;
  const MODE_ORDER = ["USB", "LSB", "CW", "CWR", "AM", "FM", "RTTY", "PKTUSB", "PKTLSB"];
  const MODE_LABEL = { PKTUSB: "DATA-U", PKTLSB: "DATA-L", CWR: "CW-R" };
  // the filter widths a mode offers, in Hz, and the slider's span
  const FILTERS = {
    USB: [1800, 2400, 3000], LSB: [1800, 2400, 3000], PKTUSB: [1800, 2400, 3000], PKTLSB: [1800, 2400, 3000],
    CW: [200, 500, 1000, 1800], CWR: [200, 500, 1000, 1800], RTTY: [200, 500, 1000, 2400], AM: [3000],
  };
  const FILTER_SPAN = { CW: [50, 2400], CWR: [50, 2400], RTTY: [100, 2700], AM: [1000, 9000] };
  const FILTER_SPAN_DEFAULT = [500, 3600];
  const HIDDEN_LABEL = {
    STRENGTH: "S-meter", SWR: "SWR meter", ALC: "ALC meter", RFPOWER: "Power control", RFPOWER_METER_WATTS: "Power meter",
    AF: "AF gain", RF: "RF gain", SQL: "Squelch", SPLIT: "Split", VFO: "VFO A/B",
  };
  const LEVEL_LABEL = { RFPOWER: "Power", AF: "AF gain", RF: "RF gain" };
  const LEVEL_ORDER = ["RFPOWER", "AF", "RF"];
  const DUMMY_WHY = "dummy model";

  // Go's JSON names for the capability fields have no tags, so the wire carries ModelName,
  // FreqRanges and so on; lower-case names are taken too. normCaps gives the page one shape.
  const pick = (o, a, b, d) => (o && o[a] !== undefined && o[a] !== null ? o[a] : o && o[b] !== undefined && o[b] !== null ? o[b] : d);
  function normCaps(c) {
    if (!c || typeof c !== "object") return null;
    return {
      modelName: String(pick(c, "ModelName", "model_name", "")),
      modes: pick(c, "Modes", "modes", []),
      vfos: pick(c, "VFOs", "vfos", []),
      canSetSplit: !!pick(c, "CanSetSplit", "can_set_split", false),
      canSetVFO: !!pick(c, "CanSetVFO", "can_set_vfo", false),
      canSetPTT: !!pick(c, "CanSetPTT", "can_set_ptt", false),
      settableLevels: pick(c, "SettableLevels", "settable_levels", []),
      settableFuncs: pick(c, "SettableFuncs", "settable_funcs", []),
      hidden: pick(c, "Hidden", "hidden", []).map((h) => ({
        name: String(pick(h, "Name", "name", "")), kind: String(pick(h, "Kind", "kind", "")), why: String(pick(h, "Why", "why", "")),
      })),
      ranges: pick(c, "FreqRanges", "freq_ranges", []).map((r) => ({ lo: Number(pick(r, "Lo", "lo", 0)), hi: Number(pick(r, "Hi", "hi", 0)) })),
    };
  }

  // formatFreq HZ: 14074000 -> "14.074.000" (groups of three, a point between)
  function formatFreq(hz) {
    const n = Math.round(Number(hz));
    if (!Number.isFinite(n) || n < 0) return "—";
    return String(n).replace(/\B(?=(\d{3})+$)/g, ".");
  }

  // parseFreq TEXT: what the keypad typed, in Hz, or null. "14.074" is MHz, "14.074.000" is Hz
  // with its points, a bare number under a million is kHz and anything bigger is Hz.
  function parseFreq(text) {
    const t = String(text).trim().replace(/\s+/g, "");
    if (!/^[0-9.]+$/.test(t) || !/[0-9]/.test(t)) return null;
    const dots = (t.match(/\./g) || []).length;
    let hz;
    if (dots === 0) {
      const v = Number(t);
      hz = v < 1000000 ? v * 1000 : v;
    } else if (dots === 1) {
      hz = Number(t) * 1e6;
    } else {
      if (!/^[0-9]{1,3}(\.[0-9]{3})+$/.test(t)) return null;
      hz = Number(t.replace(/\./g, ""));
    }
    hz = Math.round(hz);
    return Number.isFinite(hz) && hz > 0 ? hz : null;
  }

  // clampFreq HZ RANGES: HZ inside the range it is in, else at the edge of the nearest one
  function clampFreq(hz, ranges) {
    let v = Math.round(hz);
    if (v < 0) v = 0;
    if (!ranges || !ranges.length) return v;
    let best = null;
    let bestD = Infinity;
    for (const r of ranges) {
      if (v >= r.lo && v <= r.hi) return v;
      const d = v < r.lo ? r.lo - v : v - r.hi;
      if (d < bestD) { bestD = d; best = r; }
    }
    return v < best.lo ? best.lo : best.hi;
  }

  // stepFreq HZ STEP DIR RANGES: one step up (DIR 1) or down (-1), kept inside the radio's range
  function stepFreq(hz, step, dir, ranges) {
    return clampFreq(hz + (dir < 0 ? -step : step), ranges);
  }

  // bandsFromRanges RANGES: the amateur bands the radio can receive in, lowest first; start is
  // where a band button tunes to when the band has not been used yet
  function bandsFromRanges(ranges) {
    const out = [];
    for (const b of BANDS) {
      const r = (ranges || []).find((x) => x.hi >= b.lo && x.lo <= b.hi);
      if (r) out.push({ name: b.name, lo: Math.max(b.lo, r.lo), hi: Math.min(b.hi, r.hi), start: Math.max(b.lo, r.lo) });
    }
    return out;
  }

  function bandOf(hz, bands) {
    return (bands || []).find((b) => hz >= b.lo && hz <= b.hi) || null;
  }

  // modeButtons MODES: the modes the radio has that the page offers, in the page's order
  function modeButtons(modes) {
    return MODE_ORDER.filter((m) => (modes || []).includes(m)).map((m) => ({ mode: m, label: MODE_LABEL[m] || m }));
  }

  const filterPresets = (mode) => FILTERS[mode] || [];
  const filterSpan = (mode) => FILTER_SPAN[mode] || FILTER_SPAN_DEFAULT;

  // sUnits DB: Hamlib's STRENGTH is dB against S9 (6 dB an S-unit); the bar runs 0 to 15 (S9+60)
  function sUnits(db) {
    const d = Number(db);
    if (!Number.isFinite(d)) return 0;
    return Math.max(0, Math.min(15, d <= 0 ? 9 + d / 6 : 9 + d / 10));
  }

  function sText(db) {
    const d = Number(db);
    if (!Number.isFinite(d)) return "—";
    if (d > 0) return `S9+${Math.round(d / 5) * 5}`;
    return `S${Math.max(0, Math.round(9 + d / 6))}`;
  }

  const isDummy = (nc) => !!nc && nc.modelName === "Dummy";
  const hiddenNames = (nc) => new Set(((nc && nc.hidden) || []).map((h) => h.name));

  // noCat RECORD CAPS: the radio has no rig control to show
  function noCat(record, caps) {
    if (record && record.has_cat === false) return true;
    return isDummy(normCaps(caps));
  }

  // layoutFor CAPS RECORD OWNER: the sections of the page, top to bottom, by name
  function layoutFor(caps, record, owner) {
    const program = owner && owner.kind === "program" ? ["program"] : [];
    if (noCat(record, caps)) return ["nocat", "stop", "owner", ...program];
    const nc = normCaps(caps);
    if (!nc) return ["connecting", "stop", "owner", ...program];
    const hid = hiddenNames(nc);
    const out = ["readout", "dial"];
    if (bandsFromRanges(nc.ranges).length) out.push("bands");
    if (modeButtons(nc.modes).length) out.push("modes");
    if (!hid.has("VFO") || !hid.has("SPLIT")) out.push("vfo");
    out.push("filter");
    if (LEVEL_ORDER.some((n) => nc.settableLevels.includes(n))) out.push("levels");
    if (!hid.has("STRENGTH")) out.push("smeter");
    if (nc.settableFuncs.includes("TUNER")) out.push("atu");
    out.push("tune");
    out.push("stop", "owner", ...program);
    if (nc.hidden.length) out.push("why");
    return out;
  }

  // ownerLine OWNER: who has the transmitter, in words
  function ownerLine(owner) {
    if (!owner) return "Nobody has the transmitter";
    if (owner.kind === "program") return `${owner.label} has this radio`;
    return owner.mine ? "You have the transmitter" : `${owner.label} has the transmitter`;
  }

  // ownerActions OWNER: the buttons beside the owner line
  function ownerActions(owner) {
    if (!owner) return ["claim"];
    if (owner.kind === "program") return ["open"];
    return [owner.mine ? "release" : "takeover"];
  }

  const radioFromPath = (p) => {
    const m = /^\/radio\/([^/]+)$/.exec(String(p));
    return m && NAME_RE.test(m[1]) ? m[1] : null;
  };

  // levelPercent V: a 0..1 level as a whole percent
  const levelPercent = (v) => Math.round(Math.max(0, Math.min(1, Number(v) || 0)) * 100);

  // mapped to the names the station marks optimistic fields by
  const OPT_KEY = { freq: "freq", mode: "mode", vfo: "vfo", split: "split" };

  const pure = {
    BANDS, STEPS, STEP_NAMES, formatFreq, parseFreq, clampFreq, stepFreq, bandsFromRanges, bandOf, modeButtons,
    filterPresets, filterSpan, sUnits, sText, normCaps, noCat, layoutFor, ownerLine, ownerActions, radioFromPath, levelPercent,
    onRadioPage: () => typeof location !== "undefined" && radioFromPath(location.pathname) !== null,
  };
  window.dxbVFO = pure;

  if (typeof document === "undefined" || !document.getElementById || !radioFromPath(location.pathname)) return;

  // ---- the page ------------------------------------------------------------------------------
  const name = radioFromPath(location.pathname);
  // the view sits under the notices, in place of the dashboard
  const anchor = document.getElementById("notices");
  if (!anchor || !anchor.parentNode) return;
  const root = document.createElement("section");
  root.className = "vfo";
  root.setAttribute("aria-label", "Radio");
  anchor.parentNode.insertBefore(root, anchor.nextSibling);

  const S = {
    rec: null, caps: null, nc: null, owner: null, st: null, found: true, sock: false,
    pend: {},        // field -> the value shown at once after a write, until a state says it is confirmed
    want: null,      // the frequency the dial is heading for, while one is being sent
    sending: false,
    layout: "", ownerKey: "", step: 1000, tune: null, upd: [], bands: [],
  };

  function h(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === null || v === undefined || v === false) continue;
      if (k === "class") e.className = v;
      else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
      else e.setAttribute(k, v === true ? "" : v);
    }
    for (const k of kids.flat(Infinity)) {
      if (k === null || k === undefined || k === false) continue;
      e.append(typeof k === "object" ? k : String(k));
    }
    return e;
  }

  function store(k, v) { try { localStorage.setItem("vfo:" + k, String(v)); } catch (e) { /* storage blocked */ } }
  function load(k) { try { return localStorage.getItem("vfo:" + k); } catch (e) { return null; } }

  function say(text, kind) {
    if (typeof notice === "function") notice(text, kind);
  }

  const failText = (res) => `${res.reason || "The radio did not accept that."}${res.fix ? " " + res.fix + "." : ""}`;

  // the rig link or the events socket is down: controls dim, STOP stays
  const linkDown = () => !S.sock || !S.st || !S.st.connected;

  function setText(e, t) { if (e.textContent !== t) e.textContent = t; }
  function setAttr(e, k, v) { if (e.getAttribute(k) !== v) e.setAttribute(k, v); }

  // value of a field to show: the one just written until a state confirms it
  const shown = (field, real) => (field in S.pend ? S.pend[field] : real);

  // ---- sending -------------------------------------------------------------------------------
  // intent NAME BODY FIELD VALUE: send; on success show VALUE (marked) until the next state
  function intent(which, body, field, value) {
    if (field) S.pend[field] = value;
    paint();
    return dxb.api("POST", `/api/radio/${name}/${which}`, body).then((res) => {
      if (res && res.ok) return true;
      if (field) delete S.pend[field];
      say(failText(res || {}), "bad");
      paint();
      return false;
    });
  }

  // the dial can ask for many frequencies a second; one is sent at a time and the last wins
  function tuneTo(hz) {
    S.want = clampFreq(hz, S.nc ? S.nc.ranges : []);
    S.pend.freq = S.want;
    paint();
    pump();
  }

  function pump() {
    if (S.sending || S.want === null) return;
    const hz = S.want;
    S.sending = true;
    dxb.api("POST", `/api/radio/${name}/freq`, { hz }).then((res) => {
      S.sending = false;
      if (!(res && res.ok)) {
        if (S.want === hz) S.want = null;
        delete S.pend.freq;
        say(failText(res || {}), "bad");
        paint();
        return;
      }
      if (S.want === hz) {
        S.want = null;
        const b = bandOf(hz, S.bands);
        if (b) store("band:" + b.name, hz);
      }
      pump();
    });
  }

  function claimBody() { return { device: dxb.tab() }; }

  function ownerCall(which) {
    dxb.api("POST", `/api/radio/${name}/${which}`, claimBody()).then((res) => {
      if (!(res && res.ok)) say(failText(res || {}), "bad");
      loadAll();
    });
  }

  // ---- building the page ---------------------------------------------------------------------
  const sectionBuilders = {};

  function ctl(e) { e.classList.add("vfo-ctl"); return e; }

  sectionBuilders.nocat = () => h("section", { class: "card vfo-box" }, h("p", { class: "vfo-nocat" }, "no rig control on this radio"));
  sectionBuilders.connecting = () => h("section", { class: "card vfo-box" }, h("p", { class: "muted" }, "Reading what this radio can do…"));

  sectionBuilders.readout = () => {
    const num = h("span", { class: "vfo-digits" });
    const flags = h("span", { class: "vfo-flags" });
    const b = ctl(h("button", { class: "vfo-readout", type: "button", "aria-label": "Frequency in hertz. Tap to type one." }, num, h("span", { class: "vfo-unit" }, " Hz")));
    b.addEventListener("click", openKeypad);
    S.upd.push(() => {
      const hz = shown("freq", S.st && S.st.freq);
      setText(num, S.st || "freq" in S.pend ? (hz > 0 ? formatFreq(hz) : "—") : "—");
      b.classList.toggle("is-pending", pending("freq"));
      const f = [];
      const vfo = shown("vfo", S.st && S.st.vfo);
      if (vfo) f.push(h("span", { class: "badge muted" }, String(vfo).replace(/^VFO/, "VFO ")));
      if (shown("split", S.st && S.st.split)) f.push(h("span", { class: "badge warn" }, "SPLIT"));
      const key = f.map((x) => x.textContent).join("|");
      if (flags.dataset.k !== key) { flags.dataset.k = key; flags.replaceChildren(...f); }
    });
    return h("section", { class: "card vfo-box vfo-readout-box" }, b, flags);
  };

  function pending(field) {
    if (field in S.pend) return true;
    return !!(S.st && S.st.optimistic && S.st.optimistic[field]);
  }

  sectionBuilders.dial = () => {
    const knob = ctl(h("div", { class: "vfo-dial", role: "slider", tabindex: "0", "aria-label": "Tuning dial", "aria-valuemin": "0", "aria-valuemax": "0", "aria-valuenow": "0" },
      h("span", { class: "vfo-dial-mark" })));
    const stepBox = h("div", { class: "vfo-steps", role: "group", "aria-label": "Tuning step" });
    const stepBtns = STEPS.map((s) => {
      const b = ctl(h("button", { class: "btn small", type: "button", "aria-pressed": "false", onclick: () => { S.step = s; store("step", s); paint(); } }, STEP_NAMES[s]));
      b.dataset.step = String(s);
      stepBox.append(b);
      return b;
    });
    const cur = () => shown("freq", S.st && S.st.freq) || 0;
    const move = (dir, n) => {
      let hz = S.want !== null ? S.want : cur();
      if (!hz) return;
      for (let i = 0; i < n; i++) hz = stepFreq(hz, S.step, dir, S.nc ? S.nc.ranges : []);
      tuneTo(hz);
    };
    let turn = 0;
    const spin = (dir, n) => { turn += dir * n * 12; knob.style.setProperty("--turn", String(turn)); };
    let last = null;
    let acc = 0;
    knob.addEventListener("pointerdown", (e) => {
      if (linkDown()) return;
      last = e.clientX;
      acc = 0;
      if (knob.setPointerCapture) { try { knob.setPointerCapture(e.pointerId); } catch (x) { /* not capturable */ } }
      e.preventDefault();
    });
    knob.addEventListener("pointermove", (e) => {
      if (last === null) return;
      acc += e.clientX - last;
      last = e.clientX;
      const n = Math.trunc(acc / 14);
      if (n !== 0) { acc -= n * 14; spin(n > 0 ? 1 : -1, Math.abs(n)); move(n > 0 ? 1 : -1, Math.abs(n)); }
    });
    const end = () => { last = null; };
    knob.addEventListener("pointerup", end);
    knob.addEventListener("pointercancel", end);
    knob.addEventListener("wheel", (e) => {
      e.preventDefault();
      if (linkDown() || !e.deltaY) return;
      const dir = e.deltaY < 0 ? 1 : -1;
      spin(dir, 1);
      move(dir, 1);
    }, { passive: false });
    knob.addEventListener("keydown", (e) => {
      if (linkDown()) return;
      const k = { ArrowUp: 1, ArrowRight: 1, PageUp: 10, ArrowDown: -1, ArrowLeft: -1, PageDown: -10 }[e.key];
      if (!k) return;
      e.preventDefault();
      spin(k > 0 ? 1 : -1, Math.abs(k));
      move(k > 0 ? 1 : -1, Math.abs(k));
    });
    S.upd.push(() => {
      for (const b of stepBtns) setAttr(b, "aria-pressed", String(Number(b.dataset.step) === S.step));
      setAttr(knob, "aria-valuenow", String(cur()));
      setAttr(knob, "aria-valuetext", `${formatFreq(cur())} Hz, step ${STEP_NAMES[S.step]}`);
    });
    return h("section", { class: "card vfo-box vfo-dial-box" }, knob, stepBox,
      h("p", { class: "muted hint" }, "Drag or scroll the dial, or tap the frequency to type one."));
  };

  sectionBuilders.bands = () => {
    const box = h("div", { class: "vfo-bands", role: "group", "aria-label": "Bands" });
    const btns = S.bands.map((b) => {
      const e = ctl(h("button", { class: "btn small", type: "button", "aria-pressed": "false", onclick: () => {
        const rem = Number(load("band:" + b.name));
        tuneTo(rem >= b.lo && rem <= b.hi ? rem : b.start);
      } }, b.name));
      e.dataset.band = b.name;
      box.append(e);
      return e;
    });
    S.upd.push(() => {
      const on = bandOf(shown("freq", S.st && S.st.freq), S.bands);
      for (const e of btns) setAttr(e, "aria-pressed", String(!!on && on.name === e.dataset.band));
    });
    return h("section", { class: "card vfo-box" }, h("h3", {}, "Band"), box);
  };

  sectionBuilders.modes = () => {
    const box = h("div", { class: "vfo-modes", role: "group", "aria-label": "Mode" });
    const btns = modeButtons(S.nc.modes).map((m) => {
      const e = ctl(h("button", { class: "btn small", type: "button", "aria-pressed": "false", onclick: () => { intent("mode", { mode: m.mode }, "mode", m.mode); } }, m.label));
      e.dataset.mode = m.mode;
      box.append(e);
      return e;
    });
    S.upd.push(() => {
      const cur = shown("mode", S.st && S.st.mode);
      for (const e of btns) {
        setAttr(e, "aria-pressed", String(cur === e.dataset.mode));
        e.classList.toggle("is-pending", pending("mode") && cur === e.dataset.mode);
      }
    });
    return h("section", { class: "card vfo-box" }, h("h3", {}, "Mode"), box);
  };

  sectionBuilders.vfo = () => {
    const hid = hiddenNames(S.nc);
    const kids = [];
    const toggles = [];
    if (!hid.has("VFO")) {
      for (const v of ["VFOA", "VFOB"]) {
        const e = ctl(h("button", { class: "btn small", type: "button", "aria-pressed": "false", onclick: () => { intent("vfo", { vfo: v }, "vfo", v); } }, v.replace("VFO", "VFO ")));
        e.dataset.vfo = v;
        toggles.push(e);
        kids.push(e);
      }
    }
    let split = null;
    if (!hid.has("SPLIT")) {
      split = ctl(h("button", { class: "btn small", type: "button", "aria-pressed": "false", onclick: () => {
        const on = !shown("split", S.st && S.st.split);
        intent("split", { on }, "split", on);
      } }, "SPLIT"));
      kids.push(split);
    }
    S.upd.push(() => {
      const cur = shown("vfo", S.st && S.st.vfo);
      for (const e of toggles) setAttr(e, "aria-pressed", String(cur === e.dataset.vfo));
      if (split) setAttr(split, "aria-pressed", String(!!shown("split", S.st && S.st.split)));
    });
    return h("section", { class: "card vfo-box" }, h("h3", {}, "VFO"), h("div", { class: "vfo-vfos" }, kids));
  };

  sectionBuilders.filter = () => {
    const box = h("section", { class: "card vfo-box" }, h("h3", {}, "Filter width"));
    const label = h("span", { class: "vfo-val" });
    const slider = ctl(h("input", { type: "range", step: "50", min: "500", max: "3600", "aria-label": "Filter width in hertz" }));
    const presets = h("div", { class: "vfo-presets" });
    const note = h("p", { class: "muted hint" }, "This mode has no filter width to set.");
    box.append(h("div", { class: "vfo-row" }, slider, label), presets, note);
    let mode = null;
    let busy = false;
    const send = (hz) => intent("mode", { mode: shown("mode", S.st && S.st.mode), passband: hz }, "passband", String(hz));
    slider.addEventListener("pointerdown", () => { busy = true; });
    slider.addEventListener("pointerup", () => { busy = false; });
    slider.addEventListener("pointercancel", () => { busy = false; });
    slider.addEventListener("input", () => { busy = true; setText(label, `${slider.value} Hz`); });
    slider.addEventListener("change", () => { busy = false; send(Number(slider.value)); });
    S.upd.push(() => {
      const m = shown("mode", S.st && S.st.mode);
      const has = filterPresets(m).length > 0;
      box.hidden = false;
      slider.hidden = !has;
      label.hidden = !has;
      presets.hidden = !has;
      note.hidden = has;
      if (m !== mode) {
        mode = m;
        const [lo, hi] = filterSpan(m);
        slider.min = String(lo);
        slider.max = String(hi);
        presets.replaceChildren(...filterPresets(m).map((p) => ctl(h("button", { class: "btn small", type: "button", onclick: () => send(p) }, String(p)))));
      }
      const pb = Number(shown("passband", S.st && S.st.passband));
      if (has && !busy && pb > 0) {
        if (Number(slider.value) !== pb) slider.value = String(pb);
        setText(label, `${pb} Hz`);
      } else if (has && !busy) {
        setText(label, "—");
      }
    });
    return box;
  };

  sectionBuilders.levels = () => {
    const box = h("section", { class: "card vfo-box" }, h("h3", {}, "Levels"));
    const rows = [];
    for (const n of LEVEL_ORDER) {
      if (!S.nc.settableLevels.includes(n)) continue;
      const val = h("span", { class: "vfo-val" });
      const slider = ctl(h("input", { type: "range", min: "0", max: "100", step: "1", "aria-label": LEVEL_LABEL[n] }));
      let busy = false;
      let timer = null;
      const send = () => { timer = null; intent("level", { name: n, value: Number(slider.value) / 100 }, "lvl:" + n, Number(slider.value) / 100); };
      slider.addEventListener("pointerdown", () => { busy = true; });
      slider.addEventListener("pointerup", () => { busy = false; });
      slider.addEventListener("pointercancel", () => { busy = false; });
      slider.addEventListener("input", () => {
        busy = true;
        setText(val, `${slider.value}%`);
        if (timer === null) timer = setTimeout(send, 150);
      });
      slider.addEventListener("change", () => {
        busy = false;
        if (timer !== null) clearTimeout(timer);
        send();
      });
      rows.push({ n, slider, val, busy: () => busy });
      box.append(h("label", { class: "vfo-row" }, h("span", { class: "vfo-lab" }, LEVEL_LABEL[n]), slider, val));
    }
    S.upd.push(() => {
      for (const r of rows) {
        const v = shown("lvl:" + r.n, S.st && S.st.levels ? S.st.levels[r.n] : undefined);
        if (v === undefined || r.busy()) continue;
        const pct = levelPercent(v);
        if (Number(r.slider.value) !== pct) r.slider.value = String(pct);
        setText(r.val, `${pct}%`);
        r.val.classList.toggle("is-pending", pending("lvl:" + r.n));
      }
    });
    return box;
  };

  sectionBuilders.smeter = () => {
    const hid = hiddenNames(S.nc);
    const m = h("meter", { min: "0", max: "15", value: "0", "aria-label": "S-meter" });
    const t = h("span", { class: "vfo-val" });
    const extra = [];
    const bars = [];
    for (const [key, label, lo, hi] of [["SWR", "SWR", 1, 3], ["ALC", "ALC", 0, 1]]) {
      if (hid.has(key)) continue;
      const bar = h("meter", { min: String(lo), max: String(hi), value: String(lo), "aria-label": label });
      const txt = h("span", { class: "vfo-val" });
      bars.push({ key, bar, txt, lo, hi });
      extra.push(h("div", { class: "vfo-row" }, h("span", { class: "vfo-lab" }, label), bar, txt));
    }
    S.upd.push(() => {
      const db = S.st && S.st.meters ? S.st.meters.STRENGTH : undefined;
      setAttr(m, "value", String(sUnits(db)));
      setText(t, db === undefined ? "—" : sText(db));
      for (const b of bars) {
        const v = S.st && S.st.meters ? S.st.meters[b.key] : undefined;
        setAttr(b.bar, "value", String(v === undefined ? b.lo : Math.max(b.lo, Math.min(b.hi, v))));
        setText(b.txt, v === undefined ? "—" : v.toFixed(b.key === "SWR" ? 1 : 2));
      }
    });
    return h("section", { class: "card vfo-box" }, h("h3", {}, "Signal"), h("div", { class: "vfo-row" }, h("span", { class: "vfo-lab" }, "S"), m, t), extra);
  };

  // needsOwner E ON: E waits for this tab to have the transmitter (the paint pass keeps it disabled)
  function needsOwner(e, on) {
    e.dataset.needs = on ? "1" : "";
    if (on) setAttr(e, "title", "Take the transmitter first");
    else e.removeAttribute("title");
  }

  const mine = () => !!(S.owner && S.owner.kind === "tab" && S.owner.mine);

  sectionBuilders.atu = () => {
    const b = ctl(h("button", { class: "btn small", type: "button", onclick: () => {
      dxb.api("POST", `/api/radio/${name}/atu`, claimBody()).then((res) => { if (!(res && res.ok)) say(failText(res || {}), "bad"); });
    } }, "ATU"));
    S.upd.push(() => needsOwner(b, !mine()));
    return h("section", { class: "card vfo-box" }, h("h3", {}, "Antenna tuner"), b);
  };

  function tuneEnd(send) {
    if (S.tune && S.tune.timer) clearInterval(S.tune.timer);
    S.tune = null;
    if (send) dxb.api("POST", `/api/radio/${name}/tune`, { on: false, device: dxb.tab() }).then((res) => { if (!(res && res.ok)) say(failText(res || {}), "bad"); });
    paint();
  }

  sectionBuilders.tune = () => {
    const b = ctl(h("button", { class: "btn small", type: "button" }, "Tune"));
    b.addEventListener("click", () => {
      if (S.tune) { tuneEnd(true); return; }
      dxb.api("POST", `/api/radio/${name}/tune`, { on: true, device: dxb.tab() }).then((res) => {
        if (!(res && res.ok)) { say(failText(res || {}), "bad"); return; }
        S.tune = { until: Date.now() + 10000, since: Date.now(), timer: setInterval(() => {
          if (Date.now() >= S.tune.until) tuneEnd(false); else paint();
        }, 250) };
        paint();
      });
    });
    S.upd.push(() => {
      if (S.tune) {
        const left = Math.max(0, Math.ceil((S.tune.until - Date.now()) / 1000));
        setText(b, `Stop tuning (${left} s)`);
      } else {
        setText(b, "Tune");
      }
      needsOwner(b, !mine() && !S.tune);
    });
    return h("section", { class: "card vfo-box" }, h("h3", {}, "Tune"), b,
      h("p", { class: "muted hint" }, "Sends a carrier for up to 10 seconds."));
  };

  sectionBuilders.stop = () => {
    const b = h("button", { class: "btn danger vfo-stop", type: "button" }, "STOP");
    b.addEventListener("click", () => {
      tuneEnd(false);
      dxb.api("POST", `/api/radio/${name}/stop`, {}).then((res) => { if (!(res && res.ok)) say(failText(res || {}), "bad"); });
    });
    return h("section", { class: "card vfo-box vfo-stop-box" }, b);
  };

  // the owner and program sections are redrawn when what they say changes
  sectionBuilders.owner = () => h("section", { class: "card vfo-box", id: "vfo-owner" });
  sectionBuilders.program = () => h("section", { class: "card vfo-box", id: "vfo-program" });

  function programLink() {
    const owner = S.rec && S.rec.owner;
    if (!owner || !/^[a-z][a-z0-9-]*$/.test(owner)) return null;
    const label = S.owner ? S.owner.label : owner;
    if (typeof dashOpenLink === "function" && typeof last !== "undefined" && last && last.radios && last.radios.ok && last.radios.radios[name]) {
      const a = dashOpenLink(last, name, last.radios.radios[name], label);
      if (a) return a;
    }
    return h("a", { class: "btn small primary", href: `/screen#${owner}-${name}`, target: "_blank", rel: "noopener" }, `Open ${label}`);
  }

  function paintOwner() {
    const o = S.owner;
    const key = JSON.stringify([o, S.rec && S.rec.owner]);
    const box = document.getElementById("vfo-owner");
    const prog = document.getElementById("vfo-program");
    if (S.ownerKey === key) return;
    S.ownerKey = key;
    if (box) {
      const acts = ownerActions(o).map((a) => {
        if (a === "claim") return h("button", { class: "btn small primary", type: "button", onclick: () => ownerCall("claim") }, "Take the transmitter");
        if (a === "release") return h("button", { class: "btn small", type: "button", onclick: () => ownerCall("release") }, "Release");
        if (a === "takeover") return h("button", { class: "btn small", type: "button", onclick: () => ownerCall("takeover") }, "Take over");
        return null;
      });
      box.replaceChildren(h("p", { class: "vfo-owner" }, ownerLine(o)), h("div", { class: "actions" }, acts));
    }
    if (prog) prog.replaceChildren(h("div", { class: "actions" }, programLink()));
  }

  sectionBuilders.why = () => {
    const rows = S.nc.hidden.map((x) => h("li", {}, h("strong", {}, HIDDEN_LABEL[x.name] || x.name), `: ${x.why}`));
    return h("details", { class: "card vfo-box vfo-why" }, h("summary", {}, "Why isn't something here?"), h("ul", {}, rows));
  };

  function build() {
    const lay = layoutFor(S.caps, S.rec, S.owner);
    const key = lay.join(",") + "|" + (S.nc ? S.nc.modes.join(",") + S.nc.ranges.length : "");
    if (key === S.layout) return;
    S.layout = key;
    S.ownerKey = "";
    S.upd = [];
    S.bands = S.nc ? bandsFromRanges(S.nc.ranges) : [];
    root.replaceChildren(h("div", { class: "vfo-head" },
      h("h2", {}, (S.rec && S.rec.label) || name, S.nc && S.nc.modelName ? h("span", { class: "muted" }, ` ${S.nc.modelName}`) : null),
      h("span", { class: "chip", id: "vfo-chip", hidden: true }, "Reconnecting…")),
      ...lay.map((s) => sectionBuilders[s]()));
  }

  // ---- keypad --------------------------------------------------------------------------------
  let keypad = null;
  function openKeypad() {
    if (!keypad) {
      const input = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", "aria-label": "Frequency", class: "vfo-keyin" });
      const err = h("p", { class: "error", hidden: true });
      const pad = h("div", { class: "vfo-pad" });
      for (const k of ["7", "8", "9", "4", "5", "6", "1", "2", "3", ".", "0", "⌫"]) {
        pad.append(h("button", { class: "btn", type: "button", onclick: () => {
          input.value = k === "⌫" ? input.value.slice(0, -1) : input.value + k;
        } }, k));
      }
      const form = h("form", { novalidate: true },
        h("h2", {}, "Frequency"), input, pad, err,
        h("p", { class: "muted hint" }, "14.074 is MHz, 14074 is kHz, 14074000 is Hz."),
        h("div", { class: "actions" },
          h("button", { class: "btn", type: "button", onclick: () => keypad.close() }, "Cancel"),
          h("button", { class: "btn primary", type: "submit" }, "Tune")));
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        const hz = parseFreq(input.value);
        const rs = S.nc ? S.nc.ranges : [];
        if (hz === null) { err.textContent = "Type a frequency such as 14.074"; err.hidden = false; return; }
        if (rs.length && clampFreq(hz, rs) !== hz) { err.textContent = `${formatFreq(hz)} Hz is outside what this radio tunes.`; err.hidden = false; return; }
        err.hidden = true;
        keypad.close();
        tuneTo(hz);
      });
      keypad = h("dialog", { "aria-label": "Type a frequency" }, form);
      keypad.input = input;
      document.body.append(keypad);
    }
    keypad.input.value = "";
    if (keypad.showModal) keypad.showModal();
    keypad.input.focus();
  }

  // ---- painting ------------------------------------------------------------------------------
  function paint() {
    paintOwner();
    for (const f of S.upd) f();
    const down = linkDown();
    root.classList.toggle("is-down", down);
    const chip = document.getElementById("vfo-chip");
    if (chip) chip.hidden = !down;
    const hasCtl = !!S.st && !!S.st.connected;
    for (const e of root.querySelectorAll(".vfo-ctl")) {
      // ATU and Tune are also held back until this tab has the transmitter (set by their updaters)
      if (down || !hasCtl || e.dataset.needs) e.setAttribute("disabled", ""); else e.removeAttribute("disabled");
    }
  }

  // ---- data ----------------------------------------------------------------------------------
  function loadAll() {
    return dxb.api("GET", `/api/radio/${name}`).then((j) => {
      if (j && j.ok === false) {
        if (/no such radio/.test(j.reason || "")) {
          S.found = false;
          root.replaceChildren(h("section", { class: "card vfo-box" }, h("p", {}, `There is no radio called ${name}.`), h("a", { class: "btn small", href: "/" }, "Back to the dashboard")));
          S.layout = "";
          return;
        }
        say(failText(j), "bad");
        return;
      }
      S.rec = j.radio || S.rec;
      S.caps = j.caps || S.caps;
      S.nc = normCaps(S.caps);
      S.owner = j.owner || null;
      if (j.state && !S.st) S.st = j.state;
      S.found = true;
      build();
      paint();
    });
  }

  function onRig(m) {
    if (m.radio !== name || !m.data) return;
    S.st = m.data;
    const opt = m.data.optimistic || {};
    for (const k of Object.keys(S.pend)) {
      const f = k === "passband" ? "mode" : k.startsWith("lvl:") ? k.slice(4) : OPT_KEY[k] || k;
      if (!opt[f] && !(k === "freq" && S.want !== null)) delete S.pend[k];
    }
    if (S.tune && Date.now() - S.tune.since > 1500 && !m.data.ptt) tuneEnd(false);
    paint();
  }

  function watch() { dxb.events.send({ type: "watch", radio: name, level: "page" }); }

  function init() {
    // the shell shows this view in place of the dashboard
    for (const id of ["dashboard", "cards", "settings", "updates", "programs", "logs", "terminal", "top"]) {
      const e = document.getElementById(id);
      if (e) e.hidden = true;
    }
    for (const a of document.querySelectorAll("#menu-list a")) {
      const href = a.getAttribute("href");
      if (href && href.startsWith("#")) a.setAttribute("href", "/" + href);
    }
    const saved = Number(load("step"));
    if (STEPS.includes(saved)) S.step = saved;
    root.append(h("p", { class: "muted" }, "Reading the radio…"));
    dxb.events.on("rig", onRig);
    dxb.events.onOpen(() => { S.sock = true; watch(); loadAll(); });
    dxb.events.onClose(() => { S.sock = false; paint(); });
    if (dxb.events.open) { S.sock = true; watch(); }
    loadAll();
    // who holds the transmitter is not on the events stream; ask now and then
    setInterval(() => { if (!dxb.hidden && S.found) loadAll(); }, 5000);
    dxb.onVisible(() => { loadAll(); });
  }

  init();
})();
