// Crew and Code are two product definitions over the same project surface
// (agent_go/internal/workproduct and internal/codeproduct). The surface and
// its panels read what differs from this config instead of forking.
import { createContext, useContext } from 'react'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

export type ProjectProductId = 'work' | 'code'

export type ProjectProductConfig = {
  /** Agent profile id and product surface id. */
  profileId: ProjectProductId
  profileVersion: number
  /** Logical projects root below the owner's tree. */
  projectsRoot: string
  sessionPrefix: string
  slugFallback: string
  /** Product name in UI copy: "Crew" or "Code". */
  noun: string
  /** Name of one project in UI copy. */
  itemNoun: string
  /** Agent identity (role, purpose, icon) and its setup prompts. */
  hasIdentity: boolean
  /** Crew templates, playbooks and role installs. */
  hasTemplates: boolean
  /** Other owners' projects listed read-only (Crew Run mode). */
  listsSharedProjects: boolean
  /** The "Native agent tools" (hybrid) switch. */
  hasNativeAgentToolsSetting: boolean
  /** View a project opens on before the user picks one. */
  defaultView: WorkWorkspaceView
  /** localStorage key namespace for per-project UI preferences. */
  preferenceNamespace: string
}

export const CREW_PRODUCT: ProjectProductConfig = {
  profileId: 'work',
  profileVersion: 3,
  projectsRoot: 'Chats/Work/projects',
  sessionPrefix: 'work:project',
  slugFallback: 'workspace',
  noun: 'Crew',
  itemNoun: 'Crew member',
  hasIdentity: true,
  hasTemplates: true,
  listsSharedProjects: true,
  hasNativeAgentToolsSetting: true,
  defaultView: 'identity',
  preferenceNamespace: 'work',
}

export const CODE_PRODUCT: ProjectProductConfig = {
  profileId: 'code',
  profileVersion: 1,
  projectsRoot: 'Chats/Code/projects',
  sessionPrefix: 'code:project',
  slugFallback: 'code',
  noun: 'Code',
  itemNoun: 'Code workspace',
  hasIdentity: false,
  hasTemplates: false,
  listsSharedProjects: false,
  // Code stays on MCP-only agent tools until native reads are sandboxed.
  hasNativeAgentToolsSetting: false,
  defaultView: 'files',
  preferenceNamespace: 'code',
}

export const PROJECT_PRODUCT_IDS: readonly ProjectProductId[] = ['work', 'code']

export function isProjectProductId(value: unknown): value is ProjectProductId {
  return value === 'work' || value === 'code'
}

export function projectProductConfig(profileId: ProjectProductId): ProjectProductConfig {
  return profileId === 'code' ? CODE_PRODUCT : CREW_PRODUCT
}

/** The project product of a workspace path, logical or under _users/<owner>/. */
export function projectProductForPath(path: string | undefined): ProjectProductConfig | null {
  const clean = (path || '').replace(/^\/+/, '').replace(/^_users\/[^/]+\//, '')
  for (const product of [CREW_PRODUCT, CODE_PRODUCT]) {
    if (clean.startsWith(`${product.projectsRoot}/`) && clean.length > product.projectsRoot.length + 1) return product
  }
  return null
}

const ProjectProductContext = createContext<ProjectProductConfig>(CREW_PRODUCT)

export const ProjectProductProvider = ProjectProductContext.Provider

export function useProjectProduct(): ProjectProductConfig {
  return useContext(ProjectProductContext)
}
