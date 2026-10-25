// Loads station/web/vfo.js with only a window (no document, so the page itself stays out) and
// prints what its pure logic answers, as one JSON object.
// Run: node tests/station_vfo.js station/web/vfo.js
"use strict";
const fs = require("fs");
const vm = require("vm");

const ctx = { JSON, Math, Number, String, Array, Object, Set, Date };
ctx.window = ctx;
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
const v = ctx.dxbVFO;

// the IC-7300 as the station's wire carries it (Go field names, no tags)
const ic7300 = {
  ModelName: "IC-7300", Modes: ["AM", "CW", "USB", "LSB", "RTTY", "FM", "CWR", "RTTYR", "PKTLSB", "PKTUSB", "PKTFM"],
  VFOs: ["VFOA", "VFOB"], CanSetSplit: true, CanSetVFO: true, CanSetPTT: true,
  SettableLevels: ["AF", "RF", "SQL", "RFPOWER"], SettableFuncs: ["TUNER", "NB"],
  Hidden: [{ Name: "ALC", Kind: "meter", Why: "this radio does not report ALC over CAT" }],
  FreqRanges: [{ Lo: 30000, Hi: 74800000 }],
};
// the same in lower-case names
const lower = { model_name: "IC-7300", modes: ["USB"], freq_ranges: [{ lo: 30000, hi: 74800000 }], hidden: [{ name: "ALC", kind: "meter", why: "x" }], settable_funcs: [] };
const dummy = {
  ModelName: "Dummy", Modes: ["USB"], FreqRanges: [{ Lo: 0, Hi: 1000000000 }],
  Hidden: [{ Name: "STRENGTH", Kind: "meter", Why: "rig control is Hamlib's dummy model: this radio has no CAT" }],
};
const ranges = [{ lo: 1800000, hi: 2000000 }, { lo: 14000000, hi: 14350000 }];

const out = {
  fmt: [14074000, 7074000, 500, 0, 1296100000, 144174000, -5, NaN].map(v.formatFreq),
  parse: ["14.074", "14074", "14074000", "14.074.000", "7.074.5", "abc", "", "144", "1.296"].map(v.parseFreq),
  stepUp: v.stepFreq(14074000, 1000, 1, ranges),
  stepDown: v.stepFreq(14074000, 100, -1, ranges),
  stepBig: v.stepFreq(14074000, 1000000, 1, ranges),
  stepTop: v.stepFreq(14350000, 10, 1, ranges),
  stepBottom: v.stepFreq(14000000, 10, -1, ranges),
  stepGap: v.stepFreq(8000000, 10, 1, ranges),
  stepNoRanges: v.stepFreq(5, 10, -1, []),
  bands: v.bandsFromRanges(ic7300.FreqRanges.map((r) => ({ lo: r.Lo, hi: r.Hi }))).map((b) => b.name),
  bandsNormalized: v.bandsFromRanges(v.normCaps(ic7300).ranges).length,
  bandsPartial: v.bandsFromRanges([{ lo: 14100000, hi: 14200000 }]).map((b) => [b.name, b.start]),
  bandsLower: v.bandsFromRanges(v.normCaps(lower).ranges).length,
  bandOf: [v.bandOf(14074000, v.bandsFromRanges(ranges)), v.bandOf(10000000, v.bandsFromRanges(ranges))].map((b) => (b ? b.name : null)),
  modes: v.modeButtons(ic7300.Modes).map((m) => m.mode),
  modesNone: v.modeButtons(["FM"]).map((m) => m.mode),
  layoutIC: v.layoutFor(ic7300, { has_cat: true }, null),
  layoutNoCapsYet: v.layoutFor(null, { has_cat: true }, null),
  layoutDummy: v.layoutFor(dummy, { has_cat: true }, null),
  layoutNoCatRecord: v.layoutFor(null, { has_cat: false }, { kind: "program", label: "Graywolf", mine: false }),
  layoutLower: v.layoutFor(lower, { has_cat: true }, null),
  hiddenMeterGone: v.layoutFor({ ModelName: "X", Modes: ["USB"], FreqRanges: [{ Lo: 1, Hi: 2 }], Hidden: [{ Name: "STRENGTH", Kind: "meter", Why: "w" }, { Name: "SPLIT", Kind: "control", Why: "w" }, { Name: "VFO", Kind: "control", Why: "w" }] }, { has_cat: true }, null),
  owners: {
    none: v.ownerLine(null),
    mine: v.ownerLine({ kind: "tab", label: "iPad", mine: true }),
    other: v.ownerLine({ kind: "tab", label: "iPad", mine: false }),
    program: v.ownerLine({ kind: "program", label: "WSJT-X", mine: false }),
  },
  actions: [null, { kind: "tab", mine: true }, { kind: "tab", mine: false }, { kind: "program" }].map(v.ownerActions),
  s: [v.sText(-54), v.sText(-6), v.sText(0), v.sText(22), v.sText(60)],
  sUnits: [v.sUnits(-120), v.sUnits(0), v.sUnits(60), v.sUnits(200)],
  filters: [v.filterPresets("USB"), v.filterPresets("CW"), v.filterPresets("FM")],
  pathOk: v.radioFromPath("/radio/ic7300"),
  pathBad: ["/radio/Bad", "/radio/", "/radio/a/b", "/radios/x", "/"].map(v.radioFromPath),
  percent: [v.levelPercent(0.5), v.levelPercent(2), v.levelPercent(-1)],
  steps: v.STEPS,
};
console.log(JSON.stringify(out));
