import { Laptop, Server } from 'lucide-react'
import { useCodeFilesPreference } from './codeLocalFiles'

/** Read-only context. Connection changes belong to the right-side settings. */
export function CodeChatConnectionStatus({ sessionId }: { sessionId: string }) {
  const preference = useCodeFilesPreference(sessionId)
  const local = preference.location === 'computer'
  const label = local ? `Local files · ${preference.target?.resource_id || 'setup needed'}` : 'Server files'
  const Icon = local ? Laptop : Server
  return <span role="status" aria-label={`File connection: ${label}`} title={local
    ? `${preference.target ? `${preference.target.device_id} / ${preference.target.resource_id}. ` : ''}Agent and model run on the server; commands run on your computer. Manage the connection in the right-side panel.`
    : 'Agent, files and commands run on the server. Manage the connection in the right-side settings.'}
    className="inline-flex h-7 min-w-0 max-w-48 items-center gap-1.5 text-[11px] text-muted-foreground">
    <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
    <span className="truncate">{label}</span>
  </span>
}
