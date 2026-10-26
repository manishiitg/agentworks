import { useLayoutEffect, type RefObject } from 'react'

/** Size the phone shell to the visible browser area, including keyboard resizes. */
export function useAppViewport(ref: RefObject<HTMLElement | null>) {
  useLayoutEffect(() => {
    const shell = ref.current
    if (!shell) return
    const phone = window.matchMedia('(max-width: 767px)')
    const viewport = window.visualViewport
    const clear = () => {
      shell.style.removeProperty('--app-viewport-height')
      shell.style.removeProperty('--app-viewport-top')
    }
    const update = () => {
      if (!phone.matches) { clear(); return }
      // Let pinch zoom magnify the existing page instead of reflowing it.
      if (viewport && Math.abs(viewport.scale - 1) > 0.01) return
      const height = viewport?.height ?? window.innerHeight
      if (height <= 0) return
      shell.style.setProperty('--app-viewport-height', `${height}px`)
      shell.style.setProperty('--app-viewport-top', `${viewport?.offsetTop ?? 0}px`)
    }
    update()
    phone.addEventListener('change', update)
    window.addEventListener('resize', update)
    viewport?.addEventListener('resize', update)
    viewport?.addEventListener('scroll', update)
    return () => {
      phone.removeEventListener('change', update)
      window.removeEventListener('resize', update)
      viewport?.removeEventListener('resize', update)
      viewport?.removeEventListener('scroll', update)
      clear()
    }
  }, [ref])
}
