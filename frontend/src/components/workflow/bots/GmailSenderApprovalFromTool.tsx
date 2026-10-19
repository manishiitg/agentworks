import { useMemo, useState } from 'react'
import { normalizeToolCallResultValue } from '../../../utils/toolCallFormatting'
import { Button } from '../../ui/Button'
import { normalizeMCPToolName } from '../../../utils/customToolNames'
import { GmailSenderApproval } from './GmailInboundPanel'
import { useGmailInboundUIEnabled } from './gmailAskAI'

// A result can invite a review, never supply the policy or approve it. The
// review fetches current server state and the existing endpoint checks the
// signed-in owner and the exact configuration they actually reviewed.
function approvalWorkspace(result: unknown): string | null {
  let value = result
  for (let depth = 0; depth < 5; depth++) {
    value = normalizeToolCallResultValue(value)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const record = value as Record<string, unknown>
    const hint = record.sender_approval as Record<string, unknown> | undefined
    if (hint?.type === 'gmail_sender_approval' && typeof hint.workspace_path === 'string' && hint.workspace_path) return hint.workspace_path
    if (typeof record.stdout === 'string') value = record.stdout
    else if (typeof record.result === 'string') value = record.result
    else if (record.data && typeof record.data === 'object') value = record.data
    else if (typeof record.text === 'string') value = record.text
    else return null
  }
  return null
}

export function GmailSenderApprovalFromTool({ result, toolName }: { result: unknown; toolName?: string }) {
  const enabled = useGmailInboundUIEnabled()
  const workspacePath = useMemo(() => {
    const name = normalizeMCPToolName(toolName || '')
    return name === 'get_gmail_trigger' || name === 'manage_gmail_trigger' ? approvalWorkspace(result) : null
  }, [result, toolName])
  const [open, setOpen] = useState(false)
  if (!enabled || !workspacePath) return null
  return <div className="my-2 space-y-2">
    <Button size="sm" variant="outline" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? 'Close sender review' : 'Review and approve senders'}</Button>
    {open && <GmailSenderApproval key={workspacePath} workspacePath={workspacePath} />}
  </div>
}
