// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createNeonOrbit, type OrbitHUD } from './neonOrbitEngine'
afterEach(() => vi.unstubAllGlobals())

// Real renderer/input lifecycle: chat typing never fires game commands; a
// paused/hidden or removed background must not keep an idle frame loop alive.
it('keeps game keys off chat and stops frames on pause, visibility and disposal', () => {
  const frames = new Map<number, FrameRequestCallback>(); let id=0, now=0
  vi.stubGlobal('requestAnimationFrame', (fn:FrameRequestCallback) => { frames.set(++id,fn);return id })
  vi.stubGlobal('cancelAnimationFrame',(n:number)=>frames.delete(n))
  vi.stubGlobal('matchMedia',()=>({matches:false}))
  const canvas=document.createElement('canvas');document.body.appendChild(canvas)
  const ctx=new Proxy({}, {get:(_,key)=>key==='createRadialGradient'?()=>({addColorStop:()=>{}}):()=>{}}) as CanvasRenderingContext2D
  vi.spyOn(canvas,'getContext').mockReturnValue(ctx)
  let hud:OrbitHUD|undefined
  const game=createNeonOrbit(canvas,state=>{hud=state})
  const tick=()=>{const [n,fn]=[...frames][0];frames.delete(n);fn(now+=40)}
  try {
    game.play(true)
    const chat=document.createElement('textarea');document.body.appendChild(chat)
    chat.dispatchEvent(new KeyboardEvent('keydown',{key:' ',bubbles:true}));chat.remove()
    expect(hud?.novas).toBe(3)
    canvas.dispatchEvent(new KeyboardEvent('keydown',{key:' ',bubbles:true}))
    expect(hud?.novas).toBe(2)
    game.pause();tick();expect(frames.size).toBe(0)
    game.pause();expect(frames.size).toBe(1)
    const hidden=Object.getOwnPropertyDescriptor(document,'hidden')
    Object.defineProperty(document,'hidden',{configurable:true,value:true})
    document.dispatchEvent(new Event('visibilitychange'));expect(frames.size).toBe(0)
    Object.defineProperty(document,'hidden',{configurable:true,value:false})
    document.dispatchEvent(new Event('visibilitychange'));expect(frames.size).toBe(1)
    if(hidden)Object.defineProperty(document,'hidden',hidden);else delete (document as unknown as {hidden?:boolean}).hidden
    game.destroy();expect(frames.size).toBe(0)
    canvas.dispatchEvent(new KeyboardEvent('keydown',{key:' ',bubbles:true}));expect(hud?.novas).toBe(2)
  } finally {game.destroy();canvas.remove()}
})
