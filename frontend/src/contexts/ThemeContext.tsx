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
    const load = () => { void useAppearanceStore.getState().load(scope) }
    load()
    const unsubscribe = liveFeed.subscribe(['appearance'], null, load)
    const focus = () => { if (document.visibilityState === 'visible') load() }
    document.addEventListener('visibilitychange', focus)
    return () => { unsubscribe(); document.removeEventListener('visibilitychange', focus) }
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
      // Only shipped renderers may be selected. Theme CSS never supplies script.
      const backgroundScene = getComputedStyle(document.documentElement).getPropertyValue('--aw-background').trim() === 'neon-orbit' ? 'neon-orbit' : 'none'
      document.documentElement.dataset.appearanceScene = backgroundScene
      useAppearanceStore.setState({ backgroundScene })
      return () => { document.adoptedStyleSheets = document.adoptedStyleSheets.filter(item => item !== sheet); delete document.documentElement.dataset.appearanceScene }
    } catch { setCssWarning(true); useAppearanceStore.setState({ backgroundScene: 'none' }); delete document.documentElement.dataset.appearanceScene }
  }, [current.css, profile])
  const theme: Theme = profile
  return <ThemeContext.Provider value={{ theme, toggleTheme: () => setTheme(theme === 'light' ? 'dark' : 'light'), setTheme }}>
    {children}
    {cssWarning && <div role="status" className="fixed bottom-2 right-2 z-[9999] max-w-sm rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow">Some custom CSS was skipped. External resources are not allowed.</div>}
  </ThemeContext.Provider>
}
