import { Laptop } from 'lucide-react'
import { useCodeFilesPreference } from './codeLocalFiles'

/** Shows the optional laptop connection alongside the server chat. */
export function CodeChatConnectionStatus({ sessionId }: { sessionId: string }) {
  const preference = useCodeFilesPreference(sessionId)
  if (preference.location !== 'computer') return null
  const label = `Laptop · ${preference.target?.resource_id || 'setup needed'}`
  const Icon = Laptop
  return <span role="status" aria-label={`File connection: ${label}`} title={`${preference.target ? `${preference.target.device_id} / ${preference.target.resource_id}. ` : ''}Crew tools run on the server. Extra laptop command and copy tools use this connection. Manage it in Settings.`}
    className="inline-flex h-7 min-w-0 max-w-48 items-center gap-1.5 text-[11px] text-muted-foreground">
    <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
    <span className="truncate">{label}</span>
  </span>
}
