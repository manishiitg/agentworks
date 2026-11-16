import React, { useLayoutEffect, useState } from 'react'
import { useAppearanceStore } from '../stores/useAppearanceStore'
import { applyAppearanceTokens } from './appThemeTokens'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { liveFeed } from '../services/liveFeed'
import { loadAppearanceFonts, safeAppearanceSheet } from './appearanceCss'
import { ThemeContext, type Theme } from './ThemeContext'

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const current = useAppearanceStore(state => state.preview ?? state.current)
  const profile = useAppearanceStore(state => state.profile)
  const setTheme = useAppearanceStore(state => state.setProfile)
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [cssWarning, setCssWarning] = useState(false)
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
  useLayoutEffect(() => {
    try {
      const { sheet, removed, imports } = safeAppearanceSheet(current.css)
      const controller = new AbortController()
      let fontSheet: CSSStyleSheet | undefined
      document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
      setCssWarning(removed)
      if (imports.length) void loadAppearanceFonts(imports, controller.signal).then(result => {
        if (controller.signal.aborted) return
        fontSheet = result.sheet
        const sheets = document.adoptedStyleSheets
        const index = sheets.indexOf(sheet)
        if (index < 0) return
        // Imports precede the person's own overrides.
        document.adoptedStyleSheets = [...sheets.slice(0, index), fontSheet, ...sheets.slice(index)]
        setCssWarning(removed || result.removed)
      }).catch(() => { if (!controller.signal.aborted) setCssWarning(true) })
      // A custom CSS background also needs the page's inner opaque wrappers cleared.
      // Existing translucent materials opt in too: custom themes need no hidden page flag.
      const hasBackdrop = !!current.background_video || !!current.background_image || !!current.background_script || !!current.background_shader || !!current.decorations?.length || Object.values(current.materials ?? {}).some(material => (material.opacity ?? 1) < 1)
      if (hasBackdrop) document.documentElement.dataset.appearanceBackdrop = ''
      else delete document.documentElement.dataset.appearanceBackdrop
      return () => { controller.abort(); document.adoptedStyleSheets = document.adoptedStyleSheets.filter(item => item !== sheet && item !== fontSheet); delete document.documentElement.dataset.appearanceBackdrop }
    } catch { setCssWarning(true); delete document.documentElement.dataset.appearanceBackdrop }
  }, [current, profile, actor, server])
  const theme: Theme = profile
  return <ThemeContext.Provider value={{ theme, toggleTheme: () => setTheme(theme === 'light' ? 'dark' : 'light'), setTheme }}>
    {children}
    {cssWarning && <div role="status" className="fixed bottom-2 right-2 z-[9999] max-w-sm rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow">Some custom CSS or fonts could not be applied. Use HTTPS resources and keep controls visible.</div>}
  </ThemeContext.Provider>
}
