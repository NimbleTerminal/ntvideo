import {test} from 'node:test';
import assert from 'node:assert/strict';

class Element {
  style={};children=[];hidden=false;inert=false;paused=false;
  append(e){this.children.push(e);}
  setAttribute(){}
  remove(){this.removed=true;}
  pause(){this.paused=true;}
}
let frames=new Map(),next=0;
const visibilityListeners=new Set();
globalThis.requestAnimationFrame=fn=>{frames.set(++next,fn);return next;};
globalThis.cancelAnimationFrame=id=>frames.delete(id);
globalThis.document={body:new Element(),hidden:false,baseURI:'http://localhost/',createElement:()=>new Element(),addEventListener:(name,fn)=>visibilityListeners.add(fn),removeEventListener:(name,fn)=>visibilityListeners.delete(fn)};
globalThis.innerWidth=1000;globalThis.innerHeight=800;
const {registerHost,registerBoobaHost}=await import('./surface.js');
function paint(){const callbacks=[...frames.values()];frames.clear();for(const fn of callbacks)fn();}

test('CSS cell mapping, clipping, scroll visibility, resize, and host cleanup',()=>{
 let metrics={left:20,top:30,cellWidth:10,cellHeight:20,visible:true,clip:{left:30,top:50,right:120,bottom:130}};
 const unregister=registerHost('test',()=>metrics),video=new Element();
 const surface=globalThis.ntchartsVideoHost.mount('test',video,'Description','',4);
 const root=document.body.children.at(-1);
 surface.layout(0,0,20,10,true);paint();
 assert.equal(root.style.left,'30px');assert.equal(root.style.top,'50px');
 assert.equal(root.style.width,'90px');assert.equal(root.style.height,'80px');
 assert.equal(video.style.left,'-10px');assert.equal(video.style.top,'-20px');
 assert.equal(video.style.width,'200px');assert.equal(video.style.height,'200px');
 metrics={...metrics,cellWidth:5};paint();assert.equal(video.style.width,'100px');
 video.paused=false;metrics.visible=false;paint();assert.ok(root.hidden&&root.inert);assert.ok(video.paused);
 metrics.visible=true;paint();assert.ok(video.paused, "showing must not resume audio");
 metrics.visible=true;surface.layout(0,0,0,0,true);paint();assert.ok(root.hidden);
 unregister();assert.ok(root.removed&&video.paused);assert.equal(frames.size,0);
 surface.close();assert.equal(frames.size,0);
});
test('missing hosts fail explicitly and unmeasurable hosts hide surfaces',()=>{
 assert.throws(()=>ntchartsVideoHost.mount('missing',new Element(),'','',0),/No terminal/);
 const unregister=registerHost('bad',()=>{throw Error('detached')});
 const surface=ntchartsVideoHost.mount('bad',new Element(),'','',0);
 surface.layout(0,0,10,10,true);paint();assert.ok(document.body.children.at(-1).hidden);unregister();
});
test('transcript links allow only http(s) URLs',()=>{
 const unregister=registerHost('links',()=>null);
 const links=transcript=>{
  const surface=globalThis.ntchartsVideoHost.mount('links',new Element(),'',transcript,0);
  const root=document.body.children.at(-1);
  surface.close();
  return root.children.filter(e=>e.href).map(e=>e.href);
 };
 assert.deepEqual(links('javascript:alert(1)'),[]);
 assert.deepEqual(links('data:text/html,hi'),[]);
 assert.deepEqual(links('https://example.com/t.html'),['https://example.com/t.html']);
 assert.deepEqual(links('transcript.html'),['http://localhost/transcript.html']);
 assert.deepEqual(links(''),[]);
 unregister();
});

test('hidden documents pause immediately without an animation frame',()=>{
 const unregister=registerHost('tab',()=>({left:0,top:0,cellWidth:10,cellHeight:20,visible:true}));
 const video=new Element();
 const surface=ntchartsVideoHost.mount('tab',video,'','',0);
 surface.layout(0,0,10,10,true);paint();video.paused=false;
 document.hidden=true;
 for(const listener of visibilityListeners)listener();
 assert.ok(video.paused);
 assert.ok(document.body.children.at(-1).hidden);
 document.hidden=false;
 for(const listener of visibilityListeners)listener();
 paint();assert.ok(video.paused);
 unregister();assert.equal(visibilityListeners.size,0);
});

test('canonical host global preserves the legacy alias',()=>{
 assert.equal(globalThis.ntvideoHost,globalThis.ntchartsVideoHost);
 assert.equal(typeof globalThis.ntvideoHost?.mount,'function');
});
test('Booba host measures CSS cells and follows movement, resize, scrollback and visibility',()=>{
 let rect={left:20,top:30,width:800,height:400,right:820,bottom:430};
 let clip={left:0,top:0,right:700,bottom:450};
 let visible=true,alt=true;
 const canvas={isConnected:true,checkVisibility:()=>visible,getBoundingClientRect:()=>rect};
 const terminal={container:{querySelector:()=>canvas,getBoundingClientRect:()=>clip},term:{cols:80,rows:20,viewportY:0},getMode:mode=>mode===1049&&alt};
 const unregister=registerBoobaHost('booba',terminal),video=new Element();
 const surface=ntvideoHost.mount('booba',video,'Clip','',0),root=document.body.children.at(-1);
 surface.layout(2,3,80,20,true);paint();
 assert.equal(root.style.left,'40px');assert.equal(root.style.top,'90px');
 assert.equal(root.style.width,'660px');assert.equal(root.style.height,'340px');
 assert.equal(video.style.width,'800px');assert.equal(video.style.height,'400px');
 rect={left:50,top:60,width:400,height:200,right:450,bottom:260};paint();
 assert.equal(root.style.left,'60px');assert.equal(root.style.top,'90px');
 assert.equal(video.style.width,'400px');assert.equal(video.style.height,'200px');
 terminal.term.cols=40;paint();assert.equal(video.style.width,'800px');
 terminal.term.viewportY=1;video.paused=false;paint();assert.ok(root.hidden&&video.paused);
 terminal.term.viewportY=0;paint();assert.equal(root.hidden,false);assert.ok(video.paused);
 alt=false;paint();assert.ok(root.hidden);alt=true;
 visible=false;paint();assert.ok(root.hidden);visible=true;
 canvas.isConnected=false;paint();assert.ok(root.hidden);canvas.isConnected=true;
 terminal.term.rows=0;paint();assert.ok(root.hidden);terminal.term.rows=20;
 terminal.term=null;paint();assert.ok(root.hidden);
 unregister();assert.ok(root.removed);assert.equal(frames.size,0);
});
