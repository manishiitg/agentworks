import { useSyncExternalStore } from 'react'

const query = '(max-width: 767px)'
const hasMedia = () => typeof window.matchMedia === 'function'
const subscribe = (notify: () => void) => {
  if (!hasMedia()) return () => {}
  const media = window.matchMedia(query)
  media.addEventListener('change', notify)
  return () => media.removeEventListener('change', notify)
}
/** Presentation only: the same breakpoint as the shared workspace grid. */
export function useMobileViewport() {
  return useSyncExternalStore(subscribe, () => hasMedia() && window.matchMedia(query).matches, () => false)
}
