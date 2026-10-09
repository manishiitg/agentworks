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
