import { useEffect } from 'react'
import { Plug, X } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import { CliMcpSetupPanel } from '../integrations/CliMcpSetupPanel'

/**
 * McpConnectDialog - how to connect a local or hosted AI agent to this
 * server over MCP, from the account menu (admins and Code reviewers). Code
 * review is done from that agent, outside Code: the code:review tools are
 * listed there only for these accounts.
 */
export default function McpConnectDialog({ isOpen, onClose, codeReview }: { isOpen: boolean; onClose: () => void; codeReview: boolean }) {
  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [isOpen, onClose])

  if (!isOpen) return null
  return (
    <ModalPortal>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="mcp-connect-title"
          className="flex max-h-[90vh] w-full max-w-2xl flex-col rounded-lg border border-border bg-background shadow-xl"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="flex items-center justify-between border-b border-border p-4">
            <div className="flex items-center gap-2">
              <Plug className="h-5 w-5 text-muted-foreground" />
              <h2 id="mcp-connect-title" className="text-lg font-semibold">Connect an AI agent (MCP)</h2>
            </div>
            <button onClick={onClose} className="rounded p-1 hover:bg-accent" aria-label="Close">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
            {codeReview && (
              <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
                <p className="font-medium text-foreground">Code review</p>
                <p className="mt-1">
                  The approval screen lists <b>Review every Code workspace</b> for your account. Once you allow it, your agent can
                  then list every Code workspace, see its cost by person, and read its chats and files, read-only. Every call is
                  recorded in the Code review audit log.
                </p>
              </div>
            )}
            <CliMcpSetupPanel />
          </div>
        </div>
      </div>
    </ModalPortal>
  )
}
