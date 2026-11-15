import { useEffect, useState, type CSSProperties } from 'react'
import { createPortal } from 'react-dom'
import './sceneMaterials.css'
import './deepSpace.css'

// Fixed artwork: bounded DOM, no canvas loop, randomness, network or input capture.
const stars = Array.from({ length: 80 }, (_, i) => ({
  x: (i * 431 + 73) % 1600, y: (i * 307 + 97) % 1000,
  radius: i % 9 === 0 ? 1.6 : .65, opacity: .2 + (i % 5) * .12,
}))
const planets = [
  { orbit: 170, size: 7, period: 55, phase: 35, colour: '#a9b5cc' },
  { orbit: 280, size: 14, period: 90, phase: 150, colour: '#dcb38d' },
  { orbit: 420, size: 23, period: 140, phase: 260, colour: '#6aaaca' },
  { orbit: 590, size: 37, period: 210, phase: 205, colour: '#ba9bcb', ring: true },
  { orbit: 800, size: 18, period: 300, phase: 65, colour: '#81bfb8' },
]

/** Passive decoration; existing mounted activity hooks alone resume its CSS clock. */
export default function DeepSpaceBackground() {
  const [visible, setVisible] = useState(() => document.visibilityState === 'visible')
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === 'visible')
    document.addEventListener('visibilitychange', update)
    return () => document.removeEventListener('visibilitychange', update)
  }, [])
  return createPortal(<div className="aw-deep-space" aria-hidden="true" data-visible={String(visible)}>
    <div className="aw-space-nebula" />
    <svg className="aw-space-stars" viewBox="0 0 1600 1000" preserveAspectRatio="xMidYMid slice">
      {stars.map((star, i) => <circle key={i} cx={star.x} cy={star.y} r={star.radius} opacity={star.opacity} />)}
    </svg>
    <div className="aw-space-system">
      <div className="aw-space-sun" />
      {planets.map(planet => <div key={planet.orbit} className="aw-space-track" style={{
        width: planet.orbit, height: planet.orbit, '--aw-orbit-period': `${planet.period}s`,
        '--aw-orbit-phase': `${planet.phase}deg`, '--aw-planet-size': `${planet.size}px`, '--aw-planet-colour': planet.colour,
      } as CSSProperties}>
        <div className="aw-space-revolution"><div className={`aw-space-planet${planet.ring ? ' aw-space-ringed' : ''}`} /></div>
      </div>)}
    </div>
    <div className="aw-space-reading-shade" />
  </div>, document.body)
}
