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

/** The project file a reference means, from Git's file lists: the exact path in a repository, else the
 * shortest path ending with it (agents shorten paths), main checkouts before worktrees. A project with no
 * repository checks only the reference's own folder, never the whole project. */
export async function findProjectFile(folder: string, ref: CodeRef, signal?: AbortSignal): Promise<string | null> {
  const found = await workspaceGitApi.find(folder, ref.path, 'path', 5, signal).catch(() => ({ files: [], repos: 0 }))
  if (found.files[0]) return `${folder}/${found.files[0].path}`
  if (found.repos > 0) return null
  const slash = ref.path.lastIndexOf('/')
  const target = `${folder}/${ref.path}`
  const response = await agentApi.searchDocuments({ query: ref.path.slice(slash + 1), search_type: 'filename', folder: `${folder}/${ref.path.slice(0, slash)}`, limit: 20 }, undefined, signal).catch(() => null)
  const results: { filepath: string; type?: string }[] = response?.data?.results ?? []
  return results.some(result => result.type !== 'folder' && result.filepath === target) ? target : null
}
