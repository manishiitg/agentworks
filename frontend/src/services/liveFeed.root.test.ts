import { describe, expect, it, vi } from 'vitest'

// llm-config-api resolves the API base URL at import time and sits in an
// import cycle through the stores liveFeed pulls in; stub it, as other suites
// do, so this pure-function test does not depend on module init order.
vi.mock('./llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import { liveFeedWorkflowRoot } from './liveFeed'

describe('liveFeedWorkflowRoot', () => {
  it('matches the server: workflow and shared crew roots only', () => {
    expect(liveFeedWorkflowRoot('Workflow/sales/db/db.sqlite')).toBe('Workflow/sales')
    expect(liveFeedWorkflowRoot('/Crew/sde-1a2b/db/reports/index.html')).toBe('Crew/sde-1a2b')
    expect(liveFeedWorkflowRoot('Chats/Work/projects/sde')).toBeNull()
    expect(liveFeedWorkflowRoot('Crew')).toBeNull()
    expect(liveFeedWorkflowRoot(null)).toBeNull()
  })
})
