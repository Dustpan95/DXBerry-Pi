// Read-only native status logic, independent of browser wall-clock agreement.
"use strict";
const fs = require("fs");
const vm = require("vm");
const assert = require("assert/strict");
class ParseOnlyDate extends Date { static now() { throw new Error("Browser wall clock used for freshness"); } }
const context = { Date: ParseOnlyDate, JSON, Math, Number, String, Array, Object, console };
context.window = context;
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), context);
const api = context.dxbWSJTX;
const stamp = "2026-11-18T12:00:00.000123456Z";
const options = { radio: "radio1", connected: true, connectedAt: 100, receivedAt: 100, now: 100 };
function frame() {
  return { type: "program", program: "wsjtx", radio: "radio1", sent_at: stamp, data: {
    version: 1, available: true, decodes_omitted: 0,
    state: { configured: true, enabled: true, release_paused: false, recovery_required: false,
      observation: { Radio: "radio1", Generation: 1, Epoch: 1, Availability: "live", Schema: 3,
        LastSeen: stamp, StatusSeenAt: stamp, Status: { DialFrequency: 14074000, Mode: "FT8", Transmitting: false, TXEnabled: true },
        Decodes: [{ Data: { TimeMS: 0, SNR: 0, DeltaTime: 0, DeltaFrequency: 0, Text: "CQ N0CALL DM00" }, SeenLive: true }], Counters: {} } }
  } };
}
let checks = 0;
function test(name, run) { run(); checks++; console.log("ok: " + name); }
test("UTC time-of-day preserves midnight and refuses absent/invalid values", () => {
  assert.equal(api.utcTime(0), "00:00:00");
  assert.equal(api.utcTime(86399999), "23:59:59");
  for (const bad of [null, undefined, "0", -1, 86400000, NaN]) assert.equal(api.utcTime(bad), "—");
});
test("server timestamps normalize fractional seconds without browser Date.now", () => {
  assert.equal(api.serverTime(stamp), Date.parse("2026-11-18T12:00:00.000Z"));
  assert.equal(api.serverTime("2026-11-18T06:00:00-06:00"), api.serverTime(stamp));
  assert(Number.isNaN(api.serverTime(null)));
});
test("current reports retain explicit zero and false values", () => {
  const view = api.model(frame(), options);
  assert.equal(view.kind, "live");
  assert.equal(view.status.Transmitting, false);
  assert.equal(view.decodes[0].Data.TimeMS, 0);
  assert.equal(view.decodes[0].Data.SNR, 0);
});
test("protocol expiry clears readings/history at the exact boundary", () => {
  assert.equal(api.model(frame(), { ...options, now: 45099 }).kind, "live");
  const view = api.model(frame(), { ...options, now: 45100 });
  assert.equal(view.kind, "stale");
  assert.equal(view.status, null);
  assert.equal(view.decodes.length, 0);
});
test("heartbeat liveness does not refresh the last on-change status report", () => {
  const value = frame();
  value.sent_at = "2026-11-18T12:02:00Z";
  value.data.state.observation.LastSeen = value.sent_at;
  const view = api.model(value, options);
  assert.equal(view.kind, "live");
  assert.equal(view.statusAge, 120000);
  assert.equal(view.status.DialFrequency, 14074000);
});
test("missing protocol time is unavailable rather than invented freshness", () => {
  const value = frame(); value.data.state.observation.LastSeen = null;
  assert.equal(api.model(value, options).kind, "unavailable");
});
test("heartbeat before the first status leaves readings unknown", () => {
  const value = frame(); value.data.state.observation.Status = null;
  const view = api.model(value, options);
  assert.equal(view.kind, "live"); assert.equal(view.status, null);
});
test("initial and explicit loading states have a bounded timeout", () => {
  assert.equal(api.model(null, options).kind, "loading");
  assert.equal(api.model(null, { ...options, now: 8100 }).kind, "unavailable");
  const value = frame(); value.data = { version: 1, available: false, code: "loading" };
  assert.equal(api.model(value, { ...options, now: 8100 }).kind, "unavailable");
});
test("disconnect immediately removes current readings and history", () => {
  const view = api.model(frame(), { ...options, connected: false });
  assert.equal(view.kind, "disconnected"); assert.equal(view.status, null); assert.equal(view.decodes.length, 0);
});
test("wrong radio and unsupported event/program formats are refused", () => {
  let value = frame(); value.data.state.observation.Radio = "radio2";
  assert.equal(api.model(value, options).kind, "unavailable");
  value = frame(); value.data.version = 2;
  assert.equal(api.model(value, options).kind, "unsupported");
  value = frame(); value.data.state.observation.Schema = 4;
  assert.equal(api.model(value, options).kind, "unsupported");
});
test("default-off, paused and recovery states stay distinct", () => {
  const value = frame(); value.data.state.observation = null;
  value.data.state.enabled = false;
  assert.equal(api.model(value, options).kind, "off");
  value.data.state.release_paused = true;
  assert.equal(api.model(value, options).kind, "paused");
  value.data.state.recovery_required = true;
  assert.equal(api.model(value, options).kind, "recovery");
});
test("release pause retains an existing current receiver with visible guidance", () => {
  const value = frame(); value.data.state.release_paused = true;
  const view = api.model(value, options);
  assert.equal(view.kind, "live"); assert.equal(view.decodes.length, 1); assert.equal(view.warnings.length, 1);
});
test("closed and unsupported waiting sessions never expose old readings", () => {
  const value = frame(); value.data.state.observation.Availability = "closed";
  assert.equal(api.model(value, options).kind, "closed");
  value.data.state.observation.Availability = "waiting";
  value.data.state.observation.Counters.UnsupportedSchema = 1;
  assert.equal(api.model(value, options).kind, "unsupported");
});
test("oversized and ambiguous histories are refused", () => {
  const value = frame(); value.data.state.observation.Decodes = Array(201).fill({ Data: {} });
  assert.equal(api.model(value, options).kind, "unavailable");
  value.data.state.observation.Decodes = [];
  value.data.decodes_omitted = 201;
  assert.equal(api.model(value, options).kind, "unavailable");
});
test("observed transport loss is disclosed without replacing current reports", () => {
  const value = frame(); value.data.state.transport = { queue_drops: 3 };
  assert.match(api.model(value, options).warnings.join(" "), /3 program updates/);
  value.data.state.transport.failed = true;
  assert.equal(api.model(value, options).kind, "unavailable");
});
test("malformed collection flags do not coerce to consent or availability", () => {
  const value = frame(); value.data.state.enabled = "true";
  assert.equal(api.model(value, options).kind, "unavailable");
});
test("controller accepts only matching connected observations and never mutates", () => {
  const view = api.create({ radio: "radio1", h: () => { throw new Error("Unexpected DOM work"); }, retry: () => { throw new Error("Unexpected refresh"); }, formatFrequency: String, now: () => 100 });
  assert.equal(view.receive(frame()), false);
  view.connection(true);
  assert.equal(view.receive({ ...frame(), radio: "radio2" }), false);
  assert.equal(view.receive({ ...frame(), program: "js8call" }), false);
  assert.equal(view.receive(frame()), true);
  view.tick(); view.connection(false);
  assert.equal(view.receive(frame()), false);
});
console.log(`${checks} native status checks passed`);
