import { agentApi } from '../../services/api'
import type { PlannerFile, PlannerFileContent } from '../../services/api-types'
import { WORK_PROFILE_ID } from './workData'

/**
 * Mediated file access for shared Crews (Crew Run mode). The browser proxy
 * refuses raw cross-user workspace traffic, so every read for another
 * owner's Crew goes through the shared endpoints, addressed crew-relative.
 * Paths stay in the owner's physical form at the call sites; this module
 * relativizes on the way out and re-prefixes listings on the way in, so
 * panels keep working with the same path shapes they use for owned Crews.
 */

export function sharedCrewRelativePath(workspaceRoot: string, filePath: string): string | null {
  const root = workspaceRoot.replace(/\\/g, '/').replace(/\/+$/, '')
  const target = filePath.replace(/\\/g, '/')
  if (target === root) return ''
  if (!target.startsWith(`${root}/`)) return null
  return target.slice(root.length + 1)
}

export type CrewFileClient = {
  listFiles: (folder?: string, limit?: number, maxDepth?: number) => Promise<{ success: boolean; message: string; data: PlannerFile[]; truncated?: boolean }>
  readFile: (filePath: string, timeoutMs?: number) => Promise<{ success: boolean; message: string; data: PlannerFileContent }>
}

export const proxyCrewFileClient: CrewFileClient = {
  listFiles: async (folder?: string, limit: number = -1, maxDepth?: number) => agentApi.getPlannerFiles(folder, limit, maxDepth),
  readFile: async (filePath: string, timeoutMs?: number) => agentApi.getPlannerFileContent(filePath, timeoutMs),
}

export function sharedCrewFileClient(projectId: string, workspaceRoot: string, profileId: string = WORK_PROFILE_ID): CrewFileClient {
  const root = workspaceRoot.replace(/\\/g, '/').replace(/\/+$/, '')
  const blank = (message: string) => ({ success: false as const, message, data: { filepath: '', content: '' } })
  return {
    listFiles: async (folder?: string, _limit: number = -1, maxDepth?: number) => {
      const response = await agentApi.listSharedProjectFiles(profileId, projectId)
      const scope = folder ? sharedCrewRelativePath(root, folder.replace(/\\/g, '/')) : ''
      const files: PlannerFile[] = []
      if (scope !== null) {
        for (const entry of response?.files || []) {
          const rel = entry.path.replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
          if (!rel) continue
          if (scope) {
            if (rel !== scope && !rel.startsWith(`${scope}/`)) continue
            if (maxDepth !== undefined) {
              const rest = rel === scope ? '' : rel.slice(scope.length + 1)
              if (rest && rest.split('/').length > maxDepth) continue
            }
          } else if (maxDepth !== undefined && rel.split('/').length > maxDepth) {
            continue
          }
          files.push({ filepath: `${root}/${rel}`, type: entry.type === 'folder' ? 'folder' : 'file' })
        }
      }
      return { success: true, message: '', data: files, truncated: response?.truncated }
    },
    readFile: async (filePath: string) => {
      const rel = sharedCrewRelativePath(root, filePath)
      if (rel === null || rel === '') return blank('File is outside this Crew project.')
      try {
        const response = await agentApi.getSharedProjectFile(profileId, projectId, rel)
        if (typeof response?.content !== 'string') return blank('Could not open shared Crew file.')
        return { success: true, message: '', data: { filepath: filePath, content: response.content } }
      } catch (cause) {
        // Mirror the proxy client's 404 contract: callers treat a missing
        // file as empty, and every other failure as an error.
        const status = (cause as { response?: { status?: number } } | undefined)?.response?.status
        const error = new Error(status === 404 ? 'File not found.' : 'Could not open shared Crew file.') as Error & { response?: { status?: number } }
        error.response = { status: status ?? 500 }
        throw error
      }
    },
  }
}
