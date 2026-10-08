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
globalThis.requestAnimationFrame=fn=>{frames.set(++next,fn);return next;};
globalThis.cancelAnimationFrame=id=>frames.delete(id);
globalThis.document={body:new Element(),hidden:false,baseURI:'http://localhost/',createElement:()=>new Element()};
globalThis.innerWidth=1000;globalThis.innerHeight=800;
const {registerHost}=await import('./surface.js');
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
 metrics.visible=false;paint();assert.ok(root.hidden&&root.inert);
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
