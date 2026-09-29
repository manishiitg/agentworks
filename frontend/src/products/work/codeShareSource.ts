import { agentApi, type WorkflowAccessInfo } from '../../services/api'
import type { CodeShareRole, CodeSharesResponse } from '../../services/api-types'
import type { ShareSource } from '../../components/workflow/WorkflowSharePopup'

// A Code workspace's share list in the shared sharing UI (WorkflowSharePopup):
// the owner and co-owners are "Owners", editors "Editors", viewers
// "Read-only". The owner is fixed; only the owner and co-owners change it.

const MY_ACCESS: Record<CodeSharesResponse['role'], WorkflowAccessInfo['my_access']> = {
  owner: 'owner', co_owner: 'owner', editor: 'write', viewer: 'read',
}

function toAccessInfo(projectId: string, response: CodeSharesResponse): WorkflowAccessInfo {
  const of = (role: CodeShareRole) => response.grants
    .filter(grant => grant.role === role)
    .map(grant => ({ id: grant.user_id, username: grant.username || grant.user_id }))
  return {
    workspace_path: projectId,
    owners: [{ id: response.owner_id, username: response.owner_username || response.owner_id }, ...of('co_owner')],
    editors: of('editor'),
    readers: of('viewer'),
    my_access: MY_ACCESS[response.role] ?? 'none',
  }
}

export function codeShareSource(projectId: string): ShareSource {
  let ownerId = ''
  return {
    load: async () => {
      const response = await agentApi.getCodeShares(projectId)
      ownerId = response.owner_id
      return toAccessInfo(projectId, response)
    },
    save: async (owners, editors, readers) => {
      const grants = [
        ...owners.filter(id => id !== ownerId).map(user => ({ user, role: 'co_owner' as const })),
        ...editors.map(user => ({ user, role: 'editor' as const })),
        ...readers.map(user => ({ user, role: 'viewer' as const })),
      ]
      const response = await agentApi.putCodeShares(projectId, grants)
      ownerId = response.owner_id
      return toAccessInfo(projectId, response)
    },
    lockedIds: () => (ownerId ? [ownerId] : []),
    canEdit: info => info.my_access === 'owner',
    description: 'Owners (the Code’s owner and co-owners) also manage sharing. Editors run the agent and change files. Read-only people see the files. Each person keeps their own chats; admins and Code reviewers can view it, read-only, and every view is logged.',
  }
}
