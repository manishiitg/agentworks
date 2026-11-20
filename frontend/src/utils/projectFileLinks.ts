import { agentApi } from '../services/api'
import { workspaceGitApi } from '../services/workspaceGit'

/** A code reference an agent wrote in a reply, such as `src/app/main.go:42`. */
export type CodeRef = { path: string; line?: number }

export const OPEN_PROJECT_FILE_EVENT = 'agentworks:open-project-file'

// Only a screen that can open project files (the Crew surface) registers, so
// chat elsewhere keeps plain code text instead of links that go nowhere.
const openers: string[] = []
export function registerProjectFileOpener(folder: string): () => void {
  openers.push(folder)
  return () => { const index = openers.lastIndexOf(folder); if (index >= 0) openers.splice(index, 1) }
}
export const projectFileLinksEnabled = () => openers.length > 0
/** The open Crew's files folder, for ⌘/Ctrl+K file search; empty when none is open. */
export const projectFilesFolder = () => openers[openers.length - 1] ?? ''

// A relative path with at least one folder and an extension, with an optional
// :line[:column]. Bare file names, URLs, absolute paths and anything with
// spaces stay plain code.
const CODE_REF = /^(?:\.\/)?((?:[\w@+-][\w@.+-]*\/)+[\w@+-][\w@.+-]*\.[A-Za-z0-9]{1,10})(?::(\d{1,7}))?(?::\d{1,5})?$/

export function parseCodeRef(text: string): CodeRef | null {
  const match = CODE_REF.exec(text.trim())
  if (!match) return null
  const line = match[2] ? Number(match[2]) : undefined
  return { path: match[1], ...(line ? { line } : {}) }
}

/** exact: the path is already a full workspace path (a search result), so no lookup is needed. */
export type OpenProjectFileRequest = CodeRef & { exact?: boolean }

export function requestOpenProjectFile(ref: CodeRef, exact = false) {
  window.dispatchEvent(new CustomEvent<OpenProjectFileRequest>(OPEN_PROJECT_FILE_EVENT, { detail: { ...ref, exact } }))
}

// Repository folders of a project, main checkouts before linked worktrees, so a
// path means the repository people work in, not another agent's worktree.
const rootsCache = new Map<string, { at: number; roots: Promise<string[]> }>()
function candidateRoots(folder: string): Promise<string[]> {
  const cached = rootsCache.get(folder)
  if (cached && Date.now() - cached.at < 60_000) return cached.roots
  const roots = workspaceGitApi.status(folder)
    .then(repos => {
      const main = repos.filter(repo => !repo.worktree).map(repo => repo.root)
      const linked = repos.filter(repo => repo.worktree).map(repo => repo.root)
      return Array.from(new Set([...main, '', ...linked]))
    })
    .catch(() => [''])
  rootsCache.set(folder, { at: Date.now(), roots })
  return roots
}

/** The project file a reference means. It checks only the reference's own folder in each repository
 * (and the project root), never the whole project: a whole-project scan is slow on a large folder. */
export async function findProjectFile(folder: string, ref: CodeRef, signal?: AbortSignal): Promise<string | null> {
  const slash = ref.path.lastIndexOf('/')
  const dir = ref.path.slice(0, slash), name = ref.path.slice(slash + 1)
  const roots = await candidateRoots(folder)
  const found = await Promise.all(roots.map(async root => {
    const base = [folder, root].filter(Boolean).join('/')
    const target = `${base}/${ref.path}`
    try {
      const response = await agentApi.searchDocuments({ query: name, search_type: 'filename', folder: `${base}/${dir}`, limit: 20 }, undefined, signal)
      const results: { filepath: string; type?: string }[] = response?.data?.results ?? []
      return results.some(result => result.type !== 'folder' && result.filepath === target) ? target : null
    } catch { return null }
  }))
  return found.find(Boolean) ?? null
}
