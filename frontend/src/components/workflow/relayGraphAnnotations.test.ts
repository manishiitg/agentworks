import { expect, it } from 'vitest'
import { parseRelaySourceGraph } from './relayGraphAnnotations'
import { graphFromDBOSHistory } from './relayDBOSGraph'

it('reads real comments and branch labels while ignoring quoted examples, docstrings and inline strings', () => {
  const source = `"""
# @relay node {"id":"fake","type":"agent","label":"Not a real node"}
"""
example = '# @relay edge {"from":"fake","to":"fake"}'
# @relay node {"id":"extract","type":"agent","label":"Extract invoice","tools":["lookup"],"messages":["Extract","Verify"]}
# @relay node {"id":"review","type":"agent","label":"Review"}
# @relay node {"id":"result","type":"output","label":"Return invoice"}
# @relay edge {"from":"extract","to":"review","label":"Amount > 1000"}
# @relay edge {"from":"extract","to":"result","label":"Amount <= 1000"}
# @relay edge {"from":"review","to":"result"}
async def run(INPUT, ctx):
    return INPUT
`
  const graph = parseRelaySourceGraph(source)
  expect(graph.errors).toEqual([])
  expect(graph.nodes.map(node => node.id)).toEqual(['extract', 'review', 'result'])
  expect(graph.nodes[0]).toMatchObject({ line: 5, tools: ['lookup'], messages: ['Extract', 'Verify'] })
  expect(graph.edges.map(edge => edge.label)).toEqual(['Amount > 1000', 'Amount <= 1000', undefined])
})

it('reports malformed, duplicate and dangling annotations without executing or accepting a partial graph', () => {
  const graph = parseRelaySourceGraph(`# @relay node {"id":"one","type":"agent","label":"One"}
# @relay node {"id":"one","type":"agent","label":"Duplicate"}
# @relay edge {"from":"one","to":"missing"}
# @relay node not-json
# @relay node {"id":"invalid","type":"agent","label":"Bad","tools":"not a list"}
raise RuntimeError("Never execute this")`)
  expect(graph.errors).toHaveLength(4)
  expect(graph.errors.join('\n')).toContain('Duplicate node id')
  expect(graph.errors.join('\n')).toContain('unknown node')
  expect(graph.errors.join('\n')).toContain('tools must be a list')
})

it('keeps a recorded classifier separate from agents despite having a provider and model', () => {
  const graph = graphFromDBOSHistory([
    { id: 'step-1', name: 'choose_team', type: 'classifier', status: 'completed', provider: 'cloudflare', model: 'clef-flash', categories: { billing: 'Payments' }, instructions: 'Choose a team' },
    { id: 'step-2', name: 'billing_reply', status: 'completed', provider: 'provider', model: 'model' },
  ])
  expect(graph.nodes[1]).toMatchObject({ type: 'classifier', model: 'clef-flash', categories: { billing: 'Payments' }, instructions: 'Choose a team' })
  expect(graph.nodes[2].type).toBe('agent')
})

it('shows the observed sequence including repeated steps and never marks a failed run as a returned result', () => {
  const calls = [
    { id: 'step-1', name: 'reply', status: 'completed', provider: 'provider' },
    { id: 'step-2', name: 'reply', status: 'failed', provider: 'provider' },
  ]
  const graph = graphFromDBOSHistory(calls, { status: 'failed', resultRecorded: false })
  expect(graph.recorded).toBe(true)
  expect(graph.nodes.slice(1, -1).map(node => [node.id, node.call, node.sequence, node.status])).toEqual([
    ['history-0', 'step-1', 1, 'completed'], ['history-1', 'step-2', 2, 'failed'],
  ])
  expect(graph.nodes.at(-1)).toMatchObject({ label: 'Run stopped', status: 'failed' })
  expect(graphFromDBOSHistory(calls.slice(0, 1), { status: 'running', resultRecorded: false }).nodes.at(-1)).toMatchObject({ label: 'Waiting for result', status: undefined })
  expect(graphFromDBOSHistory(calls.slice(0, 1), { status: 'completed', resultRecorded: false }).nodes.at(-1)).toMatchObject({ label: 'Finished without a result', status: undefined })
  expect(graphFromDBOSHistory(calls.slice(0, 1), { status: 'completed', resultRecorded: true }).nodes.at(-1)).toMatchObject({ label: 'Final result', status: 'completed' })
})
