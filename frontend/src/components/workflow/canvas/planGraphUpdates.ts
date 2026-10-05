import type { WorkflowNode, WorkflowEdge } from '../hooks/usePlanToFlow'

// Compare the generated graph, before saved positions and selection are applied.
// All node data matters: artifact, validation and learning nodes have no `step`.
export function planNodesChanged(previous: WorkflowNode[], next: WorkflowNode[]): boolean {
  return previous.length !== next.length || previous.some((node, index) => {
    const updated = next[index]
    return !updated || node.id !== updated.id || node.type !== updated.type
      || node.position.x !== updated.position.x || node.position.y !== updated.position.y
      || JSON.stringify(node.data) !== JSON.stringify(updated.data)
  })
}

export function planEdgesChanged(previous: WorkflowEdge[], next: WorkflowEdge[]): boolean {
  // A route can retain its ID and endpoints while its label, handles, selected
  // route, color or rendering type changes.
  return JSON.stringify(previous) !== JSON.stringify(next)
}
