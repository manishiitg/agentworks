import React, { useLayoutEffect, useState } from 'react'
import { useAppearanceStore } from '../stores/useAppearanceStore'
import { applyAppearanceTokens } from './appThemeTokens'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { liveFeed } from '../services/liveFeed'
import { safeAppearanceSheet } from './appearanceCss'
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
      const { sheet, removed } = safeAppearanceSheet(current.css)
      document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
      setCssWarning(removed)
      // A custom CSS background also needs the page's inner opaque wrappers cleared.
      // Existing translucent materials opt in too: custom themes need no hidden page flag.
      const hasBackdrop = Object.values(current.materials ?? {}).some(material => (material.opacity ?? 1) < 1)
      if (hasBackdrop) document.documentElement.dataset.appearanceBackdrop = ''
      else delete document.documentElement.dataset.appearanceBackdrop
      return () => { document.adoptedStyleSheets = document.adoptedStyleSheets.filter(item => item !== sheet); delete document.documentElement.dataset.appearanceBackdrop }
    } catch { setCssWarning(true); delete document.documentElement.dataset.appearanceBackdrop }
  }, [current, profile])
  const theme: Theme = profile
  return <ThemeContext.Provider value={{ theme, toggleTheme: () => setTheme(theme === 'light' ? 'dark' : 'light'), setTheme }}>
    {children}
    {cssWarning && <div role="status" className="fixed bottom-2 right-2 z-[9999] max-w-sm rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow">Some custom CSS was skipped. External resources are not allowed.</div>}
  </ThemeContext.Provider>
}
