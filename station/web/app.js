/* The station app's shell and the layer the page scripts run on: window.dxb.
 * dxb.run sends a command to the station's command endpoint and answers the way the page scripts
 * expect: the standard output on success, a rejection carrying message, exit_status and problem
 * otherwise. Text from the Pi goes on the page as text nodes, never as HTML. */
"use strict";

(() => {
  // The same pattern the server applies to a unit name before it lets journalctl read it.
  const UNIT = /^(dxberry-[a-z0-9@._-]+|rigctld@[a-z0-9-]+|graywolf[a-z0-9-]*|gpsd|chrony|nginx|php[0-9.]*-fpm|mariadb)\.(service|timer|socket)$/;
  const LOG_LINES = "300";

  const byId = (id) => document.getElementById(id);

  // fail MESSAGE EXIT PROBLEM: a rejection shaped like the one a failed command gives the page
  function fail(message, exit, problem) {
    return {
      message,
      exit_status: exit,
      problem,
      toString() { return exit ? `Exit ${exit}: ${message}` : message; },
    };
  }

  // The page scripts name a command by its path on the Pi; the endpoint takes the bare name.
  const commandName = (arg) => String(arg).split("/").pop();

  function run(args, input) {
    return fetch("/api/cli/" + encodeURIComponent(commandName(args[0])), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ args: args.slice(1), stdin: input }),
    }).then(
      (r) => {
        if (r.status === 401) {
          location.assign("/login?next=" + encodeURIComponent(location.pathname + location.search));
          return new Promise(() => {});
        }
        if (r.status !== 200 && r.status !== 400) return Promise.reject(fail(`The station answered HTTP ${r.status}.`, null, "unreachable"));
        return r.json().then(
          (body) => {
            if (r.status === 400) return Promise.reject(fail(String(body.error || "refused"), null, "refused"));
            if (body.exit === 0) return body.stdout;
            return Promise.reject(fail(body.stderr, body.exit, null));
          },
          () => Promise.reject(fail("The station's answer could not be read.", null, "unreachable")));
      },
      (e) => Promise.reject(fail(String((e && e.message) || e), null, "unreachable")));
  }

  // ---- the log panel ----------------------------------------------------------------------
  let logUnit = null;

  function loadLog() {
    const unit = logUnit;
    const text = byId("logs-text");
    const err = byId("logs-error");
    err.hidden = true;
    return run(["journalctl", "-u", unit, "-n", LOG_LINES, "--no-pager", "-o", "short-iso"]).then(
      (out) => { if (unit === logUnit) text.textContent = out; },
      (ex) => {
        if (unit !== logUnit) return;
        text.textContent = "";
        err.textContent = `The log could not be read. ${(ex && ex.message) || ""}`.trim();
        err.hidden = false;
      });
  }

  function openLog(unit) {
    byId("logs-title").textContent = `Log of ${unit}`;
    byId("logs").hidden = false;
    if (!UNIT.test(String(unit))) {
      logUnit = null;
      byId("logs-text").textContent = "";
      byId("logs-error").textContent = "The station does not show the log of this unit.";
      byId("logs-error").hidden = false;
      return Promise.resolve();
    }
    logUnit = unit;
    byId("logs-text").textContent = "Reading…";
    if (byId("logs").scrollIntoView) byId("logs").scrollIntoView();
    return loadLog();
  }

  function closeLog() {
    logUnit = null;
    byId("logs").hidden = true;
    byId("logs-text").textContent = "";
  }

  // ---- the dxb object ---------------------------------------------------------------------
  const dxb = {
    run,
    runJSON: (args, input) => run(args, input).then(JSON.parse),
    ws: (path) => new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + path),
    get hidden() { return document.hidden; },
    onVisible(fn) { document.addEventListener("visibilitychange", () => { if (!document.hidden) fn(); }); },
    logout() {
      const done = () => location.assign("/login");
      return fetch("/api/auth/logout", { method: "POST", credentials: "same-origin" }).then(done, done);
    },
    openLog,
  };
  window.dxb = dxb;

  // ---- the shell --------------------------------------------------------------------------
  function tickClock() {
    const d = new Date();
    const p = (n) => String(n).padStart(2, "0");
    byId("utc").textContent = `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())} UTC`;
  }

  function setMenu(open) {
    byId("menu-list").hidden = !open;
    byId("menu").setAttribute("aria-expanded", open ? "true" : "false");
  }

  byId("menu").addEventListener("click", () => setMenu(byId("menu-list").hidden));
  byId("menu-list").addEventListener("click", () => setMenu(false));
  byId("logout").addEventListener("click", () => dxb.logout());
  byId("logs-refresh").addEventListener("click", () => { if (logUnit) loadLog(); });
  byId("logs-close").addEventListener("click", closeLog);
  tickClock();
  setInterval(tickClock, 1000);
})();
