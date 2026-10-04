import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { listToolVersions, type GatewayTool } from './gatewayAdminApi'
import { gatewayErrorMessage } from './gatewayConsoleUtils'

function toolJSON(encoded: string | null | undefined): string {
  if (!encoded) return 'Not provided.'
  try { return JSON.stringify(JSON.parse(atob(encoded)), null, 2) }
  catch { return 'Schema could not be displayed.' }
}

export function GatewayToolReviewActions({ tool, base, onApprove, approving }: {
  tool: GatewayTool
  base: string
  onApprove: (tool: GatewayTool) => Promise<void>
  approving: boolean
}) {
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
        <Button variant="ghost" size="xs" onClick={() => open ? setOpen(false) : void openReview()}>
          {open ? 'Hide details' : 'Review details'}
        </Button>
        {open && tool.Status === 'quarantined' && (
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

