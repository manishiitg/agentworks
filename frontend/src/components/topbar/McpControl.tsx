import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Plug } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/**
 * McpControl - top-bar icon that opens the "Connect an AI agent (MCP)" full
 * page: how a local or hosted AI agent connects to this server, and (for
 * admins and Code reviewers) the code:review tools. Shown to every signed-in
 * person; the tools an agent gets follow the person's role.
 */
export default function McpControl() {
  const sidebar = useProductNavigationSidebar()
  const user = useAuthStore(state => state.user)
  const active = useAppStore(state => state.adminPage === 'mcp')
  const setAdminPage = useAppStore(state => state.setAdminPage)
  // Every signed-in person may connect an AI agent; the tools it gets follow their role (owner, 2026-10-09). The page itself shows
  // the Code-review section to admins and Code reviewers only.
  if (!user) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-product-navigation-action
          onClick={() => { useLLMStore.getState().setShowLLMModal(false); setAdminPage('mcp') }}
          aria-label="Connect an AI agent (MCP)"
          aria-pressed={active}
          data-tour="global-mcp"
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Plug className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>Connect AI agent</ProductNavigationLabel>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>Connect an AI agent (MCP)</TooltipContent>
    </Tooltip>
  )
}
