import { useEffect, useRef, useState } from 'react'
import type { AppearanceScript } from '../../stores/appearanceTheme'
import { backgroundStatus } from './backgroundStatus'
import { mountScriptSandbox } from './scriptSandbox'

/** The script background's host. Window resizes need no rebuild: every frame
 * already carries the current size and the Worker resizes its canvas (#3085). */
export function PersonalScript({ script, colors, working, anyWorking = working }: { script: AppearanceScript; colors: Record<string, string>; working: boolean; anyWorking?: boolean }) {
  const host = useRef<HTMLDivElement>(null)
  const latest = useRef({ colors, working, anyWorking })
  latest.current = { colors, working, anyWorking }
  const [ready, setReady] = useState(false)
  useEffect(() => {
    const motion = matchMedia('(prefers-reduced-motion: reduce)')
    const report = backgroundStatus('script')
    let engine: ReturnType<typeof mountScriptSandbox> | undefined
    const refresh = () => {
      engine?.destroy()
      engine = undefined
      setReady(false)
      delete document.documentElement.dataset.appearanceScript
      if (document.hidden) {
        report.state('paused')
        return
      }
      engine = mountScriptSandbox(host.current!, script, () => latest.current, state => {
        if (state === 'ready') {
          setReady(true)
          document.documentElement.dataset.appearanceScript = ''
          report.state(motion.matches ? 'paused' : 'running')
        }
        if (state === 'failed' || state === 'stopped') {
          setReady(false)
          delete document.documentElement.dataset.appearanceScript
        }
        if (state === 'failed') report.state('error', 'The script failed or did not start.')
        if (state === 'stopped') report.state('stopped')
        if (state === 'throttled') report.state('throttled')
        if (state === 'failed' || state === 'stopped' || state === 'throttled') console.info(`[APPEARANCE_SCRIPT] outcome=${state}`)
      }, report.fps)
    }
    refresh()
    document.addEventListener('visibilitychange', refresh)
    motion.addEventListener('change', refresh)
    return () => {
      engine?.destroy()
      delete document.documentElement.dataset.appearanceScript
      document.removeEventListener('visibilitychange', refresh)
      motion.removeEventListener('change', refresh)
      report.clear()
    }
  }, [script.code, script.fps])
  return <div data-theme-script ref={host} aria-hidden inert style={{ visibility: ready ? 'visible' : 'hidden' }} />
}
