import { useSyncExternalStore } from 'react'

const query = '(max-width: 767px)'
const subscribe = (notify: () => void) => {
  const media = window.matchMedia(query)
  media.addEventListener('change', notify)
  return () => media.removeEventListener('change', notify)
}
/** Presentation only: the same breakpoint as the shared workspace grid. */
export function useMobileViewport() {
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}
