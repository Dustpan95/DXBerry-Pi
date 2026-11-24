const fs = require('fs');
const vm = require('vm');
const path = require('path');
const assert = require('assert/strict');
const {chromium} = require('playwright');
const base = 'http://127.0.0.1:8090';
const output = process.env.DXB_BROWSER_OUTPUT || '/tmp/dxberry-browser';
fs.mkdirSync(output,{recursive:true});
if (process.argv[2] === 'status') {
  const smoke = fs.readFileSync(path.join(__dirname,'..','page_smoke.js'), 'utf8');
  const source = smoke.slice(smoke.indexOf('const status ='), smoke.indexOf('const models ='));
  const report = vm.runInNewContext(source + '\nstatus;');
  report.generated = new Date().toISOString();
  report.station = {ok:true,callsign:'N0CALL'};
  report.apps = {ok:true,programs:[],logbook:{}};
  fs.writeFileSync(path.join(output,'status.json'), JSON.stringify(report));
  process.exit(0);
}
async function stopContrast(page,theme) {
  await page.emulateMedia({colorScheme:theme});
  const stop=page.getByRole('button',{name:'STOP',exact:true});
  await stop.waitFor({state:'visible'});
  const colors=await stop.evaluate(e=>{const s=getComputedStyle(e);return [s.color,s.backgroundColor];});
  const luminance=rgb=>rgb.match(/[\d.]+/g).slice(0,3).map(Number).map(v=>v/255)
    .map(v=>v<=0.04045?v/12.92:((v+0.055)/1.055)**2.4)
    .reduce((sum,v,i)=>sum+v*[0.2126,0.7152,0.0722][i],0);
  const [a,b]=colors.map(luminance);
  const ratio=(Math.max(a,b)+0.05)/(Math.min(a,b)+0.05);
  await stop.screenshot({path:path.join(output,'stop-'+theme+'.png')});
  assert(ratio>=4.5,`${theme} STOP label contrast ${ratio.toFixed(2)} is below 4.5`);
  return {theme,foreground:colors[0],background:colors[1],ratio};
}
(async()=>{
  let browser, page;
  const errors=[];
  try {
    browser=await chromium.launch({executablePath:process.env.DXB_CHROMIUM || '/usr/bin/chromium',headless:true,args:[
      '--no-sandbox','--use-fake-device-for-media-stream','--use-fake-ui-for-media-stream',
      '--allow-loopback-in-peer-connection','--disable-features=WebRtcHideLocalIpsWithMdns'
    ]});
    const ctx=await browser.newContext({viewport:{width:1280,height:900},permissions:['microphone']});
    page=await ctx.newPage();
    page.on('pageerror',e=>errors.push(e.message));
    page.on('response',r=>{if(r.status()>=400) process.stderr.write('HTTP '+r.status()+' '+r.url()+'\n');});
    await page.goto(base+'/radio/radio1');
    const stopColors=[await stopContrast(page,'light'),await stopContrast(page,'dark')];
    fs.writeFileSync(path.join(output,'stop-contrast.json'),JSON.stringify(stopColors,null,2));
    await page.emulateMedia({colorScheme:'light'});
    console.log('ok: STOP label meets contrast threshold in light and dark themes');
    await page.getByRole('button',{name:'Take the transmitter',exact:true}).click();
    const ptt=page.getByRole('button',{name:'Push to talk: hold to transmit',exact:true});
    await page.waitForFunction(()=>!document.querySelector('.vfo-ptt').disabled);
    await page.getByRole('button',{name:'Listen',exact:true}).click();
    await page.waitForFunction(()=>document.querySelector('.vfo-rx-text')?.textContent==='Receiving',{},{timeout:15000});
    console.log('ok: real browser receives generated audio over WebRTC');
    let poll=0;
    const state=async()=>await (await fetch(base+'/fixture/state?poll='+(++poll),{cache:'no-store'})).json();
    const until=async(predicate,timeout=15000)=>{const end=Date.now()+timeout;while(Date.now()<end){const s=await state();if(predicate(s))return s;await new Promise(r=>setTimeout(r,30));}throw new Error('Timed out waiting for fixture state: '+JSON.stringify(await state()));};
    await ptt.scrollIntoViewIfNeeded();
    const box=await ptt.boundingBox();
    await page.mouse.move(box.x+box.width/2,box.y+box.height/2);
    await page.mouse.down();
    await until(s=>s.keyed&&s.play_bytes>0&&s.loopbacks===1);
    assert.equal((await state()).loopbacks,1);
    await page.screenshot({path:path.join(output,'vfo-transmitting.png')});
    console.log('ok: pointer hold opens the test microphone and passes audio through guarded PTT');
    await page.mouse.up();
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0);
    assert.equal((await state()).loopbacks,0);
    console.log('ok: pointer release unkeys and unloads TX loopback');
    await ptt.focus();
    await page.keyboard.down('Space');
    await until(s=>s.keyed&&s.loopbacks===1);
    await page.keyboard.up('Space');
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0);
    console.log('ok: keyboard hold/release keys and unkeys');
    const released=async()=>{
      await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0);
      await page.waitForFunction(()=>document.querySelector('.vfo-ptt').getAttribute('aria-pressed')==='false');
    };
    const beforeStop=[];
    page.on('request',r=>{if(r.method()==='POST'&&r.url().endsWith('/stop'))beforeStop.push(r.url());});
    await ptt.focus();
    await page.keyboard.down('Space');
    await until(s=>s.keyed&&s.loopbacks===1);
    await page.getByRole('button',{name:'STOP',exact:true}).click();
    await released();
    await page.keyboard.up('Space');
    assert.ok(beforeStop.length>0);
    console.log('ok: STOP cancels a held transmission');
    await ptt.scrollIntoViewIfNeeded();
    let held=await ptt.boundingBox();
    await page.mouse.move(held.x+held.width/2,held.y+held.height/2);
    await page.mouse.down();
    await until(s=>s.keyed&&s.loopbacks===1);
    const began=Date.now();
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0,40000);
    assert.ok(Date.now()-began>=29000,'PTT ended before its configured 30-second limit');
    await page.mouse.up();
    await released();
    assert.match(await page.locator('body').innerText(),/timed out/i);
    console.log('ok: configured 30-second limit unkeys a continuing hold');
    await ptt.scrollIntoViewIfNeeded();
    held=await ptt.boundingBox();
    await page.mouse.move(held.x+held.width/2,held.y+held.height/2);
    await page.mouse.down();
    await until(s=>s.keyed&&s.loopbacks===1);
    const tab=await page.evaluate(()=>window.dxb.tab());
    const dropped=Date.now();
    await fetch(base+'/fixture/drop-audio',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({tab})});
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0,1000);
    assert.ok(Date.now()-dropped<1000);
    await page.mouse.up();
    console.log('ok: closing the actual audio bridge immediately unkeys');
    await ctx.close();
    const phone=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,colorScheme:'dark',permissions:['microphone']});
    page=await phone.newPage();
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto(base+'/radio/radio1');
    await page.getByRole('button',{name:'Take the transmitter',exact:true}).click();
    const touchPTT=page.getByRole('button',{name:'Push to talk: hold to transmit',exact:true});
    await page.waitForFunction(()=>!document.querySelector('.vfo-ptt').disabled);
    await touchPTT.scrollIntoViewIfNeeded();
    const touchBox=await touchPTT.boundingBox();
    const cdp=await phone.newCDPSession(page);
    const point={x:touchBox.x+touchBox.width/2,y:touchBox.y+touchBox.height/2};
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
    await until(s=>s.keyed&&s.loopbacks===1);
    await page.screenshot({path:path.join(output,'vfo-phone-dark.png')});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false);
    console.log('ok: narrow dark-theme touch hold/release works without horizontal overflow');
    await page.waitForFunction(()=>document.querySelector('.vfo-ptt').getAttribute('aria-pressed')==='false');
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
    await until(s=>s.keyed&&s.loopbacks===1);
    await phone.close();
    await until(s=>!s.keyed&&!s.ptt&&s.loopbacks===0,1000);
    console.log('ok: closing a transmitting tab unkeys and clears its audio loopback');
    assert.deepEqual(errors,[]);
  } catch(e) {if(page) {await page.screenshot({path:path.join(output,'vfo-failure.png')}).catch(()=>{}); console.error(await page.locator('body').innerText().catch(()=>''));}console.error(e.stack);process.exitCode=1;}
  finally {
    if(browser) await browser.close();
    try {await fetch(base+'/fixture/done',{method:'POST'});} catch{}
  }
})();
