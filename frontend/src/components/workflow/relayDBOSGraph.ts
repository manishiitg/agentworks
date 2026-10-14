import type { RelaySourceGraph } from './relayGraphAnnotations'
import type { RelayRecordedCall } from './RelaySourceGraph'

/** Observed order from DBOS history, including repeated calls as distinct nodes. */
export function graphFromDBOSHistory(calls: RelayRecordedCall[], run?: { status: string; resultRecorded: boolean }): RelaySourceGraph {
  const nodes: RelaySourceGraph['nodes'] = [{ id: 'workflow-input', type: 'input', label: 'Receive input', status: 'completed', line: 0 }]
  calls.forEach((call, index) => nodes.push({ id: `history-${index}`, call: call.id,
    type: call.type === 'classifier' ? 'classifier' : call.provider ? 'agent' : 'script',
    label: call.name, sequence: index + 1, status: call.status, line: 0, model: call.model, provider: call.provider,
    categories: call.categories, instructions: call.instructions }))
  const stopped = run && ['failed', 'stopped', 'interrupted', 'reconciliation_required'].includes(run.status)
  // A completed step is not proof that the workflow produced its final result.
  nodes.push({ id: 'workflow-result', type: 'output', line: 0,
    label: stopped ? 'Run stopped' : run?.resultRecorded ? 'Final result' : run?.status === 'completed' ? 'Finished without a result' : 'Waiting for result',
    status: stopped ? run.status : run?.status === 'completed' && run.resultRecorded ? 'completed' : undefined })
  return { native: true, recorded: true, nodes, edges: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, line: 0 })), errors: [] }
}
