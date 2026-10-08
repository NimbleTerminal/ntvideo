// Host integration only: video decoding and presentation stay in HTMLVideoElement.
// All measurements use CSS pixels, never canvas backing-store/device pixels.
const hosts = new Map();

export function registerHost(id, measure) {
  if (hosts.has(id)) throw new Error(`Duplicate video host: ${id}`);
  const host = { measure, surfaces: new Set() };
  hosts.set(id, host);
  return () => {
    for (const surface of [...host.surfaces]) surface.close();
    hosts.delete(id);
  };
}

// Only http(s) transcript links are rendered; other schemes could run script.
function webHref(url) {
  if (!url) return '';
  try {
    const parsed = new URL(url, document.baseURI);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : '';
  } catch { return ''; }
}

// measure() returns {left, top, cellWidth, cellHeight, clip: {left,top,right,bottom},
// visible}. Return null when the terminal screen cannot be mapped reliably.
// Hosts own buffer/reflow tracking. A viewport-relative alt-screen host may
// hide surfaces while viewing scrollback; inline hosts need durable anchors.
function mount(id, video, description, transcript, zIndex) {
  const host = hosts.get(id);
  if (!host) throw new Error(`No terminal surface host registered as ${id}`);
  const root = document.createElement('div');
  Object.assign(root.style, {position:'fixed', overflow:'hidden', pointerEvents:'none', zIndex:String(zIndex)});
  Object.assign(video.style, {position:'absolute', objectFit:'contain', pointerEvents:'auto', background:'black'});
  root.append(video);
  const status = document.createElement('span');
  status.setAttribute('role', 'status');
  Object.assign(status.style, {position:'absolute', width:'1px', height:'1px', overflow:'hidden', clipPath:'inset(50%)'});
  root.append(status);
  const href = webHref(transcript);
  if (href) {
    const link = document.createElement('a');
    link.textContent = 'Transcript'; link.href = href;
    Object.assign(link.style, {position:'absolute', right:'4px', top:'4px', pointerEvents:'auto', color:'white', background:'black'});
    root.append(link);
  }
  document.body.append(root);
  let bounds = null, closed = false, raf = 0, lastState = '';
  const hide = () => { root.hidden = true; root.inert = true; };
  function paint() {
    if (closed) return;
    let m;
    try { m = host.measure(); } catch { m = null; }
    if (!bounds || !bounds.visible || document.hidden || !m || !m.visible ||
        ![m.left,m.top,m.cellWidth,m.cellHeight].every(Number.isFinite) || m.cellWidth<=0 || m.cellHeight<=0) {
      hide();
    } else {
      const left=m.left+bounds.x*m.cellWidth, top=m.top+bounds.y*m.cellHeight;
      const width=bounds.cols*m.cellWidth, height=bounds.rows*m.cellHeight;
      const clip=m.clip || {left:0,top:0,right:innerWidth,bottom:innerHeight};
      const l=Math.max(left,clip.left,0), t=Math.max(top,clip.top,0);
      const r=Math.min(left+width,clip.right,innerWidth), b=Math.min(top+height,clip.bottom,innerHeight);
      if (r<=l || b<=t) hide();
      else {
        root.hidden=false; root.inert=false;
        Object.assign(root.style,{left:`${l}px`,top:`${t}px`,width:`${r-l}px`,height:`${b-t}px`});
        Object.assign(video.style,{left:`${left-l}px`,top:`${top-t}px`,width:`${width}px`,height:`${height}px`});
      }
    }
    raf=requestAnimationFrame(paint);
  }
  const surface = {
    layout(x,y,cols,rows,visible) {bounds={x,y,cols,rows,visible};},
    status(state) {if(state!==lastState){status.textContent=`${description}: ${state}`;lastState=state;}},
    close() {if(closed)return;closed=true;cancelAnimationFrame(raf);video.pause();root.remove();host.surfaces.delete(surface);}
  };
  host.surfaces.add(surface); hide();raf=requestAnimationFrame(paint);
  return surface;
}
globalThis.ntchartsVideoHost = {mount};
