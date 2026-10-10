import type { RelaySourceGraph } from './relayGraphAnnotations'
import type { RelayRecordedCall } from './RelaySourceGraph'

/** Observed order from DBOS history, including repeated calls as distinct nodes. */
export function graphFromDBOSHistory(calls: RelayRecordedCall[]): RelaySourceGraph {
  const nodes: RelaySourceGraph['nodes'] = [{ id: 'workflow-input', type: 'input', label: 'Receive input', line: 0 }]
  calls.forEach((call, index) => nodes.push({ id: `history-${index}`, call: call.id, type: call.provider ? 'agent' : 'script', label: call.name, line: 0 }))
  nodes.push({ id: 'workflow-result', type: 'output', label: 'Return result', line: 0 })
  return { native: true, nodes, edges: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, line: 0 })), errors: [] }
}
