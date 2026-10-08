import { Laptop } from 'lucide-react'
import { useCodeFilesPreference } from './codeLocalFiles'

/** Read-only context, shown only when this chat works on the user's own computer. Server files are the normal case and
 *  say nothing. Connection changes belong to the right-side settings. */
export function CodeChatConnectionStatus({ sessionId }: { sessionId: string }) {
  const preference = useCodeFilesPreference(sessionId)
  if (preference.location !== 'computer') return null
  const label = `Local files · ${preference.target?.resource_id || 'setup needed'}`
  const Icon = Laptop
  return <span role="status" aria-label={`File connection: ${label}`} title={`${preference.target ? `${preference.target.device_id} / ${preference.target.resource_id}. ` : ''}Agent and model run on the server; commands run on your computer. Manage the connection in the right-side panel.`}
    className="inline-flex h-7 min-w-0 max-w-48 items-center gap-1.5 text-[11px] text-muted-foreground">
    <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
    <span className="truncate">{label}</span>
  </span>
}
