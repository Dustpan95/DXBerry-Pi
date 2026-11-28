"use strict";
const assert = require("assert/strict");

// Exercise the shipped program page, dock, and monitor against read-only CAT fixtures.
module.exports = async function radioMonitor(context, base) {
  const page = await context.newPage();
  const errors = [], writes = [];
  page.on("pageerror", e => errors.push(e.message));
  page.on("request", r => {
    if (r.url().includes("/api/radio/") && r.method() !== "GET") writes.push(r.method());
  });
  let serial = 0, keyed = false, socket;
  const report = radio => JSON.stringify({type:"rig", radio, data:{
    connected:true, answering:true, updated_at:new Date(1700000000000 + ++serial).toISOString(),
    freq:14074700, vfo:"VFOA", mode:"PKTUSB", passband:"3000", ptt:keyed, ptt_known:true,
    meters:{VD_METER:13.8, ID_METER:0, SWR:1.5, ALC:0.2, RFPOWER_METER_WATTS:12}
  }});
  await page.route("**/api/radio/radio1", r => r.fulfill({json:{radio:{name:"radio1",label:"Test transceiver"},
    caps:{CanGetPTT:true,CanGetVFO:true,ReadableMeters:["VD_METER","ID_METER","ALC","SWR","RFPOWER_METER_WATTS"]}}}));
  await page.route("**/api/cli/dxberry-app", r => r.fulfill({json:{exit:0,stdout:JSON.stringify({
    state:"running",access:true,radio:"radio1",label:"WSJT-X",socket:"fixture"
  })}}));
  await page.route("**/vendor/novnc/core/rfb.js", r => r.fulfill({contentType:"text/javascript",
    body:"export default class RFB extends EventTarget { disconnect() {} }"}));
  await page.routeWebSocket("**/api/vnc/*", () => {});
  await page.routeWebSocket("**/api/events", ws => {
    socket = ws;
    ws.onMessage(data => {
      const msg = JSON.parse(data);
      assert.deepEqual(msg,{type:"watch",radio:"radio1",level:"page"});
      ws.send(report("radio1"));
      ws.send(report("radio1"));
    });
  });
  try {
    await page.setViewportSize({width:390,height:844});
    await page.goto(base + "/screen#wsjtx-radio1");
    await page.getByRole("button",{name:"Radio status"}).click();
    const panel = page.frameLocator("#hud-dock iframe");
    await panel.getByText("13.8 V",{exact:true}).waitFor();
    await panel.getByText("0.0 A",{exact:true}).waitFor();
    await panel.getByText("14.074700",{exact:true}).waitFor();
    assert.match(await panel.locator("#meter-note").innerText(), /temperature/);
    const swr = panel.locator("dl div").filter({has:panel.getByText("SWR",{exact:true})}).locator("dd");
    assert.equal(await swr.innerText(),"—");
    keyed = true;
    socket.send(report("other"));
    assert.equal(await swr.innerText(),"—");
    socket.send(report("radio1"));
    await panel.getByText("1.50:1",{exact:true}).waitFor();
    const screen = await page.locator("#screen-area").boundingBox(), dock = await page.locator("#hud-dock").boundingBox();
    assert(dock.y >= screen.y + screen.height - 1 && screen.height > 250);
    await page.setViewportSize({width:1200,height:800});
    const wide = await page.locator("#screen-area").boundingBox();
    assert((await page.locator("#hud-dock").boundingBox()).x >= wide.x + wide.width - 1);
    const popup = page.waitForEvent("popup");
    await panel.getByRole("link",{name:/Pop out/}).click();
    const detached = await popup;
    assert.equal(new URL(detached.url()).hash,"#radio1");
    await detached.close();
    socket.close();
    await panel.getByText("Connecting…",{exact:true}).waitFor();
    assert.equal(await panel.locator("#frequency").innerText(),"—");
    await page.getByRole("button",{name:"Radio status"}).click();
    assert.equal(await page.locator("#hud-dock iframe").count(),0);
    assert.deepEqual(errors,[]);
    assert.deepEqual(writes,[]);
  } finally { await page.close(); }
};
