import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {extname,resolve,sep} from 'node:path';
import {pathToFileURL} from 'node:url';

export function staticServer(directory) {
 const root=resolve(directory);
 return createServer(async(req,res)=>{
 try {
  const path=resolve(root,'.'+decodeURIComponent((new URL(req.url,'http://localhost').pathname === '/' ? '/index.html' : new URL(req.url,'http://localhost').pathname)));
  if(!path.startsWith(root+sep)){res.writeHead(403).end();return;}
  const data=await readFile(path);
  const types={'.html':'text/html','.js':'text/javascript','.wasm':'application/wasm','.mp4':'video/mp4','.vtt':'text/vtt'};
  res.setHeader('Content-Type',types[extname(path)]||'application/octet-stream');
  res.setHeader('Accept-Ranges','bytes');
  const range=/^bytes=(\d+)-(\d*)$/.exec(req.headers.range||'');
  if(range){
    const start=Number(range[1]),end=range[2]?Math.min(Number(range[2]),data.length-1):data.length-1;
    if(start>end){res.writeHead(416,{'Content-Range':`bytes */${data.length}`}).end();return;}
    res.writeHead(206,{'Content-Range':`bytes ${start}-${end}/${data.length}`,'Content-Length':end-start+1});
    res.end(data.subarray(start,end+1));
  }else{res.setHeader('Content-Length',data.length);res.end(data);}
 } catch {res.writeHead(404).end();}
});
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
 const port=Number(process.env.PORT || 8080);
 staticServer(process.argv[2] || 'dist').listen(port,'127.0.0.1',()=>console.log(`http://127.0.0.1:${port}/index.html`));
}
