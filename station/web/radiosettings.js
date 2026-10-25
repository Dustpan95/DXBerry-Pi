// Per-radio station settings on the Radios page: the SWR limit and the PTT timeout the transmit
// guards read, and the "Re-probe capabilities" action. dxberry.js puts section(name) into the
// radio's edit form; the fields save by themselves (they are not part of the dxberry-radio form).
"use strict";
(function () {
  function call(method, path, body) {
    const opt = { method, credentials: "same-origin" };
    if (body !== undefined) {
      opt.headers = { "Content-Type": "application/json" };
      opt.body = JSON.stringify(body);
    }
    return fetch(path, opt).then(r => r.json());
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

  function section(name) {
    const base = "/api/radio/" + encodeURIComponent(name);
    const swr = numberField("rs-swr", "SWR limit", { min: "1.5", max: "10", step: "0.1", value: "3" },
      "Transmitting stops when the SWR stays above this (1.5 to 10).");
    const ptt = numberField("rs-ptt", "PTT timeout (seconds)", { min: "30", max: "600", step: "1", value: "180" },
      "Transmitting stops after this long (30 to 600).");
    const msg = node("p", { id: "rs-msg", class: "hint muted", role: "status" });
    const say = (text, bad) => { msg.textContent = text; msg.setAttribute("class", bad ? "error" : "hint muted"); };
    const save = node("button", { id: "rs-save", type: "button" }, "Save station settings");
    const probe = node("button", { id: "rs-reprobe", type: "button" }, "Re-probe capabilities");
    const probed = node("p", { id: "rs-probed", class: "hint muted" });

    const load = () => call("GET", base + "/settings").then(s => {
      if (typeof s.swr_limit === "number") swr.input.value = String(s.swr_limit);
      if (typeof s.ptt_timeout_s === "number") ptt.input.value = String(s.ptt_timeout_s);
      probed.textContent = s.probed ? `Capabilities last read ${s.probed}.` : "Capabilities not re-read since setup.";
    }, () => say("Could not read the station settings.", true));
    load();

    save.addEventListener("click", () => {
      const body = { swr_limit: Number(swr.input.value), ptt_timeout_s: Number(ptt.input.value) };
      call("POST", base + "/settings", body).then(
        out => say(out && out.ok ? "Station settings saved." : refusal(out), !(out && out.ok)),
        () => say("Could not reach the station.", true));
    });
    probe.addEventListener("click", () => {
      say("Reading the radio...", false);
      call("POST", base + "/reprobe", {}).then(out => {
        if (out && out.ok) { say("Capabilities re-read.", false); load(); } else say(refusal(out), true);
      }, () => say("Could not reach the station.", true));
    });

    return node("fieldset", { class: "station-settings" }, node("legend", {}, "Station settings"),
      swr.box, ptt.box, node("div", { class: "actions" }, save, probe), probed, msg);
  }

  window.dxbRadioSettings = { section };
})();
