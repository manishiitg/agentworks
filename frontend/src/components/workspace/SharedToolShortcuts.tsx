import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { Terminal } from 'lucide-react'
import { useAppStore } from '../../stores/useAppStore'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'

/** CLI connection shortcut; product destinations live in Account & settings. */
export function SharedToolShortcuts() {
  const connecting = useAppStore(state => state.adminPage === 'mcp')
  return <TooltipProvider delayDuration={250}>
    <nav aria-label="AI connections" className="flex shrink-0 border-t border-border/60 px-1 py-2" data-testid="shared-tool-shortcuts">
      <Tooltip>
        <TooltipTrigger asChild>
          <button type="button" aria-label="AgentWorks CLI & MCP" aria-current={connecting ? 'page' : undefined}
            className={`flex h-10 w-full items-center justify-center rounded-md transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${connecting ? 'bg-foreground/[0.08] text-foreground' : 'text-foreground/75 hover:text-foreground'}`}
            onClick={() => openWorkspaceSettings('mcp')}>
            <Terminal className="h-[18px] w-[18px] shrink-0" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="top" sideOffset={8}>AgentWorks CLI &amp; MCP</TooltipContent>
      </Tooltip>
    </nav>
  </TooltipProvider>
}
