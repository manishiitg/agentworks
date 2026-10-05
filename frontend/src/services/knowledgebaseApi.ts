import api from './api'

export type KnowledgeType = 'skill' | 'fact' | 'note' | 'source'
export type KnowledgeRole = 'reader' | 'editor' | 'owner'
export interface KnowledgeEntry {
  entry_id: string
  path: string
  folder_path: string
  filename: string
  title: string
  type: KnowledgeType
  description: string
  tags: string[]
  version: string
  updated_at: string
  updated_by: string
  backup_status?: string
}
export interface KnowledgeFolder { path: string; name: string; effective_role?: KnowledgeRole | '' }
export interface KnowledgeIdentity { id: string; name: string; type: string; disabled?: boolean }
export interface KnowledgeGrant { identity_id: string; role: KnowledgeRole; folder_path: string; inherited: boolean }
export interface KnowledgeAccess { folder_path: string; effective_role: KnowledgeRole | ''; grants: KnowledgeGrant[]; identities: KnowledgeIdentity[]; acl_version: string }
export interface KnowledgeBootstrap { organization_id: string; profile_id: string; chat_workspace: string; is_admin: boolean; identity_id: string }
export interface KnowledgeRead { entry: KnowledgeEntry; content: string; version: string; start_line: number; end_line: number; total_lines: number }
export interface KnowledgeBackup { configured: boolean; entries: Array<{ entry_id: string; path: string; status: string }>; pending_count?: number; last_backup_error?: string }
export interface KnowledgeListQuery { folder_path: string; type?: string; tag?: string; cursor?: string; limit?: number }

type Page = { items?: Record<string, unknown>[]; next_cursor?: string | null }
export function normalizeKnowledgeRole(role: unknown): KnowledgeRole | '' {
  const value = String(role || '').toLowerCase()
  return value === 'reader' || value === 'editor' || value === 'owner' ? value : ''
}
export function normalizeKnowledgeAccess(data: KnowledgeAccess): KnowledgeAccess {
  return { ...data, effective_role: normalizeKnowledgeRole(data.effective_role), grants: (data.grants || []).map(grant => ({ ...grant, role: normalizeKnowledgeRole(grant.role) as KnowledgeRole })), identities: data.identities || [] }
}
export function normalizeKnowledgeSearch(data: Page & { results?: Array<{ entry: KnowledgeEntry; excerpt?: string }>; matches?: Record<string, unknown>[] }): Array<{ entry: KnowledgeEntry; excerpt?: string }> {
  if (!data.items && data.results) return data.results
  return (data.items || data.matches || []).map(item => ({ entry: (item.entry || item) as KnowledgeEntry, excerpt: String(item.excerpt || item.snippet || '') }))
}

export interface KnowledgeAccessProposal { id: string; arguments: Record<string, unknown>; expires_at: string }

export const knowledgebaseApi = {
 proposals: async (): Promise<{ proposals: KnowledgeAccessProposal[] }> => (await api.get("/api/knowledgebase/access-proposals")).data,
 confirmAccess: async (id: string, approve: boolean): Promise<unknown> => (await api.post("/api/knowledgebase/access-proposals", { id, approve })).data,
  bootstrap: async (signal?: AbortSignal): Promise<KnowledgeBootstrap> => (await api.get('/api/knowledgebase/bootstrap', { signal })).data,
  folders: async (folder_path: string, cursor = '', signal?: AbortSignal): Promise<{ folders: KnowledgeFolder[]; next_cursor?: string }> => {
    const { data } = await api.get<Page & { folders?: KnowledgeFolder[] }>('/api/knowledgebase/folders', { params: { folder_path, depth: 1, cursor, limit: 100 }, signal })
    return { folders: ((data.items || data.folders || []) as KnowledgeFolder[]).map(folder => ({ ...folder, effective_role: normalizeKnowledgeRole(folder.effective_role) })), next_cursor: data.next_cursor || undefined }
  },
  entries: async (params: KnowledgeListQuery, signal?: AbortSignal): Promise<{ entries: KnowledgeEntry[]; next_cursor?: string }> => {
    const { data } = await api.get<Page & { entries?: KnowledgeEntry[] }>('/api/knowledgebase/entries', { params: { limit: 50, ...params }, signal })
    return { entries: (data.items ? data.items.filter(item => item.kind === 'entry' || item.entry_id) : data.entries || []) as KnowledgeEntry[], next_cursor: data.next_cursor || undefined }
  },
  read: async (entry_id: string, signal?: AbortSignal): Promise<KnowledgeRead> => (await api.get('/api/knowledgebase/read', { params: { entry_id }, signal })).data,
  search: async (params: KnowledgeListQuery & { query: string }, signal?: AbortSignal): Promise<{ results: Array<{ entry: KnowledgeEntry; excerpt?: string }>; next_cursor?: string }> => {
    const { data } = await api.get('/api/knowledgebase/search', { params: { limit: 50, ...params }, signal })
    return { results: normalizeKnowledgeSearch(data), next_cursor: data.next_cursor || undefined }
  },
  access: async (folder_path: string, signal?: AbortSignal): Promise<KnowledgeAccess> => normalizeKnowledgeAccess((await api.get('/api/knowledgebase/access', { params: { folder_path }, signal })).data),
  backup: async (folder_path: string, signal?: AbortSignal): Promise<KnowledgeBackup> => {
    const { data } = await api.get('/api/knowledgebase/backup', { params: { folder_path }, signal })
    return { ...data, entries: (data.entries || []).map((entry: { entry_id: string; path: string; status?: string; backup_status?: string }) => ({ ...entry, status: entry.status || entry.backup_status || 'pending' })) }
  },
}

export function knowledgebaseError(error: unknown): string {
  const response = (error as { response?: { data?: { error?: unknown; message?: string } } })?.response?.data
  if (typeof response?.error === 'string') return response.error
  if (response?.error && typeof response.error === 'object' && 'message' in response.error) return String(response.error.message)
  return response?.message || (error instanceof Error ? error.message : 'Could not load Knowledge Base. Please try again.')
}
