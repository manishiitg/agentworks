import type { ActiveSessionInfo, ChatHistorySession, ScheduledJob } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import type { ProductSurface } from '../products/productSurfaceConfig'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import {
  sessionRuntimeStatus,
  runtimeHasBackgroundAgents,
  runtimeNeedsUserInput,
} from './runtimeActivity'
import { isInternalChildSession } from './workflowSessionKinds'
import { chatHistorySessionTitle } from './chatHistoryTitle'
import { pulseTurnDisplayText } from './chatMessageContent'
import { botPlatformLabel } from './globalActivityPresentation'
import { readableGeneratedChatTitle } from './generatedChatTitle'

export type SidebarProject = {
  id: string
  workspacePath: string
  title: string
  surface: 'work' | 'code'
}
const normalizedWorkspace = (path = '') =>
  path
    .replace(/^\/+/, '')
    .replace(/^_users\/[^/]+\//, '')
    .replace(/\/+$/, '')

export type SidebarChat = {
  id: string
  title: string
  workspace?: string
  workspaceTitle?: string
  createdTime?: number
  originLabel?: string
  builder?: boolean
  surface: ProductSurface
  group: 'Running' | 'Open' | 'Recent'
  time: number
  tabId?: string
  activity?: ActiveSessionInfo
  history?: ChatHistorySession
  waiting: boolean
  scheduled?: boolean
  runTime?: number
  projectId?: string
  workflowId?: string
}

/** Display copy only. Stored titles, routing IDs and scheduler names remain intact. */
export function isGeneratedSidebarPrompt(title: string) {
  return readableGeneratedChatTitle(title) !== null
}
export function sidebarChatDisplayTitle(row: SidebarChat, workspaceTitle?: string, number?: number) {
  const title = row.title.trim()
  const generated = readableGeneratedChatTitle(title, row.scheduled)
  if (generated) return generated
  if (row.scheduled && /^schedule-manual--manual-p_/i.test(row.id) && /^Pulse\b/i.test(title)) return 'Manual review'
  if (row.scheduled && /^Pulse(?:\s*[·:].*)?$/i.test(title)) return title.replace(/^Pulse/i, 'Automatic review')
  const raw = title.replace(/ · Scheduled run$/i, '').replace(row.scheduled ? /\s*\((?:Mon|Tue|Wed|Thu|Fri|Sat|Sun|Daily|Weekdays|Weekends|Every)\b[^)]*\)$/i : /$^/, '')
  if (row.builder && (title === workspaceTitle || /^(?:Automation Builder|Builder(?: \d+)?|Main chat|Chat)$/i.test(title)))
    return number ? `Chat ${number}` : 'Chat'
  if (workspaceTitle && raw === workspaceTitle)
    return row.scheduled ? 'Scheduled run' : number ? `Chat ${number}` : row.originLabel ? `${row.originLabel} chat` : 'Chat'
  return raw
}
export function sidebarScope(
  sessionId: string,
  path = '',
  profile = '',
  presetId = '',
  presets: (CustomPreset | PredefinedPreset)[] = []
): { surface: ProductSurface; projectId?: string; workflowId?: string } | null {
  const project =
    sessionId.match(/^(work|code):project:([^:]+)/) ||
    path.match(/Chats\/(Work|Code)\/projects\/([^/]+)/i)
  if (project)
    return {
      surface: project[1].toLowerCase() === 'work' ? 'work' : 'code',
      projectId: project[2],
    }
  const crew = normalizedWorkspace(path).match(/^Crew\/([^/]+)(?:\/|$)/i)
  if (crew) return { surface: 'work', projectId: crew[1] }
  if (profile === 'work' || profile === 'code') return { surface: profile }
  const aliases: Record<string, ProductSurface> = {
    knowledgebase: 'knowledgebase',
    caplayer: 'mcp-gateway',
    'mcp-gateway': 'mcp-gateway',
    'video-studio': 'video-studio',
  }
  if (aliases[profile]) return { surface: aliases[profile] }
  const singleton = normalizedWorkspace(path).match(/^Chats\/(CapLayer|Knowledgebase)(?:\/|$)/i)
  if (singleton)
    return {
      surface: {
        caplayer: 'mcp-gateway',
        knowledgebase: 'knowledgebase',
      }[singleton[1].toLowerCase()] as ProductSurface,
    }
  const preset = presets.find(
    (item) => item.id === presetId || item.selectedFolder?.filepath === path
  )
  if (preset || /^Workflow\//.test(path) || presetId)
    return {
      surface: preset?.workflowKind === 'relay' ? 'relays' : 'agentworks',
      workflowId: preset?.id || presetId,
    }
  return null
}
export function sidebarChats({
  tabs,
  active,
  recent,
  presets,
  allowed,
  projects = [],
  allowedWorkflowIds,
  jobs = [],
}: {
  tabs: Record<string, ChatTab>
  active: ActiveSessionInfo[]
  recent: ChatHistorySession[]
  presets: (CustomPreset | PredefinedPreset)[]
  allowed: ProductSurface[]
  projects?: SidebarProject[]
  allowedWorkflowIds?: string[] | null
  jobs?: ScheduledJob[]
}): SidebarChat[] {
  const rows = new Map<string, SidebarChat>()
  const accepts = (row: SidebarChat) =>
    allowed.includes(row.surface) &&
    (!allowedWorkflowIds ||
      !['agentworks', 'relays'].includes(row.surface) ||
      (!!row.workflowId && allowedWorkflowIds.includes(row.workflowId)))
  for (const tab of Object.values(tabs)) {
    const meta = tab.metadata
    const scope = sidebarScope(
      tab.sessionId || '',
      meta?.agentProfileWorkspace || '',
      meta?.agentProfileId,
      meta?.presetQueryId,
      presets
    )
    if (
      !scope ||
      isInternalChildSession({
        parentSessionId: (meta as { parentSessionId?: string })?.parentSessionId,
      })
    )
      continue
    const id = tab.sessionId || tab.tabId
    const row: SidebarChat = {
      ...scope,
      projectId: meta?.agentProfileProjectId || scope.projectId,
      id,
      tabId: tab.tabId,
      title:
        meta?.scheduledJobName?.trim() ||
        (meta?.agentProfileProjectId &&
        meta?.agentProfileConversationKey === meta.agentProfileProjectId
          ? meta?.agentProfileIdentityName ||
            projects.find(
              (project) =>
                project.id === meta?.agentProfileProjectId && project.surface === scope.surface
            )?.title ||
            meta?.agentProfileProjectTitle ||
            tab.name
          : tab.name),
      workspace: meta?.agentProfileWorkspace,
      createdTime: tab.createdAt,
      originLabel: botPlatformLabel(meta?.botPlatform, id),
      builder:
        meta?.agentProfileBuilder === true ||
        (meta?.mode === 'workflow' &&
          meta.phaseId === 'workflow-builder' &&
          meta.isViewOnly !== true),
      workspaceTitle:
        presets.find((preset) => preset.id === meta?.presetQueryId)?.label ||
        meta?.agentProfileIdentityName ||
        meta?.agentProfileProjectTitle,
      group: tab.isStreaming || tab.hasRunningBgAgents ? 'Running' : 'Open',
      time: meta?.isScheduledRun ? tab.createdAt : tab.lastAccessedAt || tab.createdAt,
      waiting: false,
      scheduled: meta?.isScheduledRun,
    }
    if (/^(Automation Builder|Builder|Chat)$/i.test(row.title))
      row.title =
        presets.find(
          (preset) =>
            preset.id === meta?.presetQueryId || preset.selectedFolder?.filepath === row.workspace
        )?.label ||
        meta?.agentProfileProjectTitle ||
        row.workspace?.split('/').filter(Boolean).pop() ||
        row.title
    if (accepts(row) && (!rows.has(id) || tab.lastAccessedAt! > rows.get(id)!.time))
      rows.set(id, row)
  }
  for (const session of active) {
    if (
      isInternalChildSession({
        parentSessionId: session.parent_session_id,
        sessionKind: session.session_kind,
      })
    )
      continue
    const busy =
      sessionRuntimeStatus(session) === 'busy' ||
      runtimeHasBackgroundAgents(session) ||
      runtimeNeedsUserInput(session)
    const existing = rows.get(session.session_id)
    // A stopped/idle server record overrides stale local streaming state.
    if (existing) {
      existing.group = busy ? 'Running' : 'Open'
      existing.originLabel =
        botPlatformLabel(session.bot_platform, session.session_id) || existing.originLabel
      existing.activity = session
      existing.waiting = runtimeNeedsUserInput(session)
      if (existing.scheduled && session.created_at)
        existing.runTime = Date.parse(session.created_at) || undefined
      continue
    }
    if (!busy) continue
    const project = projects.find(
      (item) =>
        session.workspace_path &&
        normalizedWorkspace(item.workspacePath) === normalizedWorkspace(session.workspace_path)
    )
    const scope = project
      ? { surface: project.surface, projectId: project.id }
      : sidebarScope(
          session.session_id,
          session.workspace_path,
          '',
          session.preset_query_id,
          presets
        )
    if (!scope) continue
    const row: SidebarChat = {
      ...scope,
      id: session.session_id,
      title:
        session.title ||
        session.preset_name ||
        session.workflow_label ||
        session.workflow_name ||
        session.query ||
        'Chat',
      workspace: session.workspace_path,
      workspaceTitle:
        project?.title || session.workflow_label || session.workflow_name || session.preset_name,
      createdTime: Date.parse(session.created_at) || 0,
      originLabel: botPlatformLabel(session.bot_platform, session.session_id),
      group: 'Running',
      time: Date.parse(session.last_activity || session.created_at) || 0,
      activity: session,
      waiting: runtimeNeedsUserInput(session),
    }
    if (accepts(row)) rows.set(row.id, row)
  }
  for (const session of recent) {
    const existing = rows.get(session.session_id)
    if (existing) {
      if (existing.scheduled && session.created_at)
        existing.runTime = Date.parse(session.created_at) || existing.runTime
      continue
    }
    const workspace = session.workspace_path || session.runtime?.workspace_path
    const project = projects.find(
      (item) => normalizedWorkspace(item.workspacePath) === normalizedWorkspace(workspace)
    )
    const scope = project
      ? { surface: project.surface, projectId: project.id }
      : sidebarScope(session.session_id, workspace, '', '', presets)
    if (!scope) continue
    const generatedPrompt =
      !session.title?.trim() &&
      /^(?:\[AGENTWORKS|PULSE RUN CONTEXT|SCHEDULED (?:RUN|WORKFLOW)|FULL.RUN COMPLETION)/i.test(
        session.query?.trim() || ''
      )
    const workspaceTitle =
      project?.title ||
      presets.find((preset) => preset.selectedFolder?.filepath === workspace)?.label ||
      workspace?.split('/').filter(Boolean).pop() ||
      'Chat'
    const row: SidebarChat = {
      ...scope,
      id: session.session_id,
      title: generatedPrompt
        ? `${workspaceTitle} · ${/^schedule-|^sched_/.test(session.session_id) ? 'Scheduled run' : /^PULSE/i.test(session.query || '') ? 'Review' : 'Chat'}`
        : chatHistorySessionTitle(session, 80),
      workspace,
      workspaceTitle,
      createdTime: Date.parse(session.created_at || '') || 0,
      originLabel: botPlatformLabel(session.bot_platform, session.session_id),
      group: 'Recent',
      time: Date.parse(session.updated_at || session.created_at || '') || 0,
      scheduled: /^schedule-|^sched_/.test(session.session_id),
      runTime: Date.parse(session.created_at || '') || undefined,
      history: { ...session, workspace_path: workspace },
      waiting: false,
    }
    if (accepts(row)) rows.set(row.id, row)
  }
  // The scheduler encodes the first eight job-ID characters in run IDs.
  // Resolve only a unique scoped match; prefix collisions must never mislabel a run.
  for (const row of rows.values()) {
    const match = row.id.match(/^schedule-[^-]+--([^_]+)_(\d+)$/)
    const pulse = pulseTurnDisplayText(row.history?.query || row.activity?.query || '')
    if (/^schedule-goallead--/.test(row.id) || pulse) {
      row.title = pulse && pulse !== 'Pulse turn' ? pulse.replace(/^Pulse:/, 'Pulse ·') : 'Pulse'
      row.scheduled = true
      if (!row.runTime && match && match[2].length >= 16)
        row.runTime = Math.floor(Number(match[2]) / 1e6)
      continue
    }
    const exact = jobs.filter((job) => job.last_session_id === row.id)
    const candidates = exact.length
      ? exact
      : match
        ? jobs.filter(
            (job) =>
              job.id.slice(0, 8) === match[1] &&
              (!row.workspace ||
                normalizedWorkspace(job.workspace_path) === normalizedWorkspace(row.workspace)) &&
              (!row.workflowId ||
                job.workflow_id === row.workflowId ||
                job.preset_query_id === row.workflowId)
          )
        : []
    if (candidates.length !== 1) continue
    const job = candidates[0]
    row.scheduled = true
    if (
      /^(?:Schedule|Scheduled run)$/i.test(row.title) ||
      / · Scheduled run$/i.test(row.title) ||
      row.title === row.workspaceTitle
    )
      row.title = job.name || row.title
    if (!row.workspaceTitle) row.workspaceTitle = job.workflow_label
    if (!row.runTime && match && match[2].length >= 16)
      row.runTime = Math.floor(Number(match[2]) / 1e6)
  }
  const rank = { Running: 0, Open: 1, Recent: 2 }
  return [...rows.values()].sort(
    (a, b) => rank[a.group] - rank[b.group] || b.time - a.time || a.id.localeCompare(b.id)
  )
}

/** One workspace holds every visible chat; identical names never merge sessions. */
export function sidebarWorkspaceGroups(rows: SidebarChat[]) {
  const groups = new Map<
    string,
    { id: string; title: string; surface: ProductSurface; chats: SidebarChat[] }
  >()
  for (const row of rows) {
    if (row.group === 'Recent') continue
    const id = `${row.surface}:${row.projectId || row.workflowId || normalizedWorkspace(row.workspace) || row.surface}`
    let group = groups.get(id)
    if (!group) {
      group = {
        id,
        title: row.workspaceTitle || row.workspace?.split('/').filter(Boolean).pop() || row.title,
        surface: row.surface,
        chats: [],
      }
      groups.set(id, group)
    }
    group.chats.push(row)
  }
  for (const group of groups.values())
    group.chats.sort(
      (a, b) =>
        Number(!!b.builder) - Number(!!a.builder) ||
        (a.createdTime || 0) - (b.createdTime || 0) ||
        a.id.localeCompare(b.id)
    )
  return [...groups.values()].sort(
    (a, b) => a.title.localeCompare(b.title) || a.id.localeCompare(b.id)
  )
}
