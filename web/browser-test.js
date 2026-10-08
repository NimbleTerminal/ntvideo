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
 const source=process.env.NTVIDEO_BROWSER_SOURCE || "demo.mp4";
 await page.goto(`http://127.0.0.1:${server.address().port}/index.html?source=${encodeURIComponent(source)}`);
 await page.waitForFunction(()=>{const v=document.querySelector('video');return v&&v.readyState>=2&&!v.parentElement.hidden;},null,{timeout:20000});
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
 // Escape the DOM controls and send quit to the Go program through its host.
 await page.evaluate(()=>bubbletea_write('q'));
 await page.waitForFunction(()=>!document.querySelector('video'));
 await page.waitForTimeout(250);
 assert.deepEqual(errors,[]);
 console.log('PASS: WASM playback, fit modes, mouse scrubber/volume controls, resize, placement, and cleanup');
}finally{await browser?.close();await new Promise(r=>server.close(r));}
