import React, { useEffect, useState, type CSSProperties } from 'react'
import { Pause, Play, RotateCcw } from 'lucide-react'

type MotionState = { time: number; duration: number; progress: number; playing: boolean; speed: number }
type MotionClock = {
  getState: () => Readonly<MotionState>
  subscribe: (listener: (state: Readonly<MotionState>) => void) => () => void
  play: () => void; pause: () => void; replay: () => void
  seek: (seconds: number) => void; setSpeed: (speed: number) => void
}
export type MotionToolbarProps = {
  clock?: MotionClock
  label?: string
  positionLabel?: string
  detail?: string
  placement?: 'floating' | 'inline'
  showReplay?: boolean
  showSpeed?: boolean
  showTime?: boolean
  speeds?: number[]
  className?: string
  style?: CSSProperties
}

export function MotionToolbar({ clock, label = 'Playback', positionLabel, detail,
  placement = 'floating', showReplay = true, showSpeed = true, showTime = true,
  speeds = [0.5, 1, 2], className = '', style }: MotionToolbarProps) {
  const [state, setState] = useState<Readonly<MotionState> | undefined>(() => clock?.getState())
  useEffect(() => {
    setState(clock?.getState())
    return clock?.subscribe(setState)
  }, [clock])
  const available = Boolean(clock && state)
  const playing = state?.playing ?? false
  const time = state?.time ?? 0
  const duration = state?.duration ?? 1
  const speed = state?.speed ?? 1
  const choices = [...new Set([...speeds, speed].filter(value => Number.isFinite(value) && value > 0))].sort((a, b) => a - b)
  const elapsed = `${time.toFixed(1)} / ${duration}s`
  const valueText = [positionLabel, detail, elapsed].filter(Boolean).join(' · ')
  return <div role="group" aria-label={label} data-motion-placement={placement}
    className={`dashboard-motion ${className}`} style={style}>
    <div className="dashboard-motion-actions">
      <button type="button" className="dashboard-button dashboard-motion-play" disabled={!available}
        onClick={() => playing ? clock?.pause() : clock?.play()}>
        {playing ? <Pause aria-hidden="true" size={16}/> : <Play aria-hidden="true" size={16}/>}
        {playing ? 'Pause' : 'Play'}
      </button>
      {showReplay && <button type="button" className="dashboard-button dashboard-motion-replay"
        disabled={!available} onClick={() => clock?.replay()}>
        <RotateCcw aria-hidden="true" size={16}/><span>Replay</span>
      </button>}
    </div>
    <div className="dashboard-motion-position">
      {(positionLabel || detail) && <div className="dashboard-motion-label">
        {positionLabel && <strong>{positionLabel}</strong>}{detail && <span>{detail}</span>}
      </div>}
      <input aria-label="Playback position" aria-valuetext={valueText} type="range"
        min="0" max={duration} step="0.01" value={time} disabled={!available}
        style={{ '--motion-progress': `${(state?.progress ?? 0) * 100}%` } as CSSProperties}
        onChange={event => clock?.seek(Number(event.target.value))}/>
    </div>
    {showTime && <span className="dashboard-motion-time" aria-label="Elapsed playback time">{elapsed}</span>}
    {showSpeed && <select className="dashboard-motion-speed" aria-label="Playback speed"
      value={speed} disabled={!available} onChange={event => clock?.setSpeed(Number(event.target.value))}>
      {choices.map(value => <option key={value} value={value}>{value}×</option>)}
    </select>}
  </div>
}
