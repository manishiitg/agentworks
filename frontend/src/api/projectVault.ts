import { agentApi } from '../services/api'

export type VaultItemKind = 'mcp' | 'project_secret' | 'shared_secret' | 'google' | 'slack' | 'computer'
export interface ProjectVaultItem {
  id: string; name: string; kind: VaultItemKind; provider?: string; owner?: string
  status: 'connected' | 'needs_attention' | 'stored' | 'online' | 'offline'
  selected: boolean; can_select: boolean
}
export interface ProjectVaultGroup {
  id: string; name: string; sharing: string; project: boolean; items: ProjectVaultItem[]
}
export interface ProjectVaultInventory {
  vaults: ProjectVaultGroup[]; notices: string[]
  selected_servers: string[]; selected_secrets: string[]; selected_global_secret_names: string[]
}

export async function loadProjectVault(workspacePath: string): Promise<ProjectVaultInventory> {
  return JSON.parse(await agentApi.myVaultsOp({ operation: 'project', workspace_path: workspacePath })) as ProjectVaultInventory
}

/** The server edits one item through the existing native settings services. */
export async function selectProjectVaultItem(workspacePath: string, item: ProjectVaultItem): Promise<ProjectVaultInventory> {
  return JSON.parse(await agentApi.myVaultsOp({ operation: 'project_select', workspace_path: workspacePath,
    kind: item.kind, item_id: item.id, selected: !item.selected })) as ProjectVaultInventory
}
