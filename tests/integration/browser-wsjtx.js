"use strict";
const fs = require("fs");
const vm = require("vm");
const path = require("path");
const assert = require("assert/strict");
const { chromium } = require("playwright");
const base = "http://127.0.0.1:8090";
const output = process.env.DXB_BROWSER_OUTPUT || "/tmp/dxberry-wsjtx-browser";
fs.mkdirSync(output, { recursive: true });
if (process.argv[2] === "status") {
  const smoke = fs.readFileSync(path.join(__dirname, "..", "page_smoke.js"), "utf8");
  const source = smoke.slice(smoke.indexOf("const status ="), smoke.indexOf("const models ="));
  const report = JSON.parse(JSON.stringify(vm.runInNewContext(source + "\nstatus;")));
  report.generated = new Date().toISOString();
  report.station = { ok: true, callsign: "N0CALL" };
  report.radios.radios.radio1 = { ...report.radios.radios.radio1, label: "Fixture radio", owner: "wsjtx", present: true };
  report.apps = { ok: true, programs: [{ name: "wsjtx", label: "WSJT-X", installed: true, version: "3.0.2", kind: "radio", source: "upstream", sessions: [{ radio: "radio1" }] }], logbook: {} };
  fs.writeFileSync(path.join(output, "status.json"), JSON.stringify(report));
  process.exit(0);
}

function state(rows = 60, epoch = 1) {
  const stamp = new Date().toISOString();
  return { configured: true, enabled: true, release_paused: false, recovery_required: false, phase: "installed",
    transport: { received: rows + 1, queue_drops: 0, non_loopback: 0, wrong_peer: 0, failed: false },
    observation: { Radio: "radio1", Generation: 1, Epoch: epoch, Availability: "live", Schema: 3, LastSeen: stamp, StatusSeenAt: stamp,
      Status: { DialFrequency: 14074000, Mode: "FT8", Transmitting: false, TXEnabled: true }, Counters: {},
      Decodes: Array.from({ length: rows }, (_, i) => ({ FirstSeen: stamp, LastSeen: stamp, SeenLive: true, SeenReplay: false,
        Data: { TimeMS: i * 15000, SNR: -18 + i % 20, DeltaTime: 0.2, DeltaFrequency: 600 + i, Mode: "FT8", Text: `CQ N0TEST ${i}`, LowConfidence: false, OffAir: false } })) } };
}
async function setState(value, extra = {}) {
  const res = await fetch(base + "/fixture/native", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ state: value, ...extra }) });
  assert(res.ok, "Fixture state rejected");
}
async function frame(value) {
  const res = await fetch(base + "/fixture/native-frame", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(value) });
  assert(res.ok, "Fixture frame rejected");
}
function contrast(a, b) {
  const light = (rgb) => rgb.match(/[\d.]+/g).slice(0, 3).map(Number).map(v => v / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  const x = light(a), y = light(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

(async () => {
  let browser, page;
  const errors = [], mutations = [];
  const evidence = [];
  try {
    browser = await chromium.launch({ executablePath: process.env.DXB_CHROMIUM || "/usr/bin/chromium", headless: true, args: ["--no-sandbox"] });
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    page = await context.newPage();
    await page.clock.install();
    await page.addInitScript(() => {
      window.fixtureWatches = [];
      const Original = window.WebSocket;
      window.WebSocket = class extends Original {
        constructor(...args) { super(...args); if (String(args[0]).includes("/api/events")) window.fixtureEventsSocket = this; }
        send(raw) { try { window.fixtureWatches.push(JSON.parse(raw)); } catch {} return super.send(raw); }
      };
    });
    page.on("pageerror", e => errors.push(e.message));
    page.on("request", r => {
      const u = new URL(r.url());
      if (u.pathname.startsWith("/api/radio/") && r.method() !== "GET") mutations.push(r.method() + " " + u.pathname);
    });
    // Deliberately stale status metadata must not redirect the fresh API's
    // WSJT-X program link to the old Graywolf owner.
    await page.route("**/api/cli/dxberry-status", async route => {
      const response = await route.fetch();
      const reply = await response.json();
      const report = JSON.parse(reply.stdout);
      report.radios.radios.radio1.owner = "graywolf";
      reply.stdout = JSON.stringify(report);
      await route.fulfill({ response, json: reply });
    });
    const value = state();
    await setState(value);
    await page.goto(base + "/radio/radio1");
    const panel = page.locator("#wsjtx-status");
    const waitKind = async kind => { await page.waitForFunction(k => document.querySelector("#wsjtx-status")?.dataset.state === k, kind); };
    const textIs = async (selector, text) => page.waitForFunction(({ selector, text }) => document.querySelector(selector)?.textContent === text, { selector, text });
    await waitKind("live");
    assert.equal(await page.title(), "Fixture radio — DXBerry");
    await textIs(".wsjtx-frequency", "14.074.000");
    assert.equal(await page.locator("#vfo-program").getByRole("link", { name: "Open WSJT-X", exact: true }).getAttribute("href"), "/screen#wsjtx-radio1");
    assert.equal(await panel.locator("tbody tr").count(), 20);
    assert.equal(await panel.locator(".wsjtx-decode-text").first().innerText(), "CQ N0TEST 59");
    evidence.push("current radio-bound status, complete full-window link and newest-first UTC activity");

    const stopColors = async () => page.getByRole("button", { name: "STOP", exact: true }).evaluate(e => { const s = getComputedStyle(e); return [s.color, s.backgroundColor]; });
    let colors = await stopColors();
    assert(contrast(...colors) >= 4.5, "Light STOP contrast");
    await panel.screenshot({ path: path.join(output, "native-desktop.png") });
    await page.screenshot({ path: path.join(output, "radio-desktop.png"), fullPage: true });
    const more = panel.getByRole("button", { name: "Show older decodes", exact: true });
    const region = panel.getByRole("region", { name: "WSJT-X band activity" });
    const height = await region.evaluate(e => e.getBoundingClientRect().height);
    await more.focus(); await page.keyboard.press("Enter");
    assert.equal(await panel.locator("tbody tr").count(), 40);
    await page.keyboard.press("Enter");
    assert.equal(await panel.locator("tbody tr").count(), 60);
    assert(await more.isDisabled());
    assert.equal(await region.evaluate(e => e === document.activeElement), true);
    assert.equal(await region.evaluate(e => e.getBoundingClientRect().height), height);
    await region.press("End");
    await page.waitForFunction(() => document.querySelector(".wsjtx-activity").scrollTop > 0, null, { timeout: 3000 });
    evidence.push("keyboard history expansion, boundary focus and fixed scroll ownership");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
    colors = await stopColors(); assert(contrast(...colors) >= 4.5, "Dark STOP contrast");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    assert.notEqual(await region.evaluate(e => getComputedStyle(e).scrollbarColor), "auto");
    await panel.screenshot({ path: path.join(output, "native-phone-dark.png") });
    await page.emulateMedia({ forcedColors: "active" });
    assert.equal(await region.evaluate(e => getComputedStyle(e).scrollbarColor), "auto");
    await page.emulateMedia({ forcedColors: "none" });
    evidence.push("phone/dark/reduced-motion, shared scrollbars, forced colors and emergency-label contrast");

    const frozen = state(1, 2); frozen.observation.Decodes[0].Data.Text = "FROZEN";
    await setState(frozen, { freeze: true });
    await textIs(".wsjtx-decode-text", "FROZEN");
    await page.clock.fastForward(50000);
    await waitKind("stale");
    assert.equal(await page.locator(".wsjtx-frequency").innerText(), "—");
    assert.equal(await panel.locator("tbody tr").count(), 0);
    evidence.push("idle expiry clears readings with no new events and no synchronized browser wall clock");

    const oldReport = state(0, 3); oldReport.observation.StatusSeenAt = new Date(Date.now() - 300000).toISOString();
    await setState(oldReport); await waitKind("live");
    await textIs(".wsjtx-frequency", "14.074.000");
    assert.match(await panel.locator(".wsjtx-reported").innerText(), /Status reported 3\d\d s ago/);
    assert.match(await panel.innerText(), /No decodes in the current activity list/);
    evidence.push("quiet sessions remain distinct from lost program updates; status age survives heartbeats");

    const paused = state(1, 4); paused.release_paused = true;
    await setState(paused);
    await page.waitForFunction(() => document.querySelector(".wsjtx-warnings")?.textContent.includes("paused for maintenance"));
    assert.equal(await panel.getAttribute("data-state"), "live");
    paused.observation = null;
    await setState(paused); await waitKind("paused");
    paused.recovery_required = true;
    await setState(paused); await waitKind("recovery");
    await setState({ configured: false, enabled: false, release_paused: false, recovery_required: false });
    await waitKind("off");
    evidence.push("default-off, maintenance with/without a receiver and recovery states");

    const closed = state(1, 5); closed.observation.Availability = "closed";
    await setState(closed); await waitKind("closed");
    assert.equal(await panel.locator("tbody tr").count(), 0);
    const unsupported = state(1, 6); unsupported.observation.Schema = 4;
    await setState(unsupported); await waitKind("unsupported");
    await setState(state(), { fail: true }); await waitKind("unavailable");
    assert(!(await panel.innerText()).includes("private fixture"));
    const loading = { type: "program", program: "wsjtx", radio: "radio1", sent_at: new Date().toISOString(), data: { version: 1, available: false, code: "loading" } };
    await frame(loading); await waitKind("loading");
    await page.clock.fastForward(9000); await waitKind("unavailable");
    await panel.getByRole("button", { name: "Refresh status", exact: true }).click();
    await waitKind("unavailable");
    evidence.push("closed/unsupported/error states, private-error filtering and bounded loading/refresh");

    const long = state(200, 7);
    const literal = '<img src=x onerror="window.fixtureInjected=true">';
    long.observation.Decodes.forEach(row => { row.Data.Text = literal + "A".repeat(4096 - literal.length); });
    await setState(long); await waitKind("live");
    await page.waitForFunction(() => document.querySelector(".wsjtx-decode-text")?.textContent.startsWith("<img"));
    assert.equal(await panel.locator("img").count(), 0);
    assert.equal(await page.evaluate(() => window.fixtureInjected), undefined);
    assert.match(await panel.locator(".wsjtx-footer").innerText(), /older decodes did not fit/);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    assert.equal(await region.evaluate(e => e.getBoundingClientRect().height), height);
    evidence.push("literal long message text, bounded complete history and explicit omissions");

    await setState(state(1, 8));
    await page.waitForFunction(() => document.querySelector(".wsjtx-decode-text")?.textContent === "CQ N0TEST 0");
    await page.evaluate(() => window.fixtureEventsSocket.close());
    await waitKind("disconnected");
    assert.equal(await page.locator(".wsjtx-frequency").innerText(), "—");
    assert.equal(await panel.locator("tbody tr").count(), 0);
    await waitKind("live");
    evidence.push("disconnect clears cached readings; reconnect revalidates the current session");

    await page.unroute("**/api/cli/dxberry-status");
    await page.goto(base + "/");
    await page.waitForFunction(() => typeof last !== "undefined" && last?.radios?.ok);
    const showIdentity = async (callsign, grid) => page.evaluate(([callsign, grid]) => {
      dashRender({ ...last, station: { ok: true, callsign, grid } });
    }, [callsign, grid]);
    await showIdentity("VK2ABC", "QF56od");
    await page.waitForSelector(".dash-atlas.is-projected canvas");
    assert.equal(await page.locator("#dashboard-title").innerText(), "VK2ABC");
    assert.match(await page.locator(".dash-position").innerText(), /33.85° S.*151.21° E/);
    await showIdentity("", "");
    assert.equal(await page.locator("#dashboard-title").innerText(), "Station overview");
    assert.equal(await page.locator(".dash-atlas canvas").count(), 0);
    await page.evaluate(() => dashRender(last));
    const radio = page.locator('.dash-radio[data-radio="radio1"]');
    await radio.locator("summary").click();
    await page.evaluate(() => dashRender(last));
    assert.equal(await radio.locator("details").evaluate(e => e.open), true);
    await page.goto(base + "/#programs");
    await page.waitForSelector("#programs:visible");
    assert.equal(await page.locator("#dashboard").isVisible(), false);
    await page.goBack();
    await page.waitForSelector("#dashboard:visible");
    await page.getByRole("link", { name: "WSJT-X status for radio1", exact: true }).click();
    await waitKind("live");
    assert.equal(new URL(page.url()).hash, "#wsjtx-status");
    await page.waitForFunction(() => document.activeElement === document.querySelector("#wsjtx-status h3"));
    const headingTop = await panel.locator("h3").evaluate(e => e.getBoundingClientRect().top);
    assert(headingTop >= 0 && headingTop < 250, "Deep link reveals the status heading");
    await page.evaluate(() => scrollTo(0, 0));
    await setState(state(2, 9));
    await page.waitForFunction(() => document.querySelector(".wsjtx-decode-text")?.textContent === "CQ N0TEST 1");
    assert.equal(await page.evaluate(() => scrollY), 0, "New data must not repeat the anchor jump");
    evidence.push("dashboard status link reveals and focuses the phone panel once; later updates preserve page position");

    await require("./browser-radio-monitor")(context, base);
    evidence.push("read-only program radio monitor, RX/TX, phone dock, desktop and pop-out");

    const fixtureState = await (await fetch(base + "/fixture/state")).json();
    assert.equal(fixtureState.keyed, false);
    assert.equal(fixtureState.ptt, false);
    assert.deepEqual(mutations, []);
    assert.equal(await page.evaluate(() => window.fixtureWatches.every(m => m.type === "watch")), true);
    assert.deepEqual(errors, []);
    evidence.push("zero radio/collection mutation requests or transmitter keying");
    fs.writeFileSync(path.join(output, "checks.json"), JSON.stringify({ passed: evidence, errors, mutations }, null, 2));
    evidence.forEach(text => console.log("ok: " + text));
  } catch (error) {
    console.error(error.stack);
    if (page) await page.screenshot({ path: path.join(output, "failure.png"), fullPage: true }).catch(() => {});
    process.exitCode = 1;
  } finally { if (browser) await browser.close(); }
})();
