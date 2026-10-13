import { useAuthStore } from '../stores/useAuthStore'
import { useWorkflowManifestStore } from '../stores/useWorkflowManifestStore'
import { hasWorkflowWriteAccess } from '../utils/workflowPermissions'

/** Tooltip for a control that is disabled because the user has read-only access. */
export const READ_ONLY_TITLE = 'Run access'

/**
 * Whether the current user may change workflow-affecting state.
 *
 * Two layers (docs/design/user_accounts_and_workflow_sharing.md): the
 * account decides whether the user may create or edit anything at all, and
 * — when a `workspacePath` is given — the workflow's own owner/reader lists
 * decide whether THIS workflow is theirs to edit. A workflow shared
 * read-only is read-only even for a member who owns others.
 *
 * The toolbar keeps capability views visible for readers, while the
 * capabilities panel hides its Save button and the section components
 * underneath disable their writes. Those components may write account-wide
 * state immediately (bot routes, API keys, secrets, skills, MCP connections)
 * and can be reachable through surfaces that never pass through the toolbar,
 * so every mutating control must honor this guard.
 */
export function useCanWriteWorkflow(workspacePath?: string | null): boolean {
  const accountCanWrite = useAuthStore(state => hasWorkflowWriteAccess(state.user, state.isMultiUserMode))
  const myAccess = useWorkflowManifestStore(state =>
    workspacePath ? state.workflows.find(w => w.workspace_path === workspacePath)?.my_access : undefined,
  )
  if (!accountCanWrite) return false
  if (myAccess === 'read') return false
  return true
}
