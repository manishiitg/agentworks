import { Suspense, lazy, useState } from 'react'
import { Plug } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'

// Loaded on open: it pulls in the MCP setup panel and the API client.
const McpConnectDialog = lazy(() => import('./McpConnectDialog'))

/**
 * McpControl - top-bar icon that opens "Connect an AI agent (MCP)": how a local
 * or hosted AI agent connects to this server, and (for admins and Code
 * reviewers) the code:review tools. Shown to admins and Code reviewers only.
 */
export default function McpControl() {
  const { user, isMultiUserMode } = useAuthStore()
  const [open, setOpen] = useState(false)
  if (!user || (user.is_admin !== true && user.is_code_reviewer !== true)) return null
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-label="Connect an AI agent (MCP)"
            aria-haspopup="dialog"
            className="relative rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Plug className="h-4 w-4" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="bottom">Connect an AI agent (MCP)</TooltipContent>
      </Tooltip>
      {open && (
        <Suspense fallback={null}>
          <McpConnectDialog isOpen onClose={() => setOpen(false)} codeReview={user.is_code_reviewer === true || (user.is_admin === true && isMultiUserMode)} />
        </Suspense>
      )}
    </>
  )
}
