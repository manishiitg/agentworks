import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Background, BaseEdge, Controls, getViewportForBounds, Handle, MarkerType, Position, ReactFlow, type EdgeProps, type Node, type NodeProps, type ReactFlowInstance } from '@xyflow/react'
import dagre from 'dagre'
import { Bot, CheckCircle2, CircleAlert, Code2, GitBranch, LoaderCircle, LogIn, LogOut, RotateCcw, Tags, X } from 'lucide-react'
import '@xyflow/react/dist/style.css'
import { parseRelaySourceGraph, type RelaySourceNode, type RelaySourceGraph as SourceGraph } from './relayGraphAnnotations'
import { routeColorForIndex } from './routeColors'

export interface RelayRecordedCall {
  id: string
  name: string
  status: string
  type?: 'classifier'
  categories?: unknown
  instructions?: string
  provider?: string
  model?: unknown
  output?: unknown
  error?: string
  started_at?: string | number
  completed_at?: string | number
  checkpoint_reused?: boolean
  tools?: { name: string; args?: unknown; result?: unknown; error?: string }[]
}
type GraphNode = Node<{ annotation: RelaySourceNode; calls: RelayRecordedCall[]; recorded: boolean }, 'relay-source'>
const icons = { input: LogIn, agent: Bot, classifier: Tags, script: Code2, decision: GitBranch, output: LogOut }
const tones = {
  input: { ink: 'text-teal-700 dark:text-teal-300', tint: 'bg-teal-500/10', border: 'border-teal-500/35', stroke: '#0d9488' },
  agent: { ink: 'text-blue-700 dark:text-blue-300', tint: 'bg-blue-500/10', border: 'border-blue-500/35', stroke: '#3b82f6' },
  classifier: { ink: 'text-violet-700 dark:text-violet-300', tint: 'bg-violet-500/10', border: 'border-violet-500/35', stroke: '#8b5cf6' },
  script: { ink: 'text-cyan-700 dark:text-cyan-300', tint: 'bg-cyan-500/10', border: 'border-cyan-500/35', stroke: '#0891b2' },
  decision: { ink: 'text-amber-700 dark:text-amber-300', tint: 'bg-amber-500/10', border: 'border-amber-500/35', stroke: '#d97706' },
  output: { ink: 'text-emerald-700 dark:text-emerald-300', tint: 'bg-emerald-500/10', border: 'border-emerald-500/35', stroke: '#059669' },
}
function titleFor(node: RelaySourceNode) {
  const choice = node.type === 'decision' && node.label.match(/^\w+\[['"]label['"]\]\s*==\s*(['"])([^'"]+)\1$/)
  if (choice) return `Category is ${choice[2]}?`
  const label = /^[a-zA-Z_]\w*$/.test(node.label) ? node.label.replaceAll('_', ' ') : node.label
  return label.charAt(0).toUpperCase() + label.slice(1)
}
function callDuration(call?: RelayRecordedCall) {
  const millis = (value?: string | number) => typeof value === 'number' ? value * (value < 1e12 ? 1000 : 1) : value ? Date.parse(value) : NaN
  const duration = millis(call?.completed_at) - millis(call?.started_at)
  return Number.isFinite(duration) && duration >= 0 ? duration < 1000 ? `${Math.round(duration)} ms` : `${(duration / 1000).toFixed(1)} s` : undefined
}
function modelName(value: unknown): string | undefined {
  if (typeof value === 'string') return value
  if (value && typeof value === 'object' && 'model_id' in value && typeof value.model_id === 'string') return value.model_id
}
const failedStatuses = new Set(['failed', 'stopped', 'interrupted', 'reconciliation_required'])
function nodeHeight(node: RelaySourceNode) {
  return node.type === 'input' || node.type === 'output' ? 84 : node.type === 'decision' ? 100 : 130
}
function Status({ status }: { status?: string }) {
  if (!status) return null
  const done = status === 'completed'
  const failed = failedStatuses.has(status)
  const active = status === 'running'
  const Icon = done ? CheckCircle2 : failed ? CircleAlert : active ? LoaderCircle : undefined
  return <span className={`inline-flex items-center gap-1 text-[11px] font-medium ${done ? 'text-emerald-700 dark:text-emerald-300' : failed ? 'text-destructive' : 'text-muted-foreground'}`}>
    {Icon && <Icon className={`h-3.5 w-3.5 ${active ? 'animate-spin motion-reduce:animate-none' : ''}`} />}<span className="capitalize">{status.replaceAll('_', ' ')}</span>
  </span>
}
function SourceNode({ data, selected }: NodeProps<GraphNode>) {
  const node = data.annotation
  const Icon = icons[node.type]
  const tone = tones[node.type]
  const last = data.calls.at(-1)
  const status = last?.status ?? (data.recorded ? node.status : undefined)
  const categories = node.categories && typeof node.categories === 'object' ? Object.keys(node.categories) : []
  const output = last?.output
  const category = output && typeof output === 'object' && 'label' in output && typeof output.label === 'string' ? output.label : undefined
  const model = modelName(last?.model ?? node.model)
  const duration = callDuration(last)
  const cap = node.type === 'input' || node.type === 'output'
  const description = category ? `Chosen category: ${category}` : model ?? node.description
  return <div className={`relative flex h-full w-full flex-col border bg-background shadow-sm ${cap ? 'rounded-2xl' : 'rounded-xl'} ${selected ? 'border-primary ring-2 ring-primary/25' : failedStatuses.has(status ?? '') ? 'border-destructive/50' : tone.border}`}>
    <div className={`absolute inset-x-0 top-0 h-1 ${tone.tint}`} style={{ backgroundColor: tone.stroke }} />
    {node.type !== 'input' && <Handle type="target" position={Position.Top} style={{ backgroundColor: tone.stroke }} />}
    <div className="flex min-h-0 flex-1 flex-col p-3">
      <div className="flex items-center gap-2">
        <span className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-lg ${tone.tint} ${tone.ink}`}><Icon className="h-4 w-4" /></span>
        <span className={`text-[11px] font-semibold uppercase tracking-wide ${tone.ink}`}>{node.type === 'script' ? 'Action' : node.type}</span>
        <span className="ml-auto"><Status status={status} /></span>
        {data.recorded && node.sequence !== undefined && <span aria-label={`Step ${node.sequence}`} className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${tone.tint} ${tone.ink}`}>{node.sequence}</span>}
      </div>
      <div className="mt-2 line-clamp-2 text-[15px] font-semibold leading-5" title={node.label}>{titleFor(node)}</div>
      {!cap && node.type !== 'decision' && <div className="mt-auto pt-2 text-[11px] text-muted-foreground">
        {description ? <p className="truncate" title={description}>{description}</p> : <p>{node.type === 'agent' ? 'Agent step' : node.type === 'classifier' ? 'Choose a category' : 'Scripted step'}</p>}
        {categories.length > 0 ? <div className="mt-1 flex items-center gap-1 overflow-hidden">{categories.slice(0, 2).map(label => <span key={label} className={`max-w-[68px] truncate rounded px-1.5 py-0.5 ${tone.tint} ${tone.ink}`} title={label}>{label}</span>)}{categories.length > 2 && <span className="rounded bg-muted px-1.5 py-0.5">+{categories.length - 2}</span>}{duration && <span className="ml-auto shrink-0">{duration}</span>}</div> : <div className="mt-1 flex items-center gap-2">{duration && <span>{duration}</span>}{last?.checkpoint_reused && <span className="inline-flex items-center gap-1"><RotateCcw className="h-3 w-3" />Reused</span>}{data.calls.length > 1 && <span>{data.calls.length} calls</span>}</div>}
      </div>}
    </div>
    {node.type !== 'output' && <Handle type="source" position={Position.Bottom} style={{ backgroundColor: tone.stroke }} />}
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
export function RelaySourceGraph({ source, graph: suppliedGraph, calls = noCalls, onBuild, onCode }: {
  source: string
  graph?: SourceGraph
  calls?: RelayRecordedCall[]
  onBuild: () => void
  onCode?: () => void
}) {
  const graph = useMemo(() => suppliedGraph ?? parseRelaySourceGraph(source), [source, suppliedGraph])
  const canvas = useRef<HTMLDivElement>(null)
  const flow = useRef<ReactFlowInstance<GraphNode> | null>(null)
  const userAdjusted = useRef(false)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const onSelectionChange = useCallback(({ nodes }: { nodes: Node[] }) => {
    setSelectedID(nodes[0]?.id ?? null)
  }, [])
  const selected = graph.nodes.find(node => node.id === selectedID)
  const matches = (call: RelayRecordedCall, node: RelaySourceNode) => call.name === (node.call || node.id) || call.id === node.call
  const selectedCalls = selected ? calls.filter(call => matches(call, selected)) : []
  const layout = useMemo(() => {
    const g = new dagre.graphlib.Graph({ multigraph: true })
    g.setGraph({ rankdir: 'TB', nodesep: 44, ranksep: 32 }); g.setDefaultEdgeLabel(() => ({}))
    graph.nodes.forEach(node => g.setNode(node.id, { width: 260, height: nodeHeight(node) }))
    // Dagre defaults to labels beside the route; centre them on their connector.
    graph.edges.forEach((edge, i) => g.setEdge(edge.from, edge.to, { width: edge.label ? Math.max(160, edge.label.length * 7) : 0, height: edge.label ? 24 : 0, labelpos: 'c' }, String(i)))
    if (!graph.errors.length) dagre.layout(g)
    return {
      nodes: graph.nodes.map(annotation => {
        const position = g.node(annotation.id)
        const height = nodeHeight(annotation)
        return { id: annotation.id, type: 'relay-source' as const, ariaLabel: titleFor(annotation), width: 260, height, style: { width: 260, height }, position: { x: (position.x ?? 130) - 130, y: (position.y ?? height / 2) - height / 2 }, data: { annotation, calls: calls.filter(call => matches(call, annotation)), recorded: !!graph.recorded }, selected: annotation.id === selectedID }
      }),
      edges: graph.edges.map((edge, index) => {
        const target = graph.nodes.find(node => node.id === edge.to)
        const targetCall = target && calls.find(call => matches(call, target))
        const status = targetCall?.status ?? target?.status
        const color = graph.recorded ? failedStatuses.has(status ?? '') ? 'hsl(var(--destructive))' : status === 'completed' ? '#059669' : '#64748b' : routeColorForIndex(index)
        return { id: `relay-edge-${index}`, source: edge.from, target: edge.to, label: edge.label, type: 'relay-source', data: g.edge({ v: edge.from, w: edge.to, name: String(index) }), markerEnd: { type: MarkerType.ArrowClosed, color }, style: { stroke: color, strokeWidth: graph.recorded ? 3 : 2 }, labelStyle: { fill: 'hsl(var(--foreground))', fontSize: 11 }, labelBgStyle: { fill: 'hsl(var(--background))' } }
      }),
    }
  }, [graph, calls, selectedID])
  // Fit against the panel's real dimensions, including when it becomes visible
  // or is resized. Initial React Flow measurement can happen in a narrow pane.
  const shape = JSON.stringify([graph.nodes.map(node => [node.id, node.type, node.label]), graph.edges])
  const fitCanvas = useCallback(() => {
    const element = canvas.current
    const instance = flow.current
    if (userAdjusted.current || !element || !instance || !element.clientWidth || !element.clientHeight || !layout.nodes.length) return
    const xs = layout.nodes.map(node => node.position.x), ys = layout.nodes.map(node => node.position.y)
    const bounds = { x: Math.min(...xs), y: Math.min(...ys), width: Math.max(...xs.map(x => x + 260)) - Math.min(...xs), height: Math.max(...layout.nodes.map(node => node.position.y + node.height)) - Math.min(...ys) }
    void instance.setViewport(getViewportForBounds(bounds, element.clientWidth, element.clientHeight, 0.1, 1, 0.12))
  }, [layout.nodes])
  const latestFit = useRef(fitCanvas)
  latestFit.current = fitCanvas
  useEffect(() => {
    if (!canvas.current) return
    userAdjusted.current = false
    let frame = 0
    const resize = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(() => latestFit.current()) }
    const observer = new ResizeObserver(resize)
    observer.observe(canvas.current)
    resize()
    return () => { observer.disconnect(); cancelAnimationFrame(frame) }
  }, [shape])
  if (graph.errors.length) return <div className="p-5"><h3 className="text-sm font-semibold">{graph.native ? 'Python source needs a correction' : 'Graph comments need a correction'}</h3><ul role="alert" className="mt-3 space-y-1 text-xs text-destructive">{graph.errors.map((error, i) => <li key={i}>{error}</li>)}</ul><p className="mt-3 text-xs text-muted-foreground">This affects the graph display. Python still controls execution.</p><button type="button" onClick={onBuild} className="mt-4 rounded border border-border px-3 py-2 text-sm">Fix in chat</button></div>
  if (!graph.nodes.length) return <div className="mx-auto max-w-xl p-6"><h3 className="text-lg font-semibold">Show how your Flow works</h3><p className="mt-2 text-sm leading-6 text-muted-foreground">Ask the builder to add graph comments to this Flow. Its inputs, agents, tools, decisions and result will appear here.</p><button type="button" onClick={onBuild} className="mt-5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">Add graph in chat</button></div>
  return <div className="flex min-h-[350px] flex-1 flex-col overflow-hidden">
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border/60 bg-muted/20 px-4 py-2 text-[11px] text-muted-foreground" aria-label="Graph legend">
      {graph.recorded ? <><span className="inline-flex items-center gap-1.5 font-medium text-foreground"><span className="h-0.5 w-5 bg-emerald-500" />Execution path</span><span>{calls.length} recorded {calls.length === 1 ? 'step' : 'steps'}</span><span>Numbers follow the recorded order</span></> : (['classifier', 'agent', 'script', 'decision'] as const).map(type => {
        const count = graph.nodes.filter(node => node.type === type).length
        return count > 0 && <span key={type} className="inline-flex items-center gap-1.5"><span className="h-2 w-2 rounded-full" style={{ backgroundColor: tones[type].stroke }} />{count} {type === 'script' ? count === 1 ? 'action' : 'actions' : count === 1 ? type : `${type}s`}</span>
      })}
    </div>
    <div ref={canvas} className="relative min-h-[300px] flex-1" aria-label="Flow graph"><div className="absolute inset-0"><ReactFlow<GraphNode> nodes={layout.nodes} edges={layout.edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes} nodesDraggable={false} nodesConnectable={false} edgesReconnectable={false} onInit={instance => { flow.current = instance; latestFit.current() }} onMoveStart={event => { if (event) userAdjusted.current = true }} onNodeClick={(_, node) => setSelectedID(node.id)} onSelectionChange={onSelectionChange} onPaneClick={() => setSelectedID(null)} minZoom={0.1} maxZoom={1.5} colorMode="system" proOptions={{ hideAttribution: true }}><Background gap={22} size={0.6} /><Controls showInteractive={false} onZoomIn={() => { userAdjusted.current = true }} onZoomOut={() => { userAdjusted.current = true }} onFitView={() => { userAdjusted.current = false; latestFit.current() }} /></ReactFlow></div></div>
    {selected && <section className="max-h-[45%] shrink-0 overflow-auto border-t border-border p-4" aria-label="Flow node details"><header className="flex items-center justify-between gap-2"><h3 className="text-sm font-semibold">{titleFor(selected)}</h3><button type="button" aria-label="Close node details" onClick={() => setSelectedID(null)}><X className="h-4 w-4" /></button></header><p className="mt-1 text-xs text-muted-foreground">{selected.type === 'script' ? 'Action' : selected.type} · {graph.recorded ? selected.sequence ? `Recorded step ${selected.sequence}` : 'Recorded execution' : graph.native ? selected.line ? `Python source at line ${selected.line}` : 'Recorded step' : `Source annotation at line ${selected.line}`}</p>{selected.description && <p className="mt-2 text-sm">{selected.description}</p>}
      {selected.type === 'decision' && titleFor(selected) !== selected.label && <details className="mt-3 text-xs"><summary className="cursor-pointer font-medium">Exact condition</summary><Value value={selected.label} /></details>}
      <div className="mt-3 space-y-3">{([['Inputs', selected.input], ['Categories', selected.categories], ['Instructions', selected.instructions], ['Output', selected.output], ['System prompt', selected.system_prompt], ['User message', selected.user_message], ['Agent', selected.messages], ['Provider', selected.provider], ['Model', selected.model], ['Tools', selected.tools], ['Skills', selected.skills], ['MCP connections', selected.mcp]] as const).map(([label, value]) => value !== undefined && <details key={label}><summary className="cursor-pointer text-xs font-medium">{label}</summary><Value value={value} /></details>)}</div>
      {(selected.type === 'agent' || selected.type === 'classifier' || graph.native && selected.type === 'script') && calls.length > 0 && <div className="mt-4"><h4 className="text-xs font-semibold">Recorded results</h4>{selectedCalls.length === 0 ? <p className="mt-1 text-xs text-muted-foreground">No result recorded for this step.</p> : selectedCalls.map((call, index) => <details key={call.id} className="mt-2"><summary className="cursor-pointer text-xs">{selected.sequence ? `Step ${selected.sequence}` : `Result ${index + 1}`} · {call.status}</summary>{call.model !== undefined && <Value value={call.model} />}{call.output !== undefined && <Value value={call.output} />}{call.error && <p className="text-xs text-destructive">{call.error}</p>}{call.tools?.map((tool, i) => <div key={i} className="mt-2 text-xs"><strong>Tool: {tool.name}</strong>{tool.result !== undefined && <Value value={tool.result} />}{tool.error && <p className="text-destructive">{tool.error}</p>}</div>)}</details>)}</div>}
      {onCode && <button type="button" onClick={onCode} className="mt-4 inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><Code2 className="h-3.5 w-3.5" />View code</button>}
    </section>}
  </div>
}
