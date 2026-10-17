import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
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
      <WorkspacePageHeader page="mcp" title="Connect AI tools" icon={Plug} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl p-4 sm:p-6">
          <McpConnectBody codeReview={codeReview} />
        </div>
      </div>
    </section>
  )
}
