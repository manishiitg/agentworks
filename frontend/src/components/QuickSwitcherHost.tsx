import { lazy, Suspense, useEffect } from 'react'

let loading: Promise<typeof import('./QuickSwitcher')> | undefined
export function preloadQuickSwitcher() {
  return loading ??= import('./QuickSwitcher')
}
const QuickSwitcher = lazy(preloadQuickSwitcher)

export default function QuickSwitcherHost(props: { isOpen: boolean; onClose: () => void; initialQuery?: string }) {
  // Escape must also work while the first chunk request is still pending.
  useEffect(() => {
    if (!props.isOpen) return
    const close = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); props.onClose() }
    }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [props.isOpen, props.onClose])
  return props.isOpen ? <Suspense fallback={null}><QuickSwitcher {...props} /></Suspense> : null
}
