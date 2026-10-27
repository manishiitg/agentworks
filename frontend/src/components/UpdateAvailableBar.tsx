import { useEffect, useState } from 'react'
import { RefreshCw, X } from 'lucide-react'

// A tab kept open (pinned) keeps running the build it loaded. The server serves the current build's
// index.html, whose entry script name changes with every build, so comparing that name with the
// one this tab runs tells whether AgentWorks was updated since the tab opened.
const ENTRY = /\/assets\/index-[\w-]+\.js/
const CHECK_EVERY_MS = 5 * 60 * 1000

function loadedEntry(): string | undefined {
  for (const script of Array.from(document.scripts)) {
    const match = script.src.match(ENTRY)
    if (match) return match[0]
  }
  return undefined
}

export function UpdateAvailableBar() {
  const [updated, setUpdated] = useState(false)
  const [dismissed, setDismissed] = useState(false)

  useEffect(() => {
    const current = loadedEntry()
    // The desktop app and the dev server load the bundle differently; there is nothing to compare.
    if (!current || window.location.protocol === 'file:') return
    let stopped = false
    const check = async () => {
      if (stopped || document.visibilityState === 'hidden') return
      try {
        const response = await fetch('/', { cache: 'no-store', credentials: 'same-origin' })
        if (!response.ok) return
        const served = (await response.text()).match(ENTRY)?.[0]
        if (served && served !== current) setUpdated(true)
      } catch {
        // Offline or the server is restarting: try again at the next check.
      }
    }
    const timer = window.setInterval(check, CHECK_EVERY_MS)
    window.addEventListener('focus', check)
    document.addEventListener('visibilitychange', check)
    return () => {
      stopped = true
      window.clearInterval(timer)
      window.removeEventListener('focus', check)
      document.removeEventListener('visibilitychange', check)
    }
  }, [])

  if (!updated || dismissed) return null
  return (
    <div role="status" className="fixed inset-x-0 top-0 z-[100] flex items-center justify-center gap-3 border-b border-primary/30 bg-background/95 px-4 py-1.5 text-sm text-foreground shadow-sm backdrop-blur">
      <RefreshCw className="h-3.5 w-3.5 text-primary" />
      <span>AgentWorks was updated.</span>
      <button type="button" onClick={() => window.location.reload()} className="rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90">
        Reload to get the latest
      </button>
      <button type="button" aria-label="Dismiss" onClick={() => setDismissed(true)} className="ml-1 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}
