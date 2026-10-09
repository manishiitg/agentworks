import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { listToolVersions, setToolAccess, type GatewayTool } from './gatewayAdminApi'
import { gatewayErrorMessage } from './gatewayConsoleUtils'
import { useVaultReadOnly } from './vaultReadOnly'

function toolJSON(encoded: string | null | undefined): string {
  if (!encoded) return 'Not provided.'
  try { return JSON.stringify(JSON.parse(atob(encoded)), null, 2) }
  catch { return 'Schema could not be displayed.' }
}

export function GatewayToolReviewActions({ tool, base, onApprove, approving, access, label, onAccessChanged }: {
  tool: GatewayTool
  base: string
  onApprove: (tool: GatewayTool) => Promise<void>
  approving: boolean
  /** Effective read/write label used by read-only server grants. */
  access?: 'read' | 'write'
  /** The admin's own label, if any (otherwise the server's readOnlyHint decides). */
  label?: 'read' | 'write'
  onAccessChanged?: () => void
}) {
  const readOnly = useVaultReadOnly()
  const [accessBusy, setAccessBusy] = useState(false)
  const [accessError, setAccessError] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const [versions, setVersions] = useState<GatewayTool[] | null>(null)
  const [historyError, setHistoryError] = useState<string | null>(null)

  async function openReview() {
    setOpen(true)
    if (versions !== null) return
    try {
      const response = await listToolVersions(base, tool.PublicName)
      setVersions(response.versions)
    } catch (err: unknown) {
      setHistoryError(gatewayErrorMessage(err))
    }
  }

  return (
    <>
      <span className="flex flex-wrap items-center gap-2">
        {access && (readOnly
          ? <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{access === 'read' ? 'Read' : 'Write'}</span>
          : <select aria-label={`Read or write: ${tool.PublicName}`} value={label ?? ''} disabled={accessBusy}
            title="Read-only server grants allow only read tools. Unmarked tools count as write."
            className="h-6 rounded border border-border bg-background px-1 text-[11px]"
            onChange={e => {
              const next = e.target.value as 'read' | 'write' | ''
              setAccessBusy(true)
              setAccessError(null)
              setToolAccess(base, tool.PublicName, next).then(() => onAccessChanged?.()).catch(err => setAccessError(gatewayErrorMessage(err))).finally(() => setAccessBusy(false))
            }}>
            <option value="">{label ? "Use the server's mark" : `Server's mark: ${access === 'read' ? 'read' : 'write'}`}</option>
            <option value="read">Read</option>
            <option value="write">Write</option>
          </select>)}
        {accessError && <span className="text-[11px] text-destructive">{accessError}</span>}
        <Button variant="ghost" size="xs" onClick={() => open ? setOpen(false) : void openReview()}>
          {open ? 'Hide details' : 'Review details'}
        </Button>
        {open && !readOnly && tool.Status === 'quarantined' && (
          <Button size="xs" disabled={approving} onClick={() => void onApprove(tool)}>
            {approving && <Loader2 className="animate-spin" />}Approve v{tool.Version}
          </Button>
        )}
      </span>
      {open && (
        <span className="block space-y-2 rounded-md border border-border p-2 text-xs">
          <span className="block font-medium">MCP tool name · v{tool.Version}</span>
          <span className="block break-all font-mono">{tool.PublicName}</span>
          {tool.Description && <><span className="block font-medium">Description</span><span className="block whitespace-pre-wrap">{tool.Description}</span></>}
          <span className="block font-medium">Current input schema</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.InputSchema)}</pre>
          <span className="block font-medium">Output schema</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.OutputSchema)}</pre>
          <span className="block font-medium">Annotations</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.Annotations)}</pre>
          {historyError && <span className="text-destructive">{historyError}</span>}
          {versions?.slice(-1).map((previous) => (
            <span className="block" key={previous.Version}>
              <span className="block font-medium">Previous v{previous.Version}: {previous.Description}</span>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(previous.InputSchema)}</pre>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(previous.OutputSchema)}</pre>
            </span>
          ))}
        </span>
      )}
    </>
  )
}

