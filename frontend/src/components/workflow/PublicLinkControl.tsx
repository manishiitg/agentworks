import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import { Button } from '../ui/Button'
import { Switch } from '../ui/Switch'

export type PublicLinkMode = 'on' | 'update' | 'off'
/** One dashboard's public link, worded by the server (dashboard_public_link.go); this only shows it. */
export type PublicLink = {
  dashboard_id: string; title?: string; label: string; on: boolean; url?: string; line?: string; copy_label?: string; update_label?: string; blocked?: string
  dialog?: { title: string; body: string; warning: string; note: string; cancel: string; confirm: string }
}

const failure = (cause: unknown) => {
  const data = (cause as { response?: { data?: { error?: { message?: string } | string } } })?.response?.data
  return (typeof data?.error === 'string' ? data.error : data?.error?.message) || (cause instanceof Error ? cause.message : 'Could not change the public link. Try again.')
}

/** The public link row, the same on the Team tab's Dashboards card and the Crew's Share screen. change sends the
 * person's choice and answers with the link as the server now has it. */
export function PublicLinkControl({ link: given, change }: { link: PublicLink; change: (mode: PublicLinkMode) => Promise<PublicLink> }) {
  const [link, setLink] = useState(given)
  const [asking, setAsking] = useState(false)
  const [saving, setSaving] = useState<PublicLinkMode>()
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  useEffect(() => setLink(given), [given])
  const save = async (mode: PublicLinkMode) => {
    setSaving(mode); setError('')
    try { setLink(await change(mode)); setAsking(false) } catch (cause) { setError(failure(cause)) } finally { setSaving(undefined) }
  }
  const copy = async () => {
    if (!link.url) return
    try { await navigator.clipboard.writeText(link.url); setCopied(true); window.setTimeout(() => setCopied(false), 1500) } catch { setError('Could not copy the link.') }
  }
  const dialog = link.dialog
  return <div className="space-y-1 text-xs">
    <div className="flex items-center gap-2 text-muted-foreground">
      <span className="flex-1">{link.label}</span>
      {link.blocked ? null : saving === 'off' ? <Loader2 aria-label="Saving" className="h-3.5 w-3.5 shrink-0 animate-spin" />
        : <Switch aria-label={`${link.label}: ${link.title || link.dashboard_id}`} checked={link.on} disabled={!!saving} onCheckedChange={on => { if (on) setAsking(true); else void save('off') }} />}
    </div>
    {link.blocked && <p className="text-muted-foreground">{link.blocked}</p>}
    {link.on && link.url && <div className="flex items-center gap-2">
      <span title={link.url} className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-[11px]">{link.url}</span>
      <button type="button" onClick={() => void copy()} className="shrink-0 rounded-full border border-border px-2.5 py-0.5 hover:bg-muted">{copied ? '✓' : link.copy_label}</button>
    </div>}
    {link.on && link.line && <p className="text-muted-foreground">{link.line} <button type="button" disabled={!!saving} onClick={() => void save('update')} className="text-primary underline disabled:opacity-50">{link.update_label}</button>{saving === 'update' && <Loader2 aria-label="Saving" className="ml-1 inline h-3 w-3 animate-spin" />}</p>}
    {error && !asking && <p role="alert" className="text-destructive">{error}</p>}
    {asking && dialog && <ModalPortal>
      <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/50 p-4" onClick={() => { if (!saving) setAsking(false) }}>
        <div role="dialog" aria-modal="true" aria-label={dialog.title} onClick={event => event.stopPropagation()} className="w-full max-w-md space-y-3 rounded-xl border border-border bg-card p-5 text-sm shadow-md">
          <h3 className="text-[15px] font-semibold">{dialog.title}</h3>
          <p>{dialog.body}</p>
          <p className="rounded-lg bg-amber-500/10 px-3 py-2 text-[13px]">{dialog.warning}</p>
          <p className="text-xs text-muted-foreground">{dialog.note}</p>
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" disabled={!!saving} onClick={() => setAsking(false)}>{dialog.cancel}</Button>
            <Button size="sm" disabled={!!saving} onClick={() => void save('on')}>{saving === 'on' && <Loader2 aria-label="Saving" className="animate-spin" />}{dialog.confirm}</Button>
          </div>
        </div>
      </div>
    </ModalPortal>}
  </div>
}
