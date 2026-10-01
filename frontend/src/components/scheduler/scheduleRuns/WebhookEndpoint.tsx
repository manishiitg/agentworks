import { useState } from 'react'
import { Copy } from 'lucide-react'
import { apiTriggerURL } from '../../../api/workflowWebhooks'
import { copyToClipboard } from '../../../utils/textUtils'

export function WebhookEndpoint({ id, name, address }: { id: string; name: string; address?: string }) {
  const url = address || apiTriggerURL(`/api/hooks/workflow/${encodeURIComponent(id)}`)
  const [copyStatus, setCopyStatus] = useState('')
  const copy = async () => {
    setCopyStatus(await copyToClipboard(url) ? address ? 'Address copied' : 'URL copied' : 'Copy failed — select the value to copy it.')
  }
  return (
    <div className="mt-2 space-y-1">
      <div className="flex items-start gap-2 rounded border border-border bg-background p-2">
        <span className="shrink-0 text-[10px] font-semibold text-muted-foreground">{address ? 'EMAIL' : 'POST'}</span>
        <code className="min-w-0 flex-1 select-all break-all text-xs text-foreground">{url}</code>
        <button type="button" onClick={() => void copy()} aria-label={`Copy ${address ? 'email address' : 'webhook URL'} for ${name}`} title={address ? 'Copy email address' : 'Copy webhook URL'} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
          <Copy className="h-3.5 w-3.5" />
        </button>
      </div>
      <span role="status" className="text-[11px] text-muted-foreground">{copyStatus}</span>
    </div>
  )
}
