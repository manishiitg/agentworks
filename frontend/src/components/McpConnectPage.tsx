import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'
import { Plug } from 'lucide-react'
import McpConnectBody from './topbar/McpConnectBody'
import { useAuthStore } from '../stores/useAuthStore'

/**
 * Connect an AI agent (MCP) as a full page, opened from the top-bar plug icon
 * (admins and Code reviewers).
 */
export default function McpConnectPage() {
  const user = useAuthStore(state => state.user)
  const isMultiUserMode = useAuthStore(state => state.isMultiUserMode)
  const codeReview = user?.is_code_reviewer === true || (user?.is_admin === true && isMultiUserMode)
  return (
    <section aria-label="Connect an AI agent" className="flex h-full min-h-0 flex-col bg-background">
      <header className="shrink-0 border-b border-border px-4 sm:px-6">
        <div className="flex flex-wrap items-center gap-3 py-3">
          <WorkspaceBackButton />
          <span aria-hidden="true" className="h-4 w-px bg-border" />
          <Plug className="h-4 w-4 text-primary" />
          <h1 className="text-sm font-semibold text-foreground">Connect an AI agent (MCP)</h1>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl p-4 sm:p-6">
          <McpConnectBody codeReview={codeReview} />
        </div>
      </div>
    </section>
  )
}
