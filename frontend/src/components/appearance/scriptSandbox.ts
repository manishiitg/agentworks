import type { AppearanceScript } from '../../stores/appearanceTheme'

export type ScriptInputs = { colors: Record<string,string>; working: boolean }
// These strings are PRODUCT code. Personal code is sent over a transferred port,
// never interpolated in HTML, evaluated by the app, or executed on the frame thread.
const workerPrefix = `'use strict';
const emit = self.postMessage.bind(self);
let canvas;
const paint = (()=>{\n`
const workerSuffix = `\n;return render;})();
self.onmessage = async ({data})=>{
 if(data.type==='canvas'){canvas=data.canvas;return;}
 if(data.type!=='draw')return;
 try {
  if(canvas.width!==data.width)canvas.width=data.width;
  if(canvas.height!==data.height)canvas.height=data.height;
  await paint({...data,canvas});
  emit({type:'frame',id:data.id});
 }catch{emit({type:'failed'});}
};
emit({type:'ready'});`

// Opaque-origin iframe owns a Worker. Only Worker code is personal; it cannot
// navigate a window, touch app DOM/storage/preload or fetch account resources.
// Blob Workers inherit this document's CSP (MDN CSP / workers). No same-origin
// sandbox flag, imports, network, forms, popups or navigation permissions.
export function scriptSandboxDocument(nonce:string):string {
 const bootstrap = `
 const prefix=${JSON.stringify(workerPrefix)},suffix=${JSON.stringify(workerSuffix)};
 let started=false;
 addEventListener('message',event=>{
  if(started||event.source!==parent||event.data!=='appearance-script'||!event.ports[0])return;
  started=true;
  const port=event.ports[0],canvas=document.querySelector('canvas');
  let worker=null,url='',pending=0;
  const stop=()=>{if(worker)worker.terminate();worker=null;if(url)URL.revokeObjectURL(url);url='';};
  port.onmessage=({data})=>{
   if(data.type==='stop'){stop();return;}
   if(data.type==='start'){
    try {
     url=URL.createObjectURL(new Blob([prefix,data.code,suffix],{type:'text/javascript'}));
     worker=new Worker(url);
     worker.onerror=()=>{stop();port.postMessage({type:'failed'});};
     // Transfer ONCE. Keep the offscreen object out of any theme DOM context.
     const offscreen=canvas.transferControlToOffscreen();
     worker.postMessage({type:'canvas',canvas:offscreen},[offscreen]);
     worker.onmessage=({data:reply})=>{
      if(reply?.type==='ready')port.postMessage({type:'ready'});
      else if(reply?.type==='frame'&&pending!==0&&reply.id===pending){port.postMessage({type:'frame',id:pending});pending=0;}
      else if(reply?.type==='failed'){stop();port.postMessage({type:'failed'});}
     };
    }catch{stop();port.postMessage({type:'failed'});}
   }else if(data.type==='draw'&&worker){pending=data.id;worker.postMessage(data);}
  };
  port.start();port.postMessage({type:'connected'});
 });`
 // No theme text is present here, including code containing </script>.
 return `<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; worker-src blob:; connect-src 'none'; img-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"><style nonce="${nonce}">html,body{margin:0;width:100%;height:100%;overflow:hidden;background:transparent}canvas{display:block;width:100%;height:100%}</style><canvas></canvas><script nonce="${nonce}">${bootstrap}</script>`
}

type Status = 'ready'|'failed'|'throttled'|'stopped'
/** One outstanding frame; app-owned scheduling and a killable Worker, not
 * arbitrary JS on the renderer's UI thread. CPU/memory/GPU quotas remain best
 * effort: browser process limits own allocation and individual GPU calls. */
export function mountScriptSandbox(host:HTMLElement,script:AppearanceScript,inputs:()=>ScriptInputs,status:(s:Status)=>void) {
 const frame=document.createElement('iframe')
 frame.title='Personal decorative background';frame.tabIndex=-1
 frame.setAttribute('sandbox','allow-scripts')
 frame.setAttribute('aria-hidden','true');frame.setAttribute('inert','')
 frame.referrerPolicy='no-referrer'
 frame.setAttribute('allow',"camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'; fullscreen 'none'; payment 'none'; usb 'none'")
 const nonce=crypto.randomUUID().replaceAll('-','')
 frame.srcdoc=scriptSandboxDocument(nonce)
 const channel=new MessageChannel(),port=channel.port1
 let disposed=false,stopped=false,connected=false,ready=false,sequence=0,pending=0,time=0,last=0,submitted=0,slow=0
 let fps=script.fps,timer:ReturnType<typeof setTimeout>|undefined,watchdog:ReturnType<typeof setTimeout>|undefined
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches
 const clear=()=>{clearTimeout(timer);clearTimeout(watchdog);timer=undefined;watchdog=undefined}
 const stop=(result?:Status)=>{
  if(stopped)return
  stopped=true;clear();port.postMessage({type:'stop'})
  if(result)status(result)
 }
 const guard=()=>{clearTimeout(watchdog);watchdog=setTimeout(()=>stop('stopped'),1500)}
 const draw=()=>{
  if(disposed||stopped||document.hidden||pending)return
  const now=performance.now()
  if(last)time+=Math.min(.1,(now-last)/1000)
  last=now;submitted=now;pending=++sequence
  const scale=Math.min(1,1280/Math.max(1,innerWidth),720/Math.max(1,innerHeight))
  // Only whitelisted primitive palette values and one admitted working boolean.
  const source=inputs(),colors=Object.fromEntries(Object.entries(source.colors).filter(([key,value])=>/^[a-z_]+$/.test(key)&&/^#[a-f0-9]{6}$/i.test(value)))
  port.postMessage({type:'draw',id:pending,width:Math.max(1,Math.floor(innerWidth*scale)),height:Math.max(1,Math.floor(innerHeight*scale)),time,delta:time===0?0:Math.min(.1,(now-lastDraw)/1000),colors,working:!!source.working,reducedMotion:reduced})
  lastDraw=now;guard()
 }
 let lastDraw=0
 port.onmessage=({data})=>{
  if(disposed||stopped)return
  if(data?.type==='connected'&&!connected){connected=true;port.postMessage({type:'start',code:script.code});guard()}
  else if(data?.type==='ready'&&connected&&sequence===0){clearTimeout(watchdog);draw()}
  else if(data?.type==='frame'&&pending&&data.id===pending){
   clearTimeout(watchdog);pending=0
   const ms=performance.now()-submitted
   slow=ms>33?slow+ms+1000/fps:0
   if(slow>2000){slow=0;if(fps>15){fps=15;status('throttled')}else{stop('stopped');return}}
   if(!ready){ready=true;status('ready')}
   if(reduced){stop();return}
   timer=setTimeout(draw,1000/fps)
  }else if(data?.type==='failed')stop('failed')
 }
 port.start()
 frame.onload=()=>{if(!disposed){frame.contentWindow?.postMessage('appearance-script','*',[channel.port2]);guard()}}
 host.append(frame);guard()
 return {destroy:()=>{if(disposed)return;stop();disposed=true;clear();port.close();channel.port2.close();frame.onload=null;frame.remove()}}
}
