import type {
  SessionExecutionTreeNode,
  SessionExecutionTreeResponse,
  TerminalSnapshot,
} from '../services/api-types'

const HIDDEN_EXECUTION_KINDS = new Set([
  'session_root',
  'main_agent',
  'synthetic_turn',
])

function normalizedExecutionStatus(status?: string): string {
  return (status || '').trim().toLowerCase()
}

function isLiveExecution(node: SessionExecutionTreeNode): boolean {
  return ['starting', 'queued', 'pending', 'running', 'waiting'].includes(
    normalizedExecutionStatus(node.status),
  )
}

// Event-stream nodes describe activity, not process ownership. A tool call or
// turn can have its own execution id and parent edge without ever owning a
// terminal. Projecting such a node as an unpublished terminal creates a
// permanent "Waiting for terminal" pane because no terminal can arrive for it.
//
// Concrete background/workflow registries remain the authority for temporary
// child rows. Event data may still enrich a terminal that already exists via
// terminalMatchesExecution; it simply cannot invent one.
function canProjectUnpublishedTerminal(node: SessionExecutionTreeNode): boolean {
  return (node.source || '').trim().toLowerCase() !== 'event_stream'
}

// A node's parent edge is only usable when it names a DIFFERENT execution.
// A self-parent edge (parent_execution_id === execution_id) is malformed input:
// resolving it returns the node itself, which rendered as the nonsensical
// "PULSE FINALIZER · Child of PULSE FINALIZER" rail row.
function parentExecutionIDOf(node: SessionExecutionTreeNode): string {
  const parentID = (node.parent_execution_id || '').trim()
  if (!parentID || parentID === node.execution_id) return ''
  return parentID
}

// A sequential main-agent turn is the SAME conversation as the root, not a
// child process. Kind alone is not sufficient: a node that arrives with an
// empty/unknown kind would otherwise fall through to a `background_agent`
// placeholder. Ownership is therefore checked independently of kind.
function isMainConversationExecution(node: SessionExecutionTreeNode, root: SessionExecutionTreeNode): boolean {
  const executionID = (node.execution_id || '').trim()
  if (!executionID) return true
  if (executionID === root.execution_id) return true
  if (executionID.startsWith('main:')) return true
  // Same owner as the root session's main conversation.
  if (executionID === `main:${node.session_id}`) return true
  // A self-parent edge is malformed input, and it is precisely how a sequential
  // main-agent turn arrived misprojected: the node claimed to be its own child.
  // Collapse it back into the main conversation. Merely relabelling it would
  // still leave a phantom rail row that hides real progress — which is the
  // defect PLAT-107 reports, not just the "Child of itself" caption.
  const declaredParent = (node.parent_execution_id || '').trim()
  if (declaredParent && declaredParent === executionID) return true
  const kind = (node.kind || '').trim().toLowerCase()
  if (HIDDEN_EXECUTION_KINDS.has(kind)) return true
  // An unclassified node that carries no distinct parent cannot be shown to be
  // a genuine child, so it stays in the main timeline rather than inventing a
  // placeholder terminal for it.
  if (!kind && !parentExecutionIDOf(node)) return true
  return false
}

function isVisibleChildExecution(node: SessionExecutionTreeNode, root: SessionExecutionTreeNode): boolean {
  if (!node.execution_id) return false
  return !isMainConversationExecution(node, root)
}

function flattenExecutionTree(root: SessionExecutionTreeNode): SessionExecutionTreeNode[] {
  const nodes: SessionExecutionTreeNode[] = []
  const pending = [root]
  const seen = new Set<string>()
  while (pending.length > 0) {
    const node = pending.shift()
    if (!node || !node.execution_id || seen.has(node.execution_id)) continue
    seen.add(node.execution_id)
    nodes.push(node)
    pending.push(...(node.children || []))
  }
  return nodes
}

function terminalMatchesExecution(terminal: TerminalSnapshot, node: SessionExecutionTreeNode): boolean {
  if (terminal.session_id !== node.session_id) return false
  if (terminal.execution_id === node.execution_id ||
    terminal.owner_id === node.execution_id ||
    terminal.terminal_id === `${node.session_id}:${node.execution_id}`) {
    return true
  }

  // A agent item is a lifecycle child, not a separate agent
  // process. Its item notification gets a unique `msgseq-*` execution ID,
  // while the structured agent and its events live under the parent workflow
  // step terminal (`workflow-step:<parent execution>:<step id>`). Treat that
  // published parent terminal as the item's concrete terminal. Without this
  // bridge, the rail creates an empty synthetic row which can only display the
  // misleading “Waiting for terminal” screen even as the item is making tool
  // calls.
  const parentExecutionID = parentExecutionIDOf(node)
  if (!parentExecutionID) return false
  const parentTerminalPrefix = `workflow-step:${parentExecutionID}:`
  return [terminal.execution_id, terminal.owner_id, terminal.terminal_id]
    .filter((value): value is string => Boolean(value))
    .some(value => value === parentExecutionID || value.includes(parentTerminalPrefix))
}

function runningTerminalState(node: SessionExecutionTreeNode): TerminalSnapshot['state'] {
  const status = normalizedExecutionStatus(node.status)
  if (status === 'failed') return 'failed'
  if (status === 'canceled' || status === 'cancelled') return 'completed'
  if (isLiveExecution(node)) return 'running'
  return 'completed'
}

/**
 * Project live execution-tree children into the terminal rail while their
 * detailed terminal snapshot is still being created. The projection is
 * deliberately ephemeral: as soon as a real terminal with the same execution
 * identity appears, it is enriched in place and the placeholder disappears.
 */
export function projectExecutionTreeTerminals(
  terminals: TerminalSnapshot[],
  tree?: SessionExecutionTreeResponse | null,
): TerminalSnapshot[] {
  if (!tree?.root) return terminals

  let projected = terminals
  const nodes = flattenExecutionTree(tree.root)
  const nodesByID = new Map(nodes.map(node => [node.execution_id, node]))

  for (const node of nodes) {
    if (!isVisibleChildExecution(node, tree.root)) continue

    const matchingIndexes: number[] = []
    projected.forEach((terminal, index) => {
      if (terminalMatchesExecution(terminal, node)) matchingIndexes.push(index)
    })

    if (matchingIndexes.length > 0) {
      // Clone only when execution-tree metadata adds information or corrects a
      // stale retained "completed" snapshot for an execution that is live.
      const next = [...projected]
      for (const index of matchingIndexes) {
        const terminal = next[index]
        const live = isLiveExecution(node)
        next[index] = {
          ...terminal,
          parent_execution_id: parentExecutionIDOf(node) || terminal.parent_execution_id,
          execution_kind: terminal.execution_kind || node.kind,
          agent_name: terminal.agent_name || node.name,
          display_title: terminal.display_title || node.name,
          ...(live ? {
            active: true,
            state: 'running',
            process_state: terminal.process_state || 'live',
            snapshot_kind: terminal.snapshot_kind || 'live',
          } : {}),
        }
      }
      projected = next
      continue
    }

    // Completed historical tree nodes do not need another retained UI row.
    // Only a currently live, process-owned child can otherwise become
    // invisible. Generic event nodes are activity in an existing conversation,
    // not terminals waiting to be published.
    if (!isLiveExecution(node) || !canProjectUnpublishedTerminal(node)) continue

    const parentExecutionID = parentExecutionIDOf(node)
    const parent = parentExecutionID ? nodesByID.get(parentExecutionID) : undefined
    const startedAt = node.started_at || new Date(0).toISOString()
    projected = [...projected, {
      terminal_id: `${node.session_id}:${node.execution_id}`,
      session_id: node.session_id,
      owner_id: node.execution_id,
      execution_id: node.execution_id,
      parent_execution_id: parentExecutionID || undefined,
      execution_kind: node.kind || 'background_agent',
      agent_name: node.name || 'Background agent',
      display_title: node.name || 'Background agent',
      display_meta: parent?.name ? `Child of ${parent.name}` : 'Asynchronous child',
      content_source: 'execution_tree',
      execution_tree_placeholder: true,
      content: '',
      rows: [],
      chunk_index: 0,
      active: true,
      state: runningTerminalState(node),
      process_state: 'live',
      snapshot_kind: 'live',
      status: {
        status_text: 'Running',
      },
      created_at: startedAt,
      updated_at: startedAt,
    }]
  }

  return projected
}
