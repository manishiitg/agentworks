import { useEffect, useState } from 'react'
import { Lightbulb } from 'lucide-react'
import api from '../../services/api'
import { PRODUCT_TIPS_PREFERENCE_CHANGED } from '../../utils/productTipsPreference'

/**
 * Account menu item: working tips on or off. The setting lives on the server, per person; this only shows what the
 * server says and sends the person's choice.
 */
export default function TipsToggle() {
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let stopped = false
    void api.get<{ enabled: boolean }>('/api/product-tips/preference', { skipSessionContext: true })
      .then(response => { if (!stopped) setEnabled(response.data.enabled) })
      .catch(() => undefined)
    return () => { stopped = true }
  }, [])
  if (enabled === null) return null
  return (
    <button
      type="button"
      role="menuitemcheckbox"
      aria-checked={enabled}
      disabled={busy}
      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent disabled:opacity-60"
      onClick={async () => {
        setBusy(true)
        try {
          const response = await api.put<{ enabled: boolean }>('/api/product-tips/preference', { enabled: !enabled }, { skipSessionContext: true })
          setEnabled(response.data.enabled)
          window.dispatchEvent(new CustomEvent(PRODUCT_TIPS_PREFERENCE_CHANGED, { detail: { enabled: response.data.enabled } }))
        } catch { /* the switch keeps showing what the server last said */ } finally { setBusy(false) }
      }}
    >
      <Lightbulb className="h-4 w-4 text-muted-foreground" />
      <span>Tips while working</span><span className="ml-auto text-xs text-muted-foreground">{enabled ? 'On' : 'Off'}</span>
    </button>
  )
}
