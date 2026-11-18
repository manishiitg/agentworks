import { useEffect } from 'react'
import api from '../services/api'
import { shownMode, shownTheme, useAppearanceStore, type AppearanceBackgroundStatus } from '../stores/useAppearanceStore'
import type { DroppedCSS } from './appearanceCss'
import { APPEARANCE_PREVIEW_PATH } from '../components/appearance/previewPath'

/** What this browser actually did with a theme, stored by the server for the agent
 * that wrote it (#3085): dropped CSS, how many elements each theme hook matched,
 * and how the background is running. */
export type AppearanceReport = {
  theme: string
  source: 'app' | 'preview'
  mode: 'light' | 'dark'
  at: string
  dropped: DroppedCSS[]
  hooks: Record<string, number>
  background: AppearanceBackgroundStatus
}

const REPORT_DELAY_MS = 2000
const MAX_HOOKS = 200
const MAX_REPORT_BYTES = 31 * 1024
let lastSent = ''

/** One count per hook selector the server's guide names; run once per report. */
export function countHooks(hooks: string[] | undefined): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const hook of (hooks ?? []).slice(0, MAX_HOOKS)) {
    try {
      counts[hook] = document.querySelectorAll(hook).length
    } catch {
      counts[hook] = 0 // Not a selector this browser understands.
    }
  }
  return counts
}

/** Sends a report about 2 s after the shown theme or background changes, only when its content changed. */
export function useAppearanceReport(dropped: DroppedCSS[]) {
  const theme = useAppearanceStore(shownTheme)
  const mode = useAppearanceStore(shownMode)
  const display = useAppearanceStore(state => !!state.display)
  const loaded = useAppearanceStore(state => state.loaded)
  const scope = useAppearanceStore(state => state.scope)
  const background = useAppearanceStore(state => state.background)
  const hooks = useAppearanceStore(state => state.guide?.hooks)
  const name = theme.name
  useEffect(() => {
    // The preview page reports only what it shows; the app only after the saved look loaded.
    const source = display ? 'preview' : window.location.pathname === APPEARANCE_PREVIEW_PATH ? null : 'app'
    if (!source || (source === 'app' && !loaded)) return
    const timer = setTimeout(() => {
      const report: Omit<AppearanceReport, 'at'> = { theme: name, source, mode, dropped, hooks: countHooks(hooks), background: background ?? { kind: 'none', state: 'stopped' } }
      // The server takes up to 32 KiB; long dropped lists give way first.
      while (report.dropped.length && new TextEncoder().encode(JSON.stringify(report)).length > MAX_REPORT_BYTES) report.dropped = report.dropped.slice(0, -1)
      const key = JSON.stringify([scope, report])
      if (key === lastSent) return
      lastSent = key
      void Promise.resolve()
        .then(() => api.put('/api/appearance', { operation: 'report', report: { ...report, at: new Date().toISOString() } }, { skipSessionContext: true }))
        .catch(() => { if (lastSent === key) lastSent = '' })
    }, REPORT_DELAY_MS)
    return () => clearTimeout(timer)
  }, [name, mode, display, loaded, scope, background, hooks, dropped])
}
