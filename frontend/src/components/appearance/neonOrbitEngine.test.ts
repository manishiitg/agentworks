// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createNeonOrbit, type OrbitHUD } from './neonOrbitEngine'
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

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

// PLAT-746 theme review: with no agent working the scene is one still frame (no frame loop); while one works it runs at
// 30fps, wakes about once per frame instead of every vsync, and paints its fog and glowing gate once, not per frame.
it('is a still frame until an agent works, then draws 30fps from cached static art', () => {
  vi.useFakeTimers()
  const frames = new Map<number, FrameRequestCallback>(); let id = 0, now = 0, runs = 0
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => { frames.set(++id, fn); return id })
  vi.stubGlobal('cancelAnimationFrame', (n: number) => frames.delete(n))
  vi.stubGlobal('matchMedia', () => ({ matches: false }))
  const cost = { gradients: 0, blurs: [] as number[] }
  const ctx = new Proxy({}, {
    get: (_, key) => key === 'createRadialGradient' ? () => { cost.gradients++; return { addColorStop: () => {} } } : () => {},
    set: (_, key, value) => { if (key === 'shadowBlur' && Number(value) > 0) cost.blurs.push(Number(value)); return true },
  }) as unknown as CanvasRenderingContext2D
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as never)
  const vsync = (ms: number) => { for (let spent = 0; spent < ms; spent += 16) { now += 16; vi.advanceTimersByTime(16); const due = [...frames]; frames.clear(); due.forEach(([, fn]) => { runs++; fn(now) }) } }
  const canvas = document.createElement('canvas'); document.body.appendChild(canvas)
  const game = createNeonOrbit(canvas, () => {})
  const worker = document.createElement('div')
  try {
    vsync(16)
    expect(frames.size).toBe(0)
    const gradients = cost.gradients
    expect(gradients).toBeGreaterThan(0)
    expect(Math.max(...cost.blurs)).toBeGreaterThan(15)
    cost.blurs.length = 0
    runs = 0; vsync(3000)
    expect(runs).toBe(0)

    worker.setAttribute('data-theme-agent-state', 'running'); document.body.appendChild(worker)
    vsync(700)
    runs = 0; vsync(1000)
    expect(runs).toBeGreaterThan(15); expect(runs).toBeLessThan(40)
    // No per-frame gradients, and the 22px orbital-gate glow is never re-applied (only the small ship glow remains).
    expect(cost.gradients).toBe(gradients)
    expect(Math.max(...cost.blurs)).toBeLessThan(15)

    worker.remove()
    vsync(1500)
    runs = 0; vsync(1000)
    expect(runs).toBe(0)
  } finally { game.destroy(); worker.remove(); canvas.remove(); vi.useRealTimers() }
})
