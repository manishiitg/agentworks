import { useState } from 'react'
import { Link } from 'lucide-react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'

/** The link copies only text and target IDs, never attachments or settings. */
export function CopyPromptLinkButton({ crew, agent, prompt }: { crew: string; agent?: string; prompt: string }) {
  const [busy, setBusy] = useState(false)
  return <button type="button" disabled={busy || !prompt.trim()} title="Copy prompt link" aria-label="Copy prompt link" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted disabled:opacity-50"
    onClick={() => {
      if (busy) return
      setBusy(true)
      void api.post<{ url: string }>('/api/prompt-links', { crew, agent: agent || 'main', prompt }, { skipSessionContext: true })
        .then(({ data }) => navigator.clipboard.writeText(data.url))
        .then(() => useChatStore.getState().addToast('Prompt link copied. It opens a draft and sends nothing.', 'success'))
        .catch(() => useChatStore.getState().addToast('Could not copy this prompt link. Check the Crew’s server connection and try a shorter prompt.', 'error'))
        .finally(() => setBusy(false))
    }}><Link aria-hidden="true" className="h-3.5 w-3.5" /></button>
}
