import { useEffect, useState } from 'react'

/** One quiet receipt, never a sign-in or hook-trust prompt. */
export function NativeSetupNotice() {
  const [visible, setVisible] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let active = true
    void window.electronAPI?.nativeSetupNotice?.().then(show => { if (active) setVisible(show) }).catch(() => {})
    return () => { active = false }
  }, [])
  useEffect(() => { if (visible) void window.electronAPI?.nativeSetupNoticeSeen?.().catch(() => {}) }, [visible])
  if (!visible) return null
  return <aside role="status" className="fixed bottom-4 left-4 right-4 z-50 flex items-center gap-2 rounded-md border border-border bg-popover p-3 text-xs text-popover-foreground shadow-sm md:left-auto md:max-w-xl">
    <span>{failed ? 'Automatic setup is off. Some hooks could not be removed; try Turn off again.' : 'Your Claude Code and Codex chats in Team folders now show in your Team. Codex may ask once to Allow.'}</span>
    <button disabled={busy} className="shrink-0 underline disabled:opacity-50" onClick={async () => {
      setBusy(true)
      try { const out = await window.electronAPI?.turnOffNativeSetup?.(); if (out?.ok) setVisible(false); else setFailed(true) }
      catch { setFailed(true) }
      finally { setBusy(false) }
    }}>{busy ? 'Turning off…' : 'Turn off'}</button>
    <button aria-label="Dismiss native setup notice" className="shrink-0 px-1" disabled={busy} onClick={() => setVisible(false)}>×</button>
  </aside>
}
