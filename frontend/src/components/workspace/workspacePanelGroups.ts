const GROUPS = [
  ['Work & results', ['context', 'report', 'dashboard', 'flow', 'plan']],
  ['Scheduled work', ['schedules', 'workshop', 'webhooks']],
  ['Reviews & decisions', ['pulse', 'human-actions', 'suggestions']],
  ['Knowledge & tools', ['knowledge', 'memory', 'browser', 'files', 'database', 'shell']],
  ['Activity', ['costs', 'execution-logs']],
  ['Project setup', ['identity', 'mcp', 'access', 'backup', 'publish', 'notify', 'playbooks']],
] as const

export function workspacePanelGroup(id: string): string {
  return GROUPS.find(([, ids]) => (ids as readonly string[]).includes(id))?.[0] ?? 'Other tools'
}

/** Group only the inventory already admitted by each product's permission gates. */
export function groupWorkspacePanels<T extends { id: string }>(panels: readonly T[]) {
  const known = new Set<string>(GROUPS.flatMap(([, ids]) => [...ids]))
  return [
    ...GROUPS.map(([title, ids]) => ({ title, items: panels.filter(panel => (ids as readonly string[]).includes(panel.id)) })),
    { title: 'Other tools', items: panels.filter(panel => !known.has(panel.id)) },
  ].filter(group => group.items.length)
}

/** Keep familiar/technical names searchable after making visible labels clearer. */
export const panelSearchAliases: Record<string, string> = {
  context: 'Context overview', identity: 'Identity settings purpose instructions',
  mcp: 'Integrations connections MCP apps secrets', memory: 'Memory skills',
  workshop: 'Automation schedules webhooks watchers bots chats', schedules: 'Automation',
  pulse: 'Pulse automatic reviews', 'execution-logs': 'Execution logs run history',
}
