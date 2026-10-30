import { useEffect, useRef, useState } from 'react'

/** Reveal the native menu thumb during scrolling, then let it disappear. */
export function useMenuScrollActivity() {
  const [scrolling, setScrolling] = useState(false)
  const idle = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(idle.current), [])
  return {
    'data-scrolling': scrolling || undefined,
    onScroll: () => {
      setScrolling(true)
      clearTimeout(idle.current)
      idle.current = setTimeout(() => setScrolling(false), 800)
    },
  }
}
