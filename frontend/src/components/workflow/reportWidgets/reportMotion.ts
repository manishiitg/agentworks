// Presentation only: a clock for designer-authored visuals, with no data or
// execution authority. The same controller runs in the app and preview.
export interface ReportMotionState {
  time: number
  duration: number
  progress: number
  playing: boolean
  speed: number
  reducedMotion: boolean
}

export interface ReportMotionOptions {
  id: string
  /** Playback seconds, independent of the dates represented by the data. */
  duration: number
  /** Synchronous, seekable painter. Read data before creating the player. */
  render: (state: Readonly<ReportMotionState>) => void
  onChange?: (state: Readonly<ReportMotionState>) => void
}

export interface ReportMotionPlayer {
  play: () => void
  pause: () => void
  replay: () => void
  seek: (seconds: number) => void
  setSpeed: (speed: number) => void
  getState: () => Readonly<ReportMotionState>
  /** Subscribe to this clock, including changes from capture and other controls. */
  subscribe: (listener: (state: Readonly<ReportMotionState>) => void) => () => void
  destroy: () => void
}

export interface ReportMotionApi {
  create: (options: ReportMotionOptions) => ReportMotionPlayer
}

const hosts = new WeakMap<Document, ReportMotionApi>()
const hostPlayers = new WeakMap<Document, Map<string, ReportMotionPlayer>>()

// Used by the platform capture runtime. This exposes no data or execution API.
export function reportMotionCapture(doc: Document, seconds?: number) {
  const players = hostPlayers.get(doc)
  if (seconds !== undefined) {
    for (const player of players?.values() ?? []) {
      player.pause()
      player.seek(seconds)
    }
  }
  const states = [...(players?.entries() ?? [])].map(([id, player]) => ({ id, ...player.getState() }))
  for (const [index, animation] of (doc.getAnimations?.() ?? []).entries()) {
    const end = Number(animation.effect?.getComputedTiming().endTime)
    if (!Number.isFinite(end) || end <= 0) continue
    if (seconds !== undefined) { animation.pause(); animation.currentTime = Math.min(seconds * 1000, end) }
    const time = Number(animation.currentTime ?? 0) / 1000
    states.push({ id: `animation-${index}`, duration: end / 1000, time, progress: time * 1000 / end, playing: animation.playState === 'running', speed: animation.playbackRate, reducedMotion: false })
  }
  return states
}

export function reportMotionFor(doc: Document, onError: (error: unknown) => void): ReportMotionApi {
  const installed = hosts.get(doc)
  if (installed) return installed
  const win = doc.defaultView!
  const players = new Map<string, ReportMotionPlayer>()
  hostPlayers.set(doc, players)
  const preference = win.matchMedia?.('(prefers-reduced-motion: reduce)')
  const finite = (value: number, label: string) => {
    if (!Number.isFinite(value)) throw new Error(`${label} must be a finite number`)
    return value
  }
  const api: ReportMotionApi = Object.freeze({
    create(options: ReportMotionOptions) {
      if (!options || typeof options.id !== 'string' || !options.id.trim()) throw new Error('Motion needs a stable id')
      const duration = finite(options.duration, 'Motion duration')
      if (duration <= 0 || typeof options.render !== 'function') throw new Error('Motion needs a positive duration and render function')
      const previous = players.get(options.id)?.getState()
      players.get(options.id)?.destroy()
      let time = Math.min(previous?.time ?? (preference?.matches ? duration : 0), duration)
      let speed = previous?.speed ?? 1
      let playing = false
      let destroyed = false
      let frame: number | undefined
      let last: number | undefined
      const listeners = new Set<(state: Readonly<ReportMotionState>) => void>()
      const getState = () => Object.freeze({ time, duration, progress: time / duration, playing, speed, reducedMotion: preference?.matches ?? false })
      const cancel = () => {
        if (frame !== undefined) win.cancelAnimationFrame(frame)
        frame = undefined
        last = undefined
      }
      const paint = () => {
        if (destroyed) return
        try {
          const state = getState()
          options.render(state)
          options.onChange?.(state)
        } catch (error) {
          playing = false
          cancel()
          onError(error)
        }
        for (const listener of listeners) {
          try { listener(getState()) } catch (error) { onError(error) }
        }
      }
      const tick = (now: number) => {
        frame = undefined
        if (!playing || destroyed) return
        if (last !== undefined) time = Math.min(duration, time + Math.max(0, now - last) / 1000 * speed)
        last = now
        if (time >= duration) playing = false
        paint()
        if (playing && !destroyed) frame = win.requestAnimationFrame(tick)
      }
      const player: ReportMotionPlayer = Object.freeze({
        play() {
          if (destroyed || playing || doc.hidden) return
          if (time >= duration) time = 0
          playing = true
          last = undefined
          paint()
          if (playing) frame = win.requestAnimationFrame(tick)
        },
        pause() { playing = false; cancel(); paint() },
        replay() { player.seek(0); player.play() },
        seek(seconds: number) {
          if (destroyed) return
          time = Math.max(0, Math.min(duration, finite(seconds, 'Motion position')))
          last = undefined
          if (time >= duration) { playing = false; cancel() }
          paint()
        },
        setSpeed(value: number) {
          if (destroyed) return
          if (finite(value, 'Motion speed') <= 0) throw new Error('Motion speed must be positive')
          speed = value
          last = undefined
          paint()
        },
        getState,
        subscribe(listener) {
          if (destroyed) return () => {}
          listeners.add(listener)
          try { listener(getState()) } catch (error) { onError(error) }
          return () => { listeners.delete(listener) }
        },
        destroy() {
          destroyed = true
          playing = false
          cancel()
          listeners.clear()
          if (players.get(options.id) === player) players.delete(options.id)
        },
      })
      players.set(options.id, player)
      paint()
      // Refresh replaces the painter, preserving the person's playback position.
      // A changed reduced-motion preference never starts playback automatically.
      if (previous?.playing && !preference?.matches) player.play()
      return player
    },
  })
  const pauseAll = () => { for (const player of players.values()) player.pause() }
  doc.addEventListener('visibilitychange', () => { if (doc.hidden) pauseAll() })
  preference?.addEventListener('change', pauseAll)
  win.addEventListener('pagehide', () => {
    for (const player of players.values()) player.destroy()
    preference?.removeEventListener('change', pauseAll)
  }, { once: true })
  hosts.set(doc, api)
  return api
}
