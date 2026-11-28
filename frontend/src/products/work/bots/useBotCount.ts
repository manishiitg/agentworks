import { useEffect, useState } from 'react'
import { BOTS_CHANGED_EVENT, listBots } from './botsApi'

/** How many bots are shared with you (#4545), for the left menu's Bots row; read again whenever bots change. `key`
 * changes when the server to read changes (the Mac's signed-in server); null reads nothing. */
export function useBotCount(key: string | null): number {
  const [count, setCount] = useState(0)
  useEffect(() => {
    if (key === null) return
    let cancelled = false
    const load = () => { void listBots().then(({ bots }) => { if (!cancelled) setCount(bots.length) }).catch(() => { if (!cancelled) setCount(0) }) }
    load()
    window.addEventListener(BOTS_CHANGED_EVENT, load)
    return () => { cancelled = true; window.removeEventListener(BOTS_CHANGED_EVENT, load) }
  }, [key])
  return key === null ? 0 : count
}
