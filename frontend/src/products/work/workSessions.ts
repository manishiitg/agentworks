import { createProductProject, loadProductProjects, updateProductProjectIdentity,  updateProductProjectSelections, type ProductIdentityPatch, type ProductProject } from '../../platform/chat/productProjects'
import { registerRemoteProject, remoteProjectConnection } from '../../utils/remoteSessions'
import { agentApi } from '../../services/api'
import { secretsApi } from '../../api/secrets'
import type { LLMProvider, PresetLLMConfig, SharedProjectSummary } from '../../services/api-types'
import { loadAgentProfileProviderOptions } from '../../utils/agentProfileCapabilities'
import { llmConfigService } from '../../services/llm-config-api'
import { CREW_PRODUCT, projectProductConfig, type ProjectProductConfig, type ProjectProductId } from './projectProduct'

export type WorkSession = ProductProject<ProjectProductId>

export type WorkLLMSelection = {
  connectionId?: string
  provider: string
  modelId: string
  reasoningEffort?: string
}

export function workLLMConfigFromSelection(selection: WorkLLMSelection): PresetLLMConfig {
  return {
    schema_version: 2,
    mode: 'explicit',
    builder_llm: {
      provider: selection.provider as LLMProvider,
      connection_id: selection.connectionId,
      model_id: selection.modelId,
      ...(selection.reasoningEffort ? { options: { reasoning_effort: selection.reasoningEffort } } : {}),
    },
  }
}

export function workLLMSelectionFromConfig(config?: PresetLLMConfig): WorkLLMSelection | null {
  const builder = config?.builder_llm
  if (!builder?.provider || !builder.model_id) return null
  const reasoningEffort = typeof builder.options?.reasoning_effort === 'string'
    ? builder.options.reasoning_effort
    : undefined
  return { connectionId: builder.connection_id, provider: builder.provider, modelId: builder.model_id, reasoningEffort }
}

export async function loadWorkSessions(product: ProjectProductConfig = CREW_PRODUCT, connection?: string): Promise<WorkSession[]> {
  const sessions = await loadProductProjects(product.projectsRoot, product.profileId, { runtimeManifestName: 'workflow.json', includeOwnSharedProjects: product.listsSharedProjects, connection })
  return sessions
}

export async function createWorkSession(title: string, description: string, icon?: string, product: ProjectProductConfig = CREW_PRODUCT, runsOn?: WorkLLMSelection, rootPath?: string, firstMessage?: string, connection?: string): Promise<WorkSession> {
  // On a server (#4491) the server picks the model, as it does for a project made there.
  if (connection) {
    const project = await createProductProject({
      root: product.projectsRoot, firstMessage, product: product.profileId, title, description: product.hasIdentity ? description : '',
      sessionPrefix: product.sessionPrefix, slugFallback: product.slugFallback, commitLabel: `Create ${product.noun} project`,
      ...(product.hasIdentity ? { identity: { name: title.trim(), icon: icon?.trim() || Array.from(title.trim() || firstMessage?.trim() || '')[0]?.toLocaleUpperCase() || 'C' } } : {}),
      runtimeManifestName: 'workflow.json', connection,
    })
    registerRemoteProject(project.id, connection, project.workspacePath, project.filesPath)
    return project
  }
  const options = await loadAgentProfileProviderOptions(product.profileId)
  // The product default, unless this person cannot use it here (Claude Code and Codex are admins-only on some servers):
  // then the first coding agent they may run, so a new project does not start on a refused account (server B
  // 2026-10-07). Unknown accounts keep the product default.
  const accounts = await llmConfigService.getProviderConnections({ product: product.profileId }).catch(() => null)
  const usable = (option: { provider?: string }) => accounts === null || accounts.some(account => account.provider === option.provider && account.usable !== false)
  const ordered = [...options.filter(option => option.default), ...options.filter(option => !option.default)]
  const selected = ordered.find(usable) || ordered[0]
  const reasoningEffort = typeof selected?.options?.reasoning_effort === 'string'
    ? selected.options.reasoning_effort
    : selected?.reasoning_efforts?.[0]
  // The create dialog's "Runs on" choice (CLI, model and account) wins over the product default.
  const runsOnModel = runsOn?.provider
    ? runsOn.modelId || options.find(option => option.provider === runsOn.provider)?.model_id || ''
    : ''
  const llmConfig = runsOn?.provider && runsOnModel
    ? workLLMConfigFromSelection({ ...runsOn, modelId: runsOnModel })
    : selected?.provider && selected.model_id
      ? workLLMConfigFromSelection({ provider: selected.provider, modelId: selected.model_id, reasoningEffort })
      : undefined
  const project = await createProductProject({
    root: product.projectsRoot,
    rootPath,
    firstMessage,
    product: product.profileId,
    title,
    // Optional identity details can be added after name-only creation.
    description: product.hasIdentity ? description : '',
    sessionPrefix: product.sessionPrefix,
    slugFallback: product.slugFallback,
    commitLabel: `Create ${product.noun} project`,
    ...(product.hasIdentity ? {
      identity: {
        name: title.trim(),
        icon: icon?.trim() || Array.from(title.trim() || firstMessage?.trim() || '')[0]?.toLocaleUpperCase() || 'C',
      },
    } : {}),
    llmConfig,
    runtimeManifestName: 'workflow.json',
  })
  return project
}

export async function deleteWorkSession(session: WorkSession): Promise<void> {
  if (isWorkSessionRun(session)) throw new Error('Shared projects can only be deleted by their owner.')
  try {
    await agentApi.deleteAgentProfileProject(session.product, session.id)
  } catch (cause) {
    // A folder removed outside the project action is already gone. Let the
    // caller finish closing its tabs and clearing the cached project entry.
    if ((cause as { response?: { status?: number } })?.response?.status !== 404) throw cause
  }
}

export function sharedProjectToWorkSession(row: SharedProjectSummary, product: ProjectProductConfig = CREW_PRODUCT): WorkSession {
  const llm = row.llm?.provider && row.llm.model_id
    ? {
      schema_version: 2,
      mode: 'explicit',
      builder_llm: {
        provider: row.llm.provider,
        model_id: row.llm.model_id,
        ...(row.llm.reasoning_effort ? { options: { reasoning_effort: row.llm.reasoning_effort } } : {}),
      },
    } as PresetLLMConfig
    : undefined
  return {
    schemaVersion: 1,
    product: product.profileId,
    id: row.id,
    title: row.title || `Untitled ${product.noun}`,
    description: row.description || '',
    identity: row.icon || row.name ? { icon: row.icon || undefined, name: row.name || undefined } : undefined,
    // No session binding: opening a shared Crew resolves the reader's own
    // conversation server-side, never the owner's live session.
    sessionId: '',
    workspacePath: row.workspace_path,
    filesPath: row.files_path,
    filesName: row.files_name,
    createdAt: row.created_at || '',
    updatedAt: row.updated_at || '',
    llmConfig: llm,
    selectedServers: row.selected_servers || [],
    selectedSkills: row.selected_skills || [],
    selectedGlobalSecrets: row.selected_global_secrets || [],
    workflowContextPaths: row.workflow_context_paths || [],
    // Shared rows arrive fully formed. Marking every config initialized
    // keeps the owned-project migration path from ever writing to
    // another owner's manifests.
    selectionConfigInitialized: true,
    runtimeConfigInitialized: true,
    shared: {
      canBuild: row.can_build === true,
      ownerId: row.owner_id,
      ownerUsername: row.owner_username || undefined,
      triggers: row.triggers || [],
      schedules: row.schedules || [],
    },
  }
}

export async function loadSharedWorkSessions(product: ProjectProductConfig = CREW_PRODUCT, connection?: string): Promise<WorkSession[]> {
  if (!product.listsSharedProjects) return []
  const response = await agentApi.listSharedProjects(product.profileId, connection)
  return (response?.projects || []).map(row => sharedProjectToWorkSession(row, product))
}

/**
 * Owned Crews first, then other owners' Crews (Crew Run mode). A shared row
 * whose id collides with an owned Crew loses: the owned project is the one
 * the reader can open and change. Shared-listing failures degrade to
 * owned-only rather than failing the whole Crew surface.
 */
export async function loadWorkSessionsIncludingShared(product: ProjectProductConfig = CREW_PRODUCT, connection?: string): Promise<WorkSession[]> {
  if (!product.listsSharedProjects) return loadWorkSessions(product, connection)
  const [owned, shared] = await Promise.all([
    loadWorkSessions(product, connection),
    loadSharedWorkSessions(product, connection).catch(() => [] as WorkSession[]),
  ])
  const ownedIds = new Set(owned.map(session => session.id))
  return [...owned, ...shared.filter(session => !ownedIds.has(session.id))]
}

/** A project on another of this Mac's connections, opened in this window without a reload (#3383): read from its
 * own server, tagged with that connection, and its folders registered so its file reads go there too. */
export async function loadRemoteWorkSession(product: ProjectProductConfig, projectId: string | null | undefined): Promise<WorkSession | undefined> {
  const connection = remoteProjectConnection(projectId)
  if (!projectId || !connection) return undefined
  const session = (await loadWorkSessionsIncludingShared(product, connection)).find(item => item.id === projectId)
  if (!session) return undefined
  registerRemoteProject(session.id, connection, session.workspacePath, session.filesPath)
  return { ...session, connectionId: connection }
}

/** The window's own projects plus the open server project, if any; the server project replaces a same-ID row. */
export async function loadWorkSessionsWithOpenRemote(product: ProjectProductConfig, selectedId: string | null | undefined): Promise<WorkSession[]> {
  const [listed, remote] = await Promise.all([
    loadWorkSessionsIncludingShared(product),
    loadRemoteWorkSession(product, selectedId).catch(() => undefined),
  ])
  return remote ? [...listed.filter(item => item.id !== remote.id), remote] : listed
}



export async function updateWorkSessionIdentity(session: WorkSession, patch: ProductIdentityPatch): Promise<WorkSession> {
  if (isWorkSessionRun(session)) throw new Error('Only the project owner can change this.')
  return updateProductProjectIdentity(session, patch, `Update ${projectProductConfig(session.product).noun} project identity ${session.title}`)
}

export function isWorkSessionRun(session: WorkSession | null | undefined): boolean {
  return Boolean(session?.shared && !session.shared.canBuild)
}
