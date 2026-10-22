import {chromium} from 'playwright';
import {staticServer} from '../scripts/serve-web.mjs';
import assert from 'node:assert/strict';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

const server=staticServer('dist');
await new Promise(r=>server.listen(0,'127.0.0.1',r));
let browser;
try{
 browser=await chromium.launch({channel:process.env.CHROME_CHANNEL||'chrome',headless:true});
 const page=await browser.newPage({viewport:{width:1000,height:800}});
 const errors=[];page.on('pageerror',e=>errors.push(e.stack));
 const cueEvents=[];page.on('console',msg=>{if(msg.text().startsWith('ntvideo cue '))cueEvents.push(JSON.parse(msg.text().slice('ntvideo cue '.length)));});
 const waitCue=async predicate=>{
   const deadline=Date.now()+5000;
   while(!cueEvents.some(predicate)&&Date.now()<deadline)await page.waitForTimeout(25);
   assert.ok(cueEvents.some(predicate),JSON.stringify(cueEvents));
 };
 const source=process.env.NTVIDEO_BROWSER_SOURCE || "demo.mp4";
 await page.route('**/captions-*.vtt',route=>route.fulfill({contentType:'text/vtt',body:'WEBVTT\n\n00:00.000 --> 00:04.000\nCaption test\n'}));
 await page.goto(`http://127.0.0.1:${server.address().port}/index.html?source=${encodeURIComponent(source)}&captions=captions-en.vtt&captions=captions-es.vtt&cue-events=1`);
 await page.waitForFunction(()=>{const v=document.querySelector('video');return v&&v.readyState>=2&&!v.parentElement.hidden;},null,{timeout:20000});
 const controlState=await page.evaluate(()=>{
   const v=document.querySelector('video');
   return {controls:v.controls,tabIndex:v.tabIndex,pointerEvents:getComputedStyle(v).pointerEvents,label:v.getAttribute('aria-label'),bridge:ntvideoHost===ntchartsVideoHost};
 });
 assert.deepEqual(controlState,{controls:false,tabIndex:-1,pointerEvents:'none',label:'Video demonstration',bridge:true});
 // Clicking the overlay must leave input with the terminal. Use real keyboard
 // events, not the WASM bridge, to verify the host retains Space and arrow keys.
 await page.evaluate(()=>{document.querySelector('video').muted=true;});
 await page.locator('#terminal canvas').first().click();
 await page.keyboard.press('Space');
 await page.waitForFunction(()=>!document.querySelector('video').paused);
 await page.keyboard.press('Space');
 await page.waitForFunction(()=>document.querySelector('video').paused);
 assert.equal(await page.evaluate(()=>document.activeElement?.tagName==='VIDEO'),false);
 // Host-owned caption controls select real browser TextTracks and load cues.
 await page.keyboard.press('c');
 await page.waitForFunction(()=>{const t=document.querySelector('video').textTracks;return t.length===2&&t[0].mode==='showing'&&t[0].cues?.length>0;});
 await waitCue(e=>e.Kind==='enter'&&e.Cue.TrackID==='0'&&e.Cue.Text==='Caption test');
 await page.keyboard.press('c');
 await page.waitForFunction(()=>[...document.querySelector('video').textTracks].every(t=>t.mode==='disabled'));
 await page.keyboard.press('Space');
 await page.waitForFunction(()=>!document.querySelector('video').paused);
 await page.keyboard.press('C');
 await page.waitForFunction(()=>{const v=document.querySelector('video');return v.paused&&v.parentElement.hidden;});
 await page.keyboard.press('ArrowDown');
 await page.keyboard.press('ArrowDown');
 await page.keyboard.press('Enter');
 await page.waitForFunction(()=>{const v=document.querySelector('video');return !v.parentElement.hidden&&v.textTracks[1].mode==='showing'&&v.textTracks[0].mode==='disabled';});
 assert.equal(await page.evaluate(()=>document.querySelector('video').paused),true);
 await page.keyboard.press('c');
 await page.waitForFunction(()=>[...document.querySelector('video').textTracks].every(t=>t.mode==='disabled'));
 await page.keyboard.press('c');
 await page.waitForFunction(()=>document.querySelector('video').textTracks[1].mode==='showing');
 await waitCue(e=>e.Kind==='exit'&&e.Reason==='track-change');
 await waitCue(e=>e.Kind==='enter'&&e.Cue.TrackID==='1');
 // The example starts expanded. Verify width, then switch to the small player.
 const cells=await page.evaluate(()=>{const v=document.querySelector('video').getBoundingClientRect(),c=document.querySelector('#terminal canvas').getBoundingClientRect();return {width:v.left-c.left,height:(v.top-c.top)/3};});
 assert.ok(cells.width>0&&cells.height>0);
 await page.evaluate(()=>bubbletea_write('\r'));
 await page.waitForFunction(()=>document.querySelector('video').getBoundingClientRect().width<document.querySelector('#terminal canvas').getBoundingClientRect().width*0.9);
 let geometry=await page.evaluate(()=>{
  const v=document.querySelector('video'),canvas=document.querySelector('#terminal canvas');
  return {video:v.getBoundingClientRect().toJSON(),canvas:canvas.getBoundingClientRect().toJSON()};
 });
 assert.ok(geometry.video.width>100&&geometry.video.height>100);
 assert.ok(Math.abs((geometry.video.x+geometry.video.width/2)-(geometry.canvas.x+geometry.canvas.width/2))<cells.width);
 assert.ok(geometry.video.y>geometry.canvas.y);
 // Muted playback makes the test silent and avoids autoplay-policy dependence.
 // Media playback remains in the browser, not WASM.
 await page.evaluate(()=>{const v=document.querySelector('video');v.muted=true;return v.play();});
 await page.waitForFunction(()=>document.querySelector('video').currentTime>0.15);
 await page.evaluate(()=>{const v=document.querySelector('video');v.pause();v.currentTime=1;});
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').currentTime-1)<0.2);
 // Exercise the portable Go API through the actual Tea keyboard path as well.
 await page.evaluate(()=>bubbletea_write('\x1b[D'));
 await page.waitForFunction(()=>document.querySelector('video').currentTime<0.1);
 await page.evaluate(()=>bubbletea_write(' '));
 await page.waitForFunction(()=>!document.querySelector('video').paused);
 await page.evaluate(()=>bubbletea_write(' '));
 await page.waitForFunction(()=>document.querySelector('video').paused);
 // Change fitting while paused; it must not reload or resume the media.
 for(const fit of ['cover','fill','contain']) {
   await page.evaluate(()=>bubbletea_write('f'));
   await page.waitForFunction(f=>document.querySelector('video').style.objectFit===f,fit);
   assert.ok(await page.evaluate(()=>document.querySelector('video').paused));
 }
 // Hit the terminal scrubber below the DOM video using real mouse events.
 const scrub=await page.evaluate(cellHeight=>{
   const v=document.querySelector('video').getBoundingClientRect();
   const canvas=document.querySelector('#terminal canvas').getBoundingClientRect();

   return {x:v.left,cw:v.width/60,y:v.bottom+cellHeight/2,volumeY:v.bottom+3.5*cellHeight};
 },cells.height);
 const barCells=60-' 0:00 / 0:04'.length-2;
 await page.mouse.click(scrub.x+(1+Math.floor((barCells-1)/2)+0.5)*scrub.cw,scrub.y);
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').currentTime-2)<0.25);
 await page.mouse.move(scrub.x+1.5*scrub.cw,scrub.y);await page.mouse.down();
 await page.mouse.move(scrub.x+(1+Math.floor((barCells-1)*0.75)+0.5)*scrub.cw,scrub.y,{steps:5});
 await page.mouse.up();
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').currentTime-3)<0.25);
 // Click/drag the TUI volume controls, which sit below the scrubber.
 await page.mouse.click(scrub.x+21.5*scrub.cw,scrub.volumeY);
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').volume-5/19)<0.02);
 await page.mouse.click(scrub.x+13.5*scrub.cw,scrub.volumeY);
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').volume-(5/19-0.1))<0.02);
 await page.mouse.click(scrub.x+38.5*scrub.cw,scrub.volumeY);
 await page.waitForFunction(()=>Math.abs(document.querySelector('video').volume-5/19)<0.02);
 await page.mouse.move(scrub.x+16.5*scrub.cw,scrub.volumeY);await page.mouse.down();
 await page.mouse.move(scrub.x+35.5*scrub.cw,scrub.volumeY,{steps:4});await page.mouse.up();
 await page.waitForFunction(()=>document.querySelector('video').volume>0.99);
 await page.mouse.click(scrub.x+5.5*scrub.cw,scrub.volumeY);
 await page.waitForFunction(()=>!document.querySelector('video').muted);
 await page.mouse.click(scrub.x+5.5*scrub.cw,scrub.volumeY);
 await page.waitForFunction(()=>document.querySelector('video').muted);
 await page.screenshot({path:join(tmpdir(),'ntvideo-ui.png')});
 await page.setViewportSize({width:650,height:600});
 await page.waitForTimeout(250);
 assert.ok(await page.evaluate(()=>{const v=document.querySelector('video').getBoundingClientRect();return v.right<=innerWidth&&v.bottom<=innerHeight;}));
 // Moving the terminal tests host geometry independently of Tea's layout.
 await page.evaluate(()=>{document.getElementById('terminal').style.marginLeft='50px';});
 await page.waitForTimeout(100);
 assert.ok(await page.evaluate(()=>{const v=document.querySelector('video').getBoundingClientRect(),c=document.querySelector('#terminal canvas').getBoundingClientRect();return Math.abs(v.left+v.width/2-c.left-c.width/2)<12;}));
 // Simulate a host supplying in-memory bytes. The host owns the URL; closing
 // ntvideo removes the element but must not revoke the host's blob URL.
 const blobURL=await page.evaluate(async source=>{
   const blob=await (await fetch(source)).blob();
   const url=URL.createObjectURL(blob),v=document.querySelector('video');
   v.pause();v.src=url;v.load();return url;
 },source);
 await page.waitForFunction(()=>document.querySelector('video').readyState>=2);
 await page.keyboard.press('Space');
 await page.waitForFunction(()=>!document.querySelector('video').paused);
 await page.keyboard.press('Space');
 await page.waitForFunction(()=>document.querySelector('video').paused);
 // Escape the DOM controls and send quit to the Go program through its host.
 await page.evaluate(()=>bubbletea_write('q'));
 await page.waitForFunction(()=>!document.querySelector('video'));
 await page.waitForTimeout(250);
 assert.ok(await page.evaluate(async url=>(await fetch(url)).ok,blobURL),'host-owned blob was revoked');
 await page.evaluate(url=>URL.revokeObjectURL(url),blobURL);
 assert.deepEqual(errors,[]);
 console.log('PASS: WASM playback, fit modes, mouse scrubber/volume controls, resize, placement, and cleanup');
}finally{await browser?.close();await new Promise(r=>server.close(r));}
