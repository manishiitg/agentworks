import { useEffect, useState } from 'react'
import QuickSwitcher, { preloadQuickSwitcher } from '../QuickSwitcherHost'
import { markFeatureUsed } from '../../utils/featureUsage'

/** Global search stays available while the main page changes or loads. */
export function AppQuickSwitcher() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  useEffect(() => {
    const warm = () => { void preloadQuickSwitcher().catch(() => {}) }
    if (typeof window.requestIdleCallback === 'function') {
      const idle = window.requestIdleCallback(warm, { timeout: 1500 })
      return () => window.cancelIdleCallback(idle)
    }
    const timer = window.setTimeout(warm, 1500)
    return () => window.clearTimeout(timer)
  }, [])
  useEffect(() => {
    const show = (event: Event) => {
      void preloadQuickSwitcher().catch(() => {})
      setQuery((event as CustomEvent<{ query?: string }>).detail?.query || '')
      setOpen(true)
    }
    const keyboard = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'k') return
      event.preventDefault()
      void preloadQuickSwitcher().catch(() => {})
      setQuery(''); markFeatureUsed('quick-switcher'); setOpen(value => !value)
    }
    window.addEventListener('open-quick-switcher', show)
    window.addEventListener('keydown', keyboard)
    return () => { window.removeEventListener('open-quick-switcher', show); window.removeEventListener('keydown', keyboard) }
  }, [])
  useEffect(() => { if (open) window.dispatchEvent(new CustomEvent('quick-switcher-opened')) }, [open])
  return <QuickSwitcher isOpen={open} initialQuery={query} onClose={() => setOpen(false)} />
}
