import { addProductProjectTemplate, removeProductProjectTemplate, createProductProject, loadProductProjects, parseProductProjectManifest, updateProductProjectIdentity, updateProductProjectLocalFiles, updateProductProjectSelections, type ProductIdentityPatch, type ProductMode, type ProductProject } from '../../platform/chat/productProjects'
import { agentApi } from '../../services/api'
import { secretsApi } from '../../api/secrets'
import type { LLMProvider, PresetLLMConfig, SharedProjectSummary } from '../../services/api-types'
import { responseContent, slugifyTitle } from '../../utils/plannerFiles'
import { loadAgentProfileProviderOptions } from '../../utils/agentProfileCapabilities'
import { llmConfigService } from '../../services/llm-config-api'
import { CREW_PRODUCT, projectProductConfig, type ProjectProductConfig, type ProjectProductId } from './projectProduct'
import { crewTemplates, getCrewTemplate, type CrewTemplateId } from './crewTemplates'

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

export function sessionSlug(title: string): string {
  return slugifyTitle(title, 'workspace')
}

export function parseSessionManifest(content: string, workspacePath: string, lastModified?: string, product: ProjectProductConfig = CREW_PRODUCT): WorkSession | null {
  return parseProductProjectManifest(content, workspacePath, product.profileId, lastModified)
}

export async function loadWorkSessions(product: ProjectProductConfig = CREW_PRODUCT): Promise<WorkSession[]> {
  const sessions = await loadProductProjects(product.projectsRoot, product.profileId, { runtimeManifestName: 'workflow.json', includeOwnSharedProjects: product.listsSharedProjects })
  return Promise.all(sessions.map(async original => {
    let session = original
    if (!session.runtimeConfigInitialized || !session.selectionConfigInitialized) {
      session = await updateProductProjectSelections(session, {
        ...(!session.selectionConfigInitialized ? { selectedServers: [], selectedSkills: [] } : {}),
      }, `Initialize ${projectProductConfig(session.product).noun} project runtime ${session.title}`, 'workflow.json')
    }
    if (!original.secretSelectionInitialized) {
      try {
        const stored = await secretsApi.listWorkflowSecrets(session.workspacePath)
        session = await updateProductProjectSelections(session, { selectedSecrets: stored.map(secret => secret.name) }, `Initialize ${projectProductConfig(session.product).noun} project secret attachments ${session.title}`, 'workflow.json')
      } catch {
        // Keep the project usable during a transient secret-store failure. The
        // server performs the same migration before the next agent turn.
      }
    }
    return session
  }))
}

export async function createWorkSession(title: string, description: string, icon?: string, templateId?: CrewTemplateId, product: ProjectProductConfig = CREW_PRODUCT, runsOn?: WorkLLMSelection, mode?: ProductMode): Promise<WorkSession> {
  const template = templateId && product.hasTemplates ? getCrewTemplate(templateId) : undefined
  const options = await loadAgentProfileProviderOptions(product.profileId)
  // The product default, unless this person cannot use it here (Claude Code and Codex are admins-only on some servers):
  // then the first coding agent they may run, so a new project does not start on a refused account (excellence
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
    product: product.profileId,
    title,
    // A Code has a name only: no purpose, role or icon.
    description: product.hasIdentity ? description : '',
    sessionPrefix: product.sessionPrefix,
    slugFallback: product.slugFallback,
    commitLabel: `Create ${product.noun} project`,
    ...(product.hasIdentity ? {
      identity: {
        name: title.trim(),
        icon: icon?.trim() || Array.from(title.trim())[0]?.toLocaleUpperCase() || 'C',
        ...(template ? { role: template.role } : {}),
      },
    } : {}),
    ...(template ? {
      templates: [{ id: template.id, version: template.version }],
      selectedSkills: template.selectedSkills,
      initialFiles: template.files,
    } : {}),
    llmConfig,
    runtimeManifestName: 'workflow.json',
    ...(mode ? { mode } : {}),
  })
  await agentApi.createPlannerFolder(
    `${project.workspacePath}/code`,
    `Initialize ${product.noun} project code folder ${project.title}`,
  )
  return project
}

export async function installWorkSessionTemplate(session: WorkSession, templateId: CrewTemplateId): Promise<WorkSession> {
  if (session.shared) throw new Error('Only the Crew owner can install templates.')
  const template = getCrewTemplate(templateId)
  if (session.templates.some(item => item.id === template.id)) throw new Error(`${template.name} is already installed in this Crew.`)
  for (const [relativePath, content] of Object.entries(template.files)) {
    const path = `${session.workspacePath}/${relativePath}`
    try {
      const existing = responseContent(await agentApi.getPlannerFileContent(path))
      // Reattaching a removed template preserves edited skills and setup progress.
      if (existing) continue
    } catch (cause) {
      const status = (cause as { response?: { status?: number } })?.response?.status
      if (status !== 404) throw cause
    }
    await agentApi.updatePlannerFile(path, content, `Install ${template.name} file ${relativePath}`)
  }
  const selectedSkills = [...new Set([...session.selectedSkills, ...template.selectedSkills])]
  const withSkill = await updateProductProjectSelections(session, { selectedSkills }, `Select ${template.name} skill`, 'workflow.json')
  return addProductProjectTemplate(withSkill, { id: template.id, version: template.version }, `Install ${template.name} in Crew ${session.title}`)
}

export async function removeWorkSessionTemplate(session: WorkSession, templateId: CrewTemplateId): Promise<WorkSession> {
  if (session.shared) throw new Error('Only the Crew owner can remove templates.')
  if (session.product !== 'work') throw new Error('This project does not support Crew templates.')
  const template = getCrewTemplate(templateId)
  return removeProductProjectTemplate(session, templateId,
    id => crewTemplates.find(item => item.id === id)?.selectedSkills ?? [],
    `Remove ${template.name} from Crew ${session.title}`, 'workflow.json')
}

export async function deleteWorkSession(session: WorkSession): Promise<void> {
  if (session.shared) throw new Error('Shared Crew projects can only be deleted by their owner.')
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
    templates: [],
    identity: row.icon || row.name ? { icon: row.icon || undefined, name: row.name || undefined } : undefined,
    // No session binding: opening a shared Crew resolves the reader's own
    // conversation server-side, never the owner's live session.
    sessionId: '',
    workspacePath: row.workspace_path,
    createdAt: row.created_at || '',
    updatedAt: row.updated_at || '',
    llmConfig: llm,
    selectedServers: row.selected_servers || [],
    selectedSkills: row.selected_skills || [],
    selectedSecrets: row.selected_secrets || [],
    selectedGlobalSecrets: row.selected_global_secrets || [],
    workflowContextPaths: row.workflow_context_paths || [],
    // Shared rows arrive fully formed. Marking every config initialized
    // keeps the owned-project migration path from ever writing to
    // another owner's manifests.
    selectionConfigInitialized: true,
    secretSelectionInitialized: true,
    runtimeConfigInitialized: true,
    shared: {
      ownerId: row.owner_id,
      ownerUsername: row.owner_username || undefined,
      triggers: row.triggers || [],
      schedules: row.schedules || [],
    },
  }
}

export async function loadSharedWorkSessions(product: ProjectProductConfig = CREW_PRODUCT): Promise<WorkSession[]> {
  if (!product.listsSharedProjects) return []
  const response = await agentApi.listSharedProjects(product.profileId)
  return (response?.projects || []).map(row => sharedProjectToWorkSession(row, product))
}

/**
 * Owned Crews first, then other owners' Crews (Crew Run mode). A shared row
 * whose id collides with an owned Crew loses: the owned project is the one
 * the reader can open and change. Shared-listing failures degrade to
 * owned-only rather than failing the whole Crew surface.
 */
export async function loadWorkSessionsIncludingShared(product: ProjectProductConfig = CREW_PRODUCT): Promise<WorkSession[]> {
  if (!product.listsSharedProjects) return loadWorkSessions(product)
  const [owned, shared] = await Promise.all([
    loadWorkSessions(product),
    loadSharedWorkSessions(product).catch(() => [] as WorkSession[]),
  ])
  const ownedIds = new Set(owned.map(session => session.id))
  return [...owned, ...shared.filter(session => !ownedIds.has(session.id))]
}

/** Code: the computer and folder this workspace works in (product.json `local_files`); undefined = the server's files. */
export async function updateWorkSessionLocalFiles(session: WorkSession, localFiles: { device_id: string; resource_id: string } | undefined): Promise<WorkSession> {
  if (session.shared) throw new Error('Only the owner can change this.')
  return updateProductProjectLocalFiles(session, localFiles, `Set files location for ${session.title}`)
}

export async function updateWorkSessionIdentity(session: WorkSession, patch: ProductIdentityPatch): Promise<WorkSession> {
  if (session.shared) throw new Error('Only the Crew owner can change this.')
  return updateProductProjectIdentity(session, patch, `Update ${projectProductConfig(session.product).noun} project identity ${session.title}`)
}
