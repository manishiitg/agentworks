import { useSyncExternalStore } from 'react'

/** Test files by their usual names: a tests folder, or test/spec in the file name
 * (login.test.ts, login_test.go, test_login.py, LoginSpec.kt). */
export function isTestPath(path: string): boolean {
  const name = path.split('/').pop() || ''
  return /(^|\/)(__tests__|tests?|spec|specs|e2e)\//i.test(path)
    || /[._-](test|spec)s?\.[^.]+$/i.test(name)
    || /^test_.+\.py$/i.test(name)
    || /(Test|Tests|Spec)\.[^.]+$/.test(name)
}

const KEY = 'agentworks.changes.hideTests'
const EVENT = 'agentworks:hide-tests'
const read = () => { try { return window.localStorage.getItem(KEY) === '1' } catch { return false } }
const subscribe = (change: () => void) => {
  window.addEventListener(EVENT, change); window.addEventListener('storage', change)
  return () => { window.removeEventListener(EVENT, change); window.removeEventListener('storage', change) }
}

/** The Changes tab's "Hide tests" switch, remembered on this computer. */
export function useHideTests(): [boolean, (hide: boolean) => void] {
  const hide = useSyncExternalStore(subscribe, read, () => false)
  const set = (next: boolean) => {
    try { window.localStorage.setItem(KEY, next ? '1' : '0') } catch { /* optional preference */ }
    window.dispatchEvent(new Event(EVENT))
  }
  return [hide, set]
}
