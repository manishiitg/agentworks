import { useEffect, useState } from 'react'
import { RefreshCw, X } from 'lucide-react'
import { useChatStore } from '../stores/useChatStore'
import { reportClientEvent } from '../services/clientEvents'

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

// On a phone a reload is awkward (in-app browsers have none) and servers update often (owner, 3 Nov): reload quietly
// at a safe moment, and keep an unsent message across it.
const DRAFT_KEY = 'agentworks:reload-draft'
const isPhone = () => window.matchMedia('(max-width: 767px)').matches
function composer(): HTMLTextAreaElement | null {
  return document.querySelector('[data-app-main] textarea')
}
function busy(): boolean {
  const active = document.activeElement
  const typing = active instanceof HTMLTextAreaElement || active instanceof HTMLInputElement
  const text = Boolean(composer()?.value.trim())
  const sending = Object.values(useChatStore.getState().chatTabs).some(tab => Boolean(tab.config?.pendingSubmission))
  return typing || text || sending
}
function reloadNow(how: 'auto' | 'tap') {
  const draft = composer()?.value || ''
  try { if (draft.trim()) sessionStorage.setItem(DRAFT_KEY, JSON.stringify({ path: window.location.pathname + window.location.search, draft })) } catch { /* private mode: the draft is lost */ }
  reportClientEvent('app_reloaded', { detail: how })
  window.location.reload()
}
// After the reload, put the kept draft back into the same conversation's message box.
function restoreDraft() {
  let saved: { path: string; draft: string } | null = null
  try { saved = JSON.parse(sessionStorage.getItem(DRAFT_KEY) || 'null'); sessionStorage.removeItem(DRAFT_KEY) } catch { return }
  if (!saved || saved.path !== window.location.pathname + window.location.search) return
  const started = Date.now()
  const timer = window.setInterval(() => {
    const box = composer()
    if (!box && Date.now() - started < 10_000) return
    window.clearInterval(timer)
    if (!box || box.value) return
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(box, saved!.draft)
    box.dispatchEvent(new Event('input', { bubbles: true }))
  }, 300)
}

export function UpdateAvailableBar() {
  const [updated, setUpdated] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  useEffect(restoreDraft, [])
  // Phone: once an update is known, reload at the next safe moment (back in view, or 10 s without a touch).
  useEffect(() => {
    if (!updated || !isPhone()) return
    let lastTouch = Date.now()
    const touched = () => { lastTouch = Date.now() }
    const tryReload = () => { if (document.visibilityState === 'visible' && !busy()) reloadNow('auto') }
    const visible = () => { if (document.visibilityState === 'visible') tryReload() }
    const idle = window.setInterval(() => { if (Date.now() - lastTouch >= 10_000) tryReload() }, 2_000)
    window.addEventListener('touchstart', touched, { passive: true })
    document.addEventListener('visibilitychange', visible)
    return () => { window.clearInterval(idle); window.removeEventListener('touchstart', touched); document.removeEventListener('visibilitychange', visible) }
  }, [updated])

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
  if (isPhone()) return <button type="button" onClick={() => reloadNow('tap')} className="fixed left-1/2 top-[calc(env(safe-area-inset-top)+6px)] z-[100] -translate-x-1/2 rounded-full border border-primary/30 bg-background/95 px-3 py-1 text-xs text-foreground shadow-sm backdrop-blur">
    Update ready · Tap to refresh
  </button>
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
