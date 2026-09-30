import { useCallback, useEffect, useState, type RefObject } from 'react'
import { isGuideRemembered, rememberGuide } from '../utils/onboarding'

let activeTipKey: string | null = null

/** A first-visit hint waits for a visible section and yields to other dialogs. */
export function useContextualGuide(key: string, anchor: RefObject<HTMLElement | null>, enabled = true) {
  const [shownKey, setShownKey] = useState<string | null>(null)
  const dismiss = useCallback(() => {
    if (activeTipKey === key) activeTipKey = null
    setShownKey(null)
  }, [key])

  useEffect(() => {
    setShownKey(null)
    if (!enabled || isGuideRemembered(key)) return
    let timer: ReturnType<typeof setTimeout>
    let consumed = false
    const check = () => {
      const element = anchor.current
      if (!element) return
      const rect = element.getBoundingClientRect()
      const dialogs = Array.from(document.querySelectorAll('[data-testid="workflow-walkthrough-dialog"], [data-contextual-guide], [role="dialog"][aria-modal="true"]'))
      const blocked = dialogs.some(dialog => dialog.getAttribute('data-contextual-guide') !== key && !dialog.contains(element) && !element.parentElement?.contains(dialog))
      const focused = document.activeElement
      const editing = focused instanceof HTMLElement && (focused.matches('input, textarea, [role="combobox"]') || focused.isContentEditable)
      const visible = rect.width > 0 && rect.height > 0 && rect.top >= 0 && rect.bottom <= window.innerHeight && rect.left >= 0 && rect.right <= window.innerWidth
      if (consumed) {
        if (blocked || !visible) {
          if (activeTipKey === key) activeTipKey = null
          setShownKey(null)
        }
        return
      }
      if (blocked || !visible || editing || activeTipKey || isGuideRemembered(key)) return
      // Remember showing it, even if the user navigates away before closing it.
      // This prevents repeat popups on remount or on the next desktop launch.
      consumed = true
      activeTipKey = key
      rememberGuide(key)
      setShownKey(key)
    }
    const schedule = () => {
      clearTimeout(timer)
      timer = setTimeout(check, 600)
    }
    schedule()
    const observer = new MutationObserver(() => consumed ? check() : schedule())
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden', 'aria-expanded'] })
    window.addEventListener('resize', schedule)
    window.addEventListener('scroll', schedule, true)
    document.addEventListener('focusout', schedule)
    return () => {
      if (activeTipKey === key) activeTipKey = null
      clearTimeout(timer)
      observer.disconnect()
      window.removeEventListener('resize', schedule)
      window.removeEventListener('scroll', schedule, true)
      document.removeEventListener('focusout', schedule)
    }
  }, [anchor, enabled, key])

  return { open: enabled && shownKey === key, dismiss }
}
