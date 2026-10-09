import { responseContent } from './plannerFiles'

interface RawFileApi {
  getPlannerFileContent: (path: string) => Promise<unknown>
  updatePlannerFile: (path: string, content: string, commitMessage?: string) => Promise<{ success?: boolean; message?: string }>
}

/** The file's raw text as stored: what an edit starts from (the viewer's copy is unescaped and reformatted). */
export async function readRawFile(api: Pick<RawFileApi, 'getPlannerFileContent'>, path: string): Promise<string> {
  const raw = responseContent(await api.getPlannerFileContent(path))
  if (!raw) throw new Error('empty response')
  return raw.content
}

/**
 * Saves an edit made to the raw text opened earlier. It refuses ("changed") when the file is not what the edit started
 * from, because the agent or another tab wrote it meanwhile; the last save would otherwise silently win.
 */
export async function saveEditedFile(api: RawFileApi, path: string, opened: string, draft: string): Promise<'saved' | 'changed'> {
  if (await readRawFile(api, path) !== opened) return 'changed'
  const result = await api.updatePlannerFile(path, draft, `Edit ${path.split('/').pop()}`)
  if (!result?.success) throw new Error(result?.message || 'save failed')
  return 'saved'
}

/** A new file's name: one path segment, no hidden traversal or control characters. */
export function newFileNameProblem(name: string): string | null {
  const trimmed = name.trim()
  if (!trimmed) return 'File name is required'
  if (trimmed !== name) return 'File name cannot start or end with a space'
  if (/[/\\]/.test(trimmed)) return 'File name cannot contain / or \\ (create the folder first, then the file in it)'
  if (trimmed === '.' || trimmed === '..' || /[\u0000-\u001f]/.test(trimmed)) return 'That file name is not allowed'
  if (trimmed.length > 200) return 'File name is too long'
  return null
}

/** Creates an empty file in folder. It never overwrites: "exists" when something is already there. */
export async function createNewFile(api: RawFileApi, folder: string, name: string): Promise<'created' | 'exists'> {
  const path = `${folder.replace(/\/+$/, '')}/${name}`
  try {
    await api.getPlannerFileContent(path)
    return 'exists'
  } catch (cause) {
    if ((cause as { response?: { status?: number } })?.response?.status !== 404) throw cause
  }
  const result = await api.updatePlannerFile(path, '', `Create ${name}`)
  if (!result?.success) throw new Error(result?.message || 'create failed')
  return 'created'
}
