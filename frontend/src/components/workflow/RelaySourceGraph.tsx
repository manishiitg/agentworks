import { useCallback, useMemo, useState } from 'react'
import { Background, BaseEdge, Controls, Handle, MarkerType, Position, ReactFlow, type EdgeProps, type Node, type NodeProps } from '@xyflow/react'
import dagre from 'dagre'
import { Bot, Code2, GitBranch, LogIn, LogOut, X } from 'lucide-react'
import '@xyflow/react/dist/style.css'
import { parseRelaySourceGraph, type RelaySourceNode } from './relayGraphAnnotations'
import { routeColorForIndex } from './routeColors'

export interface RelayRecordedCall {
  id: string
  name: string
  status: string
  provider?: string
  model?: unknown
  output?: unknown
  error?: string
  tools?: { name: string; args?: unknown; result?: unknown; error?: string }[]
}
type GraphNode = Node<{ annotation: RelaySourceNode; calls: RelayRecordedCall[] }, 'relay-source'>
const icons = { input: LogIn, agent: Bot, script: Code2, decision: GitBranch, output: LogOut }
function SourceNode({ data, selected }: NodeProps<GraphNode>) {
  const Icon = icons[data.annotation.type]
  const last = data.calls.at(-1)
  return <div className={`h-[90px] w-[230px] rounded-xl border bg-background p-3 shadow-sm ${selected ? 'border-primary ring-2 ring-primary/20' : 'border-border'}`}>
    {data.annotation.type !== 'input' && <Handle type="target" position={Position.Top} />}
    <div className="flex items-center gap-2 text-xs text-muted-foreground"><Icon className="h-4 w-4" /><span className="capitalize">{data.annotation.type}</span>{last && <span className={`ml-auto ${last.status === 'failed' ? 'text-destructive' : last.status === 'completed' ? 'text-green-600 dark:text-green-400' : 'text-primary'}`}>{last.status}{data.calls.length > 1 ? ` ×${data.calls.length}` : ''}</span>}</div>
    <div className="mt-2 truncate text-sm font-semibold" title={data.annotation.label}>{data.annotation.label}</div>
    {data.annotation.type !== 'output' && <Handle type="source" position={Position.Bottom} />}
  </div>
}
const nodeTypes = { 'relay-source': SourceNode }
const noCalls: RelayRecordedCall[] = []
// Use Dagre's routed points so skip branches do not cross intermediate nodes.
function SourceEdge({ id, sourceX, sourceY, targetX, targetY, markerEnd, style, label, data }: EdgeProps) {
  const route = data as { points: { x: number; y: number }[]; x?: number; y?: number } | undefined
  const points = [{ x: sourceX, y: sourceY }, ...(route?.points.slice(1, -1) ?? []), { x: targetX, y: targetY }]
  const path = points.map((point, i) => `${i ? 'L' : 'M'} ${point.x} ${point.y}`).join(' ')
  return <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} label={label} labelX={route?.x} labelY={route?.y} labelStyle={{ fill: 'hsl(var(--foreground))', fontSize: 11 }} labelBgStyle={{ fill: 'hsl(var(--background))' }} />
}
const edgeTypes = { 'relay-source': SourceEdge }
function Value({ value }: { value: unknown }) {
  return <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/40 p-2 text-xs">{typeof value === 'string' ? value : JSON.stringify(value, null, 2)}</pre>
}

/** Reuses the workflow canvas's React Flow/Dagre stack, without a workflow plan. */
export function RelaySourceGraph({ source, calls = noCalls, onBuild, onCode }: {
  source: string
  calls?: RelayRecordedCall[]
  onBuild: () => void
  onCode?: () => void
}) {
  const graph = useMemo(() => parseRelaySourceGraph(source), [source])
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const onSelectionChange = useCallback(({ nodes }: { nodes: Node[] }) => {
    setSelectedID(nodes[0]?.id ?? null)
  }, [])
  const selected = graph.nodes.find(node => node.id === selectedID)
  const selectedCalls = selected?.type === 'agent' ? calls.filter(call => call.name === (selected.call || selected.id)) : []
  const layout = useMemo(() => {
    const g = new dagre.graphlib.Graph({ multigraph: true })
    g.setGraph({ rankdir: 'TB', nodesep: 65, ranksep: 80 }); g.setDefaultEdgeLabel(() => ({}))
    graph.nodes.forEach(node => g.setNode(node.id, { width: 230, height: 90 }))
    graph.edges.forEach((edge, i) => g.setEdge(edge.from, edge.to, { width: edge.label ? Math.max(160, edge.label.length * 7) : 0, height: edge.label ? 24 : 0 }, String(i)))
    if (!graph.errors.length) dagre.layout(g)
    return {
      nodes: graph.nodes.map(annotation => {
        const position = g.node(annotation.id)
        return { id: annotation.id, type: 'relay-source' as const, ariaLabel: annotation.label, position: { x: (position.x ?? 115) - 115, y: (position.y ?? 45) - 45 }, data: { annotation, calls: annotation.type === 'agent' ? calls.filter(call => call.name === (annotation.call || annotation.id)) : [] }, selected: annotation.id === selectedID }
      }),
      edges: graph.edges.map((edge, index) => ({ id: `relay-edge-${index}`, source: edge.from, target: edge.to, label: edge.label, type: 'relay-source', data: g.edge({ v: edge.from, w: edge.to, name: String(index) }), markerEnd: { type: MarkerType.ArrowClosed, color: routeColorForIndex(index) }, style: { stroke: routeColorForIndex(index), strokeWidth: 2 }, labelStyle: { fill: 'hsl(var(--foreground))', fontSize: 11 }, labelBgStyle: { fill: 'hsl(var(--background))' } })),
    }
  }, [graph, calls, selectedID])
  if (graph.errors.length) return <div className="p-5"><h3 className="text-sm font-semibold">Graph comments need a correction</h3><ul role="alert" className="mt-3 space-y-1 text-xs text-destructive">{graph.errors.map((error, i) => <li key={i}>{error}</li>)}</ul><p className="mt-3 text-xs text-muted-foreground">This affects the graph display. Python still controls execution.</p><button type="button" onClick={onBuild} className="mt-4 rounded border border-border px-3 py-2 text-sm">Fix in chat</button></div>
  if (!graph.nodes.length) return <div className="mx-auto max-w-xl p-6"><h3 className="text-lg font-semibold">Show how your Relay works</h3><p className="mt-2 text-sm leading-6 text-muted-foreground">Ask the builder to add graph comments to this Relay. Its inputs, agents, tools, decisions and result will appear here.</p><button type="button" onClick={onBuild} className="mt-5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">Add graph in chat</button></div>
  return <div className="flex min-h-[350px] flex-1 flex-col overflow-hidden">
    <div className="relative min-h-[300px] flex-1" aria-label="Relay graph"><div className="absolute inset-0"><ReactFlow nodes={layout.nodes} edges={layout.edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes} nodesDraggable={false} nodesConnectable={false} edgesReconnectable={false} onNodeClick={(_, node) => setSelectedID(node.id)} onSelectionChange={onSelectionChange} onPaneClick={() => setSelectedID(null)} fitView fitViewOptions={{ padding: 0.15, maxZoom: 1 }} minZoom={0.1} maxZoom={1.5} colorMode="system" proOptions={{ hideAttribution: true }}><Background /><Controls showInteractive={false} /></ReactFlow></div></div>
    {selected && <section className="max-h-[45%] shrink-0 overflow-auto border-t border-border p-4" aria-label="Relay node details"><header className="flex items-center justify-between gap-2"><h3 className="text-sm font-semibold">{selected.label}</h3><button type="button" aria-label="Close node details" onClick={() => setSelectedID(null)}><X className="h-4 w-4" /></button></header><p className="mt-1 text-xs text-muted-foreground">{selected.type} · Source annotation at line {selected.line}</p>{selected.description && <p className="mt-2 text-sm">{selected.description}</p>}
      <div className="mt-3 space-y-3">{([['Inputs', selected.input], ['Output', selected.output], ['System prompt', selected.system_prompt], ['User message', selected.user_message], ['Message sequence', selected.messages], ['Model', selected.model], ['Tools', selected.tools], ['Skills', selected.skills], ['MCP connections', selected.mcp]] as const).map(([label, value]) => value !== undefined && <details key={label}><summary className="cursor-pointer text-xs font-medium">{label}</summary><Value value={value} /></details>)}</div>
      {selected.type === 'agent' && calls.length > 0 && <div className="mt-4"><h4 className="text-xs font-semibold">Recorded agent calls</h4>{selectedCalls.length === 0 ? <p className="mt-1 text-xs text-muted-foreground">No recorded call matches {selected.call || selected.id}.</p> : selectedCalls.map(call => <details key={call.id} className="mt-2"><summary className="cursor-pointer text-xs">{call.id} · {call.status}</summary>{call.model !== undefined && <Value value={call.model} />}{call.output !== undefined && <Value value={call.output} />}{call.error && <p className="text-xs text-destructive">{call.error}</p>}{call.tools?.map((tool, i) => <div key={i} className="mt-2 text-xs"><strong>Tool: {tool.name}</strong>{tool.result !== undefined && <Value value={tool.result} />}{tool.error && <p className="text-destructive">{tool.error}</p>}</div>)}</details>)}</div>}
      {onCode && <button type="button" onClick={onCode} className="mt-4 inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><Code2 className="h-3.5 w-3.5" />View code</button>}
    </section>}
  </div>
}
