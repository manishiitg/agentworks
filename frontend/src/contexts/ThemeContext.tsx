import React, { useLayoutEffect, useState } from 'react'
import { shownMode, shownTheme, useAppearanceStore } from '../stores/useAppearanceStore'
import { applyAppearanceTokens } from './appThemeTokens'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { liveFeed } from '../services/liveFeed'
import { loadAppearanceFonts, safeAppearanceSheet, type DroppedCSS } from './appearanceCss'
import { useAppearanceReport } from './appearanceReport'
import { ThemeContext, type Theme } from './ThemeContext'

function readSheet(css: string) {
  try {
    return safeAppearanceSheet(css)
  } catch {
    return null
  }
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const current = useAppearanceStore(shownTheme)
  const profile = useAppearanceStore(shownMode)
  const setTheme = useAppearanceStore(state => state.setProfile)
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  useLayoutEffect(() => {
    const scope = JSON.stringify([server, actor])
    const store = useAppearanceStore.getState()
    store.resetScope(scope)
    if (!actor) return
    const load = (force: boolean) => () => { void useAppearanceStore.getState().load(scope, { force }) }
    load(true)()
    const unsubscribe = liveFeed.subscribe(['appearance'], null, load(true))
    // Coming back to the window reloads only a document that has gone stale.
    const focus = () => {
      const visible = document.visibilityState === 'visible'
      document.documentElement.dataset.appearanceVisible = String(visible)
      if (visible) load(false)()
    }
    document.documentElement.dataset.appearanceVisible = String(document.visibilityState === 'visible')
    document.addEventListener('visibilitychange', focus)
    return () => { unsubscribe(); document.removeEventListener('visibilitychange', focus); delete document.documentElement.dataset.appearanceVisible }
  }, [actor, server])
  useLayoutEffect(() => {
    const root = document.documentElement
    root.classList.remove('light', 'dark'); root.classList.add(profile)
    root.dataset.appearance = profile; root.dataset.theme = profile; root.style.colorScheme = profile
    return applyAppearanceTokens(root, current, profile)
  }, [current, profile])
  // Keyed on the CSS text alone: a Light/Dark toggle or a live-feed reload of the
  // same theme does not re-parse it or refetch its fonts (#3085).
  const css = current.css
  const [dropped, setDropped] = useState<DroppedCSS[]>([])
  useLayoutEffect(() => {
    const parsed = readSheet(css)
    if (!parsed) {
      setDropped([{ text: css.slice(0, 300), reason: 'the CSS could not be read' }])
      return
    }
    const { sheet, imports } = parsed
    const controller = new AbortController()
    let fontSheet: CSSStyleSheet | undefined
    document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
    setDropped(parsed.dropped)
    if (imports.length) {
      void loadAppearanceFonts(imports, controller.signal).then(result => {
        if (controller.signal.aborted) return
        fontSheet = result.sheet
        const sheets = document.adoptedStyleSheets
        const index = sheets.indexOf(sheet)
        if (index < 0) return
        // Imports precede the person's own overrides.
        document.adoptedStyleSheets = [...sheets.slice(0, index), fontSheet, ...sheets.slice(index)]
        if (result.dropped.length) setDropped([...parsed.dropped, ...result.dropped].slice(0, 50))
      }).catch(() => { /* Aborted: a newer theme replaced this one. */ })
    }
    return () => {
      controller.abort()
      document.adoptedStyleSheets = document.adoptedStyleSheets.filter(item => item !== sheet && item !== fontSheet)
    }
  }, [css])
  // A custom CSS background also needs the page's inner opaque wrappers cleared.
  // Existing translucent materials opt in too: custom themes need no hidden page flag.
  const hasBackdrop = !!current.background_video || !!current.background_image || !!current.background_script || !!current.background_shader
    || !!current.decorations?.length || Object.values(current.materials ?? {}).some(material => (material.opacity ?? 1) < 1)
  useLayoutEffect(() => {
    if (!hasBackdrop) return
    document.documentElement.dataset.appearanceBackdrop = ''
    return () => { delete document.documentElement.dataset.appearanceBackdrop }
  }, [hasBackdrop])
  useAppearanceReport(dropped)
  const theme: Theme = profile
  return <ThemeContext.Provider value={{ theme, toggleTheme: () => setTheme(theme === 'light' ? 'dark' : 'light'), setTheme }}>
    {children}
    {dropped.length > 0 && <div role="status" className="fixed bottom-2 right-2 z-[9999] max-w-sm rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow">{dropped.length === 1 ? '1 part' : `${dropped.length} parts`} of this theme's CSS could not be applied. The agent that made it can read which ones and why.</div>}
  </ThemeContext.Provider>
}
