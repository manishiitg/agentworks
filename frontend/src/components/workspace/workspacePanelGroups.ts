const GROUPS = [
  ['Work & results', ['context', 'report', 'dashboard', 'flow', 'plan']],
  ['Scheduled work', ['schedules', 'workshop', 'webhooks']],
  ['Reviews & decisions', ['goals', 'human-actions', 'suggestions']],
  ['Knowledge & tools', ['knowledge', 'memory', 'browser', 'files', 'database', 'shell']],
  ['Activity', ['costs', 'execution-logs']],
  ['Project setup', ['identity', 'mcp', 'access', 'backup', 'publish', 'notify', 'playbooks']],
] as const

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
  mcp: 'Connections Use & share sharing people channels Integrations MCP apps tools secrets tools & secrets Apps & tools MCPs & secrets', memory: 'Memory skills',
  workshop: 'Automation schedules webhooks watchers bots chats', schedules: 'Automation',
  goals: 'Goals metrics progress', 'execution-logs': 'Execution logs run history',
}
