import { useEffect, useState } from 'react'
import { schedulerApi } from '../api/scheduler'

/**
 * Global scheduler pause for surfaces outside the Schedules panel (e.g. the
 * top-bar Schedules button). Polls the scheduler config; null until the
 * first load settles. A failed poll keeps the last known value — it must
 * never flip the indicator on its own.
 */
export function useGlobalSchedulerPaused(product?: string, pollMs = 30_000): boolean | null {
  const [paused, setPaused] = useState<boolean | null>(null)

  useEffect(() => {
    let disposed = false
    const refresh = async () => {
      try {
        const config = await schedulerApi.getConfig()
        // Paused for everything, or for this product on its own (PLAT-782).
        if (!disposed) setPaused(!!config.globally_paused || (product !== undefined && !!config.paused_products?.includes(product)))
      } catch {
        // Keep the last known value.
      }
    }
    void refresh()
    const interval = window.setInterval(() => {
      if (!document.hidden) void refresh()
    }, pollMs)
    return () => {
      disposed = true
      window.clearInterval(interval)
    }
  }, [pollMs, product])

  return paused
}
