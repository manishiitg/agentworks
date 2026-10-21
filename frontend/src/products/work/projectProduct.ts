import { createContext, useContext } from 'react'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

export type ProjectProductId = 'work'

export type ProjectProductConfig = {
  /** Agent profile id and product surface id. */
  profileId: ProjectProductId
  profileVersion: number
  /** Canonical project root. */
  projectsRoot: string
  sessionPrefix: string
  slugFallback: string
  /** Product name in UI copy. */
  noun: string
  /** Name of one project in UI copy. */
  itemNoun: string
  /** Agent identity (role, purpose, icon) and its setup prompts. */
  hasIdentity: boolean
  /** Crew templates, playbooks and role installs. */
  hasTemplates: boolean
  /** Existing owner working tools; independent of legacy route/storage IDs. */
  hasTerminal: boolean
  /**
   * Other owners' projects in the list: every Crew (Crew Run mode), or only
   * the Codes shared with the caller.
   */
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
  projectsRoot: 'Crew',
  sessionPrefix: 'work:project',
  slugFallback: 'workspace',
  noun: 'Crew',
  itemNoun: 'Crew',
  hasIdentity: true,
  hasTemplates: true,
  hasTerminal: true,
  listsSharedProjects: true,
  // No switch (owner decision 2026-09-29): native agent tools are always on.
  hasNativeAgentToolsSetting: false,
  defaultView: 'context',
  preferenceNamespace: 'work',
}

export function isProjectProductId(value: unknown): value is ProjectProductId {
  return value === 'work'
}

export function projectProductConfig(profileId: ProjectProductId): ProjectProductConfig {
  return CREW_PRODUCT
}

/** The project product of a workspace path, logical or under _users/<owner>/. */
export function projectProductForPath(path: string | undefined): ProjectProductConfig | null {
  const clean = (path || '').replace(/^\/+/, '')
  // A Crew at the shared root (Crew/<folder>) is a Crew project like one in its owner's tree.
  if (/^Crew\/[^/.][^/]*(\/|$)/.test(clean)) return CREW_PRODUCT
  return null
}

const ProjectProductContext = createContext<ProjectProductConfig>(CREW_PRODUCT)

export const ProjectProductProvider = ProjectProductContext.Provider

export function useProjectProduct(): ProjectProductConfig {
  return useContext(ProjectProductContext)
}
