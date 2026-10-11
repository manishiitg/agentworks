import { agentApi, getApiBaseUrl, getAuthToken } from '../../services/api'
import { createProductProject, loadProductProjects, parseProductProjectManifest, type ProductProject } from '../../platform/chat/productProjects'
import { flattenFiles, responseContent, responseFiles, slugifyTitle } from '../../utils/plannerFiles'
import { loadWorkspacePresentations, parseWorkspacePresentations, type WorkspacePresentation } from '../../platform/presentations/presentationData'

export const VIDEO_PROJECTS_ROOT = 'Chats/Video Studio/projects'
export const VIDEO_PROFILE_ID = 'video-studio'
export const VIDEO_PROFILE_VERSION = 2

export type VideoProductCommand = {
  name: string
  description: string
  icon: string
  prompt: string
}

type AgentProfileResponse = {
  commands?: Array<{
    name?: unknown
    description?: unknown
    icon?: unknown
    prompt?: unknown
  }>
}

// Slash commands the product ships with itself, declared in its product.yaml.
// A command with no prompt is dropped rather than offered: it would appear in
// the menu and then submit nothing, which reads as the product being broken.
export function parseProductCommands(profile: { commands?: Array<Record<string, unknown>> }): VideoProductCommand[] {
  return (profile.commands ?? []).flatMap((command) => {
    const name = asString(command.name)
    const prompt = asString(command.prompt)
    if (!name || !prompt) return []
    return [{
      name,
      description: asString(command.description),
      icon: asString(command.icon) || 'terminal',
      prompt,
    }]
  })
}

export async function loadVideoProductCommands(): Promise<VideoProductCommand[]> {
  const token = getAuthToken()
  const response = await fetch(`${getApiBaseUrl()}/api/agent-profiles/${encodeURIComponent(VIDEO_PROFILE_ID)}?version=${VIDEO_PROFILE_VERSION}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  if (!response.ok) throw new Error(`Unable to load Video Studio commands (${response.status})`)
  return parseProductCommands(await response.json() as AgentProfileResponse)
}

export type VideoProject = ProductProject<'video-studio'> & {
  videos: number
}

export type VideoPresentation = {
  id: string
  title: string
  path: string
  qaReportPath: string
  note: string
  verdict: string
  revision: number
  updatedAt: string
  workspacePresentation: WorkspacePresentation
}

// A character is the reference every later shot of that subject is generated
// against, so the panel shows the image and its spec together — checking a
// shot against a remembered description is exactly how identity drift gets
// missed.
export type CharacterPresentation = {
  id: string
  name: string
  imagePath: string
  specPath: string
  spec: string
  model: string
  provider: string
  note: string
  revision: number
  updatedAt: string
  workspacePresentation: WorkspacePresentation
}

// Visual-development references are approved production constraints, not loose
// attachments: later footage is conditioned on them to keep backgrounds,
// wardrobe, props, and sequence boundaries stable.
export type ReferencePresentation = {
  id: string
  title: string
  path: string
  role: string
  note: string
  revision: number
  updatedAt: string
  workspacePresentation: WorkspacePresentation
}

export type DocumentPresentation = {
  id: string
  title: string
  path: string
  markdown: string
  note: string
  revision: number
  updatedAt: string
  workspacePresentation: WorkspacePresentation
}

export type VideoAsset = {
  path: string
  name: string
  size?: number
  mimeType?: string
  modifiedAt?: string
}

export type VideoWorkflowStep = {
  id: string
  title: string
  type: string
  routes?: Array<{ id: string; title: string; nextStepId: string }>
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

export function projectSlug(title: string): string {
  return slugifyTitle(title, 'video-project')
}

export function parseProjectManifest(content: string, workspacePath: string, lastModified?: string): VideoProject | null {
  const project = parseProductProjectManifest(content, workspacePath, VIDEO_PROFILE_ID, lastModified)
  if (!project) return null
  return {
    ...project,
    videos: 0,
  }
}

async function projectVideoCount(workspacePath: string): Promise<number> {
  try {
    const response = await agentApi.queryWorkflowDB(
      `${workspacePath}/db/db.sqlite`,
      "SELECT COUNT(*) AS count FROM ui_presentations WHERE kind = 'media.video' AND status = 'ready'",
    )
    const value = response.data?.rows?.[0]?.count
    return typeof value === 'number' ? value : Number(value) || 0
  } catch {
    return 0
  }
}

export async function loadVideoProjects(): Promise<VideoProject[]> {
  const projects = await loadProductProjects(VIDEO_PROJECTS_ROOT, VIDEO_PROFILE_ID)
  return Promise.all(projects.map(async (project) => ({
    ...project,
    videos: await projectVideoCount(project.workspacePath),
  })))
}

export async function createVideoProject(title: string, description: string): Promise<VideoProject> {
  const project = await createProductProject({
    root: VIDEO_PROJECTS_ROOT,
    product: VIDEO_PROFILE_ID,
    title,
    description,
    sessionPrefix: 'video-studio:project',
    slugFallback: 'video-project',
    commitLabel: 'Create Video Studio project',
  })
  return {
    ...project,
    videos: 0,
  }
}

export function parsePresentations(rows: Record<string, unknown>[]): VideoPresentation[] {
  return parseWorkspacePresentations(rows).flatMap((presentation) => {
    if (presentation.kind !== 'media.video' || presentation.status !== 'ready') return []
    const path = asString(presentation.payload.path)
    if (!path) return []
    return [{
      id: presentation.id,
      title: presentation.title || path.split('/').pop() || 'Video',
      path,
      qaReportPath: asString(presentation.payload.qa_report_path),
      note: asString(presentation.payload.note),
      verdict: asString(presentation.payload.verdict),
      revision: presentation.revision,
      updatedAt: presentation.updatedAt,
      workspacePresentation: presentation,
    }]
  })
}

export async function loadVideoPresentations(project: VideoProject): Promise<VideoPresentation[]> {
  const presentations = await loadWorkspacePresentations(project.workspacePath, ['media.video'])
  return presentations.flatMap((presentation) => {
    const path = asString(presentation.payload.path)
    if (!path) return []
    return [{
      id: presentation.id,
      title: presentation.title,
      path,
      qaReportPath: asString(presentation.payload.qa_report_path),
      note: asString(presentation.payload.note),
      verdict: asString(presentation.payload.verdict),
      revision: presentation.revision,
      updatedAt: presentation.updatedAt,
      workspacePresentation: presentation,
    }]
  })
}

// A character without a reference image is not a character this panel can do
// its job with -- the image is what a later shot gets compared against -- so
// it is dropped rather than rendered as a broken tile.
export function toCharacterPresentations(presentations: WorkspacePresentation[]): CharacterPresentation[] {
  return presentations.flatMap((presentation) => {
    const imagePath = asString(presentation.payload.image_path)
    if (!imagePath) return []
    return [{
      id: presentation.id,
      name: asString(presentation.payload.name) || presentation.title,
      imagePath,
      specPath: asString(presentation.payload.spec_path),
      spec: asString(presentation.payload.spec),
      model: asString(presentation.payload.model),
      provider: asString(presentation.payload.provider),
      note: asString(presentation.payload.note),
      revision: presentation.revision,
      updatedAt: presentation.updatedAt,
      workspacePresentation: presentation,
    }]
  })
}

export async function loadCharacterPresentations(project: VideoProject): Promise<CharacterPresentation[]> {
  return toCharacterPresentations(await loadWorkspacePresentations(project.workspacePath, ['media.character']))
}

export function toReferencePresentations(presentations: WorkspacePresentation[]): ReferencePresentation[] {
  return presentations.flatMap((presentation) => {
    const path = asString(presentation.payload.path)
    if (!path) return []
    return [{
      id: presentation.id,
      title: presentation.title || path.split('/').pop() || 'Reference',
      path,
      role: asString(presentation.payload.role),
      note: asString(presentation.payload.note),
      revision: presentation.revision,
      updatedAt: presentation.updatedAt,
      workspacePresentation: presentation,
    }]
  })
}

export async function loadReferencePresentations(project: VideoProject): Promise<ReferencePresentation[]> {
  return toReferencePresentations(await loadWorkspacePresentations(project.workspacePath, ['media.reference']))
}

export function toDocumentPresentations(presentations: WorkspacePresentation[]): DocumentPresentation[] {
  return presentations.flatMap((presentation) => {
    const path = asString(presentation.payload.path)
    if (!path) return []
    return [{
      id: presentation.id,
      title: presentation.title || path.split('/').pop() || 'Document',
      path,
      markdown: asString(presentation.payload.markdown),
      note: asString(presentation.payload.note),
      revision: presentation.revision,
      updatedAt: presentation.updatedAt,
      workspacePresentation: presentation,
    }]
  })
}

export async function loadDocumentPresentations(project: VideoProject): Promise<DocumentPresentation[]> {
  return toDocumentPresentations(await loadWorkspacePresentations(project.workspacePath, ['document.markdown']))
}

export async function loadVideoAssets(project: VideoProject): Promise<VideoAsset[]> {
  try {
    const response = await agentApi.getPlannerFiles(project.workspacePath, -1, 5)
    return flattenFiles(responseFiles(response))
      .filter((file) => file.type !== 'folder')
      .filter((file) => /\/(uploads|work|outputs|runs)\//.test(file.filepath))
      .map((file) => ({
        path: file.filepath,
        name: file.filepath.split('/').pop() || file.filepath,
        size: file.size,
        mimeType: file.mime_type,
        modifiedAt: file.last_modified,
      }))
      .sort((a, b) => (b.modifiedAt || '').localeCompare(a.modifiedAt || ''))
  } catch {
    return []
  }
}

export function parseWorkflowSteps(content: string): VideoWorkflowStep[] {
  try {
    const parsed = JSON.parse(content) as { steps?: unknown }
    if (!Array.isArray(parsed.steps)) return []
    return parsed.steps.flatMap((item) => {
      if (!item || typeof item !== 'object') return []
      const step = item as Record<string, unknown>
      const id = asString(step.id)
      if (!id) return []
      const routes = Array.isArray(step.routes)
        ? step.routes.flatMap((route) => {
            if (!route || typeof route !== 'object') return []
            const value = route as Record<string, unknown>
            return [{ id: asString(value.route_id), title: asString(value.route_name), nextStepId: asString(value.next_step_id) }]
          })
        : undefined
      return [{ id, title: asString(step.title) || id, type: asString(step.type) || 'scripted', routes }]
    })
  } catch {
    return []
  }
}

export async function loadVideoWorkflow(project: VideoProject): Promise<VideoWorkflowStep[]> {
  try {
    const response = await agentApi.getPlannerFileContent(`${project.workspacePath}/planning/plan.json`)
    const document = responseContent(response)
    return document ? parseWorkflowSteps(document.content) : []
  } catch {
    return []
  }
}

function base64Path(path: string): string {
  const bytes = new TextEncoder().encode(path)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

// This URL streams from the generic authenticated AgentWorks file proxy and
// preserves HTTP Range, so seeking never requires loading a full video Blob.
export function workspaceMediaURL(path: string): string {
  const params = new URLSearchParams({ path: base64Path(path) })
  const token = getAuthToken()
  if (token) params.set('token', token)
  return `${getApiBaseUrl()}/api/public/file?${params.toString()}`
}

export function relativeTime(value: string, now = Date.now()): string {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return 'Recently'
  const seconds = Math.max(0, Math.round((now - timestamp) / 1000))
  if (seconds < 60) return 'Just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return new Date(timestamp).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function formatBytes(size?: number): string {
  if (!size || size < 1) return '—'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MB`
  return `${(size / (1024 * 1024 * 1024)).toFixed(1)} GB`
}
