/* Read-only WSJT-X observations. The full program remains the control surface. */
"use strict";

(() => {
  const FRESH_MS = 45000;
  const INITIAL_WAIT_MS = 8000;
  const ROW_STEP = 20;
  const MAX_ROWS = 200;
  const finite = (v) => typeof v === "number" && Number.isFinite(v);
  const integer = (v) => Number.isSafeInteger(v);

  // Normalize Go's fractional seconds before parsing; no browser clock offset
  // is used. A program's decode time-of-day goes through utcTime, never Date.
  function serverTime(value) {
    if (typeof value !== "string") return NaN;
    const m = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)$/.exec(value);
    return m ? Date.parse(`${m[1]}.${(m[2] || "").padEnd(3, "0").slice(0, 3)}${m[3]}`) : NaN;
  }

  function utcTime(ms) {
    if (!integer(ms) || ms < 0 || ms >= 86400000) return "—";
    const seconds = Math.floor(ms / 1000);
    return [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60].map((n) => String(n).padStart(2, "0")).join(":");
  }

  function ageOf(value, sentAt, receivedAt, now) {
    const age = serverTime(sentAt) - serverTime(value);
    return finite(age) && finite(receivedAt) && finite(now) ? Math.max(0, age) + Math.max(0, now - receivedAt) : Infinity;
  }

  function stateView(kind, label, message, tone = "muted") {
    return { kind, label, message, tone, status: null, statusAge: null, decodes: [], warnings: [], omitted: 0, transport: null };
  }

  function model(frame, options) {
    const { connected, connectedAt, receivedAt, now, radio } = options;
    if (!connected) return stateView("disconnected", "Reconnecting…", "Waiting for the station connection. Current program readings are unavailable.", "warn");
    const waiting = () => now - (frame ? receivedAt : connectedAt) < INITIAL_WAIT_MS
      ? stateView("loading", "Reading status…", "Waiting for WSJT-X status from the station.")
      : stateView("unavailable", "Status unavailable", "No status update arrived. Retry or use the full WSJT-X window.", "warn");
    if (!frame) return waiting();
    const data = frame.data;
    if (!data || data.version !== 1) return stateView("unsupported", "Unsupported status", "This view cannot read the station's status format. Use the full WSJT-X window.", "warn");
    if (data.available !== true) {
      if (data.code === "loading") return waiting();
      return stateView("unavailable", "Status unavailable", "Program status cannot be displayed right now. Retry or use the full WSJT-X window.", "warn");
    }
    const s = data.state;
    if (!s || ["configured", "enabled", "release_paused", "recovery_required"].some((key) => typeof s[key] !== "boolean")) {
      return stateView("unavailable", "Status unavailable", "The station did not provide a complete collection state. Use the full WSJT-X window.", "warn");
    }
    if (s.recovery_required) return stateView("recovery", "Collection needs recovery", "Collection settings need review. Use the full WSJT-X window in the meantime.", "warn");
    const obs = s.observation;
    if (s.release_paused && !obs) return stateView("paused", "Paused for maintenance", "New status collection is paused. The full program window remains available.", "warn");
    if ((!s.configured || !s.enabled) && !obs) return stateView("off", "Native status is off", "To see mode, dial frequency and band activity here, stop WSJT-X from the radio card and review its status destination.");
    if (!obs) return stateView("waiting", "Waiting for WSJT-X", "No program updates are available yet.");
    if (obs.Radio !== radio || !integer(obs.Generation) || !integer(obs.Epoch)) return stateView("unavailable", "Status unavailable", "The station's program session could not be verified.", "warn");
    if ((obs.Schema && ![2, 3].includes(obs.Schema)) || (obs.Availability === "live" && ![2, 3].includes(obs.Schema)) || (obs.Availability === "waiting" && obs.Counters && obs.Counters.UnsupportedSchema > 0)) {
      return stateView("unsupported", "Unsupported program data", "Incoming WSJT-X data is not supported by this view. Use the full program window.", "warn");
    }
    if (obs.Availability === "closed") return stateView("closed", "WSJT-X disconnected", "The program stopped reporting to this session. Use the full window or wait for a new session.", "warn");
    if (s.transport && s.transport.failed) return stateView("unavailable", "Collection interrupted", "The station stopped receiving program updates. Use the full program window.", "warn");
    if (obs.Availability === "waiting") return stateView("waiting", "Waiting for WSJT-X", "No supported program updates have arrived for this session.");
    const protocolAge = ageOf(obs.LastSeen, frame.sent_at, receivedAt, now);
    if (obs.Availability === "live" && !finite(protocolAge)) return stateView("unavailable", "Freshness unavailable", "The program's update time could not be verified. Use the full program window.", "warn");
    if (obs.Availability === "stale" || (obs.Availability === "live" && protocolAge >= FRESH_MS)) {
      return stateView("stale", "Program updates stopped", "Waiting for new program updates. Current readings and band activity have been cleared.", "warn");
    }
    if (obs.Availability !== "live") return stateView("unavailable", "Status unavailable", "The station did not provide a current program observation.", "warn");
    const history = obs.Decodes == null ? [] : obs.Decodes;
    if (!Array.isArray(history) || history.length > MAX_ROWS || !integer(data.decodes_omitted) || data.decodes_omitted < 0 || data.decodes_omitted > MAX_ROWS) {
      return stateView("unavailable", "Activity unavailable", "The program activity could not be displayed. Use the full WSJT-X window.", "warn");
    }
    // WSJT-X sends Status on changes, not on a guaranteed periodic cadence.
    // Keep the last report with its own age while protocol liveness continues;
    // a heartbeat never makes that report new or verifies current RF output.
    const statusAge = ageOf(obs.StatusSeenAt, frame.sent_at, receivedAt, now);
    const status = obs.Status && finite(statusAge) ? obs.Status : null;
    const view = stateView("live", status ? "Updates received" : "Waiting for a status report", status
      ? "Mode, frequency and transmit state are reported by WSJT-X. RF output is not verified here."
      : "Program updates are arriving. Waiting for a fresh mode and frequency report.", status ? "good" : "warn");
    view.status = status;
    view.statusAge = status ? statusAge : null;
    view.decodes = history;
    view.omitted = data.decodes_omitted;
    view.transport = s.transport || null;
    if (s.release_paused) {
      view.warnings.push("New collection is paused for maintenance; this session is still reporting.");
      view.label = "Updates received · collection paused";
      view.tone = "warn";
    }
    if (!s.enabled) view.warnings.push("Collection is disabled for future sessions; this session is still reporting.");
    if (view.transport && integer(view.transport.queue_drops) && view.transport.queue_drops > 0) view.warnings.push(`${view.transport.queue_drops} program updates were dropped before processing.`);
    return view;
  }

  function create({ radio, h, retry, configure, formatFrequency, now = () => performance.now() }) {
    let target = null, ui = null, frame = null, receivedAt = 0;
    let connected = false, connectedAt = now(), count = ROW_STEP, epoch = "", historyKey = "";

    function receive(message) {
      if (!message || message.type !== "program" || message.program !== "wsjtx" || message.radio !== radio || !connected) return false;
      frame = message;
      receivedAt = now();
      const observation = message.data && message.data.state && message.data.state.observation;
      const key = observation ? `${observation.Generation}:${observation.Epoch}` : "";
      if (key !== epoch) { epoch = key; count = ROW_STEP; }
      paint();
      return true;
    }

    function connection(on) {
      connected = !!on;
      connectedAt = now();
      frame = null;
      receivedAt = 0;
      count = ROW_STEP;
      epoch = "";
      paint();
    }

    function refresh() {
      if (!connected) return;
      frame = null;
      connectedAt = now();
      paint();
      retry();
    }

    function mount(element) {
      if (target === element) return;
      target = element;
      ui = null;
      historyKey = "";
      if (!target) { frame = null; count = ROW_STEP; epoch = ""; return; }
      const status = h("span", { class: "badge muted", role: "status" });
      const retryButton = h("button", { class: "btn small", type: "button", onclick: refresh }, "Refresh status");
      const message = h("p", { class: "muted wsjtx-message" });
      let fingerprint = "";
      const feedback = h("p", { class: "hint", role: "status" });
      const setup = h("button", { class: "btn small", type: "button", onclick: async () => {
        const approved = fingerprint;
        fingerprint = "";
        setup.disabled = true;
        try {
          const reply = await configure(approved ? "enable" : "preview", approved ? { fingerprint: approved } : {});
          if (reply.ok === false) feedback.textContent = [reply.reason, reply.fix].filter(Boolean).join(" ");
          else if (approved && reply.ok === true) {
            feedback.textContent = "Collection enabled. Start WSJT-X from the radio card.";
            retry();
          } else if (reply.radio === radio && /^[a-f0-9]{64}$/.test(reply.fingerprint) && reply.settings?.Current) {
            const route = reply.settings.Current;
            const server = route.Server.Present ? route.Server.Value : "default host";
            const port = route.Port.Present ? route.Port.Value : "default port";
            feedback.textContent = `WSJT-X currently sends status to ${server}:${port}. While it runs, DXBerry will receive that stream instead. Stopping WSJT-X restores this destination. The secondary logging destination stays separate.`;
            fingerprint = reply.fingerprint;
          } else feedback.textContent = "Could not verify the status destination. Review it again.";
        } catch (_) { feedback.textContent = "Could not reach the station. Review the destination again."; }
        finally {
          setup.disabled = false;
          setup.textContent = fingerprint ? "Enable status collection" : "Review status destination";
        }
      } }, "Review status destination");
      const frequency = h("dd", { class: "wsjtx-frequency" }, "—");
      const mode = h("dd", { class: "wsjtx-mode" }, "—");
      const transmit = h("p", { class: "hint wsjtx-transmit" });
      const reported = h("p", { class: "muted hint wsjtx-reported" });
      const warnings = h("div", { class: "wsjtx-warnings" });
      const body = h("tbody");
      const empty = h("p", { class: "muted wsjtx-empty" });
      const table = h("table", { class: "wsjtx-table" },
        h("caption", { class: "sr-only" }, "WSJT-X band activity, newest received first"),
        h("colgroup", {}, ...["utc", "snr", "dt", "hz", "message"].map((name) => h("col", { class: `wsjtx-col-${name}` }))),
        h("thead", {}, h("tr", {}, ...["UTC", "dB", "Δt (s)", "Hz", "Message"].map((text) => h("th", { scope: "col" }, text)))), body);
      const region = h("div", { class: "wsjtx-activity", role: "region", "aria-label": "WSJT-X band activity", tabindex: "0" }, empty, table);
      const range = h("p", { class: "muted hint" });
      const announce = h("span", { class: "sr-only", role: "status" });
      const more = h("button", { class: "btn small", type: "button", onclick: () => {
        count = Math.min(MAX_ROWS, count + ROW_STEP);
        paint();
        announce.textContent = range.textContent;
        if (more.disabled) region.focus({ preventScroll: true });
      } }, "Show older decodes");
      target.replaceChildren(
        h("div", { class: "wsjtx-heading" }, h("h3", { tabindex: "-1" }, "WSJT-X status"), status, h("span", { class: "muted hint" }, "Read-only"), retryButton),
        h("div", { class: "wsjtx-summary" }, message, setup, feedback, warnings,
          h("dl", { class: "wsjtx-readouts" }, h("div", {}, h("dt", {}, "Last reported dial · Hz"), frequency), h("div", {}, h("dt", {}, "Last reported mode"), mode)), transmit, reported),
        h("div", { class: "wsjtx-heading wsjtx-activity-heading" }, h("h4", {}, "Band activity"), h("span", { class: "muted hint" }, "Newest received first")),
        region, h("div", { class: "wsjtx-footer" }, range, more, announce));
      ui = { status, retryButton, message, setup, feedback, frequency, mode, transmit, reported, warnings, body, empty, table, region, range, more };
      paint();
    }

    function decodeRow(entry) {
      const data = entry && entry.Data && typeof entry.Data === "object" ? entry.Data : {};
      const notes = [];
      if (typeof data.Mode === "string" && data.Mode) notes.push(data.Mode);
      if (entry && entry.SeenReplay) notes.push(entry.SeenLive ? "Live + replay" : "Replay");
      if (data.LowConfidence === true) notes.push("Low confidence");
      if (data.OffAir === true) notes.push("Recorded audio");
      const text = typeof data.Text === "string" ? data.Text || "Empty message" : "Message unavailable";
      return h("tr", {},
        h("td", {}, utcTime(data.TimeMS)),
        h("td", {}, integer(data.SNR) ? String(data.SNR) : "—"),
        h("td", {}, finite(data.DeltaTime) ? data.DeltaTime.toFixed(1) : "—"),
        h("td", {}, integer(data.DeltaFrequency) && data.DeltaFrequency >= 0 ? String(data.DeltaFrequency) : "—"),
        h("td", { class: "wsjtx-message-cell" }, h("span", { class: "wsjtx-decode-text" }, text), notes.length ? h("span", { class: "muted hint wsjtx-decode-notes" }, notes.join(" · ")) : null));
    }

    function paint() {
      if (!target || !ui) return;
      const view = model(frame, { radio, connected, connectedAt, receivedAt, now: now() });
      target.dataset.state = view.kind;
      if (ui.status.textContent !== view.label) ui.status.textContent = view.label;
      ui.status.className = `badge ${view.tone}`;
      ui.message.textContent = view.message;
      ui.setup.hidden = !configure || view.kind !== "off" || !frame?.data?.state?.configured;
      if (view.kind === "off" && !frame.data.state.configured)
        ui.message.textContent = "Station setup is required. On the Pi, run sudo /opt/dxberry/bin/dxberryd -wsjtx-initialize once, then refresh status.";
      ui.retryButton.disabled = !connected;
      const st = view.status;
      ui.frequency.textContent = st && integer(st.DialFrequency) && st.DialFrequency >= 0 ? formatFrequency(st.DialFrequency) : "—";
      ui.mode.textContent = st && typeof st.Mode === "string" && st.Mode ? st.Mode : "—";
      ui.transmit.textContent = st && typeof st.Transmitting === "boolean"
        ? `${st.Transmitting ? "Last report: transmitting" : "Last report: no transmission"}${typeof st.TXEnabled === "boolean" ? st.TXEnabled ? " · TX enabled (reported)" : " · TX disabled (reported)" : ""}`
        : "Transmit report unavailable";
      ui.reported.textContent = view.statusAge === null ? "Status report time unavailable" : `Status reported ${Math.floor(view.statusAge / 1000).toLocaleString("en")} s ago`;
      ui.warnings.replaceChildren(...view.warnings.map((text) => h("p", { class: "hint" }, text)));
      const shown = view.decodes.slice(-count).reverse();
      const key = JSON.stringify([epoch, shown]);
      if (key !== historyKey) {
        const scroll = ui.region.scrollTop;
        ui.body.replaceChildren(...shown.map(decodeRow));
        ui.region.scrollTop = scroll;
        historyKey = key;
      }
      ui.table.hidden = shown.length === 0;
      ui.empty.hidden = shown.length > 0;
      ui.empty.textContent = view.kind === "live" ? "No decodes in the current activity list." : "Band activity will appear when current program updates are available.";
      ui.region.setAttribute("aria-busy", view.kind === "loading" ? "true" : "false");
      ui.range.textContent = shown.length ? `Showing ${shown.length} of ${view.decodes.length} available decodes.${view.omitted ? ` ${view.omitted} older decodes did not fit in this update.` : ""}` : "No decodes displayed.";
      const focused = document.activeElement === ui.more;
      ui.more.disabled = view.decodes.length <= shown.length;
      if (focused && ui.more.disabled) ui.region.focus({ preventScroll: true });
    }

    return { receive, connection, mount, tick: paint };
  }

  window.dxbWSJTX = { FRESH_MS, INITIAL_WAIT_MS, ROW_STEP, MAX_ROWS, serverTime, utcTime, ageOf, model, create };
})();
