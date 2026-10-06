import { MarkerType } from '@xyflow/react'
import type { ScheduledJob } from '../../../services/api-types'
import type { WorkflowNode, WorkflowEdge, RoutingStepNodeData } from '../hooks/usePlanToFlow'

export const TRIGGER_CARD_WIDTH = 288
export const TRIGGER_CARD_HEIGHT = 236
export const triggerNodeID = (id: string) => `workflow-trigger-${id}`

export function triggerRouteSummary(job: ScheduledJob, nodes: WorkflowNode[]) {
  if (job.pulse_review_only) return { label: 'Pulse review · does not run plan steps', canTrace: false }
  if (job.workshop_mode === 'optimizer') return { label: 'Optimizer · workflow maintenance', canTrace: false }
  if (job.step_id) {
    const node = nodes.find(node => (node.data.step as { id?: string } | undefined)?.id === job.step_id)
    return { label: `Step only: ${node?.data.title || job.step_id}${node ? '' : ' (unavailable)'}`, canTrace: !!node }
  }
  const choices = Object.entries(job.route_selections || {})
  if (!choices.length) return { label: 'Full workflow · routes chosen during execution', canTrace: true }
  let missing = false
  const labels = choices.map(([stepID, routeID]) => {
    const node = nodes.find(node => (node.data.step as { id?: string } | undefined)?.id === stepID && (node.type === 'routing' || node.type === 'branch'))
    const route = (node?.data as RoutingStepNodeData | undefined)?.routes?.find(route => route.route_id === routeID)
    if (!route) missing = true
    return route ? `${node!.data.title}: ${route.route_name || route.route_id}` : `${stepID}: ${routeID} (unavailable)`
  })
  return { label: labels.join(' · '), canTrace: !missing }
}

// Presentation nodes are added after saved plan positions are restored. They
// never become editable steps or persisted custom layout positions.
export function appendTriggerCards(nodes: WorkflowNode[], edges: WorkflowEdge[], jobs: ScheduledJob[], options: {
  relayMode?: boolean; loading: boolean; error?: string; selectedID?: string; onSelect: (id: string) => void; onSettings: (kind: 'schedules' | 'webhooks') => void; onRefresh: () => void
}) {
  const start = nodes.find(node => node.id === 'start')
  if (!start) return { nodes, edges }
  const columns = Math.max(1, jobs.length)
  const rows = Math.max(1, Math.ceil(jobs.length / columns))
  const left = start.position.x + 48 - (columns * (TRIGGER_CARD_WIDTH + 24) - 24) / 2
  const top = Math.min(...nodes.map(node => node.position.y)) - (jobs.length ? rows * (TRIGGER_CARD_HEIGHT + 24) + 100 : 40)
  const cards: WorkflowNode[] = jobs.map((job, index) => ({
    id: triggerNodeID(job.id), type: 'workflow-trigger', draggable: false, selectable: false,
    position: { x: left + index % columns * (TRIGGER_CARD_WIDTH + 24), y: top + Math.floor(index / columns) * (TRIGGER_CARD_HEIGHT + 24) },
    width: TRIGGER_CARD_WIDTH, height: TRIGGER_CARD_HEIGHT,
    // These fixed-size presentation nodes are not stored in useNodesState.
    // Preserve their measured size so reconciliation retains handle bounds.
    measured: { width: TRIGGER_CARD_WIDTH, height: TRIGGER_CARD_HEIGHT },
    data: { relayMode: options.relayMode, id: triggerNodeID(job.id), title: job.name, job, routeSummary: triggerRouteSummary(job, nodes), active: options.selectedID === job.id, onSelect: () => options.onSelect(job.id), onSettings: options.onSettings },
  }))
  cards.unshift({ id: 'workflow-trigger-heading', type: 'workflow-trigger-heading', draggable: false, selectable: false,
    position: { x: left, y: top - 90 }, width: columns * (TRIGGER_CARD_WIDTH + 24) - 24, height: 64,
    data: { id: 'workflow-trigger-heading', title: 'Triggers', loading: options.loading, error: options.error, count: jobs.length, onSettings: options.onSettings, onRefresh: options.onRefresh },
  })
  const links: WorkflowEdge[] = jobs.filter(job => triggerRouteSummary(job, nodes).canTrace).flatMap(job => {
    const active = options.selectedID === job.id
    const color = active ? '#38bdf8' : '#64748b'
    const style = { stroke: color, strokeWidth: active ? 1.6 : 1, opacity: active ? 0.9 : job.enabled ? 0.35 : 0.2 }
    const labelStyle = { fill: 'hsl(var(--muted-foreground))', fontSize: 11 }
    const labelBgStyle = { fill: 'hsl(var(--background))', fillOpacity: 0.9 }
    const markerEnd = active ? { type: MarkerType.ArrowClosed, color, width: 12, height: 12 } : undefined
    if (job.step_id) {
      const target = nodes.find(node => (node.data.step as { id?: string } | undefined)?.id === job.step_id)!
      return [{ id: `trigger-entry-${job.id}`, source: triggerNodeID(job.id), target: target.id, type: 'smoothstep', label: active ? 'Step only' : undefined, ariaLabel: `${job.name}: Step only`, style, labelStyle, labelBgStyle, markerEnd } as WorkflowEdge]
    }
    const choices = Object.entries(job.route_selections || {})
    const entry: WorkflowEdge = {
      id: `trigger-entry-${job.id}`, source: triggerNodeID(job.id), target: 'start', targetHandle: 'trigger-input', type: 'smoothstep',
      label: active ? choices.length ? 'Starts workflow' : 'Full workflow' : undefined,
      ariaLabel: `${job.name}: ${choices.length ? 'Starts workflow' : 'Full workflow'}`,
      style, labelStyle, labelBgStyle, labelBgBorderRadius: 4, markerEnd,
    }
    // These dashed links describe saved route choices, not execution shortcuts.
    // The entry link and trace still retain all prerequisites from Start.
    const routes = choices.flatMap(([stepID, routeID]) => {
      const router = nodes.find(node => (node.data.step as { id?: string } | undefined)?.id === stepID && (node.type === 'routing' || node.type === 'branch'))
      const route = (router?.data as RoutingStepNodeData | undefined)?.routes?.find(route => route.route_id === routeID)
      return edges.filter(edge => edge.source === router?.id && (edge.sourceHandle === `route-${routeID}` || edge.sourceHandle === `handoff-${routeID}`)).map(edge => ({
        id: `trigger-route-${job.id}-${edge.id}`, source: triggerNodeID(job.id), target: edge.target, targetHandle: edge.targetHandle, type: 'smoothstep',
        label: active ? `Selects: ${route?.route_name || routeID}` : undefined,
        ariaLabel: `${job.name}: Selects ${route?.route_name || routeID}`,
        style: { ...style, strokeDasharray: '3 6' },
        labelStyle, labelBgStyle, labelBgBorderRadius: 4, markerEnd,
      } as WorkflowEdge))
    })
    return [entry, ...routes]
  })
  return { nodes: [...nodes, ...cards], edges: [...edges, ...links] }
}

// Start at entry to retain prerequisites. At each saved router choice, follow
// only that choice; unresolved runtime decisions retain all possible branches.
export function traceTriggerGraph(nodes: WorkflowNode[], edges: WorkflowEdge[], job?: ScheduledJob) {
  if (!job || !triggerRouteSummary(job, nodes).canTrace) return { nodes, edges }
  const byID = new Map(nodes.map(node => [node.id, node]))
  const outgoing = new Map<string, WorkflowEdge[]>()
  for (const edge of edges) {
    if (edge.id.startsWith('dep-')) continue
    outgoing.set(edge.source, [...(outgoing.get(edge.source) || []), edge])
  }
  const selectedNodes = new Set<string>(['variables', triggerNodeID(job.id), 'workflow-trigger-heading'])
  const selectedEdges = new Set(edges.filter(edge => edge.source === triggerNodeID(job.id)).map(edge => edge.id))
  const queue = [job.step_id ? nodes.find(node => (node.data.step as { id?: string } | undefined)?.id === job.step_id)!.id : 'start']
  for (let i = 0; i < queue.length; i++) {
    const id = queue[i]
    if (selectedNodes.has(id)) continue
    selectedNodes.add(id)
    const node = byID.get(id)
    const stepID = (node?.data.step as { id?: string } | undefined)?.id
    const choice = stepID && job.route_selections?.[stepID]
    if (job.step_id) continue
    for (const edge of outgoing.get(id) || []) {
      if (choice && (node?.type === 'routing' || node?.type === 'branch') && edge.sourceHandle !== `route-${choice}` && edge.sourceHandle !== `handoff-${choice}`) continue
      selectedEdges.add(edge.id)
      queue.push(edge.target)
    }
  }
  for (const node of nodes) {
    if (typeof node.data.parentStepId === 'string' && selectedNodes.has(node.data.parentStepId)) selectedNodes.add(node.id)
  }
  return {
    nodes: nodes.map(node => ({ ...node, style: { ...node.style, opacity: selectedNodes.has(node.id) ? 1 : 0.14 } })),
    edges: edges.map(edge => ({ ...edge, style: { ...edge.style, opacity: selectedEdges.has(edge.id) && selectedNodes.has(edge.target) ? 1 : 0.08 } })),
  }
}
