import { agentApi } from '../services/api'

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

// A relative path with a folder and an extension, or a bare file name with a
// line ("main.go:42"); an optional :line[:column] follows. URLs, absolute
// paths and anything with spaces stay plain code.
const CODE_REF = /^(?:\.\/)?((?:[\w@+-][\w@.+-]*\/)+[\w@+-][\w@.+-]*\.[A-Za-z0-9]{1,10}|[\w@+-][\w@.+-]*\.[A-Za-z0-9]{1,10}(?=:\d))(?::(\d{1,7}))?(?::\d{1,5})?$/

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

/** The project file a reference means: the server's file-name search, then the
 * result whose path ends with the reference (agents often write paths relative
 * to one repository inside the project). Shortest match wins. */
export async function findProjectFile(folder: string, ref: CodeRef, signal?: AbortSignal): Promise<string | null> {
  const name = ref.path.split('/').pop() || ref.path
  const response = await agentApi.searchDocuments({ query: name, search_type: 'filename', folder, limit: 500 }, undefined, signal)
  const results: { filepath: string; type?: string }[] = response?.data?.results ?? []
  const suffix = `/${ref.path}`
  const matches = results
    .filter(result => result.type !== 'folder' && (result.filepath === ref.path || result.filepath.endsWith(suffix)))
    .map(result => result.filepath)
    .sort((a, b) => a.length - b.length)
  return matches[0] ?? null
}
