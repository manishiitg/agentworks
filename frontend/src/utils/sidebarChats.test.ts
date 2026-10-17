import { expect, it } from 'vitest'
import { sidebarChats, sidebarScope, sidebarWorkspaceGroups, sidebarChatDisplayTitle, type SidebarChat } from './sidebarChats'
import type { ChatTab } from '../stores/useChatStore'
import type { ActiveSessionInfo } from '../services/api-types'
const tab = (id: string, patch: Partial<ChatTab> = {}) =>
  ({
    tabId: id,
    sessionId: id,
    name: 'Chat',
    createdAt: 1,
    metadata: {
      agentProfileId: 'code',
      agentProfileProjectId: 'project',
      agentProfileProjectTitle: 'My Code',
    },
    ...patch,
  }) as ChatTab
const activity = (id: string, patch: Partial<ActiveSessionInfo> = {}) =>
  ({
    session_id: id,
    status: 'running',
    agent_mode: 'multi-agent',
    created_at: '',
    last_activity: '',
    observer_id: '',
    ...patch,
  }) as ActiveSessionInfo

it('uses readable display labels while preserving custom titles and original session data', () => {
  const row = { id: 'schedule-cron--workflow_123', title: 'WORKFLOW REVIEW BEFORE A RUN. workspace_path="Workflow/trading", pulse_run_id="one"', surface: 'agentworks', group: 'Open', time: 1, waiting: false, scheduled: true } as SidebarChat
  expect(sidebarChatDisplayTitle(row, 'Trading')).toBe('Scheduled review')
  expect(row.title).toContain('workspace_path=')
  expect(sidebarChatDisplayTitle({ ...row, title: 'Pulse' })).toBe('Automatic review')
  expect(sidebarChatDisplayTitle({ ...row, title: 'Morning review (Mon-Fri 8 AM)' })).toBe('Morning review')
  expect(sidebarChatDisplayTitle({ ...row, scheduled: false, builder: true, title: 'Trading' }, 'Trading', 2)).toBe('Chat 2')
  expect(sidebarChatDisplayTitle({ ...row, scheduled: false, builder: true, title: 'Quarterly planning' }, 'Trading')).toBe('Quarterly planning')
  expect(sidebarChatDisplayTitle({ ...row, title: 'Workflow review before a run' })).toBe('Workflow review before a run')
})
it('deduplicates running/open/recent and lets authoritative completion stop a stale spinner', () => {
  const rows = sidebarChats({
    tabs: { one: tab('one', { isStreaming: true }) },
    active: [activity('one', { status: 'completed' }), activity('code:project:two')],
    recent: [{ session_id: 'one' }, { session_id: 'code:project:two' }],
    presets: [],
    allowed: ['code'],
  })
  expect(rows).toHaveLength(2)
  expect(rows[0].id).toBe('code:project:two')
  expect(rows[0].group).toBe('Running')
  expect(rows[1].group).toBe('Open')
  expect(rows[1].tabId).toBe('one')
})
it('uses runtime workspace metadata and the directory’s real project ID for recent chats', () => {
  const rows = sidebarChats({
    tabs: {},
    active: [],
    recent: [
      {
        session_id: 'old-uuid',
        title: 'Saved chat',
        runtime: { resume_supported: false, workspace_path: '_users/alice/Chats/Code/projects/code-slug' },
      },
      { session_id: 'vault-old', runtime: { resume_supported: false, workspace_path: 'Chats/CapLayer' } },
    ],
    projects: [
      {
        surface: 'code',
        id: 'real-project-id',
        title: 'Code',
        workspacePath: 'Chats/Code/projects/code-slug',
      },
    ],
    presets: [],
    allowed: ['code', 'mcp-gateway'],
  })
  expect(rows.find((row) => row.id === 'old-uuid')?.projectId).toBe('real-project-id')
  expect(rows.find((row) => row.id === 'vault-old')?.surface).toBe('mcp-gateway')
})
it('filters disabled products and internal child activity without hiding other accessible products', () => {
  const rows = sidebarChats({
    tabs: { one: tab('one') },
    active: [
      activity('work:project:private'),
      activity('code:project:child', { parent_session_id: 'parent' }),
    ],
    recent: [],
    presets: [],
    allowed: ['code'],
  })
  expect(rows.map((row) => row.id)).toEqual(['one'])
})
it('preserves schedule names and resolves Flows from the existing workflow registry', () => {
  const preset = {
    id: 'flow',
    workflowKind: 'relay',
    label: 'API Flow',
    selectedFolder: { filepath: 'Workflow/api' },
  } as never
  expect(sidebarScope('schedule-one', 'Workflow/api', '', '', [preset])?.surface).toBe('relays')
  const rows = sidebarChats({
    tabs: { one: tab('schedule-one', { name: 'Morning run · 08:00', metadata: { presetQueryId: 'flow' } }) },
    active: [],
    recent: [],
    presets: [preset],
    allowed: ['relays'],
  })
  expect(rows[0].title).toBe('Morning run · 08:00')
})

it('does not use internal continuity prompts as chat names and honors workflow access', () => {
  const preset = { id: 'wf', label: 'Trading', selectedFolder: { filepath: 'Workflow/trading' } } as never
  const input = {
    tabs: {},
    active: [],
    recent: [
      {
        session_id: 'schedule-run',
        query: 'PULSE RUN CONTEXT. Internal instructions',
        workspace_path: 'Workflow/trading',
      },
    ],
    presets: [preset],
    allowed: ['agentworks' as const],
  }
  expect(sidebarChats({ ...input, allowedWorkflowIds: ['wf'] })[0].title).toBe('Trading · Scheduled run')
  expect(sidebarChats({ ...input, allowedWorkflowIds: [] })).toEqual([])
})

it('keeps multiple running tabs nested under their workspace and separates products with the same project ID', () => {
  const rows = sidebarChats({
    tabs: {
      one: tab('one', { name: 'Analysis', isStreaming: true }),
      two: tab('two', { name: 'Analysis', isStreaming: true }),
      crew: tab('crew', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'project', agentProfileProjectTitle: 'Company CA' } }),
    },
    active: [], recent: [], presets: [], allowed: ['code', 'work'],
  })
  const groups = sidebarWorkspaceGroups(rows)
  expect(groups).toHaveLength(2)
  expect(groups.find((group) => group.surface === 'code')?.chats.map((row) => row.id)).toEqual(['one', 'two'])
  expect(groups.find((group) => group.surface === 'code')?.chats.every((row) => row.group === 'Running')).toBe(true)
  expect(groups.find((group) => group.surface === 'work')?.title).toBe('Company CA')
})
it('uses the saved scheduled run time instead of the restored tab creation time', () => {
  const createdAt = '2026-10-15T02:00:00Z'
  const rows = sidebarChats({
    tabs: { run: tab('schedule-run', { createdAt: Date.parse('2026-10-15T16:00:00Z'), metadata: { agentProfileId: 'code', isScheduledRun: true, scheduledJobName: 'Daily report' } }) },
    active: [],
    recent: [{ session_id: 'schedule-run', created_at: createdAt }],
    presets: [], allowed: ['code'],
  })
  expect(rows).toHaveLength(1)
  expect(rows[0].title).toBe('Daily report')
  expect(rows[0].runTime).toBe(Date.parse(createdAt))
})

it('resolves real schedule names for distinct runs, refuses ambiguous prefixes and identifies Pulse', () => {
  const id = 'schedule-cron--9db4dc39_1785164105108836000'
  const job = { id: '9db4dc39-a', name: 'News Briefing', workspace_path: 'Workflow/trading', preset_query_id: 'wf' } as any
  const input = {
    tabs: { run: tab(id, { name: 'Trading · Scheduled run', metadata: { presetQueryId: 'wf', isScheduledRun: true } }), pulse: tab('schedule-goallead--pulse-fi_1785164105108836000', { name: 'Trading · Scheduled run', metadata: { presetQueryId: 'wf', isScheduledRun: true } }) },
    active: [], recent: [],
    presets: [{ id: 'wf', label: 'Trading', selectedFolder: { filepath: 'Workflow/trading' } }] as any,
    allowed: ['agentworks' as const],
  }
  const rows = sidebarChats({ ...input, jobs: [job] })
  expect(rows.find((row) => row.id === id)?.title).toBe('News Briefing')
  expect(rows.find((row) => row.id === id)?.runTime).toBe(1785164105108)
  expect(rows.find((row) => row.id.includes('goallead'))?.title).toBe('Pulse')
  expect(sidebarChats({ ...input, jobs: [job, { ...job, id: '9db4dc39-b', name: 'Different job' }] }).find((row) => row.id === id)?.title).toBe('Trading · Scheduled run')
})

it('keeps open workspace and chat order stable when access times change', () => {
  const make = (access: number) => sidebarWorkspaceGroups(sidebarChats({
    tabs: { one: tab('one', { createdAt: 1, lastAccessedAt: access }), two: tab('two', { createdAt: 2, lastAccessedAt: 1 }), crew: tab('crew', { createdAt: 3, metadata: { agentProfileId: 'work', agentProfileProjectId: 'ca', agentProfileProjectTitle: 'Accounting' } }) },
    active: [], recent: [], presets: [], allowed: ['code', 'work'],
  })).map((group) => ({ id: group.id, chats: group.chats.map((row) => row.id) }))
  expect(make(1)).toEqual(make(999999))
})
it('includes authorized Slack and WhatsApp activity under their real workspaces with source labels', () => {
  const rows = sidebarChats({ tabs: {}, active: [
    activity('bot-slack-thread', { bot_platform: 'slack', workspace_path: '_users/owner/Crew/ca', preset_query_id: 'ca' }),
    activity('bot-whatsapp-thread', { bot_platform: 'whatsapp', workspace_path: 'Workflow/trading', preset_query_id: 'wf' }),
  ], recent: [], presets: [{ id: 'wf', label: 'Trading', selectedFolder: { filepath: 'Workflow/trading' } }] as any,
    projects: [{ id: 'ca-real', surface: 'work', title: 'Company CA', workspacePath: 'Crew/ca' }], allowed: ['work', 'agentworks'] })
  expect(rows.find((row) => row.originLabel === 'Slack')?.projectId).toBe('ca-real')
  expect(rows.find((row) => row.originLabel === 'WhatsApp')?.workflowId).toBe('wf')
  expect(rows.every((row) => row.group === 'Running')).toBe(true)
})
