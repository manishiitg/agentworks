import { expect, it } from 'vitest'
import { parseRelaySourceGraph } from './relayGraphAnnotations'

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
