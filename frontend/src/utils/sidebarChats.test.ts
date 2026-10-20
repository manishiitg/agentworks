import { expect, it } from 'vitest'
import { sidebarChats, sidebarScope, sidebarWorkspaceGroups, sidebarChatDisplayTitle, type SidebarChat } from './sidebarChats'
import type { ChatTab } from '../stores/useChatStore'
import type { ActiveSessionInfo } from '../services/api-types'
import type { WorkflowBuilderSessionResponse } from '../services/api-types'

it('groups registered project aliases by server identity, drops absent projects and keeps equal-name projects distinct', () => {
  const legacy = 'Chats/Code/projects/private-old'
  const canonical = 'Crew/private-current'
  const tabs = {
    main: tab('main', { name: 'Main', metadata: { agentProfileId: 'code', agentProfileProjectId: 'private-id', agentProfileWorkspace: legacy } }),
    schedule: tab('schedule-run', { name: 'Daily wrap-up', metadata: { agentProfileId: 'work', agentProfileProjectId: 'legacy-id', agentProfileWorkspace: canonical, isScheduledRun: true } }),
    other: tab('other', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'other-id', agentProfileWorkspace: 'Crew/other' } }),
    ghost: tab('ghost', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'deleted-test', agentProfileWorkspace: 'Crew/deleted-test' } }),
  }
  const rows = sidebarChats({ tabs, active: [], recent: [], presets: [], allowed: ['code', 'work'], projectInventoryKnown: true,
    projects: [
      { id: 'private-id', workspaceId: 'code:private-id', surface: 'code', title: 'Private Crew', workspacePath: canonical, aliases: [legacy] },
      { id: 'other-id', workspaceId: 'work:other-id', surface: 'work', title: 'Private Crew', workspacePath: 'Crew/other' },
    ],
  })
  expect(rows.map(row => row.id).sort()).toEqual(['main', 'other', 'schedule-run'])
  const groups = sidebarWorkspaceGroups(rows)
  expect(groups).toHaveLength(2)
  expect(groups.find(group => group.id === 'code:private-id')?.chats.map(row => row.id)).toEqual(['main', 'schedule-run'])
  expect(groups.find(group => group.id === 'work:other-id')?.chats).toHaveLength(1)
  expect(tabs.schedule.metadata?.agentProfileProjectId).toBe('legacy-id')
})

it('does not treat inventory failure as revocation or canonical aliases as a product grant', () => {
  const tabs = { one: tab('one', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'project', agentProfileWorkspace: 'Crew/project' } }) }
  const base = { tabs, active: [], recent: [], presets: [], allowed: ['work' as const] }
  expect(sidebarChats({ ...base, projectInventoryKnown: false, projects: [] })).toHaveLength(1)
  expect(sidebarChats({ ...base, projectInventoryKnown: true, projects: [] })).toHaveLength(0)
  expect(sidebarChats({ ...base, projectInventoryKnown: true, projects: [
    { id: 'project', workspaceId: 'code:project', surface: 'code', title: 'Private', workspacePath: 'Crew/project' },
  ] })).toHaveLength(0)
})
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

it('shows only the server current Goal Builder, groups path aliases and preserves run lanes and stored history', () => {
  const preset = { id: 'linkedin', label: 'LinkedIn', selectedFolder: { filepath: 'Workflow/linkedin' } } as never
  const builderMeta = { mode: 'workflow' as const, phaseId: 'workflow-builder', presetQueryId: 'linkedin' }
  const tabs = {
    current: tab('current', { metadata: { ...builderMeta, agentProfileProjectId: 'stale-project-id' } }),
    old: tab('old', { lastAccessedAt: 999, metadata: { ...builderMeta, agentProfileWorkspace: '/_users/alice/Workflow/linkedin/' } }),
    pulse: tab('schedule-goallead--pulse_123', { metadata: { mode: 'workflow', presetQueryId: 'linkedin', isViewOnly: true, isScheduledRun: true } }),
    run: tab('run', { metadata: { mode: 'workflow', presetQueryId: 'linkedin', isViewOnly: true, isExecutionRun: true } }),
  }
  const recent = [{ session_id: 'old', workspace_path: 'Workflow/linkedin', message_count: 8 },
    { session_id: 'older', workspace_path: '/_users/alice/Workflow/linkedin/', message_count: 8 }]
  const input = { tabs, active: [], recent, presets: [preset], allowed: ['agentworks' as const],
    builders: { linkedin: { success: true, events: [], source: 'live', session_id: 'current', status: 'running', sessions: {
      current: { is_builder: true, kind: 'builder' }, old: { is_builder: false, kind: 'history' }, older: { is_builder: false, kind: 'history' },
      'schedule-goallead--pulse_123': { is_builder: false, kind: 'run' }, run: { is_builder: false, kind: 'run' },
    } } as WorkflowBuilderSessionResponse } }
  const rows = sidebarChats(input)
  expect(rows.map(row => row.id).sort()).toEqual(['current', 'run', 'schedule-goallead--pulse_123'].sort())
  expect(sidebarWorkspaceGroups(rows)).toHaveLength(1)
  expect(sidebarWorkspaceGroups(rows)[0].id).toBe('agentworks:linkedin')
  expect(tabs.old.sessionId).toBe('old')
  expect(recent).toHaveLength(2)
  expect(sidebarChats({ ...input, allowedWorkflowIds: [] })).toEqual([])
})

it('uses the same one-Builder rule for Flows while leaving named Crew chats alone', () => {
  const preset = { id: 'flow', workflowKind: 'relay', label: 'API', selectedFolder: { filepath: 'Workflow/api' } } as never
  const rows = sidebarChats({
    tabs: {
      old: tab('old', { metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'flow' } }),
      first: tab('first', { name: 'Sales', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew' } }),
      second: tab('second', { name: 'Research', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew' } }),
    }, active: [], recent: [], presets: [preset], allowed: ['relays', 'work'],
    builders: { flow: { success: true, events: [], source: 'workspace', session_id: 'current', status: 'completed', sessions: {
      current: { is_builder: true, kind: 'builder' }, old: { is_builder: false, kind: 'history' },
    } } as WorkflowBuilderSessionResponse },
  })
  expect(rows.map(row => row.id).sort()).toEqual(['current', 'first', 'second'])
  expect(rows.find(row => row.id === 'current')?.surface).toBe('relays')
  expect(rows.find(row => row.id === 'current')?.workflowId).toBe('flow')
})

it('drops saved Builder tabs of a deleted workflow instead of grouping them as "Automation Builder"', () => {
  const preset = { id: 'live', label: 'Live', selectedFolder: { filepath: 'Workflow/live' } } as never
  const meta = (presetQueryId: string) => ({ mode: 'workflow' as const, phaseId: 'workflow-builder', presetQueryId })
  const input = {
    tabs: {
      current: tab('current', { name: 'Automation Builder', metadata: meta('live') }),
      gone: tab('gone', { name: 'Automation Builder', metadata: meta('deleted') }),
    },
    active: [], recent: [], presets: [preset], allowed: ['agentworks' as const],
    builders: { live: { success: true, events: [], source: 'live', session_id: 'current', status: 'completed', sessions: {
      current: { is_builder: true, kind: 'builder' },
    } } as WorkflowBuilderSessionResponse },
  }
  expect(sidebarChats(input).map(row => row.id)).toEqual(['current'])
  // Before the workflow list loads, nothing is treated as deleted.
  expect(sidebarChats({ ...input, presets: [], builders: {} }).map(row => row.id).sort()).toEqual(['current', 'gone'])
})

it('uses readable display labels while preserving custom titles and original session data', () => {
  const row = { id: 'schedule-cron--workflow_123', title: 'WORKFLOW REVIEW BEFORE A RUN. workspace_path="Workflow/trading", pulse_run_id="one"', surface: 'agentworks', group: 'Open', time: 1, waiting: false, scheduled: true } as SidebarChat
  expect(sidebarChatDisplayTitle(row, 'Trading')).toBe('Scheduled review')
  expect(row.title).toContain('workspace_path=')
  expect(sidebarChatDisplayTitle({ ...row, title: 'Pulse' })).toBe('Automatic review')
  expect(sidebarChatDisplayTitle({ ...row, title: 'Morning review (Mon-Fri 8 AM)' })).toBe('Morning review')
  expect(sidebarChatDisplayTitle({ ...row, scheduled: false, builder: true, title: 'Trading' }, 'Trading', 2)).toBe('Builder 2')
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

it('renders server main role and saved tab positions, never creation or activity order', () => {
  const side = (id: string, position: number, createdAt: number) => tab(id, { createdAt, metadata: {
    agentProfileId: 'work', agentProfileProjectId: 'project', agentProfileConversationKey: `project:chat:${id}`,
    agentProfileTabPosition: position,
  } })
  const tabs = {
    main: tab('main', { createdAt: 999, metadata: { agentProfileId: 'work', agentProfileProjectId: 'project',
      agentProfileConversationKey: 'project:chat:physical-main', agentProfileIsMainChat: true } }),
    second: side('second', 2, 1), first: side('first', 1, 2),
    z: side('z', 0, 3), a: side('a', 0, 4),
  }
  const order = () => sidebarWorkspaceGroups(sidebarChats({ tabs, active: [], recent: [], presets: [], allowed: ['work'] }))[0].chats.map(row => row.id)
  expect(order()).toEqual(['main', 'first', 'second', 'a', 'z'])
  tabs.second.lastAccessedAt = 999999
  tabs.second.isStreaming = true
  expect(order()).toEqual(['main', 'first', 'second', 'a', 'z'])
  tabs.second.metadata!.agentProfileTabPosition = 1
  tabs.first.metadata!.agentProfileTabPosition = 2
  expect(order()).toEqual(['main', 'second', 'first', 'a', 'z'])
})

it('puts running projects first and uses latest server activity within each group', () => {
  const project = (id: string, title: string) => tab(id, { createdAt: Date.parse('2026-10-20T12:00:00Z'), metadata: {
    agentProfileId: 'work', agentProfileProjectId: id, agentProfileProjectTitle: title,
  } })
  const tabs = {
    runningOld: project('running-old', 'Alpha'), runningNew: project('running-new', 'Zulu'),
    idleNew: project('idle-new', 'Beta'), idleOld: project('idle-old', 'Charlie'),
  }
  const active = [
    activity('running-old', { last_activity: '2026-10-20T09:00:00Z' }),
    activity('running-new', { last_activity: '2026-10-20T10:00:00Z' }),
  ]
  const input = { tabs, active, recent: [
    { session_id: 'idle-new', updated_at: '2026-10-20T11:00:00Z' },
    { session_id: 'idle-old', updated_at: '2026-10-20T08:00:00Z' },
  ], presets: [], allowed: ['work' as const] }
  const order = () => sidebarWorkspaceGroups(sidebarChats(input)).map(group => group.id)
  expect(order()).toEqual(['work:running-new', 'work:running-old', 'work:idle-new', 'work:idle-old'])

  // Merely opening an older chat must not make it look newly active, even
  // when its local tab was restored after all of the server timestamps.
  tabs.idleOld.lastAccessedAt = Date.parse('2026-10-20T13:00:00Z')
  expect(order()).toEqual(['work:running-new', 'work:running-old', 'work:idle-new', 'work:idle-old'])

  active[1].last_activity = '2026-10-20T08:30:00Z'
  expect(order().slice(0, 2)).toEqual(['work:running-old', 'work:running-new'])
  active[0].status = 'completed'
  expect(order()).toEqual(['work:running-new', 'work:idle-new', 'work:running-old', 'work:idle-old'])
})

it('uses activity in any child chat to rank its project while preserving saved chat order', () => {
  const rows = sidebarChats({ tabs: {
    main: tab('main', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: true } }),
    side: tab('side', { metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileTabPosition: 1 } }),
    other: tab('other', { metadata: { agentProfileId: 'code', agentProfileProjectId: 'other' } }),
  }, active: [
    activity('side', { last_activity: '2026-10-20T10:00:00Z' }),
    activity('other', { last_activity: '2026-10-20T09:00:00Z' }),
  ], recent: [], presets: [], allowed: ['work', 'code'] })
  const groups = sidebarWorkspaceGroups(rows)
  expect(groups.map(group => group.id)).toEqual(['work:crew', 'code:other'])
  expect(groups[0].chats.map(row => row.id)).toEqual(['main', 'side'])
})

it('does not rank a Builder lookup or a restored tab as fresh conversation activity', () => {
  const presets = [
    { id: 'credit', label: 'Credit Card', selectedFolder: { filepath: 'Workflow/credit' } },
    { id: 'trading', label: 'Trading', selectedFolder: { filepath: 'Workflow/trading' } },
    { id: 'unknown', label: 'Unknown', selectedFolder: { filepath: 'Workflow/unknown' } },
  ] as never
  const builder = (id: string, activity?: string) => ({ success: true, session_id: id, status: 'completed',
    updated_at: '2026-10-20T12:00:00Z', activity_updated_at: activity,
    sessions: { [id]: { is_builder: true, kind: 'builder' } },
  }) as WorkflowBuilderSessionResponse
  const input = { tabs: {
    credit: tab('credit-chat', { createdAt: Date.parse('2026-10-20T13:00:00Z'), metadata: { presetQueryId: 'credit' } }),
  }, active: [], recent: [], presets, allowed: ['agentworks' as const], builders: {
    credit: builder('credit-chat', '2026-10-18T09:00:00Z'),
    trading: builder('trading-chat', '2026-10-20T10:00:00Z'),
    unknown: builder('unknown-chat'),
  } }
  expect(sidebarWorkspaceGroups(sidebarChats(input)).map(group => group.id)).toEqual(['agentworks:trading', 'agentworks:credit', 'agentworks:unknown'])

  // Older backends have no activity_updated_at. Their generated lookup time
  // must not make an old chat appear newer than known real activity either.
  input.builders.credit = builder('credit-chat')
  expect(sidebarWorkspaceGroups(sidebarChats(input))[0].id).toBe('agentworks:trading')
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
