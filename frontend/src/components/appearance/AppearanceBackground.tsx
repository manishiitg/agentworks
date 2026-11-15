import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Gamepad2, Pause, Play, X } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { createNeonOrbit, type OrbitController, type OrbitHUD } from './neonOrbitEngine'
import './neonOrbit.css'

const initial: OrbitHUD = { score: 0, wave: 1, hp: 5, novas: 3, paused: false, playing: false, gameOver: false, announcement: '' }
/** Canvas is passive until Play. Playing is a separate input surface, not chat. */
export default function AppearanceBackground() {
  const canvas = useRef<HTMLCanvasElement>(null)
  const engine = useRef<OrbitController | null>(null)
  const region = useRef<HTMLDivElement>(null)
  const launch = useRef<HTMLButtonElement>(null)
  const returnFocus = useRef<HTMLElement | null>(null)
  const [hud, setHud] = useState(initial)
  const [failed, setFailed] = useState(false)
  const change = useAppearanceStore(state => state.change)
  useEffect(() => {
    try { engine.current = createNeonOrbit(canvas.current!, setHud) }
    catch { setFailed(true) }
    return () => { engine.current?.destroy(); engine.current = null }
  }, [])
  useEffect(() => {
    if (!hud.playing) return
    const root = document.getElementById('root')
    const wasInert = root?.inert
    const previous = returnFocus.current
    if (root) root.inert = true
    canvas.current?.focus({ preventScroll: true })
    return () => { if (root) root.inert = wasInert ?? false; if (previous?.isConnected) previous.focus({ preventScroll: true }); else launch.current?.focus({ preventScroll: true }) }
  }, [hud.playing])
  return createPortal(<div ref={region} className={`aw-orbit ${hud.playing ? 'aw-orbit-playing' : ''}`} role={hud.playing ? 'dialog' : undefined} aria-modal={hud.playing || undefined} aria-label={hud.playing ? 'Neon Orbit game' : undefined}
    onKeyDown={event => {
      // The underlying chat's shortcuts must never respond to game inputs.
      if (!hud.playing) return
      event.stopPropagation()
      if (event.key === 'Tab') {
        const items = [...(region.current?.querySelectorAll<HTMLElement>('canvas, button:not(:disabled)') ?? [])]
        const first = items[0], last = items.at(-1)
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
      }
    }}>
    <canvas ref={canvas} tabIndex={hud.playing ? 0 : -1} aria-label={hud.playing ? 'Game arena. Mouse, touch or WASD to move. Space for nova, Shift to dash, Escape to pause.' : undefined} aria-hidden={!hud.playing} />
    {hud.playing && <>
      <div className="aw-orbit-hud"><span>Score <b>{String(hud.score).padStart(6, '0')}</b></span><span>Wave <b>{hud.wave}</b></span><span>Hull <b>{'●'.repeat(hud.hp)}{'·'.repeat(5 - hud.hp)}</b></span></div>
      <p className="aw-orbit-announcement" role="status">{hud.announcement}</p>
      <p className="aw-orbit-help">Mouse / touch / WASD to steer · Auto-fire · Space: nova · Shift: dash · Esc: pause</p>
      {hud.gameOver && <div className="aw-orbit-over"><h2>Beautiful chaos.</h2><p>Score {hud.score} · Wave {hud.wave}</p><button type="button" onClick={() => engine.current?.play(true)}>Fly again</button></div>}
    </>}
    <div className="aw-orbit-controls" aria-label="Neon Orbit controls">
      <Gamepad2 size={14} aria-hidden="true" /><span>{failed ? 'Background unavailable' : 'Neon Orbit'}</span>
      {!failed && <>
        {!hud.playing && <button ref={launch} type="button" onClick={() => { returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; engine.current?.play(true) }}>Play</button>}
        {hud.playing && <button type="button" disabled={hud.gameOver || hud.novas === 0} onClick={() => engine.current?.nova()}>Nova × {hud.novas}</button>}
        {!hud.gameOver && <button type="button" aria-label={hud.paused ? 'Resume background' : 'Pause background'} onClick={() => engine.current?.pause()}>{hud.paused ? <Play size={13} /> : <Pause size={13} />}</button>}
      </>}
      <button type="button" aria-label={hud.playing ? 'Return to chat' : 'Restore default theme'} title={hud.playing ? 'Return to chat' : 'Restore default theme'} onClick={() => hud.playing ? engine.current?.ambient() : void change('reset')}>{hud.playing ? 'Back to chat' : <X size={14} />}</button>
    </div>
  </div>, document.body)
}
