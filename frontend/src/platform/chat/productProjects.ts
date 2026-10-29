import { agentApi } from '../../services/api'
import type { PresetLLMConfig, SharedProjectSchedule, SharedProjectTrigger } from '../../services/api-types'
import { dedupeByFilepath, flattenFiles, responseContent, responseFiles, slugifyTitle } from '../../utils/plannerFiles'
import { nativeAgentToolsEnabled } from '../../utils/nativeAgentTools'


/** Server project mode; the optional laptop connection does not change it. */
export type ProductMode = 'dev' | 'cowork'
export const PRODUCT_MODES: readonly ProductMode[] = ['dev', 'cowork']

export type ProductProject<P extends string = string> = {
  schemaVersion: 1
  product: P
  id: string
  title: string
  description: string
  identity?: ProductIdentity
  sessionId: string
  workspacePath: string
  /** Opaque logical file root supplied by the server for a bound local folder. */
  filesPath?: string
  createdAt: string
  updatedAt: string
  llmConfig?: PresetLLMConfig
  selectedServers: string[]
  selectedSkills: string[]
  selectedSecrets: string[]
  selectedGlobalSecrets: string[]
  workflowContextPaths: string[]
  /** Crew "Native agent tools": capabilities.native_agent_tools in workflow.json, on unless explicitly false. */
  nativeAgentTools?: boolean
  /** Absent on projects made before modes: they are dev. */
  mode?: ProductMode
  /** Set by the server only: "agentworks" marks the person's own AgentWorks Crew. */
  role?: 'agentworks'
  creationWarnings?: string[]
  selectionConfigInitialized: boolean
  secretSelectionInitialized: boolean
  runtimeConfigInitialized: boolean
  /**
   * Set when another user owns the project (Crew Run mode). The UI must
   * treat the session as read-only: no manifest writes, no deletions, no
   * management panels — and file reads go through the mediated shared
   * endpoints, never the workspace proxy with the owner's path.
   */
  shared?: ProductProjectShare
}

export type ProductProjectShare = {
  canBuild?: boolean
  ownerId: string
  ownerUsername?: string
  triggers: SharedProjectTrigger[]
  schedules: SharedProjectSchedule[]
}

export type ProductIdentity = {
  icon?: string
  name?: string
}

type ProductManifest = {
  schema_version?: unknown
  product?: unknown
  id?: unknown
  title?: unknown
  description?: unknown
  identity?: unknown
  session_id?: unknown
  created_at?: unknown
  updated_at?: unknown
  capabilities?: unknown
  workflow_context_paths?: unknown
  mode?: unknown
  role?: unknown
}

const asString = (value: unknown): string => typeof value === 'string' ? value.trim() : ''

function parseProductIdentity(value: unknown): ProductIdentity | undefined {
  if (!value || typeof value !== 'object') return undefined
  const raw = value as Record<string, unknown>
  const identity = {
    icon: asString(raw.icon),
    name: asString(raw.name),
  }
  return Object.values(identity).some(Boolean) ? identity : undefined
}

const localFileId = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/


function manifestMode(raw: ProductManifest): ProductMode | undefined {
  return PRODUCT_MODES.find(mode => mode === raw.mode)
}

/** The server mode a project works in. */
export function productMode(project: Pick<ProductProject, 'mode'>): ProductMode {
  return project.mode ?? 'dev'
}

function parseProductLLMConfig(value: unknown): PresetLLMConfig | undefined {
  if (!value || typeof value !== 'object') return undefined
  const config = value as Partial<PresetLLMConfig>
  if (config.schema_version !== 2 || (config.mode !== 'provider_profile' && config.mode !== 'explicit')) return undefined
  return config as PresetLLMConfig
}

function manifestLLMConfig(raw: ProductManifest): PresetLLMConfig | undefined {
  if (!raw.capabilities || typeof raw.capabilities !== 'object') return undefined
  return parseProductLLMConfig((raw.capabilities as { llm_config?: unknown }).llm_config)
}

function manifestStringList(raw: ProductManifest, key: 'selected_servers' | 'selected_skills' | 'selected_secrets' | 'selected_global_secret_names' | 'workflow_context_paths'): string[] {
  if (key === 'workflow_context_paths' && Array.isArray(raw.workflow_context_paths)) {
    return [...new Set(raw.workflow_context_paths.map(asString).filter(Boolean))]
  }
  if (!raw.capabilities || typeof raw.capabilities !== 'object') return []
  const value = (raw.capabilities as Record<string, unknown>)[key]
  if (!Array.isArray(value)) return []
  return [...new Set(value.map(asString).filter(Boolean))]
}

function manifestGlobalSecretSelection(raw: ProductManifest): string[] {
  if (!raw.capabilities || typeof raw.capabilities !== 'object') return []
  const capabilities = raw.capabilities as Record<string, unknown>
  if (capabilities.selected_global_secret_names === null) return []
  return manifestStringList(raw, 'selected_global_secret_names')
}

function manifestHasSelectionConfig(raw: ProductManifest): boolean {
  if (!raw.capabilities || typeof raw.capabilities !== 'object') return false
  const capabilities = raw.capabilities as Record<string, unknown>
  return Array.isArray(capabilities.selected_servers) && Array.isArray(capabilities.selected_skills)
}

function manifestHasSecretSelection(raw: ProductManifest): boolean {
  return !!raw.capabilities
    && typeof raw.capabilities === 'object'
    && Array.isArray((raw.capabilities as Record<string, unknown>).selected_secrets)
}

export function parseProductProjectManifest<P extends string>(
  content: string,
  workspacePath: string,
  product: P,
  lastModified?: string,
): ProductProject<P> | null {
  let raw: ProductManifest
  try {
    raw = JSON.parse(content) as ProductManifest
  } catch {
    return null
  }
  const id = asString(raw.id)
  const title = asString(raw.title)
  const sessionId = asString(raw.session_id)
  if (raw.schema_version !== 1 || raw.product !== product || !id || !title || !sessionId) return null
  const createdAt = asString(raw.created_at) || lastModified || new Date(0).toISOString()
  return {
    schemaVersion: 1,
    product,
    id,
    title,
    description: asString(raw.description),
    identity: parseProductIdentity(raw.identity),
    sessionId,
    workspacePath,
    createdAt,
    updatedAt: lastModified || asString(raw.updated_at) || createdAt,
    llmConfig: manifestLLMConfig(raw),
    selectedServers: manifestStringList(raw, 'selected_servers'),
    selectedSkills: manifestStringList(raw, 'selected_skills'),
    selectedSecrets: manifestStringList(raw, 'selected_secrets'),
    selectedGlobalSecrets: manifestGlobalSecretSelection(raw),
    workflowContextPaths: manifestStringList(raw, 'workflow_context_paths'),
    nativeAgentTools: manifestNativeAgentTools(raw),
    mode: manifestMode(raw),
    ...(raw.role === 'agentworks' ? { role: 'agentworks' as const } : {}),
    selectionConfigInitialized: manifestHasSelectionConfig(raw),
    secretSelectionInitialized: manifestHasSecretSelection(raw),
    runtimeConfigInitialized: true,
  }
}

type ProductProjectStorageOptions = {
  runtimeManifestName?: string
  /** Load owned Crews from the server's project catalog. */
  includeOwnSharedProjects?: boolean
}

function manifestNativeAgentTools(raw: ProductManifest): boolean {
  const capabilities = raw.capabilities
  const setting = capabilities && typeof capabilities === 'object'
    ? (capabilities as { native_agent_tools?: unknown }).native_agent_tools
    : undefined
  return nativeAgentToolsEnabled(setting)
}

function applyRuntimeManifest<P extends string>(project: ProductProject<P>, content: string): ProductProject<P> {
  let raw: ProductManifest
  try {
    raw = JSON.parse(content) as ProductManifest
  } catch {
    return project
  }
  return {
    ...project,
    llmConfig: manifestLLMConfig(raw),
    selectedServers: manifestStringList(raw, 'selected_servers'),
    selectedSkills: manifestStringList(raw, 'selected_skills'),
    selectedSecrets: manifestStringList(raw, 'selected_secrets'),
    selectedGlobalSecrets: manifestGlobalSecretSelection(raw),
    workflowContextPaths: manifestStringList(raw, 'workflow_context_paths'),
    nativeAgentTools: manifestNativeAgentTools(raw),
    selectionConfigInitialized: manifestHasSelectionConfig(raw),
    secretSelectionInitialized: manifestHasSecretSelection(raw),
    runtimeConfigInitialized: true,
  }
}

export async function loadProductProjects<P extends string>(root: string, product: P, storage: ProductProjectStorageOptions = {}): Promise<ProductProject<P>[]> {
  // The server selects each Crew's current registered location. Folder scans
  // can include retired filesystem links and cannot decide project identity.
  const directory = storage.includeOwnSharedProjects ? (await agentApi.listOwnSharedProjects(product)).projects : undefined
  const filesRoots = new Map(directory?.map(project => [project.workspace_path, project.files_path]))
  const manifests = directory
    ? directory.map(project => ({ filepath: `${project.workspace_path}/product.json`, type: 'file', last_modified: undefined }))
    : dedupeByFilepath(flattenFiles(responseFiles(await agentApi.getPlannerFiles(root, -1, 2)))
      .filter(file => file.type !== 'folder' && file.filepath.endsWith('/product.json')))
  const projects = await Promise.all(manifests.map(async (file) => {
    const workspacePath = file.filepath.replace(/\/product\.json$/, '')
    const runtimeRead = storage.runtimeManifestName
      ? agentApi.getPlannerFileContent(`${workspacePath}/${storage.runtimeManifestName}`).then(
        (runtimeResponse) => { try { return responseContent(runtimeResponse) } catch { return undefined } },
        () => undefined,
      )
      : undefined
    try {
      const response = await agentApi.getPlannerFileContent(file.filepath)
      const document = responseContent(response)
      if (!document) return null
      const project = parseProductProjectManifest(
        document.content,
        workspacePath,
        product,
        document.lastModified || file.last_modified,
      )
      if (project) project.filesPath = filesRoots.get(workspacePath)
      if (!project || !runtimeRead) return project
      const runtimeDocument = await runtimeRead
      return runtimeDocument ? applyRuntimeManifest(project, runtimeDocument.content) : { ...project, runtimeConfigInitialized: false }
    } catch {
      return null
    }
  }))
  return projects
    .filter((project): project is ProductProject<P> => project !== null)
    .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
}

export async function createProductProject<P extends string>(options: {
  root: string
  rootPath?: string
  product: P
  title: string
  description: string
  sessionPrefix: string
  slugFallback: string
  commitLabel: string
  llmConfig?: PresetLLMConfig
  identity?: ProductIdentity
  selectedSkills?: readonly string[]
  initialFiles?: Readonly<Record<string, string>>
  runtimeManifestName?: string
  mode?: ProductMode
}): Promise<ProductProject<P>> {
  const title = options.title.trim()
  const description = options.description.trim()
  if (options.product === 'work' || options.product === 'code') {
    const created = await agentApi.createProductProject(options.product, {
      name: title, purpose: description, root_path: options.rootPath, icon: options.identity?.icon,
      mode: options.mode, llm_config: options.llmConfig,
      selected_skills: options.selectedSkills, files: options.initialFiles,
    })
    const project = parseProductProjectManifest(JSON.stringify(created.product_manifest), created.workspace_path, options.product)
    if (!project || project.id !== created.id) throw new Error('The new Crew could not be opened. Reload your projects to find it.')
    return { ...applyRuntimeManifest(project, JSON.stringify(created.runtime_manifest)), filesPath: created.files_path, creationWarnings: created.warnings }
  }
  // Personal products retain their existing creation path. Crew and Code
  // have already returned through server-owned creation above.
  const reserved = await (async () => agentApi.reserveProject(options.product, title, options.mode))().catch((error: unknown) => {
    const response = (error as { response?: { status?: number; data?: { error?: string } } })?.response
    if (response?.status === 403) throw new Error(response.data?.error || 'This account cannot create Crews.')
    return { shared: false } as { shared: boolean; id?: string; workspace_path?: string }
  })
  const shared = reserved.shared && !!reserved.id && !!reserved.workspace_path
  const id = shared ? reserved.id! : globalThis.crypto.randomUUID()
  const sessionId = `${options.sessionPrefix}:${id}`
  const workspacePath = shared ? reserved.workspace_path! : `${options.root}/${slugifyTitle(title, options.slugFallback)}-${id.slice(0, 8)}`
  const now = new Date().toISOString()
  const capabilities: Record<string, unknown> = {
    selected_servers: [],
    selected_tools: [],
    selected_skills: [...new Set(options.selectedSkills || [])],
    selected_secrets: [],
    selected_global_secret_names: [],
    use_code_execution_mode: false,
    ...(options.llmConfig ? { llm_config: options.llmConfig } : {}),
  }
  if (!options.runtimeManifestName) capabilities.workflow_context_paths = []
  const manifest: Record<string, unknown> = {
    schema_version: 1,
    product: options.product,
    id,
    title,
    description,
    session_id: sessionId,
    created_at: now,
    updated_at: now,
    ...(options.identity ? { identity: options.identity } : {}),
    ...(options.mode ? { mode: options.mode } : {}),
  }
  if (!options.runtimeManifestName) manifest.capabilities = capabilities
  if (options.runtimeManifestName) {
    const runtimeManifest = {
      schema_version: 1,
      id,
      label: title,
      capabilities,
      workflow_context_paths: [],
      schedules: [],
      triggers: [],
      created_at: now,
      updated_at: now,
    }
    await agentApi.updatePlannerFile(
      `${workspacePath}/${options.runtimeManifestName}`,
      `${JSON.stringify(runtimeManifest, null, 2)}\n`,
      `${options.commitLabel} runtime ${title}`,
    )
  }
  for (const [relativePath, content] of Object.entries(options.initialFiles || {})) {
    if (!relativePath || relativePath.startsWith('/') || relativePath.split('/').some(part => !part || part === '.' || part === '..')) {
      throw new Error(`Invalid initial project file path: ${relativePath}`)
    }
    await agentApi.updatePlannerFile(
      `${workspacePath}/${relativePath}`,
      content,
      `${options.commitLabel} file ${relativePath}`,
    )
  }
  await agentApi.updatePlannerFile(
    `${workspacePath}/product.json`,
    `${JSON.stringify(manifest, null, 2)}\n`,
    `${options.commitLabel} ${title}`,
  )
  return {
    schemaVersion: 1,
    product: options.product,
    id,
    title,
    description,
    identity: options.identity,
    sessionId,
    workspacePath,
    createdAt: now,
    updatedAt: now,
    // The new project is shown right away from this object, so it carries its mode (Cowork's toolbar and welcome); a refresh reads it from product.json.
    ...(options.mode ? { mode: options.mode } : {}),
    llmConfig: options.llmConfig,
    selectedServers: [],
    selectedSkills: [...new Set(options.selectedSkills || [])],
    selectedSecrets: [],
    selectedGlobalSecrets: [],
    workflowContextPaths: [],
    selectionConfigInitialized: true,
    secretSelectionInitialized: true,
    runtimeConfigInitialized: true,
  }
}

async function readProjectRuntimeManifest<P extends string>(project: ProductProject<P>, runtimeManifestName?: string): Promise<{ path: string; manifest: Record<string, unknown> }> {
  const path = `${project.workspacePath}/${runtimeManifestName || 'product.json'}`
  try {
    const response = await agentApi.getPlannerFileContent(path)
    const document = responseContent(response)
    if (document) return { path, manifest: JSON.parse(document.content) as Record<string, unknown> }
  } catch (cause) {
    if (!runtimeManifestName) throw cause
  }
  const productResponse = await agentApi.getPlannerFileContent(`${project.workspacePath}/product.json`)
  const productDocument = responseContent(productResponse)
  if (!productDocument) throw new Error('Project configuration was not found.')
  const legacy = JSON.parse(productDocument.content) as Record<string, unknown>
  const legacyCapabilities = legacy.capabilities && typeof legacy.capabilities === 'object'
    ? { ...(legacy.capabilities as Record<string, unknown>) }
    : {}
  const legacyWorkflowContextPaths = Array.isArray(legacy.workflow_context_paths)
    ? legacy.workflow_context_paths
    : (Array.isArray(legacyCapabilities.workflow_context_paths) ? legacyCapabilities.workflow_context_paths : [])
  delete legacyCapabilities.workflow_context_paths
  if (!Array.isArray(legacyCapabilities.selected_tools)) legacyCapabilities.selected_tools = []
  if (!Array.isArray(legacyCapabilities.selected_global_secret_names)) legacyCapabilities.selected_global_secret_names = []
  if (typeof legacyCapabilities.use_code_execution_mode !== 'boolean') legacyCapabilities.use_code_execution_mode = false
  return {
    path,
    manifest: {
      schema_version: 1,
      id: project.id,
      label: project.title,
      capabilities: legacyCapabilities,
      workflow_context_paths: legacyWorkflowContextPaths,
      schedules: Array.isArray(legacy.schedules) ? legacy.schedules : [],
      triggers: Array.isArray(legacy.triggers) ? legacy.triggers : [],
      created_at: project.createdAt,
      updated_at: project.updatedAt,
    },
  }
}

async function stripLegacyRuntimeFromProduct<P extends string>(project: ProductProject<P>, commitLabel: string): Promise<void> {
  const path = `${project.workspacePath}/product.json`
  const response = await agentApi.getPlannerFileContent(path)
  const document = responseContent(response)
  if (!document) return
  const manifest = JSON.parse(document.content) as Record<string, unknown>
  const hadRuntime = ['capabilities', 'schedules', 'triggers', 'workflow_context_paths'].some(key => key in manifest)
  if (!hadRuntime) return
  delete manifest.capabilities
  delete manifest.schedules
  delete manifest.triggers
  delete manifest.workflow_context_paths
  manifest.updated_at = new Date().toISOString()
  await agentApi.updatePlannerFile(path, `${JSON.stringify(manifest, null, 2)}\n`, `${commitLabel} metadata migration`)
}

export type ProductIdentityPatch = {
  icon?: string
  name?: string
  purpose?: string
}

// Identity always lives in product.json, never in the runtime manifest.
// Mirrors the server set_work_identity tool: omitted fields are preserved,
// empty fields are removed, and an identity with nothing left is dropped.
// Purpose is stored as the top-level project description.
export async function updateProductProjectIdentity<P extends string>(
  project: ProductProject<P>,
  patch: ProductIdentityPatch,
  commitLabel: string,
): Promise<ProductProject<P>> {
  const path = `${project.workspacePath}/product.json`
  const response = await agentApi.getPlannerFileContent(path)
  const document = responseContent(response)
  if (!document) throw new Error('Project configuration was not found.')
  let manifest: Record<string, unknown>
  try {
    manifest = JSON.parse(document.content) as Record<string, unknown>
  } catch {
    throw new Error('Project configuration is invalid JSON.')
  }
  const current = manifest.identity && typeof manifest.identity === 'object'
    ? { ...(manifest.identity as Record<string, unknown>) }
    : {}
  const merged: Record<string, string> = {}
  for (const key of ['icon', 'name'] as const) {
    const value = patch[key] === undefined ? asString(current[key]) : patch[key].trim()
    if (value) merged[key] = value
  }
  if (Object.keys(merged).length > 0) manifest.identity = merged
  else delete manifest.identity
  const description = patch.purpose === undefined ? asString(manifest.description) : patch.purpose.trim()
  if (description) manifest.description = description
  const updatedAt = new Date().toISOString()
  manifest.updated_at = updatedAt
  await agentApi.updatePlannerFile(path, `${JSON.stringify(manifest, null, 2)}\n`, commitLabel)
  return { ...project, description: asString(manifest.description), identity: parseProductIdentity(manifest.identity), updatedAt }
}

export async function updateProductProjectLLMConfig<P extends string>(
  project: ProductProject<P>,
  llmConfig: PresetLLMConfig,
  commitLabel: string,
  runtimeManifestName?: string,
): Promise<ProductProject<P>> {
  try {
    const { path: manifestPath, manifest } = await readProjectRuntimeManifest(project, runtimeManifestName)
    const capabilities = manifest.capabilities && typeof manifest.capabilities === 'object'
      ? { ...(manifest.capabilities as Record<string, unknown>) }
      : {}
    // A model change keeps the project's idle compaction choice.
    const saved = savedLLMConfig(capabilities)
    const next: PresetLLMConfig = llmConfig.idle_compaction === undefined && saved?.idle_compaction !== undefined
      ? { ...llmConfig, idle_compaction: saved.idle_compaction }
      : llmConfig
    capabilities.llm_config = next
    const updatedAt = new Date().toISOString()
    manifest.capabilities = capabilities
    manifest.updated_at = updatedAt
    await agentApi.updatePlannerFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, commitLabel)
    if (runtimeManifestName && !project.runtimeConfigInitialized) await stripLegacyRuntimeFromProduct(project, commitLabel)
    return { ...project, llmConfig: next, runtimeConfigInitialized: true, updatedAt }
  } catch {
    throw new Error('Project configuration is invalid JSON.')
  }
}

function savedLLMConfig(capabilities: Record<string, unknown>): PresetLLMConfig | undefined {
  return parseProductLLMConfig(capabilities.llm_config)
}

/** Sets the project's "Tidy long chats while idle" switch (capabilities.llm_config.idle_compaction, PLAT-872-71). */
export async function updateProductProjectIdleCompaction<P extends string>(
  project: ProductProject<P>,
  enabled: boolean,
  commitLabel: string,
  runtimeManifestName?: string,
): Promise<ProductProject<P>> {
  let manifestPath: string
  let manifest: Record<string, unknown>
  try {
    ({ path: manifestPath, manifest } = await readProjectRuntimeManifest(project, runtimeManifestName))
  } catch {
    throw new Error('Project configuration is invalid JSON.')
  }
  const capabilities = manifest.capabilities && typeof manifest.capabilities === 'object'
    ? { ...(manifest.capabilities as Record<string, unknown>) }
    : {}
  const saved = savedLLMConfig(capabilities)
  if (!saved) throw new Error('Choose a model first, then change this setting.')
  const llmConfig: PresetLLMConfig = { ...saved, idle_compaction: enabled }
  capabilities.llm_config = llmConfig
  const updatedAt = new Date().toISOString()
  manifest.capabilities = capabilities
  manifest.updated_at = updatedAt
  await agentApi.updatePlannerFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, commitLabel)
  return { ...project, llmConfig, updatedAt }
}

/** Sets the crew's "Native agent tools" switch (capabilities.native_agent_tools). */
export async function updateProductProjectNativeAgentTools<P extends string>(
  project: ProductProject<P>,
  enabled: boolean,
  commitLabel: string,
  runtimeManifestName?: string,
): Promise<ProductProject<P>> {
  let manifestPath: string
  let manifest: Record<string, unknown>
  try {
    ({ path: manifestPath, manifest } = await readProjectRuntimeManifest(project, runtimeManifestName))
  } catch {
    throw new Error('Project configuration is invalid JSON.')
  }
  const capabilities = manifest.capabilities && typeof manifest.capabilities === 'object'
    ? { ...(manifest.capabilities as Record<string, unknown>) }
    : {}
  // Saved explicitly: unset means on, so turning it off must write false.
  capabilities.native_agent_tools = enabled
  const updatedAt = new Date().toISOString()
  manifest.capabilities = capabilities
  manifest.updated_at = updatedAt
  await agentApi.updatePlannerFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, commitLabel)
  return { ...project, nativeAgentTools: enabled, updatedAt }
}

export async function updateProductProjectSelections<P extends string>(
  project: ProductProject<P>,
  patch: { selectedServers?: string[]; selectedSkills?: string[]; selectedSecrets?: string[]; selectedGlobalSecrets?: string[]; workflowContextPaths?: string[] },
  commitLabel: string,
  runtimeManifestName?: string,
): Promise<ProductProject<P>> {
  try {
    const { path: manifestPath, manifest } = await readProjectRuntimeManifest(project, runtimeManifestName)
    const capabilities = manifest.capabilities && typeof manifest.capabilities === 'object'
      ? { ...(manifest.capabilities as Record<string, unknown>) }
      : {}
    const normalize = (values: string[]) => [...new Set(values.map(value => value.trim()).filter(Boolean))]
    // Vault or another settings view may have saved since this project was
    // listed. Keep untouched selections from the manifest we just read.
    const saved = (key: string): string[] => Array.isArray(capabilities[key]) ? capabilities[key] as string[] : []
    if (patch.selectedServers !== undefined) capabilities.selected_servers = normalize(patch.selectedServers)
    if (patch.selectedSkills !== undefined) capabilities.selected_skills = normalize(patch.selectedSkills)
    if (patch.selectedSecrets !== undefined) capabilities.selected_secrets = normalize(patch.selectedSecrets)
    if (patch.selectedGlobalSecrets !== undefined) capabilities.selected_global_secret_names = normalize(patch.selectedGlobalSecrets)
    if (patch.workflowContextPaths !== undefined) {
      if (runtimeManifestName) manifest.workflow_context_paths = normalize(patch.workflowContextPaths)
      else capabilities.workflow_context_paths = normalize(patch.workflowContextPaths)
    }
    const selectedServers = saved('selected_servers')
    const selectedSkills = saved('selected_skills')
    const selectedSecrets = saved('selected_secrets')
    const selectedGlobalSecrets = saved('selected_global_secret_names')
    const savedContextPaths = runtimeManifestName ? manifest.workflow_context_paths : capabilities.workflow_context_paths
    const workflowContextPaths = Array.isArray(savedContextPaths) ? savedContextPaths as string[] : []
    const updatedAt = new Date().toISOString()
    manifest.capabilities = capabilities
    manifest.updated_at = updatedAt
    await agentApi.updatePlannerFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, commitLabel)
    if (runtimeManifestName && !project.runtimeConfigInitialized) await stripLegacyRuntimeFromProduct(project, commitLabel)
    return { ...project, selectedServers, selectedSkills, selectedSecrets, selectedGlobalSecrets, workflowContextPaths, selectionConfigInitialized: true, secretSelectionInitialized: true, runtimeConfigInitialized: true, updatedAt }
  } catch {
    throw new Error('Project configuration is invalid JSON.')
  }
}
