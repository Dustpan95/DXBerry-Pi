"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const { chromium } = require("playwright");

async function test() {
  const origin = "https://dxberry.local";
  const password = fs.readFileSync(0, "utf8");
  const browser = await chromium.launch({ executablePath: "/usr/bin/chromium", headless: true });
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    const loginPage = await page.goto("http://dxberry.local/");
    const tls = await loginPage.securityDetails();
    assert.equal(tls.issuer, "Fixture-CA");
    assert.match(tls.protocol, /^TLS/);
    assert.equal(new URL(page.url()).origin, origin);
    assert.equal(new URL(page.url()).pathname, "/login");
    const check = () => page.evaluate(async () => (await fetch("/api/auth/check")).status);
    assert.equal(await check(), 401);
    await page.getByLabel("User", { exact: true }).fill("fixtureadmin");
    await page.getByLabel("Password", { exact: true }).fill(password);
    await Promise.all([
      page.waitForURL(origin + "/"),
      page.getByRole("button", { name: "Log in" }).click(),
    ]);
    assert.equal(await check(), 200);
    const session = (await context.cookies()).find(cookie => cookie.name === "dxb_session");
    assert.ok(session && session.secure && session.httpOnly && session.sameSite === "Strict");
    assert.equal(session.domain, "dxberry.local");
    assert.equal(session.path, "/");
    assert.equal(await page.evaluate(() => document.cookie.includes("dxb_session=")), false);
    await page.reload();
    assert.equal(await check(), 200);
    await Promise.all([
      page.waitForURL(origin + "/login"),
      page.getByRole("button", { name: "Log out" }).click(),
    ]);
    assert.equal(await check(), 401);
    assert.equal((await context.cookies()).some(cookie => cookie.name === "dxb_session"), false);
    console.log("PASS browser login, authenticated reload and logout through the verified station TLS front end");
  } finally {
    await browser.close();
  }
}

test().catch(error => { console.error(error.message); process.exitCode = 1; });
