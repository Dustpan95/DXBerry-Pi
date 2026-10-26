// Per-radio station settings on the Radios page: the SWR limit and the PTT timeout the transmit
// guards of the next release read (this one only stores them), and the "Re-probe capabilities"
// action. dxberry.js puts section(name) into the radio's edit form; the fields save by
// themselves (they are not part of the dxberry-radio form).
"use strict";
(function () {
  function call(method, path, body) {
    const opt = { method, credentials: "same-origin" };
    if (body !== undefined) {
      opt.headers = { "Content-Type": "application/json" };
      opt.body = JSON.stringify(body);
    }
    return fetch(path, opt).then(r => {
      if (r.status === 401 || r.status === 403) return Promise.reject({ login: true });
      return r.json();
    });
  }

  function node(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    for (const k of kids) if (k) e.append(k);
    return e;
  }

  function numberField(id, label, attrs, hint) {
    const input = node("input", Object.assign({ id, type: "number" }, attrs));
    return { input, box: node("div", { class: "field" }, node("label", { for: id }, label), input, node("p", { class: "hint muted" }, hint)) };
  }

  // refusal: the words for an {ok:false} answer, or for anything that is not one.
  function refusal(out) {
    if (out && out.reason) return out.fix ? `${out.reason}: ${out.fix}.` : `${out.reason}.`;
    return "The station did not accept that.";
  }

  // failed: the words for a request that did not come back as an answer.
  const failed = ex => (ex && ex.login ? "Your session has ended; log in again." : "Could not reach the station.");

  const NO_GUARD = "no SWR guard: this radio does not report SWR over CAT";

  function section(name) {
    const base = "/api/radio/" + encodeURIComponent(name);
    const swr = numberField("rs-swr", "SWR limit", { min: "1.5", max: "10", step: "0.1", value: "3" },
      "Used by the transmit guards in the next release; this release does not watch SWR yet.");
    const ptt = numberField("rs-ptt", "PTT timeout (seconds)", { min: "30", max: "600", step: "1", value: "180" },
      "Used by the transmit guards in the next release; this release does not time transmissions yet.");
    const msg = node("p", { id: "rs-msg", class: "hint muted", role: "status" });
    const say = (text, bad) => { msg.textContent = text; msg.setAttribute("class", bad ? "error" : "hint muted"); };
    const save = node("button", { id: "rs-save", type: "button" }, "Save station settings");
    const probe = node("button", { id: "rs-reprobe", type: "button" }, "Re-probe capabilities");
    const probed = node("p", { id: "rs-probed", class: "hint muted" });
    const guard = node("p", { id: "rs-guard", class: "hint muted" });

    const load = () => call("GET", base + "/settings").then(s => {
      if (typeof s.swr_limit === "number") swr.input.value = String(s.swr_limit);
      if (typeof s.ptt_timeout_s === "number") ptt.input.value = String(s.ptt_timeout_s);
      // the station says whether the radio has an SWR guard at all: it needs SWR readable over CAT
      guard.textContent = s.swr_guard === false ? NO_GUARD : "";
      probed.textContent = s.probed ? `Capabilities last read ${s.probed}.` : "Capabilities not re-read since setup.";
    }, ex => say(ex && ex.login ? failed(ex) : "Could not read the station settings.", true));
    load();

    const doSave = () => {
      const body = { swr_limit: Number(swr.input.value), ptt_timeout_s: Number(ptt.input.value) };
      call("POST", base + "/settings", body).then(
        out => say(out && out.ok ? "Station settings saved." : refusal(out), !(out && out.ok)),
        ex => say(failed(ex), true));
    };
    save.addEventListener("click", doSave);
    // Enter in a number field saves these settings; it must not submit the radio form around them
    for (const f of [swr.input, ptt.input]) {
      f.addEventListener("keydown", e => {
        if (e.key !== "Enter") return;
        e.preventDefault();
        doSave();
      });
    }
    probe.addEventListener("click", () => {
      say("Reading the radio...", false);
      call("POST", base + "/reprobe", {}).then(out => {
        if (out && out.ok) { say("Capabilities re-read.", false); load(); } else say(refusal(out), true);
      }, ex => say(failed(ex), true));
    });

    return node("fieldset", { class: "station-settings" }, node("legend", {}, "Station settings"),
      swr.box, guard, ptt.box, node("div", { class: "actions" }, save, probe), probed, msg);
  }

  window.dxbRadioSettings = { section };
})();
