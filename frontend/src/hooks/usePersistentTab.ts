import { useEffect, useState } from 'react'

// Remembers a tab row's selection across reloads. The stored value is only
// honored when it is still a valid tab; anything else falls back to the
// default. Storage failures (private mode, no window) silently disable
// persistence instead of breaking the panel.
export function usePersistentTab<T extends string>(
  storageKey: string,
  defaultValue: T,
  validValues: readonly T[],
  explicitValue?: T,
): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    if (explicitValue !== undefined) return explicitValue
    if (typeof window === 'undefined') return defaultValue
    try {
      const stored = window.localStorage.getItem(storageKey)
      return stored !== null && (validValues as readonly string[]).includes(stored)
        ? (stored as T)
        : defaultValue
    } catch {
      return defaultValue
    }
  })

  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, value)
    } catch {
      // Persistence is a convenience; a failing store must not break tabs.
    }
  }, [storageKey, value])

  return [value, setValue]
}
