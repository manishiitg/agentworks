import { useMemo, useRef, useEffect } from 'react'
import type { Node, Edge } from '@xyflow/react'
import dagre from 'dagre'
import type { PlanStep, PlanningResponse, AgentLLMConfig, ValidationSchema, RoutingRoute, AgentItem } from '../../../utils/stepConfigMatching'
import { isHumanInputStep, isLegacyAgentStep, isRoutingStep, isBranchStep, isAgentStep, isRegularStep, isCrewStep, runsAsAgent, configuredAgentItems, effectiveAgentItems } from '../../../utils/stepConfigMatching'
import type { ChangeType, PlanChanges } from './usePlanData'
import type { VariablesManifest, ScheduledJob } from '../../../services/api-types'
import type { VariablesNodeData } from '../nodes/VariablesNode'
import { useActiveWorkflowPreset } from '../../../hooks/useActiveWorkflowPreset'
import { useLLMStore } from '../../../stores/useLLMStore'
import { routeColorForIndex } from '../routeColors'

const ROUTE_EDGE_LABEL_STYLE = { fill: 'hsl(var(--muted-foreground))', fontWeight: 500, fontSize: 10 }
const COMPLETION_EDGE_LABEL_STYLE = { fill: 'hsl(var(--primary))', fontWeight: 600, fontSize: 11 }
const EDGE_LABEL_BG_STYLE = { fill: 'hsl(var(--popover))', fillOpacity: 0.92 }

// Node data types for our custom nodes
export interface StepNodeData extends Record<string, unknown> {
  id: string
  title: string
  description?: string
  success_criteria?: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType  // Highlight type for visual feedback
  workspacePath?: string | null  // Workspace path for file opening
  selectedRunFolder?: string  // Selected iteration folder for file opening
  validation_schema?: ValidationSchema  // Validation schema from plan.json
  // Sub-agent specific fields
  parentOrchestratorTitle?: string  // Title of parent orchestrator node (for sub-agents)
  routeName?: string  // Route name from orchestration_routes (for sub-agents)
  routeCondition?: string  // Condition from orchestration_routes (for sub-agents)
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface LegacyAgentNodeData extends Record<string, unknown> {
  id: string
  title: string
  todo_task_step?: PlanStep  // DEPRECATED: kept for backwards compat
  predefined_routes?: Array<{ route_id: string; route_name: string; condition: string; sub_agent_step?: PlanStep; orphan_step_ref?: string }>
  enable_generic_agent?: boolean
  status: 'pending' | 'running' | 'failed' | 'executing' | 'evaluating' | 'orchestrating' | 'completed'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType  // Highlight type for visual feedback
  workspacePath?: string | null  // Workspace path for file opening
  selectedRunFolder?: string  // Selected iteration folder for file opening
  validation_schema?: ValidationSchema  // Validation schema from plan.json (now flat on step)
  parentOrchestratorTitle?: string  // Title of parent orchestrator node (for nested todo sub-agents)
  routeName?: string  // Route name from orchestration/todo routes
  routeCondition?: string  // Condition from orchestration/todo routes
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface HumanInputNodeData extends Record<string, unknown> {
  id: string
  title: string
  question?: string
  response_type?: string
  options?: string[]
  status: 'pending' | 'waiting' | 'completed'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType  // Highlight type for visual feedback
  workspacePath?: string | null  // Workspace path for file opening
  selectedRunFolder?: string  // Selected iteration folder for file opening
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface RoutingStepNodeData extends Record<string, unknown> {
  route_source?: 'human' // branch only: asks the person when nothing was preseeded
  tracedRouteId?: string
  onTraceRoute?: (routeId: string) => void
  id: string
  title: string
  routing_question?: string
  routes?: RoutingRoute[]
  status: 'pending' | 'running' | 'completed' | 'failed' | 'executing' | 'evaluating' | 'routed'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType
  workspacePath?: string | null
  selectedRunFolder?: string
  validation_schema?: ValidationSchema
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface AgentNodeData extends Record<string, unknown> {
  isRelayOutput?: boolean
  id: string
  title: string
  description?: string
  items?: AgentItem[]   // Ordered queue of user_message / prevalidation / foreach items
  predefined_routes?: Array<{ route_id: string; route_name: string; condition: string; sub_agent_step?: PlanStep; orphan_step_ref?: string }>
  status: 'pending' | 'running' | 'completed' | 'failed' | 'executing' | 'evaluating' | 'orchestrating'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType
  workspacePath?: string | null
  selectedRunFolder?: string
  validation_schema?: ValidationSchema
  enable_generic_agent?: boolean
  parentOrchestratorTitle?: string
  routeName?: string
  routeCondition?: string
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface CrewStepNodeData extends Record<string, unknown> {
  id: string
  title: string
  crew_profile_id?: string
  crew_project_id?: string
  trigger_id?: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  stepIndex: number
  step: PlanStep
  changeType?: ChangeType
  workspacePath?: string | null
  selectedRunFolder?: string
  isOrphan?: boolean  // True for orphan steps (workshop-only, not in main execution flow)
}

export interface ValidationNodeData extends Record<string, unknown> {
  id: string
  parentStepId: string
  parentStepTitle: string
  status: 'pending' | 'running' | 'passed' | 'failed'
  llmProvider?: string  // LLM provider (e.g., 'claude-code', 'codex-cli')
  llmModel?: string  // LLM model name
}

export interface LearningNodeData extends Record<string, unknown> {
  id: string
  parentStepId: string
  parentStepTitle: string
  status: 'pending' | 'running' | 'completed' | 'skipped'
  llmProvider?: string  // LLM provider (e.g., 'claude-code', 'codex-cli')
  llmModel?: string  // LLM model name
}

export interface WorkflowArtifactNodeData extends Record<string, unknown> {
  id: string
  title: string
  description?: string
  kind: 'output'
  configured: boolean
  detail?: string
}

export interface WorkflowTriggerNodeData extends Record<string, unknown> {
  relayMode?: boolean
  id: string
  title: string
  job?: ScheduledJob
  routeSummary?: { label: string; canTrace: boolean }
  active?: boolean
  loading?: boolean
  error?: string
  count?: number
  onSelect?: () => void
  onSettings?: (kind: 'schedules' | 'webhooks') => void
  onRefresh?: () => void
}

export type WorkflowNodeData = WorkflowTriggerNodeData | StepNodeData | LegacyAgentNodeData | HumanInputNodeData | RoutingStepNodeData | AgentNodeData | CrewStepNodeData | ValidationNodeData | LearningNodeData | VariablesNodeData | WorkflowArtifactNodeData

// Node and edge types
export type WorkflowNode = Node<WorkflowNodeData>
export type WorkflowEdge = Edge

interface UsePlanToFlowResult {
  nodes: WorkflowNode[]
  edges: WorkflowEdge[]
}

interface UsePlanToFlowOptions {
  showDependencyEdges?: boolean // Default: false (hide dependency edges for cleaner view)
  changes?: PlanChanges | null  // Optional: highlight changes on nodes
  completedStepIndices?: number[]  // 0-based indices of completed steps (from steps_done.json)
  workspacePath?: string | null  // Workspace path for file opening
  selectedRunFolder?: string  // Selected iteration folder for file opening
  relayMode?: boolean  // Relays show configuration without execution groups
  variablesManifest?: VariablesManifest | null  // Variables manifest for Variables node
  onOpenVariablesSidebar?: () => void  // Callback for opening variables sidebar
  isLoadingVariables?: boolean  // Whether variables are being loaded
  layoutDirection?: 'LR' | 'TB'  // Layout direction: 'LR' = horizontal, 'TB' = vertical
  disabled?: boolean  // Keep the last computed graph and skip layout work when hidden
}

// Dagre layout configuration - returns config based on layout direction
const getDagreConfig = (direction: 'LR' | 'TB') => ({
  rankdir: direction,
  // For LR: nodesep = vertical spacing, ranksep = horizontal spacing
  // For TB: nodesep = horizontal spacing, ranksep = vertical spacing
  // TB nodesep is the MINIMUM horizontal gap between sibling nodes; dagre adds
  // more automatically so each route gets room proportional to its own subtree.
  // Keep this a modest floor and let dagre drive the dynamic, per-subtree spread.
  nodesep: direction === 'LR' ? 200 : 160,
  ranksep: direction === 'LR' ? 150 : 220,
  marginx: 80,
  marginy: 80
})

// Node dimensions for layout calculation (base dimensions) - smaller since content is simplified
const NODE_DIMENSIONS = {
  step: { width: 280, height: 120 },
  routing: { width: 280, height: 200 },
  agent: { width: 320, height: 240 },
  todo_task: { width: 300, height: 120 },
  human_input: { width: 260, height: 120 },
  crew: { width: 280, height: 160 },
  loop: { width: 300, height: 140 },
  start: { width: 96, height: 40 },
  end: { width: 96, height: 40 },
  variables: { width: 220, height: 120 },
  'workflow-artifact': { width: 220, height: 120 }
}

const SUB_AGENT_LAYOUT = {
  parentGap: 72,
  cellGap: 32,
  cellWidth: Math.max(NODE_DIMENSIONS.step.width, NODE_DIMENSIONS.todo_task.width),
  cellHeight: NODE_DIMENSIONS.step.height
}

function getSubAgentGridMetrics(count: number, direction: 'LR' | 'TB') {
  if (count <= 0) {
    return { columns: 0, rows: 0, width: 0, height: 0 }
  }

  const verticalColumns = count <= 2 ? count : 2
  const columns = direction === 'TB'
    ? verticalColumns
    : Math.ceil(count / (count >= 5 ? 2 : 1))
  const rows = direction === 'TB'
    ? Math.ceil(count / verticalColumns)
    : (count >= 5 ? 2 : 1)

  return {
    columns,
    rows,
    width: (columns * SUB_AGENT_LAYOUT.cellWidth) + ((columns - 1) * SUB_AGENT_LAYOUT.cellGap),
    height: (rows * SUB_AGENT_LAYOUT.cellHeight) + ((rows - 1) * SUB_AGENT_LAYOUT.cellGap)
  }
}

// Count both orchestrator routes and sequence script calls so reusable scripts
// are not shown as unused orphan definitions.
function countOrphanStepRefs(steps: PlanStep[] | undefined): Map<string, number> {
  const counts = new Map<string, number>()
  const visit = (list?: PlanStep[]) => {
    if (!list) return
    for (const s of list) {
      if (isAgentStep(s)) {
        for (const item of s.items || []) {
          for (const call of item.scripted_steps || []) {
            counts.set(call.step_id, (counts.get(call.step_id) || 0) + 1)
          }
        }
        for (const r of s.predefined_routes || []) {
          if (r.orphan_step_ref) counts.set(r.orphan_step_ref, (counts.get(r.orphan_step_ref) || 0) + 1)
          if (r.sub_agent_step) visit([r.sub_agent_step])
        }
      }
      if (isLegacyAgentStep(s) && Array.isArray(s.predefined_routes)) {
        for (const r of s.predefined_routes) {
          if (r.orphan_step_ref) counts.set(r.orphan_step_ref, (counts.get(r.orphan_step_ref) || 0) + 1)
          if (r.sub_agent_step) visit([r.sub_agent_step])
        }
      }
    }
  }
  visit(steps)
  return counts
}

/**
 * Estimate node height based on content
 * Simplified version - only agent item rows add variable height.
 */
function estimateNodeHeight(node: WorkflowNode): number {
  const baseDimensions = NODE_DIMENSIONS[node.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
  let estimatedHeight = baseDimensions.height

  // Get node data
  const data = node.data as StepNodeData | Record<string, unknown>

  // Base height components (header, padding, footer) - simplified
  const headerHeight = 60 // Header with buttons
  const footerHeight = 40 // Config footer
  const padding = 16 // Top and bottom padding

  // Content height estimation
  let contentHeight = 0

  // For agent nodes, add height for title, badges, and item rows
  if (node.type === 'agent') {
    const messageData = data as AgentNodeData
    const seqItems = messageData.items || []
    const visibleCount = Math.min(seqItems.length, 6)
    const hiddenCount = seqItems.length - visibleCount
    contentHeight += 35
    if (messageData.description) contentHeight += 20
    contentHeight += 20 + Math.max(visibleCount, 1) * 24
    if (hiddenCount > 0) contentHeight += 18
  }

  // For routing/branch nodes, add height for the decision question + route labels
  if (node.type === 'routing' || node.type === 'branch') {
    const routingData = data as RoutingStepNodeData
    if (routingData.routing_question) {
      contentHeight += 40 // routing question box
    }
    contentHeight += 25 // route count badge
    if (routingData.routes && routingData.routes.length > 0) {
      contentHeight += (routingData.routes.length * 34) + 12
    }
  }

  // Calculate total estimated height
  estimatedHeight = headerHeight + padding + contentHeight + footerHeight

  // Add safety margin (20% extra) - reduced from 40% since nodes are simpler
  estimatedHeight = Math.ceil(estimatedHeight * 1.2)

  // Ensure minimum height
  estimatedHeight = Math.max(estimatedHeight, baseDimensions.height)

  return estimatedHeight
}

function getNodeLayoutDimensions(node: WorkflowNode): { width: number; height: number } {
  const baseDimensions = NODE_DIMENSIONS[node.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
  return {
    width: baseDimensions.width,
    height: Math.max(baseDimensions.height, estimateNodeHeight(node))
  }
}

function getImmediateSubAgentParentId(nodeId: string, parentIds: Set<string>): string | null {
  if (!nodeId.includes('-sub-agent-')) {
    return null
  }

  return Array.from(parentIds)
    .filter(parentId => nodeId.startsWith(`${parentId}-sub-agent-`))
    .sort((a, b) => b.length - a.length)[0] || null
}

function getSubAgentGridMetricsFromDimensions(dimensions: Array<{ width: number; height: number }>, direction: 'LR' | 'TB') {
  const count = dimensions.length
  const base = getSubAgentGridMetrics(count, direction)
  if (count <= 0) {
    return { ...base, columnWidths: [], rowHeights: [] }
  }

  const columnWidths = Array.from({ length: base.columns }, (_, column) => {
    return dimensions.reduce((max, dims, index) => {
      return index % base.columns === column ? Math.max(max, dims.width) : max
    }, SUB_AGENT_LAYOUT.cellWidth)
  })
  const rowHeights = Array.from({ length: base.rows }, (_, row) => {
    return dimensions.reduce((max, dims, index) => {
      return Math.floor(index / base.columns) === row ? Math.max(max, dims.height) : max
    }, SUB_AGENT_LAYOUT.cellHeight)
  })
  const width = columnWidths.reduce((sum, value) => sum + value, 0) + Math.max(0, base.columns - 1) * SUB_AGENT_LAYOUT.cellGap
  const height = rowHeights.reduce((sum, value) => sum + value, 0) + Math.max(0, base.rows - 1) * SUB_AGENT_LAYOUT.cellGap

  return {
    ...base,
    width,
    height,
    columnWidths,
    rowHeights
  }
}

function getNodeFootprintDimensions(
  node: WorkflowNode,
  allNodes: WorkflowNode[],
  parentIds: Set<string>,
  direction: 'LR' | 'TB',
  visited: Set<string> = new Set()
): { width: number; height: number } {
  const ownDimensions = getNodeLayoutDimensions(node)
  if (visited.has(node.id) || !isAgentCoordinatorNode(node)) {
    return ownDimensions
  }

  const nextVisited = new Set(visited)
  nextVisited.add(node.id)

  const childFootprints = allNodes
    .filter(candidate => getImmediateSubAgentParentId(candidate.id, parentIds) === node.id)
    .map(child => getNodeFootprintDimensions(child, allNodes, parentIds, direction, nextVisited))

  if (childFootprints.length === 0) {
    return ownDimensions
  }

  const childGrid = getSubAgentGridMetricsFromDimensions(childFootprints, direction)
  if (direction === 'TB') {
    return {
      width: Math.max(ownDimensions.width, childGrid.width),
      height: ownDimensions.height + SUB_AGENT_LAYOUT.parentGap + childGrid.height
    }
  }

  return {
    width: ownDimensions.width + SUB_AGENT_LAYOUT.parentGap + childGrid.width,
    height: Math.max(ownDimensions.height, childGrid.height)
  }
}

function isAgentCoordinatorNode(node: WorkflowNode): boolean {
  if (node.type !== 'agent' && node.type !== 'todo_task') return false
  const data = node.data as AgentNodeData | LegacyAgentNodeData
  return (data.predefined_routes?.length ?? 0) > 0 || data.enable_generic_agent === true
}

/**
 * Calculate topology metrics to adjust layout spacing
 */
function calculateTopologyMetrics(nodes: WorkflowNode[]): { hasOrchestrator: boolean; maxOrchestratorDepth: number; maxOrchestratorSubAgents: number; maxRoutingBranches: number } {
  let hasOrchestrator = false
  let maxOrchestratorDepth = 0
  let maxOrchestratorSubAgents = 0
  let maxRoutingBranches = 0

  nodes.forEach(node => {
    if (isAgentCoordinatorNode(node)) {
      hasOrchestrator = true
      const data = node.data as AgentNodeData | LegacyAgentNodeData
      const routes = data.predefined_routes
      const numRoutes = routes?.length || 0

      // Count actual sub-agents
      maxOrchestratorSubAgents = Math.max(maxOrchestratorSubAgents, numRoutes)

      maxOrchestratorDepth = Math.max(maxOrchestratorDepth, numRoutes)
    }
    if (node.type === 'routing' || node.type === 'branch') {
      const routes = (node.data as RoutingStepNodeData).routes
      maxRoutingBranches = Math.max(maxRoutingBranches, routes?.length || 0)
    }
  })

  return { hasOrchestrator, maxOrchestratorDepth, maxOrchestratorSubAgents, maxRoutingBranches }
}

function layoutWithDagre(nodes: WorkflowNode[], edges: WorkflowEdge[], direction: 'LR' | 'TB'): { nodes: WorkflowNode[], edges: WorkflowEdge[] } {
  // Calculate topology metrics to determine spacing requirements
  const { maxOrchestratorSubAgents, maxRoutingBranches } = calculateTopologyMetrics(nodes)

  // Get config based on layout direction
  const baseConfig = getDagreConfig(direction)

  // Dynamic config based on topology. Routing branches need substantially more
  // space than ordinary siblings: users trace these colored paths from a route
  // card to a downstream step, so give each possible route its own visible lane.
  const fanOut = Math.max(maxOrchestratorSubAgents, maxRoutingBranches)
  const generalSpacingMultiplier = fanOut > 3 ? 1.6 : fanOut > 2 ? 1.35 : 1
  const routingLaneMultiplier = maxRoutingBranches >= 6
    ? 2.5
    : maxRoutingBranches >= 4
      ? 2.1
      : maxRoutingBranches >= 3
        ? 1.6
        : 1
  const laneSpacingMultiplier = Math.max(generalSpacingMultiplier, routingLaneMultiplier)
  const rankSpacingMultiplier = maxRoutingBranches >= 4
    ? 1.45
    : Math.min(generalSpacingMultiplier, 1.3)

  const dynamicConfig = {
    ...baseConfig,
    // nodesep is the sibling-lane gap (horizontal in the top-to-bottom plan).
    // ranksep gives the lines room to leave a routing card before another row.
    nodesep: baseConfig.nodesep * laneSpacingMultiplier,
    ranksep: baseConfig.ranksep * rankSpacingMultiplier
  }

  const g = new dagre.graphlib.Graph()
  g.setGraph(dynamicConfig)
  g.setDefaultEdgeLabel(() => ({}))

  // Exclude SUB-AGENT nodes and HEADER nodes from Dagre
  // Sub-agents are positioned manually below todo_task nodes
  // Header nodes (start, variables) are positioned manually in a horizontal row
  // Branch nodes MUST be in Dagre to maintain graph connectivity
  const excludedNodeIds = new Set<string>()

  nodes.forEach(node => {
    if (node.id.includes('-sub-agent-')) {
      excludedNodeIds.add(node.id)
    }
    // Exclude header nodes - they're positioned manually before Dagre runs
    if (node.id === 'start' || node.id === 'variables') {
      excludedNodeIds.add(node.id)
    }
  })

  // Log excluded nodes for debugging
  if (excludedNodeIds.size > 0) {
    const headerNodes = Array.from(excludedNodeIds).filter(id => id === 'start' || id === 'variables')
    if (headerNodes.length > 0) {
      // console.log('[LAYOUT BUG] Excluding header nodes from Dagre:', headerNodes.join(', '))
    }
  }

  const todoTaskNodeIds = new Set(nodes.filter(isAgentCoordinatorNode).map(node => node.id))

  // Add all nodes except excluded nodes to Dagre graph
  nodes.forEach(node => {
    if (!excludedNodeIds.has(node.id)) {
      const layoutDimensions = getNodeLayoutDimensions(node)
      let width = layoutDimensions.width
      let height = layoutDimensions.height

      // For todo tasks, use compound dimensions to reserve space for sub-agents
      if (isAgentCoordinatorNode(node)) {
        const immediateSubAgentDimensions = nodes
          .filter(candidate => getImmediateSubAgentParentId(candidate.id, todoTaskNodeIds) === node.id)
          .map(candidate => getNodeFootprintDimensions(candidate, nodes, todoTaskNodeIds, direction))
        const numSubAgents = immediateSubAgentDimensions.length

        if (numSubAgents > 0) {
          const subAgentGrid = getSubAgentGridMetricsFromDimensions(immediateSubAgentDimensions, direction)

          if (direction === 'LR') {
            height = height + SUB_AGENT_LAYOUT.parentGap + subAgentGrid.height
            width = Math.max(width, subAgentGrid.width)
          } else {
            width = Math.max(width, subAgentGrid.width)
            height = height + SUB_AGENT_LAYOUT.parentGap + subAgentGrid.height
          }
        }
      }

      g.setNode(node.id, { width, height })
    }
  })

  // Add all edges except those involving sub-agents
  edges.forEach(edge => {
    if (!excludedNodeIds.has(edge.source) && !excludedNodeIds.has(edge.target)) {

      
      const minlen = 1

      // Note: With compound dimensions, minlen logic is less critical but still useful for extra safety
      // We keep a simplified version to ensure connections don't look cramped

      // Apply the calculated minlen (if > 1)
      if (minlen > 1) {
        g.setEdge(edge.source, edge.target, { minlen })
      } else {
        g.setEdge(edge.source, edge.target)
      }
    }
  })

  // Run layout
  dagre.layout(g)

  // Apply positions to nodes (only for nodes that were in Dagre)
  const layoutedNodes = nodes.map(node => {
    if (excludedNodeIds.has(node.id)) {
      // Keep excluded nodes at initial position (will be positioned manually later)
      return node
    }

    const nodeWithPosition = g.node(node.id)
    if (!nodeWithPosition) {
      // Node wasn't in Dagre graph, keep original position
      return node
    }

    const dims = getNodeLayoutDimensions(node)

    // Calculate position based on Compound vs Standard dimensions
    let x = nodeWithPosition.x
    let y = nodeWithPosition.y

    // Default centering (Dagre returns center)
    x -= dims.width / 2
    y -= dims.height / 2

    // Adjust for TodoTask Compound positioning
    if (isAgentCoordinatorNode(node)) {
      const immediateSubAgentDimensions = nodes
        .filter(candidate => getImmediateSubAgentParentId(candidate.id, todoTaskNodeIds) === node.id)
        .map(candidate => getNodeFootprintDimensions(candidate, nodes, todoTaskNodeIds, direction))
      const numSubAgents = immediateSubAgentDimensions.length

      if (numSubAgents > 0) {
        const subAgentGrid = getSubAgentGridMetricsFromDimensions(immediateSubAgentDimensions, direction)

        if (direction === 'LR') {
          const compoundHeight = dims.height + SUB_AGENT_LAYOUT.parentGap + subAgentGrid.height
          const compoundTop = nodeWithPosition.y - (compoundHeight / 2)
          y = compoundTop
          x = nodeWithPosition.x - (dims.width / 2)
        } else {
          const compoundHeight = dims.height + SUB_AGENT_LAYOUT.parentGap + subAgentGrid.height
          const compoundTop = nodeWithPosition.y - (compoundHeight / 2)
          y = compoundTop
          x = nodeWithPosition.x - (dims.width / 2)
        }
      }
    }

    return {
      ...node,
      position: { x, y }
    }
  })

  // Dagre owns route separation; manual repositioning is unnecessary.
  return { nodes: layoutedNodes, edges }
}

function explicitPlanStepSuccessors(step: PlanStep): string[] {
  if (isRoutingStep(step) || isBranchStep(step)) {
    return step.routes.map(route => route.next_step_id).filter(Boolean)
  }

  const routable = step as PlanStep & {
    next_step_id?: string
    if_yes_next_step_id?: string
    if_no_next_step_id?: string
    option_routes?: Record<string, string>
  }
  return [
    routable.next_step_id,
    routable.if_yes_next_step_id,
    routable.if_no_next_step_id,
    ...Object.values(routable.option_routes || {}),
  ].filter((id): id is string => Boolean(id && id !== 'end'))
}

function hasExplicitPlanContinuation(step: PlanStep): boolean {
  if (isRoutingStep(step) || isBranchStep(step)) return step.routes.length > 0

  const routable = step as PlanStep & {
    next_step_id?: string
    if_yes_next_step_id?: string
    if_no_next_step_id?: string
    option_routes?: Record<string, string>
  }
  return Boolean(
    routable.next_step_id ||
    routable.if_yes_next_step_id ||
    routable.if_no_next_step_id ||
    Object.keys(routable.option_routes || {}).length > 0
  )
}

// The visual graph includes a sequential edge when a top-level step has no
// explicit continuation and the following step is not the target of another
// route. Mirror that rule here, otherwise an "after X" branch gets detached
// from the route it governs and is laid out in an unrelated corner.
function buildPlanSuccessorMap(plan: PlanningResponse): Map<string, string[]> {
  const explicitTargets = new Set<string>()
  const directSuccessors = new Map<string, string[]>()

  plan.steps.forEach(step => {
    const successors = explicitPlanStepSuccessors(step)
    directSuccessors.set(step.id, successors)
    successors.forEach(successor => explicitTargets.add(successor))
  })

  const successorsByID = new Map<string, string[]>()
  plan.steps.forEach((step, index) => {
    const successors = [...(directSuccessors.get(step.id) || [])]
    const canContinueSequentially = !hasExplicitPlanContinuation(step) &&
      !isRoutingStep(step) &&
      !isBranchStep(step) &&
      !isHumanInputStep(step) &&
      !isLegacyAgentStep(step)
    const nextStep = plan.steps[index + 1]
    if (canContinueSequentially && nextStep && !explicitTargets.has(nextStep.id)) {
      successors.push(nextStep.id)
    }
    successorsByID.set(step.id, successors)
  })

  return successorsByID
}

// Stable topological order; retain deterministic order for the cyclic remainder.
function orderRouteSteps(ids: string[], successors: Map<string, string[]>): string[] {
  const members = new Set(ids)
  const incoming = new Map(ids.map(id => [id, 0]))
  ids.forEach(id => successors.get(id)?.forEach(next => {
    if (members.has(next)) incoming.set(next, incoming.get(next)! + 1)
  }))
  const ready = ids.filter(id => incoming.get(id) === 0)
  const ordered: string[] = []
  for (let index = 0; index < ready.length; index++) {
    const id = ready[index]
    ordered.push(id)
    successors.get(id)?.forEach(next => {
      if (!members.has(next)) return
      incoming.set(next, incoming.get(next)! - 1)
      if (incoming.get(next) === 0) ready.push(next)
    })
  }
  const placed = new Set(ordered)
  return [...ordered, ...ids.filter(id => !placed.has(id))]
}

// A major routing step describes several independently runnable pipelines.
// Dagre must honour every handoff between those pipelines, which can turn a
// small set of routes into a very tall, sparse tree. Keep the workflow header
// where the canvas put it, then give each route body a separate column.
// Handoff edges are intentionally retained between columns.
export function distributePrimaryRouteLanes(nodes: WorkflowNode[], plan: PlanningResponse): WorkflowNode[] {
  if (nodes.some(node => node.id.includes('-sub-agent-'))) return nodes

  const router = plan.steps.find(isRoutingStep)
  if (!router) return nodes

  const routes = router.routes
  if (routes.length < 3) return nodes

  const stepByID = new Map(plan.steps.map(step => [step.id, step]))
  const nodeByID = new Map(nodes.map(node => [node.id, node]))
  const routeEntries = new Set(routes.map(route => route.next_step_id))
  const successorsByID = buildPlanSuccessorMap(plan)
  const inboundCounts = new Map<string, number>()
  successorsByID.forEach(successors => {
    successors.forEach(successor => {
      inboundCounts.set(successor, (inboundCounts.get(successor) || 0) + 1)
    })
  })
  // Shared terminal work is a convergence point, not part of whichever route
  // happens to be visited first. Keep it below the routes so each pipeline
  // reads top-to-bottom before the flow rejoins.
  const convergenceStepIDs = new Set(
    Array.from(inboundCounts.entries())
      .filter(([id, count]) => count > 1 && !routeEntries.has(id))
      .map(([id]) => id)
  )

  // A routed workflow is often a *forest*, not six unrelated columns. For
  // example, a Reddit route can hand off to X and then to a digest route. The
  // old grid put all route entry points at the same height, which made those
  // handoff arrows travel backwards up the canvas. Treat route-to-route branch
  // targets as downstream segments instead: each segment stays in a separate
  // column and starts below the branch that reaches it.
  const planIndexByID = new Map(plan.steps.map((step, index) => [step.id, index]))
  const routeEntryIDs = new Set(routes.map(route => route.next_step_id))
  const incomingRouteEntryIDs = new Set<string>()

  plan.steps.forEach(step => {
    if (!isBranchStep(step)) return
    for (const targetID of successorsByID.get(step.id) || []) {
      if (routeEntryIDs.has(targetID)) incomingRouteEntryIDs.add(targetID)
    }
  })

  // Directly selectable routes which are also branch targets belong below the
  // branch that can continue into them. This preserves their direct router
  // edge without turning the graph into a set of backward arrows.
  const rootRouteEntryIDs = routes
    .map(route => route.next_step_id)
    .filter(entryID => !incomingRouteEntryIDs.has(entryID))
  if (rootRouteEntryIDs.length === 0) return nodes

  interface RouteSegment {
    entryID: string
    stepIDs: string[]
    nextEntries: Array<{ entryID: string; sourceID: string }>
  }

  const segmentsByEntryID = new Map<string, RouteSegment>()
  const collectRouteSegment = (entryID: string): RouteSegment => {
    const existing = segmentsByEntryID.get(entryID)
    if (existing) return existing

    const seen = new Set<string>()
    const queue = [entryID]
    const stepIDs: string[] = []
    const nextEntries: Array<{ entryID: string; sourceID: string }> = []

    while (queue.length > 0) {
      const stepID = queue.shift()
      if (!stepID || seen.has(stepID) || !stepByID.has(stepID)) continue
      // Crossing into a different route entry starts a new visual segment.
      if (stepID !== entryID && routeEntryIDs.has(stepID)) continue
      if (convergenceStepIDs.has(stepID)) continue

      seen.add(stepID)
      stepIDs.push(stepID)
      for (const successorID of successorsByID.get(stepID) || []) {
        if (routeEntryIDs.has(successorID) && successorID !== entryID) {
          nextEntries.push({ entryID: successorID, sourceID: stepID })
        } else if (!seen.has(successorID)) {
          queue.push(successorID)
        }
      }
    }

    const segment = {
      entryID,
      stepIDs: orderRouteSteps(stepIDs, successorsByID),
      nextEntries,
    }
    segmentsByEntryID.set(entryID, segment)
    return segment
  }

  const assignedRouteEntries = new Set<string>()
  const routeForests = rootRouteEntryIDs.flatMap(rootEntryID => {
    if (assignedRouteEntries.has(rootEntryID)) return []

    const depthByEntryID = new Map<string, number>([[rootEntryID, 0]])
    const entries: string[] = []
    const queue = [rootEntryID]
    let iterations = 0

    while (queue.length > 0 && iterations < 200) {
      iterations += 1
      const entryID = queue.shift()!
      if (assignedRouteEntries.has(entryID)) continue
      assignedRouteEntries.add(entryID)
      entries.push(entryID)

      const currentDepth = depthByEntryID.get(entryID) || 0
      for (const transition of collectRouteSegment(entryID).nextEntries) {
        const nextDepth = currentDepth + 1
        const previousDepth = depthByEntryID.get(transition.entryID)
        if (previousDepth === undefined || nextDepth > previousDepth) {
          depthByEntryID.set(transition.entryID, nextDepth)
        }
        if (!assignedRouteEntries.has(transition.entryID)) queue.push(transition.entryID)
      }
    }

    return [{
      rootEntryID,
      entries: entries.sort((left, right) => {
        const depthDelta = (depthByEntryID.get(left) || 0) - (depthByEntryID.get(right) || 0)
        return depthDelta || ((planIndexByID.get(left) || 0) - (planIndexByID.get(right) || 0))
      }),
      depthByEntryID,
      width: entries.length,
    }]
  })

  const laneStepIDs = new Set(
    routeForests.flatMap(forest => forest.entries.flatMap(entryID => collectRouteSegment(entryID).stepIDs))
  )
  const preRouterIDs = plan.steps
    .filter(step => successorsByID.get(step.id)?.includes(router.id))
    .map(step => step.id)
  const independentIDs = plan.steps
    .filter(step =>
      step.id !== router.id &&
      !laneStepIDs.has(step.id) &&
      !preRouterIDs.includes(step.id) &&
      !convergenceStepIDs.has(step.id)
    )
    .map(step => step.id)

  const workflowNodes = nodes.filter(node =>
    node.id !== 'start' && node.id !== 'variables' && node.id !== 'end'
  )
  if (workflowNodes.length === 0) return nodes

  // The header is a persistent left column. Reuse the body bounds produced by
  // the header-aware layout above so routes can never collide with Variables.
  const bodyLeft = Math.min(...workflowNodes.map(node => node.position.x))
  const bodyTop = Math.min(...workflowNodes.map(node => node.position.y))
  const columns = Math.max(1, routeForests.reduce((total, forest) => total + forest.width, 0))
  // Route cards need enough white space for labels and their connecting lines.
  // Keep these deliberately larger than the normal Dagre sibling gap: this is
  // the readable overview of a multi-pipeline workflow, not a dense DAG dump.
  const laneGap = 240
  const stepGap = 72
  const routeRowGap = 220
  const maxRouteWidth = Math.max(
    280,
    ...Array.from(laneStepIDs).map(id => nodeByID.get(id))
      .filter((node): node is WorkflowNode => Boolean(node))
      .map(node => getNodeLayoutDimensions(node).width)
  )
  const laneWidth = maxRouteWidth + laneGap
  const gridWidth = (columns * maxRouteWidth) + ((columns - 1) * laneGap)
  const centerX = bodyLeft + (gridWidth / 2)
  const positionByID = new Map<string, { x: number; y: number }>()

  const placeCentered = (id: string, y: number) => {
    const node = nodeByID.get(id)
    if (!node) return
    const dims = getNodeLayoutDimensions(node)
    positionByID.set(id, { x: centerX - (dims.width / 2), y })
  }

  let nextY = bodyTop
  preRouterIDs.forEach(id => {
    const node = nodeByID.get(id)
    if (!node) return
    placeCentered(id, nextY)
    nextY += getNodeLayoutDimensions(node).height + stepGap
  })

  placeCentered(router.id, nextY)
  const routerNode = nodeByID.get(router.id)
  nextY += (routerNode ? getNodeLayoutDimensions(routerNode).height : 0) + routeRowGap

  // Every selectable route gets a distinct column, including sibling handoffs
  // at the same depth. Keep related routes adjacent in a single wide row.
  const forestRows: Array<Array<typeof routeForests[number] & { startColumn: number }>> = []
  let currentRow: Array<typeof routeForests[number] & { startColumn: number }> = []
  let usedColumns = 0
  routeForests.forEach(forest => {
    const width = Math.min(columns, forest.width)
    if (currentRow.length > 0 && usedColumns + width > columns) {
      forestRows.push(currentRow)
      currentRow = []
      usedColumns = 0
    }
    currentRow.push({ ...forest, startColumn: usedColumns })
    usedColumns += width
  })
  if (currentRow.length > 0) forestRows.push(currentRow)

  let laneBottom = nextY
  forestRows.forEach(row => {
    const rowTop = laneBottom
    let rowBottom = rowTop

    row.forEach(forest => {
      const sourcePositionByID = new Map<string, { x: number; y: number; height: number }>()

      forest.entries.forEach((entryID, entryIndex) => {
        const segment = collectRouteSegment(entryID)
        const x = bodyLeft + ((forest.startColumn + entryIndex) * laneWidth)

        const precedingSourcePositions = forest.entries
          .flatMap(candidateEntryID => collectRouteSegment(candidateEntryID).nextEntries)
          .filter(transition => transition.entryID === entryID)
          .map(transition => sourcePositionByID.get(transition.sourceID))
          .filter((position): position is { x: number; y: number; height: number } => Boolean(position))
        const entryNode = nodeByID.get(entryID)
        const entryHeight = entryNode ? getNodeLayoutDimensions(entryNode).height : NODE_DIMENSIONS.step.height
        // A branch-to-route handoff connects through side handles. Centre the
        // destination card against the decision so the continuation reads
        // left-to-right, rather than adding another full vertical card gap.
        const y = precedingSourcePositions.length > 0
          ? Math.max(
            rowTop,
            ...precedingSourcePositions.map(position => position.y + Math.max(0, (position.height - entryHeight) / 2))
          )
          : rowTop

        let segmentBottom = y
        segment.stepIDs.forEach(stepID => {
          const node = nodeByID.get(stepID)
          if (!node) return
          const dims = getNodeLayoutDimensions(node)
          positionByID.set(stepID, { x, y: segmentBottom })
          sourcePositionByID.set(stepID, { x, y: segmentBottom, height: dims.height })
          segmentBottom += dims.height + stepGap
        })
        rowBottom = Math.max(rowBottom, segmentBottom - stepGap)
      })
    })

    laneBottom = rowBottom + routeRowGap
  })

  // Steps which are not reachable from a primary route are genuine standalone
  // jobs. Give them a compact bottom strip rather than letting them occupy
  // arbitrary corners of the route map.
  const independentTop = laneBottom
  let independentBottom = independentTop
  for (let index = 0; index < independentIDs.length; index += columns) {
    const row = independentIDs.slice(index, index + columns)
    const rowTop = independentBottom
    row.forEach((id, column) => {
      const node = nodeByID.get(id)
      if (!node) return
      const dims = getNodeLayoutDimensions(node)
      positionByID.set(id, { x: bodyLeft + column * laneWidth, y: rowTop })
      independentBottom = Math.max(independentBottom, rowTop + dims.height + stepGap)
    })
  }

  let convergenceY = independentBottom + routeRowGap
  orderRouteSteps([...convergenceStepIDs], successorsByID).forEach(id => {
    const node = nodeByID.get(id)
    if (!node) return
    const dims = getNodeLayoutDimensions(node)
    positionByID.set(id, { x: centerX - dims.width / 2, y: convergenceY })
    convergenceY += dims.height + stepGap
  })

  return nodes.map(node => {
    const position = positionByID.get(node.id)
    if (position) return { ...node, position }

    // Keep supporting nodes attached to their plan step when that step moves
    // into a route cell. They retain the original side-by-side offset.
    const parentStepID = (node.data as { parentStepId?: string }).parentStepId
    const parentPosition = parentStepID ? positionByID.get(parentStepID) : undefined
    const originalParent = parentStepID ? nodeByID.get(parentStepID) : undefined
    if (parentPosition && originalParent) {
      return {
        ...node,
        position: {
          x: parentPosition.x + (node.position.x - originalParent.position.x),
          y: parentPosition.y + (node.position.y - originalParent.position.y),
        },
      }
    }
    return node
  })
}

/**
 * Determine change type for a step based on detected changes
 */
function getChangeType(stepId: string, changes?: PlanChanges | null): ChangeType | undefined {
  if (!changes) return undefined
  if (changes.added.includes(stepId)) return 'added'
  if (changes.updated.includes(stepId)) return 'updated'
  if (changes.deleted.includes(stepId)) return 'deleted'
  return undefined
}

/**
 * Convert a PlanStep to a React Flow node
 */
function stepToNode(
  step: PlanStep,
  stepIndex: number,
  changes?: PlanChanges | null,
  workspacePath?: string | null,
  selectedRunFolder?: string,
  completedStepIds?: Set<string> // Set of completed step IDs (converted from indices for step_id-based matching)
): WorkflowNode {
  const nodeId = step.id || `step-${stepIndex}`

  // Determine change type for highlighting
  const changeType = getChangeType(step.id || nodeId, changes)

  // Determine status: completedStepIds (from steps_done.json) > pending.
  // Event-driven step status was removed on 2026-09-22 (no readers need it).
  let status: 'pending' | 'running' | 'completed' | 'failed' = 'pending'
  const stepId = step.id || nodeId

  if (completedStepIds && completedStepIds.has(stepId)) {
    status = 'completed' as const
  } else {
    // Default: pending
    status = 'pending' as const
  }

  const getStepTitle = () => {
    if (isLegacyAgentStep(step)) {
      return step.title || `Agent ${stepIndex + 1}`
    }
    if (isHumanInputStep(step)) {
      // For human input nodes, prefer question over generic title
      return step.title || step.question || `Human Input ${stepIndex + 1}`
    }
    return step.title || `Step ${stepIndex + 1}`
  }

  const baseData = {
    id: nodeId,
    title: getStepTitle(),
    description: step.description,
    success_criteria: step.success_criteria,
    status,
    stepIndex,
    step,
    changeType,
    workspacePath,
    selectedRunFolder,
    validation_schema: step.validation_schema
  }

  if (isRoutingStep(step)) {
    return {
      id: nodeId,
      type: 'routing',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        routing_question: step.routing_question,
        routes: step.routes,
        validation_schema: step.validation_schema
      } as RoutingStepNodeData
    }
  }

  if (isBranchStep(step)) {
    return {
      id: nodeId,
      type: 'branch',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        // RoutingStepNode reads routing_question -- branch reuses the same
        // component/node data shape, just its own node-type key. See PLAT-259.
        routing_question: step.branch_question,
        routes: step.routes,
        route_source: step.route_source,
        validation_schema: step.validation_schema
      } as RoutingStepNodeData
    }
  }

  if (isLegacyAgentStep(step)) {
    return {
      id: nodeId,
      type: 'agent',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        items: effectiveAgentItems(step),
        predefined_routes: step.predefined_routes,
        enable_generic_agent: step.enable_generic_agent,
        // Flat format: validation_schema is directly on step
        validation_schema: step.validation_schema || step.todo_task_step?.validation_schema
        // Note: status is inherited from baseData (computed based on completedStepIndices)
      } as AgentNodeData
    }
  }

  if (isHumanInputStep(step)) {
    return {
      id: nodeId,
      type: 'human_input',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        question: step.question,
        response_type: step.response_type || 'text',
        options: step.options
        // Note: status is inherited from baseData (computed based on completedStepIndices)
      } as HumanInputNodeData
    }
  }

  if (isCrewStep(step)) {
    return {
      id: nodeId,
      type: 'crew',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        crew_profile_id: step.crew_profile_id,
        crew_project_id: step.crew_project_id,
        trigger_id: step.trigger_id
        // Note: status is inherited from baseData (computed based on completedStepIndices)
      } as CrewStepNodeData
    }
  }

  // Covers authored agent steps AND stored `regular` steps, which the
  // runtime normalizes into a sequence before running (see runsAsAgent).
  // Showing the latter as a plain step card described an execution path that no
  // longer exists.
  if (runsAsAgent(step)) {
    return {
      id: nodeId,
      type: 'agent',
      position: { x: 0, y: 0 },
      data: {
        ...baseData,
        items: configuredAgentItems(step),
        predefined_routes: isAgentStep(step) ? step.predefined_routes : undefined
        // Note: status is inherited from baseData (computed based on completedStepIndices)
      } as AgentNodeData
    }
  }

  return {
    id: nodeId,
    type: 'step',
    position: { x: 0, y: 0 },
    data: baseData as StepNodeData
  }
}

/**
 * Create validation and learning nodes for a step
 * DISABLED: Validation and learning nodes are no longer displayed in the workflow canvas
 * Returns empty nodes and edges, with the step itself as the exit node
 */
function createValidationLearningNodes(
  stepNodeId: string
): { nodes: WorkflowNode[], edges: WorkflowEdge[], exitNodeId: string } {
  // Validation and learning nodes are no longer displayed in the workflow canvas
  // Simply return the step itself as the exit node
  return { nodes: [], edges: [], exitNodeId: stepNodeId }
}

/**
 * Process top-level steps and their routed sub-agents.
 */
function processSteps(
  steps: PlanStep[],
  changes: PlanChanges | null | undefined,
  presetUseCodeExecutionMode: boolean,
  presetLLMConfig: AgentLLMConfig | undefined,
  availableLLMs: Array<{ provider: string; model: string; label: string }>,
  workspacePath?: string | null,
  selectedRunFolder?: string,
  stepIdToNodeIdMap?: Map<string, string>, // Map of step ID to node ID for next_step_id lookups
  completedStepIds?: Set<string> // Set of completed step IDs (converted from indices for step_id-based matching)
): { nodes: WorkflowNode[], edges: WorkflowEdge[] } {
  const nodes: WorkflowNode[] = []
  const edges: WorkflowEdge[] = []

  // Track the last "exit" node ID for edge connections
  let lastExitNodeId: string | null = null

  // Nodes reached by an explicit route edge (routing routes, human-input choices,
  // or todo next_step_id). These are entered only via that route, so they must not
  // receive an array-order sequential edge.
  const explicitRouteTargetNodeIds = new Set<string>()
  const addRouteTarget = (stepId?: string) => {
    if (!stepId || stepId === 'end') return
    const targetNodeId = stepIdToNodeIdMap?.get(stepId)
    if (targetNodeId) explicitRouteTargetNodeIds.add(targetNodeId)
  }
  steps.forEach(s => {
    if (isRoutingStep(s) && s.routes) s.routes.forEach(r => addRouteTarget(r.next_step_id))
    if (isBranchStep(s) && s.routes) s.routes.forEach(r => addRouteTarget(r.next_step_id))
    if (isHumanInputStep(s)) {
      addRouteTarget(s.if_yes_next_step_id)
      addRouteTarget(s.if_no_next_step_id)
    }
    if (isLegacyAgentStep(s)) addRouteTarget(s.next_step_id)
  })
  const primaryRouteEntryStepIDs = new Set(
    steps.find(isRoutingStep)?.routes.map(route => route.next_step_id) || []
  )

  const buildTodoTaskSubAgentGraph = (
    todoTaskStep: PlanStep,
    todoTaskNodeId: string,
    todoTaskNodeData: AgentNodeData,
    includeCompletionEdge: boolean
  ): { nodes: WorkflowNode[], edges: WorkflowEdge[] } => {
    const todoTaskEdges: WorkflowEdge[] = []
    const todoTaskSubAgentNodes: WorkflowNode[] = []
    const parentStepIndex = todoTaskNodeData.stepIndex
    const todoTaskTitle = todoTaskNodeData.title || todoTaskStep.title || `Agent ${parentStepIndex + 1}`

    if ((isLegacyAgentStep(todoTaskStep) || isAgentStep(todoTaskStep)) && todoTaskStep.predefined_routes && todoTaskStep.predefined_routes.length > 0) {
      todoTaskStep.predefined_routes.forEach((route) => {
        const isEndRoute = route.route_id?.toLowerCase() === 'end'

        if (isEndRoute) {
          if (includeCompletionEdge) {
            todoTaskEdges.push({
              id: `${todoTaskNodeId}-route-${route.route_id}-to-end`,
              source: todoTaskNodeId,
              sourceHandle: route.route_id,
              target: 'end',
              type: 'step',
              style: { stroke: '#ef4444', strokeWidth: 2 },
              animated: false
            })
          }
          return
        }

        if (!route.sub_agent_step) {
          return
        }

        const routeId = route.route_id || route.sub_agent_step.id || String(todoTaskStep.predefined_routes?.indexOf(route) ?? 0)
        const subAgentNodeId = `${todoTaskNodeId}-sub-agent-${routeId}`
        const subAgentStep = route.sub_agent_step
        const stepId = subAgentStep.id || subAgentNodeId

        const status: 'pending' | 'running' | 'completed' | 'failed' = 'pending'

        const changeType = getChangeType(stepId, changes)

        if (isLegacyAgentStep(subAgentStep)) {
          const nestedTodoNode: WorkflowNode = {
            id: subAgentNodeId,
            type: 'agent',
            position: { x: 0, y: 0 },
            data: {
              id: subAgentNodeId,
              title: subAgentStep.title || `${route.route_name || route.route_id || routeId}`,
              description: subAgentStep.description,
              success_criteria: subAgentStep.success_criteria,
              status,
              stepIndex: parentStepIndex,
              step: subAgentStep,
              changeType,
              items: effectiveAgentItems(subAgentStep),
              predefined_routes: subAgentStep.predefined_routes,
              enable_generic_agent: subAgentStep.enable_generic_agent,
              validation_schema: subAgentStep.validation_schema || subAgentStep.todo_task_step?.validation_schema,
              workspacePath,
              selectedRunFolder,
              parentOrchestratorTitle: todoTaskTitle,
              routeName: route.route_name || undefined,
              routeCondition: route.condition || undefined
            } as AgentNodeData
          }

          todoTaskSubAgentNodes.push(nestedTodoNode)

          const nestedTodoGraph = buildTodoTaskSubAgentGraph(
            subAgentStep,
            subAgentNodeId,
            nestedTodoNode.data as AgentNodeData,
            false
          )
          todoTaskSubAgentNodes.push(...nestedTodoGraph.nodes)
          todoTaskEdges.push(...nestedTodoGraph.edges)
        } else if (runsAsAgent(subAgentStep)) {
          // A agent sub-agent must render as a AgentNode so its
          // ordered items show — not as a generic step card. A stored `regular`
          // sub-agent runs as one too, so it gets the same treatment.
          const seqNode: WorkflowNode = {
            id: subAgentNodeId,
            type: 'agent',
            position: { x: 0, y: 0 },
            data: {
              id: subAgentNodeId,
              title: subAgentStep.title || `${route.route_name || route.route_id || routeId}`,
              description: subAgentStep.description,
              items: configuredAgentItems(subAgentStep),
              predefined_routes: isAgentStep(subAgentStep) ? subAgentStep.predefined_routes : undefined,
              status,
              stepIndex: parentStepIndex,
              step: subAgentStep,
              changeType,
              validation_schema: subAgentStep.validation_schema,
              workspacePath,
              selectedRunFolder,
              parentOrchestratorTitle: todoTaskTitle,
              routeName: route.route_name || undefined,
              routeCondition: route.condition || undefined
            } as AgentNodeData
          }

          todoTaskSubAgentNodes.push(seqNode)

          if (isAgentStep(subAgentStep) && subAgentStep.predefined_routes?.length) {
            const nestedAgentGraph = buildTodoTaskSubAgentGraph(
              subAgentStep,
              subAgentNodeId,
              seqNode.data as AgentNodeData,
              false
            )
            todoTaskSubAgentNodes.push(...nestedAgentGraph.nodes)
            todoTaskEdges.push(...nestedAgentGraph.edges)
          }
        } else {
          const subAgentNode: WorkflowNode = {
            id: subAgentNodeId,
            type: 'step',
            position: { x: 0, y: 0 },
            data: {
              id: subAgentNodeId,
              title: subAgentStep.title || `${route.route_name || route.route_id || routeId}`,
              description: subAgentStep.description,
              success_criteria: subAgentStep.success_criteria,
              status,
              stepIndex: parentStepIndex,
              step: subAgentStep,
              changeType,
              validation_schema: subAgentStep.validation_schema,
              workspacePath,
              selectedRunFolder,
              parentOrchestratorTitle: todoTaskTitle,
              routeName: route.route_name || undefined,
              routeCondition: route.condition || undefined
            } as StepNodeData
          }

          todoTaskSubAgentNodes.push(subAgentNode)
        }

        todoTaskEdges.push({
          id: `${todoTaskNodeId}-route-${route.route_id}-to-sub-agent`,
          source: todoTaskNodeId,
          sourceHandle: route.route_id,
          target: subAgentNodeId,
          targetHandle: 'top',
          type: 'step',
          style: { stroke: '#8b5cf6', strokeWidth: 2, strokeDasharray: '5,5' },
          animated: false
        })
      })
    }

    if (isLegacyAgentStep(todoTaskStep) && todoTaskStep.enable_generic_agent) {
      const routeId = 'generic'
      const subAgentNodeId = `${todoTaskNodeId}-sub-agent-${routeId}`

      const status: 'pending' | 'running' | 'completed' | 'failed' = 'pending'

      const subAgentNode: WorkflowNode = {
        id: subAgentNodeId,
        type: 'step',
        position: { x: 0, y: 0 },
        data: {
          id: subAgentNodeId,
          title: 'Generic Agent',
          description: 'Executes ad-hoc tasks using workspace tools',
          success_criteria: 'Task completion verified by the parent agent',
          status,
          stepIndex: parentStepIndex,
          step: {
            id: subAgentNodeId,
            title: 'Generic Agent',
            description: 'Executes ad-hoc tasks using workspace tools',
            type: 'regular',
            agent_configs: {
              disable_learning: true
            }
          } as PlanStep,
          changeType: undefined,
          workspacePath,
          selectedRunFolder,
          parentOrchestratorTitle: todoTaskTitle,
          routeName: 'Generic Execution',
          routeCondition: 'Ad-hoc tasks'
        } as StepNodeData
      }

      todoTaskSubAgentNodes.push(subAgentNode)

      todoTaskEdges.push({
        id: `${todoTaskNodeId}-route-generic-to-sub-agent`,
        source: todoTaskNodeId,
        target: subAgentNodeId,
        targetHandle: 'top',
        type: 'smoothstep',
        style: { stroke: '#8b5cf6', strokeWidth: 2, strokeDasharray: '5,5' },
        animated: false
      })
    }

    if (includeCompletionEdge && isLegacyAgentStep(todoTaskStep) && todoTaskStep.next_step_id) {
      const targetNodeId = stepIdToNodeIdMap?.get(todoTaskStep.next_step_id)
      if (targetNodeId) {
        todoTaskEdges.push({
          id: `${todoTaskNodeId}-todo-task-to-${targetNodeId}`,
          source: todoTaskNodeId,
          target: targetNodeId,
          type: 'smoothstep',
          style: { stroke: '#8b5cf6', strokeWidth: 2 },
          animated: false
        })
      } else if (todoTaskStep.next_step_id === 'end') {
        todoTaskEdges.push({
          id: `${todoTaskNodeId}-todo-task-to-end`,
          source: todoTaskNodeId,
          target: 'end',
          type: 'smoothstep',
          label: 'Complete',
          labelStyle: COMPLETION_EDGE_LABEL_STYLE,
          labelBgStyle: EDGE_LABEL_BG_STYLE,
          labelBgPadding: [4, 4] as [number, number],
          labelBgBorderRadius: 4,
          style: { stroke: '#8b5cf6', strokeWidth: 2 },
          animated: false
        })
      }
    }

    return { nodes: todoTaskSubAgentNodes, edges: todoTaskEdges }
  }

  steps.forEach((step, index) => {
    const node = stepToNode(step, index, changes, workspacePath, selectedRunFolder, completedStepIds)
    nodes.push(node)

    // Array order supplies the normal sequential edge. Explicit route targets
    // already receive an edge from routing, human input, or a todo task.
    if (lastExitNodeId && !explicitRouteTargetNodeIds.has(node.id)) {
      edges.push({
        id: `${lastExitNodeId}-to-${node.id}`,
        source: lastExitNodeId,
        target: node.id,
        type: 'smoothstep',
        animated: false,
        style: { stroke: '#6b7280', strokeWidth: 2 }
      })
    }

    if (!isHumanInputStep(step)) {
      const vlResult = createValidationLearningNodes(node.id)
      nodes.push(...vlResult.nodes)
      edges.push(...vlResult.edges)
      lastExitNodeId = vlResult.exitNodeId
    } else {
      lastExitNodeId = node.id
    }
    // Handle routing/branch step edge routing
    // Both evaluate a question and route to one of N possible next steps --
    // identical mechanics, routing is now the "route"/major-fork concept
    // and branch is the small in-flow decision. See PLAT-259.
    if (isRoutingStep(step) || isBranchStep(step)) {
      const edgeType = isBranchStep(step) ? 'branch' : 'routing'
      const routingEdges: WorkflowEdge[] = []
      const sourceNodeId = (typeof lastExitNodeId === 'string' ? lastExitNodeId : node.id)

      if (step.routes) {
        step.routes.forEach((route, routeIndex) => {
          const targetNodeId = stepIdToNodeIdMap?.get(route.next_step_id)
          const routeColor = routeColorForIndex(routeIndex)
          const isLateralHandoff = isBranchStep(step) && primaryRouteEntryStepIDs.has(route.next_step_id)

          if (targetNodeId) {
            const isSelectedRoute = !step.selected_route_id || route.route_id === step.selected_route_id
            routingEdges.push({
              id: `${sourceNodeId}-routing-${route.route_id}-to-${targetNodeId}`,
              source: sourceNodeId,
              sourceHandle: isLateralHandoff ? `handoff-${route.route_id}` : `route-${route.route_id}`,
              target: targetNodeId,
              targetHandle: isLateralHandoff ? 'handoff' : undefined,
              type: edgeType,
              label: route.route_name || route.route_id,
              labelStyle: { ...ROUTE_EDGE_LABEL_STYLE, opacity: isSelectedRoute ? 1 : 0.5 },
              labelBgStyle: EDGE_LABEL_BG_STYLE,
              labelBgPadding: [3, 3] as [number, number],
              labelBgBorderRadius: 4,
              data: {
                routeIndex,
                routeCount: step.routes?.length || 0,
                routeName: route.route_name || route.route_id,
                isBranch: isBranchStep(step),
                isLateralHandoff,
                selected: isSelectedRoute,
                color: routeColor
              },
              style: {
                stroke: isSelectedRoute ? routeColor : '#94a3b8',
                strokeWidth: isSelectedRoute ? 2.5 : 1.25,
                strokeDasharray: isBranchStep(step) ? '7 5' : undefined,
                opacity: isSelectedRoute ? 1 : 0.4
              },
              animated: false
            })
          } else if (route.next_step_id === 'end') {
            const isSelectedRoute = !step.selected_route_id || route.route_id === step.selected_route_id
            routingEdges.push({
              id: `${sourceNodeId}-routing-${route.route_id}-to-end`,
              source: sourceNodeId,
              sourceHandle: `route-${route.route_id}`,
              target: 'end',
              type: edgeType,
              label: route.route_name || route.route_id,
              labelStyle: { ...ROUTE_EDGE_LABEL_STYLE, opacity: isSelectedRoute ? 1 : 0.5 },
              labelBgStyle: EDGE_LABEL_BG_STYLE,
              labelBgPadding: [3, 3] as [number, number],
              labelBgBorderRadius: 4,
              data: {
                routeIndex,
                routeCount: step.routes?.length || 0,
                routeName: route.route_name || route.route_id,
                isBranch: isBranchStep(step),
                selected: isSelectedRoute,
                color: routeColor
              },
              style: {
                stroke: isSelectedRoute ? routeColor : '#94a3b8',
                strokeWidth: isSelectedRoute ? 2.5 : 1.25,
                strokeDasharray: isBranchStep(step) ? '7 5' : undefined,
                opacity: isSelectedRoute ? 1 : 0.4
              },
              animated: false
            })
          }
        })
      }

      edges.push(...routingEdges)
      lastExitNodeId = null // Routing steps handle their own routing
    }

    // Handle human input step edge routing
    // Human input steps ask a question and route based on response (yes/no or multiple choice)
    if (isHumanInputStep(step)) {
      const humanInputEdges: WorkflowEdge[] = []
      // Use the human input node itself as source (no validation/learning nodes for human input)
      const sourceNodeId = node.id

      // Determine routing based on response_type
      if (step.response_type === 'yesno') {
        // Yes/No routing
        if (step.if_yes_next_step_id) {
          const targetNodeId = stepIdToNodeIdMap?.get(step.if_yes_next_step_id)
          if (targetNodeId) {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-yes-to-${targetNodeId}`,
              source: sourceNodeId,
              target: targetNodeId,
              type: 'smoothstep',
              label: 'Yes',
              labelStyle: { fill: '#22c55e', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#f0fdf4', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#22c55e', strokeWidth: 2 },
              animated: false
            })
          } else if (step.if_yes_next_step_id === 'end') {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-yes-to-end`,
              source: sourceNodeId,
              target: 'end',
              type: 'smoothstep',
              label: 'Yes',
              labelStyle: { fill: '#22c55e', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#f0fdf4', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#22c55e', strokeWidth: 2 },
              animated: false
            })
          }
        }

        if (step.if_no_next_step_id) {
          const targetNodeId = stepIdToNodeIdMap?.get(step.if_no_next_step_id)
          if (targetNodeId) {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-no-to-${targetNodeId}`,
              source: sourceNodeId,
              target: targetNodeId,
              type: 'smoothstep',
              label: 'No',
              labelStyle: { fill: '#ef4444', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#fef2f2', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#ef4444', strokeWidth: 2 },
              animated: false
            })
          } else if (step.if_no_next_step_id === 'end') {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-no-to-end`,
              source: sourceNodeId,
              target: 'end',
              type: 'smoothstep',
              label: 'No',
              labelStyle: { fill: '#ef4444', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#fef2f2', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#ef4444', strokeWidth: 2 },
              animated: false
            })
          }
        }
      } else if (step.response_type === 'multiple_choice' && step.option_routes) {
        // Multiple choice routing - create edges for each option route
        Object.entries(step.option_routes).forEach(([optionKey, nextStepId]) => {
          const targetNodeId = stepIdToNodeIdMap?.get(nextStepId)
          const optionLabel = step.options?.[parseInt(optionKey)] || optionKey

          if (targetNodeId) {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-option-${optionKey}-to-${targetNodeId}`,
              source: sourceNodeId,
              target: targetNodeId,
              type: 'smoothstep',
              label: optionLabel,
              labelStyle: { fill: '#3b82f6', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#eff6ff', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#3b82f6', strokeWidth: 2 },
              animated: false
            })
          } else if (nextStepId === 'end') {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-option-${optionKey}-to-end`,
              source: sourceNodeId,
              target: 'end',
              type: 'smoothstep',
              label: optionLabel,
              labelStyle: { fill: '#ef4444', fontWeight: 600, fontSize: 11 },
              labelBgStyle: { fill: '#fef2f2', fillOpacity: 0.9 },
              labelBgPadding: [4, 4] as [number, number],
              labelBgBorderRadius: 4,
              style: { stroke: '#ef4444', strokeWidth: 2 },
              animated: false
            })
          }
        })
      } else {
        // Text response or default routing - use next_step_id
        if (step.next_step_id) {
          const targetNodeId = stepIdToNodeIdMap?.get(step.next_step_id)
          if (targetNodeId) {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-to-${targetNodeId}`,
              source: sourceNodeId,
              target: targetNodeId,
              type: 'smoothstep',
              style: { stroke: '#6b7280', strokeWidth: 2 },
              animated: false
            })
          } else if (step.next_step_id === 'end') {
            humanInputEdges.push({
              id: `${sourceNodeId}-human-input-to-end`,
              source: sourceNodeId,
              target: 'end',
              type: 'smoothstep',
              style: { stroke: '#6b7280', strokeWidth: 2 },
              animated: false
            })
          }
        }
      }

      edges.push(...humanInputEdges)

      // Human input steps handle their own routing - don't connect to next sequential step
      lastExitNodeId = null
    }

    // Handle todo_task step edge routing
    // Todo task steps have predefined routes (sub-agents)
    // and optionally a generic agent. After sub-agents complete, they return to the main todo task node.
    // The todo task step connects to next_step_id when all tasks are complete.
    if (isLegacyAgentStep(step) || (isAgentStep(step) && (step.predefined_routes?.length ?? 0) > 0)) {
      const todoTaskGraph = buildTodoTaskSubAgentGraph(
        step,
        node.id,
        node.data as AgentNodeData,
        true
      )

      nodes.push(...todoTaskGraph.nodes)
      edges.push(...todoTaskGraph.edges)

      // Todo task steps handle their own routing - don't connect to next sequential step
      lastExitNodeId = null
    }

    // Handle scripted and agent next_step_id: draw an EXPLICIT edge to the
    // target. Without this, a step's next_step_id only connected via array
    // order, so when several route steps all point at the same downstream
    // step (e.g. each portal -> normalize), only the last one linked and the
    // shared step looked unconnected. Now every explicit successor draws its own edge, so
    // the convergence (shared finish line) is visible.
    if ((isAgentStep(step) || isRegularStep(step)) && step.next_step_id) {
      const sourceNodeId = (typeof lastExitNodeId === 'string' ? lastExitNodeId : node.id)
      const targetNodeId = step.next_step_id === 'end' ? 'end' : stepIdToNodeIdMap?.get(step.next_step_id)
      if (targetNodeId) {
        edges.push({
          id: `${sourceNodeId}-${isRegularStep(step) ? 'scripted' : 'msgseq'}-next-to-${targetNodeId}`,
          source: sourceNodeId,
          target: targetNodeId,
          type: 'smoothstep',
          animated: false,
          style: { stroke: '#6b7280', strokeWidth: 2 }
        })
        lastExitNodeId = null // explicit next_step_id edge created; don't also array-chain
      }
    }
  })

  return { nodes, edges }
}

/**
 * Check if a node is a step-type node (has step data)
 */
function isStepTypeNode(node: WorkflowNode): node is WorkflowNode & { data: StepNodeData | LegacyAgentNodeData | HumanInputNodeData | AgentNodeData } {
  return node.type === 'step' || node.type === 'todo_task' || node.type === 'human_input' || node.type === 'agent'
}

/**
 * Create edges based on context dependencies
 */
function createDependencyEdges(nodes: WorkflowNode[]): WorkflowEdge[] {
  const edges: WorkflowEdge[] = []

  // Filter to only step-type nodes (not validation/learning)
  const stepNodes = nodes.filter(isStepTypeNode)

  // Create a map of context_output to node ID
  const outputToNodeMap = new Map<string, string>()
  stepNodes.forEach(node => {
    const step = node.data.step
    if (step.context_output) {
      const outputs = Array.isArray(step.context_output)
        ? step.context_output
        : [step.context_output]
      outputs.forEach((output: string) => {
        outputToNodeMap.set(output, node.id)
      })
    }
  })

  // Create edges for context dependencies
  stepNodes.forEach(node => {
    const step = node.data.step
    if (step.context_dependencies && step.context_dependencies.length > 0) {
      step.context_dependencies.forEach((dep: string) => {
        const sourceNodeId = outputToNodeMap.get(dep)
        if (sourceNodeId && sourceNodeId !== node.id) {
          // Shorten the dependency label for readability
          const shortLabel = dep.length > 20 ? dep.substring(0, 18) + '...' : dep
          edges.push({
            id: `dep-${sourceNodeId}-to-${node.id}-${dep}`,
            source: sourceNodeId,
            target: node.id,
            type: 'smoothstep',
            style: { stroke: '#8b5cf6', strokeDasharray: '4,4', strokeWidth: 1.5, opacity: 0.7 },
            animated: false,
            label: shortLabel,
            labelStyle: { fill: '#8b5cf6', fontSize: 9, fontWeight: 500 },
            labelBgStyle: { fill: '#f5f3ff', fillOpacity: 0.85 },
            labelBgPadding: [3, 3] as [number, number],
            labelBgBorderRadius: 3
          })
        }
      })
    }
  })

  return edges
}

/**
 * Hook to convert plan.json to React Flow nodes and edges
 */
export function usePlanToFlow(
  plan: PlanningResponse | null,
  options: UsePlanToFlowOptions = {}
): UsePlanToFlowResult {
  const {
    showDependencyEdges = false,
    changes = null,
    completedStepIndices = [],
    variablesManifest = null,
    relayMode = false,
    onOpenVariablesSidebar,
    isLoadingVariables = false,
    layoutDirection = 'TB',
    disabled = false
  } = options

  // Get preset for code execution mode default
  const activePreset = useActiveWorkflowPreset()

  const presetUseCodeExecutionMode = activePreset?.useCodeExecutionMode ?? false

  // Get preset LLM configs
  const presetLLMConfig = activePreset?.llmConfig || undefined
  // Get available LLMs for model name formatting
  const availableLLMs = useLLMStore(state => state.availableLLMs)

  const lastComputedFlowRef = useRef<UsePlanToFlowResult>({ nodes: [], edges: [] })

  return useMemo(() => {
    if (disabled) {
      return lastComputedFlowRef.current
    }

    if (!plan || !plan.steps || plan.steps.length === 0) {
      const emptyResult = { nodes: [], edges: [] }
      lastComputedFlowRef.current = emptyResult
      return emptyResult
    }

    // Convert completedStepIndices to completedStepIds (Set of step IDs) for step_id-based matching
    // This ensures we match by step_id instead of index for better reliability
    const completedStepIds = new Set<string>()
    const convertIndicesToIds = (steps: PlanStep[], indices: number[]) => {
      indices.forEach(index => {
        if (index >= 0 && index < steps.length) {
          const step = steps[index]
          if (step?.id) {
            completedStepIds.add(step.id)
          }
        }
      })
    }
    convertIndicesToIds(plan.steps, completedStepIndices)

    // Create step ID to node ID map for next_step_id lookups
    // First pass: create all nodes to build the map
    const stepIdToNodeIdMap = new Map<string, string>()
    const buildStepIdMap = (steps: PlanStep[]) => {
      steps.forEach((step, index) => {
        const nodeId = step.id || `step-${index}`
        if (step.id) {
          stepIdToNodeIdMap.set(step.id, nodeId)
        }
      })
    }
    buildStepIdMap(plan.steps)

    // Also map orphan step IDs
    if (plan.orphan_steps) {
      plan.orphan_steps.forEach((step, index) => {
        const nodeId = `orphan-${step.id || `step-${index}`}`
        if (step.id) {
          stepIdToNodeIdMap.set(step.id, nodeId)
        }
      })
    }

    // Process all steps to create nodes and sequential edges (with change highlighting)
    const { nodes: processedNodes, edges: sequentialEdges } = processSteps(
      plan.steps,
      changes,
      presetUseCodeExecutionMode,
      presetLLMConfig,
      availableLLMs,
      options.workspacePath,
      options.selectedRunFolder,
      stepIdToNodeIdMap,
      completedStepIds
    )

    // Process orphan steps (workshop-only, not connected to main flow)
    let orphanNodes: WorkflowNode[] = []
    if (plan.orphan_steps && plan.orphan_steps.length > 0) {
      const { nodes: orphanProcessedNodes } = processSteps(
        plan.orphan_steps,
        changes,
        presetUseCodeExecutionMode,
        presetLLMConfig,
        availableLLMs,
        options.workspacePath,
        options.selectedRunFolder,
        stepIdToNodeIdMap,
        new Set<string>()  // no completed step IDs for orphan steps
      )

      // Mark all orphan nodes and remap IDs with 'orphan-' prefix. Attach how
      // many routes reuse each orphan via orphan_step_ref so the node can show
      // whether it's a shared/reused definition or genuinely unused.
      const orphanReuseCounts = countOrphanStepRefs([...(plan.steps || []), ...(plan.orphan_steps || [])])
      orphanNodes = orphanProcessedNodes.map((node) => {
        const origId = (node.data as { id?: string }).id || node.id
        return {
          ...node,
          id: `orphan-${node.id}`,
          data: {
            ...node.data,
            isOrphan: true,
            orphanReuseCount: orphanReuseCounts.get(origId) || 0,
          }
        }
      })
    }

    // Add orphan section label node if there are orphan steps
    if (orphanNodes.length > 0) {
      const orphanLabelNode: WorkflowNode = {
        id: 'orphan-label',
        type: 'start',  // Reuse start node type for simple label
        position: { x: 0, y: 0 },
        data: {
          id: 'orphan-label',
          title: 'Orphan Steps (workshop-only)',
          status: 'pending' as const,
          stepIndex: -1,
          step: {} as PlanStep
        }
      }
      orphanNodes = [orphanLabelNode, ...orphanNodes]
    }

    // Add start node
    const startNode: WorkflowNode = {
      id: 'start',
      type: 'start',
      position: { x: 0, y: 0 },
      data: {
        id: 'start',
        title: 'Start',
        status: 'completed',
        stepIndex: -1,
        step: {} as PlanStep
      }
    }

    // Add variables node (between start and first step)
    const variablesNode: WorkflowNode = {
      id: 'variables',
      type: 'variables',
      position: { x: 0, y: 0 },
      data: {
        manifest: variablesManifest,
        relayMode,
        onOpenSidebar: onOpenVariablesSidebar,
        isLoading: isLoadingVariables
      } as VariablesNodeData
    }

    // Add end node
    const endNode: WorkflowNode = {
      id: 'end',
      type: 'end',
      position: { x: 0, y: 0 },
      data: {
        id: 'end',
        title: 'End',
        status: 'pending',
        stepIndex: -1,
        step: {} as PlanStep
      }
    }

    // Node order: Start -> Execution Settings -> Variables -> Steps -> End (+ orphan nodes)
    const nodes = [startNode, variablesNode, ...processedNodes, endNode, ...orphanNodes]

    // Create edges: Start -> Execution Settings -> Variables -> First step (or End if no steps)
    const edges: WorkflowEdge[] = []

    edges.push({
      id: 'start-to-variables',
      source: 'start',
      target: 'variables',
      type: 'smoothstep',
      style: { stroke: '#6b7280', strokeWidth: 2 }
    })

    // Variables to first step (or to End if no steps)
    if (processedNodes.length > 0) {
      edges.push({
        id: 'variables-to-first',
        source: 'variables',
        target: processedNodes[0].id,
        type: 'smoothstep',
        style: { stroke: '#6b7280', strokeWidth: 2 }
      })
    } else {
      // Connect Variables to End if no steps
      edges.push({
        id: 'variables-to-end',
        source: 'variables',
        target: 'end',
        type: 'smoothstep',
        style: { stroke: '#6b7280', strokeWidth: 2 }
      })
    }

    // Add sequential edges
    edges.push(...sequentialEdges)

    // Create dependency edges (context flow) - only if enabled
    if (showDependencyEdges) {
      const dependencyEdges = createDependencyEdges(processedNodes)
      edges.push(...dependencyEdges)
    }

    // Connect a terminal step to End only when no explicit routing edge already
    // leaves it. Routing, human-input, todo-task, and agent steps own
    // their deterministic next-step edges.
    if (processedNodes.length > 0) {
      const lastNode = processedNodes[processedNodes.length - 1]
      const hasOutgoingEdge = edges.some(edge => edge.source === lastNode.id)
      if (!hasOutgoingEdge) {
        edges.push({
          id: 'last-to-end',
          source: lastNode.id,
          target: 'end',
          type: 'smoothstep',
          style: { stroke: '#6b7280', strokeWidth: 2 }
        })
      }
    }

    // CRITICAL: Position header nodes BEFORE Dagre runs
    // This ensures they're excluded from Dagre and maintain horizontal layout
    const HEADER_GAP = layoutDirection === 'TB' ? 40 : 100
    const HEADER_START_X = 80
    const HEADER_Y = 80
    
    // Position header nodes horizontally BEFORE Dagre
    const headerNodesWithPositions = nodes.map(node => {
      if (node.id === 'start') {
        return { ...node, position: { x: HEADER_START_X, y: HEADER_Y } }
      }
      if (node.id === 'variables') {
        const startDims = NODE_DIMENSIONS.start
        const varsX = HEADER_START_X + startDims.width + HEADER_GAP
        return { ...node, position: { x: varsX, y: HEADER_Y } }
      }
      return node
    })

    // Apply dagre layout (header nodes are excluded from Dagre)
    const layoutedResult = layoutWithDagre(headerNodesWithPositions, edges, layoutDirection)

    // Header nodes are already positioned correctly above, but verify and ensure they stay horizontal
    const HEADER_TO_WORKFLOW_GAP = layoutDirection === 'TB' ? 180 : 150

    const startNodeIndex = layoutedResult.nodes.findIndex(n => n.id === 'start')
    const variablesNodeIndex = layoutedResult.nodes.findIndex(n => n.id === 'variables')

    if (startNodeIndex !== -1 && variablesNodeIndex !== -1) {
      const startDims = NODE_DIMENSIONS.start
      const variablesDims = NODE_DIMENSIONS.variables

      // Calculate max height for vertical centering
      const maxHeaderHeight = Math.max(startDims.height, variablesDims.height)

      // CRITICAL: Enforce header node positions (they were set before Dagre, but ensure they're still correct)
      // Since header nodes are excluded from Dagre, they should already have correct positions
      // But we enforce them here to be absolutely sure
      // TEST: Using same large gaps as above
      let startPos = { x: HEADER_START_X, y: HEADER_Y }
      let varsPos = { x: HEADER_START_X + startDims.width + HEADER_GAP, y: HEADER_Y }

      // Enforce positions (even though they should already be correct since header nodes are excluded from Dagre)
      layoutedResult.nodes[startNodeIndex] = {
        ...layoutedResult.nodes[startNodeIndex],
        position: startPos
      }
      layoutedResult.nodes[variablesNodeIndex] = {
        ...layoutedResult.nodes[variablesNodeIndex],
        position: varsPos
      }

      // Header positions are now correctly set horizontally

      // Calculate where the workflow should start (after the header row)
      const headerRowEndX = varsPos.x + variablesDims.width

      // Find the first step node (step-0 or the first non-header node connected to variables)
      const firstStepNode = layoutedResult.nodes.find(n =>
        n.id === 'step-0' ||
        (isStepTypeNode(n) && !n.id.includes('-sub-agent-'))
      )

      if (firstStepNode) {
        // Calculate the leftmost point of this node (accounting for sub-agent overflow if it's a compound node)
        let firstStepLeftEdge = firstStepNode.position.x
        if (isAgentCoordinatorNode(firstStepNode)) {
          const data = firstStepNode.data as AgentNodeData | LegacyAgentNodeData
          const routes = data.predefined_routes
          const numSubAgents = routes?.length || 0
          
          if (numSubAgents > 0 && layoutDirection === 'LR') {
            const subAgentRowWidth = getSubAgentGridMetrics(numSubAgents, layoutDirection).width
            const parentWidth = 300
            if (subAgentRowWidth > parentWidth) {
              // Sub-agents extend further left than the parent card
              const overflow = (subAgentRowWidth - parentWidth) / 2
              firstStepLeftEdge -= overflow
            }
          }
        }

        if (layoutDirection === 'TB') {
          // Start and Variables stack VERTICALLY on the left (Start on top), and
          // the workflow graph sits to the RIGHT of that column, top-aligned with
          // Start. This keeps the tall, expandable Variables panel beside the
          // graph instead of on top of it, so it never overlaps workflow nodes.
          const VERTICAL_HEADER_GAP = 24
          const COLUMN_TO_WORKFLOW_GAP = 140
          const headerColumnWidth = Math.max(startDims.width, variablesDims.width)

          startPos = { x: HEADER_START_X, y: HEADER_Y }
          varsPos = { x: HEADER_START_X, y: HEADER_Y + startDims.height + VERTICAL_HEADER_GAP }
          layoutedResult.nodes[startNodeIndex] = { ...layoutedResult.nodes[startNodeIndex], position: startPos }
          layoutedResult.nodes[variablesNodeIndex] = { ...layoutedResult.nodes[variablesNodeIndex], position: varsPos }

          // Shift the whole dagre-laid workflow right of the header column and
          // align its top with Start. Measure the workflow's bounding box first.
          let workflowMinX = Number.POSITIVE_INFINITY
          let workflowMinY = Number.POSITIVE_INFINITY
          layoutedResult.nodes.forEach((node, index) => {
            if (node.id === 'start' || node.id === 'variables') return
            if (index === startNodeIndex || index === variablesNodeIndex) return
            if (node.type === 'end') return
            workflowMinX = Math.min(workflowMinX, node.position.x)
            workflowMinY = Math.min(workflowMinY, node.position.y)
          })
          if (!Number.isFinite(workflowMinX)) { workflowMinX = 0; workflowMinY = 0 }

          const offsetX = (HEADER_START_X + headerColumnWidth + COLUMN_TO_WORKFLOW_GAP) - workflowMinX
          const offsetY = HEADER_Y - workflowMinY

          layoutedResult.nodes = layoutedResult.nodes.map((node, index) => {
            if (node.id === 'start' || node.id === 'variables') return node
            if (index === startNodeIndex || index === variablesNodeIndex) return node
            if (node.type === 'end') return node
            return { ...node, position: { x: node.position.x + offsetX, y: node.position.y + offsetY } }
          })
        } else {
          // LR mode: workflow flows horizontally, so first step should be to the right of header
          const firstStepTargetX = headerRowEndX + HEADER_TO_WORKFLOW_GAP
          // Align the first step vertically with the center of the header row
          const firstStepTargetY = HEADER_Y + maxHeaderHeight / 2

          // Calculate offset to shift all workflow nodes
          // Use firstStepLeftEdge to ensure sub-agents don't overlap with header
          const offsetX = firstStepTargetX - firstStepLeftEdge
          const offsetY = firstStepTargetY - firstStepNode.position.y

          // Shift all non-header nodes by this offset
          layoutedResult.nodes = layoutedResult.nodes.map((node, index) => {
            // CRITICAL: Never shift header nodes - check by ID, not just index
            if (node.id === 'start' || node.id === 'variables') {
              return node // Keep header nodes in place
            }
            if (index === startNodeIndex || index === variablesNodeIndex) {
              return node // Also check by index as backup
            }
            if (node.type === 'end') {
              // End node - will be repositioned later
              return node
            }
            return {
              ...node,
              position: {
                x: node.position.x + offsetX,
                y: node.position.y + offsetY
              }
            }
          })
        }
      }
    }

    // Routing paths are spread by dagre (subtree-aware), not the manual
    // lane layout — disabled as part of the dagre-only simplification.

    // Position sub-agents relative to their parent todo_task nodes.
    // TB is the active canvas layout: children form a vertical tree, with
    // sibling routes spread horizontally by their recursive footprint.
    const parentNodeMap = new Map<string, { nodeIndex: number; subAgentIndices: number[] }>()

    // Pass 1: Find all todo task nodes first to initialize map
    layoutedResult.nodes.forEach((node, index) => {
      if (isAgentCoordinatorNode(node)) {
        parentNodeMap.set(node.id, { nodeIndex: index, subAgentIndices: [] })
      }
    })

    // Pass 2: Find all sub-agents and attach to their immediate parent
    const todoTaskParentIds = new Set(parentNodeMap.keys())
    layoutedResult.nodes.forEach((node, index) => {
      if (node.id.includes('-sub-agent-')) {
        const parentId = getImmediateSubAgentParentId(node.id, todoTaskParentIds)
        if (!parentId) return
        const parentInfo = parentNodeMap.get(parentId)
        if (parentInfo) {
          parentInfo.subAgentIndices.push(index)
        }
      }
    })

    // Position sub-agents based on layout direction
    parentNodeMap.forEach(({ nodeIndex: parentNodeIndex, subAgentIndices }) => {
      const parentNode = layoutedResult.nodes[parentNodeIndex]
      const parentDimensions = getNodeLayoutDimensions(parentNode)

      if (subAgentIndices.length === 0) return

      const subAgentDimensions = subAgentIndices.map(index => getNodeLayoutDimensions(layoutedResult.nodes[index]))
      const subAgentFootprints = subAgentIndices.map(index =>
        getNodeFootprintDimensions(layoutedResult.nodes[index], layoutedResult.nodes, todoTaskParentIds, layoutDirection)
      )

      const subAgentGrid = getSubAgentGridMetricsFromDimensions(subAgentFootprints, layoutDirection)
      const columnWidths = subAgentGrid.columnWidths
      const rowHeights = subAgentGrid.rowHeights
      const startX = parentNode.position.x + (parentDimensions.width - subAgentGrid.width) / 2
      const startY = parentNode.position.y + parentDimensions.height + SUB_AGENT_LAYOUT.parentGap

      subAgentIndices.forEach((subAgentIndex, index) => {
        const subAgent = layoutedResult.nodes[subAgentIndex]
        const dimensions = subAgentDimensions[index]
        const footprint = subAgentFootprints[index]
        const column = index % subAgentGrid.columns
        const row = Math.floor(index / subAgentGrid.columns)
        const cellX = startX + columnWidths.slice(0, column).reduce((sum, width) => sum + width, 0) + (column * SUB_AGENT_LAYOUT.cellGap)
        const cellY = startY + rowHeights.slice(0, row).reduce((sum, height) => sum + height, 0) + (row * SUB_AGENT_LAYOUT.cellGap)

        layoutedResult.nodes[subAgentIndex] = {
          ...subAgent,
          position: {
            x: cellX + (footprint.width - dimensions.width) / 2,
            y: cellY
          }
        }
      })
    })

    // After Dagre + todo_task positioning, keep validation/learning nodes
    // visually close to their parent step/decision nodes (but with overall higher spacing)
    const positionedNodes: WorkflowNode[] = layoutedResult.nodes.map(node => ({ ...node }))
    const nodeIndexById = new Map<string, number>()
    positionedNodes.forEach((node, index) => {
      nodeIndexById.set(node.id, index)
    })

    const getDimensions = (type: string | undefined) => {
      if (!type) {
        return NODE_DIMENSIONS.step
      }
      return NODE_DIMENSIONS[type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
    }

    // Group validation and learning nodes by their parent step ID
    const validationByParent = new Map<string, WorkflowNode>()
    const learningByParent = new Map<string, WorkflowNode>()

    positionedNodes.forEach(node => {
      if (node.type === 'validation') {
        const data = node.data as ValidationNodeData
        if (data.parentStepId && !data.parentStepId.includes('-sub-agent-') && !node.id.includes('-sub-agent-')) {
          validationByParent.set(data.parentStepId, node)
        }
      } else if (node.type === 'learning') {
        const data = node.data as LearningNodeData
        if (data.parentStepId && !data.parentStepId.includes('-sub-agent-') && !node.id.includes('-sub-agent-')) {
          learningByParent.set(data.parentStepId, node)
        }
      }
    })

    // Position validation nodes just to the right of their parent step
    validationByParent.forEach((validationNode, parentId) => {
      const parentIndex = nodeIndexById.get(parentId)
      if (parentIndex === undefined) return
      const parentNode = positionedNodes[parentIndex]
      const parentDims = getDimensions(parentNode.type)
      const valDims = getDimensions(validationNode.type)

      const baseX = parentNode.position.x + parentDims.width + 48
      const baseY = parentNode.position.y + (parentDims.height - valDims.height) / 2

      const validationIndex = nodeIndexById.get(validationNode.id)
      if (validationIndex === undefined) return
      positionedNodes[validationIndex] = {
        ...positionedNodes[validationIndex],
        position: { x: baseX, y: baseY }
      }
    })

    // Position learning nodes to the right of validation (if present) or parent step
    learningByParent.forEach((learningNode, parentId) => {
      const learningIndex = nodeIndexById.get(learningNode.id)
      if (learningIndex === undefined) return

      const validationNode = validationByParent.get(parentId)
      let anchorNode: WorkflowNode | null = null
      if (validationNode) {
        const vIndex = nodeIndexById.get(validationNode.id)
        if (vIndex !== undefined) {
          anchorNode = positionedNodes[vIndex]
        }
      }

      if (!anchorNode) {
        const parentIndex = nodeIndexById.get(parentId)
        if (parentIndex === undefined) return
        anchorNode = positionedNodes[parentIndex]
      }

      const anchorDims = getDimensions(anchorNode.type)
      const learnDims = getDimensions(learningNode.type)

      const baseX = anchorNode.position.x + anchorDims.width + 36
      const baseY = anchorNode.position.y + (anchorDims.height - learnDims.height) / 2

      positionedNodes[learningIndex] = {
        ...positionedNodes[learningIndex],
        position: { x: baseX, y: baseY }
      }
    })

    // Replace nodes with the adjusted positions
    layoutedResult.nodes = positionedNodes

    if (layoutDirection === 'TB') {
      layoutedResult.nodes = distributePrimaryRouteLanes(layoutedResult.nodes, plan)
    }

    // Position the end node at the end of the workflow
    const endNodeIndex = layoutedResult.nodes.findIndex(n => n.id === 'end')
    if (endNodeIndex !== -1) {
      const endDims = NODE_DIMENSIONS.end
      // Find all workflow nodes (exclude header and end nodes)
      const workflowNodes = layoutedResult.nodes.filter(n =>
        n.id !== 'start' && n.id !== 'variables' && n.id !== 'end'
      )

      if (workflowNodes.length > 0) {
        if (layoutDirection === 'TB') {
          // TB mode: end node at the bottom, centered horizontally
          const maxY = Math.max(...workflowNodes.map(n => {
            const dims = NODE_DIMENSIONS[n.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
            return n.position.y + dims.height
          }))
          const avgX = workflowNodes.reduce((sum, n) => sum + n.position.x, 0) / workflowNodes.length
          const workflowWidth = workflowNodes.reduce((max, n) => {
            const dims = NODE_DIMENSIONS[n.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
            return Math.max(max, dims.width)
          }, 0)

          layoutedResult.nodes[endNodeIndex] = {
            ...layoutedResult.nodes[endNodeIndex],
            position: {
              x: avgX + (workflowWidth - endDims.width) / 2,
              y: maxY + 100
            }
          }
        } else {
          // LR mode: end node at the right, centered vertically with the workflow
          const maxX = Math.max(...workflowNodes.map(n => {
            const dims = NODE_DIMENSIONS[n.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
            return n.position.x + dims.width
          }))
          const minY = Math.min(...workflowNodes.map(n => n.position.y))
          const maxY = Math.max(...workflowNodes.map(n => {
            const dims = NODE_DIMENSIONS[n.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
            return n.position.y + dims.height
          }))
          const centerY = (minY + maxY) / 2

          layoutedResult.nodes[endNodeIndex] = {
            ...layoutedResult.nodes[endNodeIndex],
            position: {
              x: maxX + 100,
              y: centerY - endDims.height / 2
            }
          }
        }
      }
    }

    // Global collision resolution is disabled — dagre already separates nodes by
    // their subtree extent (nodesep is the floor), so the extra shove-apart pass
    // (which ignored tree grouping) is no longer needed.

    if (layoutDirection === 'TB') {
      const startHeaderNode = layoutedResult.nodes.find(node => node.id === 'start')
      const variablesHeaderNode = layoutedResult.nodes.find(node => node.id === 'variables')
      const workflowNodes = layoutedResult.nodes.filter(node =>
        node.id !== 'start' &&
        node.id !== 'variables' &&
        !node.id.startsWith('orphan-')
      )

      if (startHeaderNode && variablesHeaderNode && workflowNodes.length > 0) {
        const startDims = getNodeLayoutDimensions(startHeaderNode)
        const variablesDims = getNodeLayoutDimensions(variablesHeaderNode)
        const headerBottom = Math.max(
          startHeaderNode.position.y + startDims.height,
          variablesHeaderNode.position.y + variablesDims.height
        )
        const minWorkflowTop = headerBottom + HEADER_TO_WORKFLOW_GAP
        const currentWorkflowTop = Math.min(...workflowNodes.map(node => node.position.y))
        const shiftY = minWorkflowTop - currentWorkflowTop

        if (shiftY > 0) {
          layoutedResult.nodes = layoutedResult.nodes.map(node => {
            if (node.id === 'start' || node.id === 'variables' || node.id.startsWith('orphan-')) {
              return node
            }
            return {
              ...node,
              position: {
                x: node.position.x,
                y: node.position.y + shiftY
              }
            }
          })
        }
      }
    }

    // Inject read-only context into step-type nodes.
    layoutedResult.nodes = layoutedResult.nodes.map(node => {
      if (node.type === 'step' || node.type === 'human_input' || node.type === 'todo_task' || node.type === 'agent') {
        return {
          ...node,
          data: {
            ...node.data,
            workspacePath: options.workspacePath,
            selectedRunFolder: options.selectedRunFolder
          }
        } as WorkflowNode
      }

      return node
    }) as WorkflowNode[]

    // Log critical nodes only

    // Position orphan nodes below the main flow
    if (orphanNodes.length > 0) {
      // Find max Y position of all non-orphan nodes
      const mainNodes = layoutedResult.nodes.filter(n => !n.id.startsWith('orphan-'))
      const maxY = Math.max(...mainNodes.map(n => {
        const dims = NODE_DIMENSIONS[n.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
        return n.position.y + dims.height
      }))

      const ORPHAN_GAP = 200  // Gap between main flow and orphan section
      const ORPHAN_SPACING = 30  // Spacing between orphan nodes

      // Position orphan nodes below the main flow
      let currentX = HEADER_START_X
      layoutedResult.nodes = layoutedResult.nodes.map(node => {
        if (!node.id.startsWith('orphan-')) return node

        if (node.id === 'orphan-label') {
          return {
            ...node,
            position: { x: HEADER_START_X, y: maxY + ORPHAN_GAP }
          }
        }

        const dims = NODE_DIMENSIONS[node.type as keyof typeof NODE_DIMENSIONS] || NODE_DIMENSIONS.step
        const positioned = {
          ...node,
          position: { x: currentX, y: maxY + ORPHAN_GAP + 60 }  // 60px below label
        }
        currentX += dims.width + ORPHAN_SPACING
        return positioned
      })
    }

    lastComputedFlowRef.current = layoutedResult
    return layoutedResult
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [disabled, plan, showDependencyEdges, changes, presetUseCodeExecutionMode, presetLLMConfig, availableLLMs, completedStepIndices, options.workspacePath, options.selectedRunFolder, variablesManifest, relayMode, onOpenVariablesSidebar, isLoadingVariables, layoutDirection])
}

export default usePlanToFlow
