import { useId, useMemo, useRef } from 'react'
import dagre from 'dagre'
import { ChatDiagramFrame, useDiagramViewportWidth } from './ChatDiagramFrame'
import type { ChatElement, DiagramNode } from './chatElementSpec'

type Point = { x: number; y: number }
type Spec = Extract<ChatElement, { type: 'diagram' }>
type Direction = 'vertical' | 'horizontal'

function textLines(value: string, units: number, limit: number) {
  const length = Array.from(value).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 255 ? 2 : 1), 0)
  return Math.min(limit, Math.max(1, Math.ceil(length / Math.max(1, units))))
}

function nodeSize(node: DiagramNode, maxColumns = 3, baseWidth = 264) {
  const columns = Math.min(maxColumns, node.items?.length ?? 0)
  const rows = columns ? Math.ceil(node.items!.length / columns) : 0
  const width = columns ? Math.max(baseWidth, columns * 164 + (columns - 1) * 12 + 32) : baseWidth
  const tileHeight = Math.max(0, ...node.items?.map(item => 20 + textLines(item.title, 22, 2) * 16 +
    (item.description ? 4 + textLines(item.description, 22, 2) * 16 : 0)) ?? [])
  return {
    width,
    height: 32 + textLines(node.title, (width - 32) / 7, 2) * 20 +
      (node.description ? 8 + textLines(node.description, (width - 32) / 6, 3) * 16 : 0) +
      (rows ? 16 + rows * tileHeight + (rows - 1) * 12 : 0),
    columns, tileHeight,
  }
}

function roundedPath(points: Point[]) {
  if (!points.length) return ''
  let path = `M${points[0].x},${points[0].y}`
  for (let i = 1; i < points.length - 1; i++) {
    const before = points[i - 1], at = points[i], after = points[i + 1]
    const incoming = Math.hypot(at.x - before.x, at.y - before.y) || 1
    const outgoing = Math.hypot(after.x - at.x, after.y - at.y) || 1
    const radius = Math.min(12, incoming / 2, outgoing / 2)
    const x1 = at.x - (at.x - before.x) / incoming * radius, y1 = at.y - (at.y - before.y) / incoming * radius
    const x2 = at.x + (after.x - at.x) / outgoing * radius, y2 = at.y + (after.y - at.y) / outgoing * radius
    path += ` L${x1},${y1} Q${at.x},${at.y} ${x2},${y2}`
  }
  const last = points[points.length - 1]
  return path + ` L${last.x},${last.y}`
}

function buildDiagramLayout(
  definition: Pick<Spec, 'nodes' | 'edges'>,
  direction: Direction,
  options: { baseWidth: number; groupColumns: number; nodesep: number; ranksep: number; margin: number; maxLabelWidth: number },
) {
  const graph = new dagre.graphlib.Graph({ multigraph: true })
  graph.setGraph({
    rankdir: direction === 'horizontal' ? 'LR' : 'TB',
    nodesep: options.nodesep,
    ranksep: options.ranksep,
    marginx: options.margin,
    marginy: options.margin,
  })
  graph.setDefaultEdgeLabel(() => ({}))
  // Prefix engine keys; model IDs never become bare object-property names.
  const id = (value: string) => `node:${value}`
  definition.nodes.forEach(node => graph.setNode(id(node.id), nodeSize(node, options.groupColumns, options.baseWidth)))
  definition.edges.forEach((edge, index) => {
    const width = edge.label ? Math.min(options.maxLabelWidth, Math.max(96, edge.label.length * 8 + 24)) : 0
    graph.setEdge(id(edge.from), id(edge.to), {
      width, height: edge.label ? textLines(edge.label, (width - 18) / 7, 3) * 16 + 12 : 0, labelpos: 'c',
    }, String(index))
  })
  dagre.layout(graph)
  return {
    direction,
    width: Math.ceil(graph.graph().width ?? 312),
    height: Math.ceil(graph.graph().height ?? 120),
    nodes: definition.nodes.map(node => {
      const position = graph.node(id(node.id))
      return { node, ...nodeSize(node, options.groupColumns, options.baseWidth), x: position.x, y: position.y }
    }),
    edges: definition.edges.map((edge, index) => {
      const route = graph.edge({ v: id(edge.from), w: id(edge.to), name: String(index) })
      return { edge, points: route.points as Point[], x: route.x ?? 0, y: route.y ?? 0, width: route.width ?? 0, height: route.height ?? 0 }
    }),
  }
}

export function ChatDiagram({ spec }: { spec: Spec }) {
  const arrow = `chat-arrow-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const region = useRef<HTMLDivElement>(null)
  const viewportWidth = useDiagramViewportWidth(region)
  const groupColumns = viewportWidth ? Math.max(1, Math.min(3, Math.floor((viewportWidth - 68) / 176))) : 3
  // Partial JSON may change every token without adding a complete node/edge.
  // Keep geometry stable until the validated diagram or viewport width changes.
  const layoutSource = JSON.stringify({ direction: spec.direction, nodes: spec.nodes, edges: spec.edges, groupColumns, viewportWidth })
  const layout = useMemo(() => {
    try {
      const definition = JSON.parse(layoutSource) as Pick<Spec, 'direction' | 'nodes' | 'edges'> & { groupColumns: number; viewportWidth: number | null }
      const available = definition.viewportWidth
      const tbWidth = available ? Math.max(180, Math.min(264, available - 48)) : 264
      const tbMaxLabel = available ? Math.min(264, Math.max(120, available - 48)) : 264
      if (definition.direction === 'horizontal') {
        const normal = buildDiagramLayout(definition, 'horizontal', {
          baseWidth: 264, groupColumns: definition.groupColumns, nodesep: 32, ranksep: 40, margin: 24, maxLabelWidth: 264,
        })
        if (!available || normal.width <= available) return normal
        const compact = buildDiagramLayout(definition, 'horizontal', {
          baseWidth: 200, groupColumns: definition.groupColumns, nodesep: 24, ranksep: 28, margin: 16, maxLabelWidth: 168,
        })
        if (compact.width <= available) return compact
        const vertical = buildDiagramLayout(definition, 'vertical', {
          baseWidth: tbWidth, groupColumns: definition.groupColumns, nodesep: 28, ranksep: 36, margin: 20, maxLabelWidth: tbMaxLabel,
        })
        if (vertical.width <= available || vertical.width < compact.width) return vertical
        return compact
      }
      return buildDiagramLayout(definition, 'vertical', {
        baseWidth: tbWidth, groupColumns: definition.groupColumns, nodesep: 32, ranksep: 40, margin: 24, maxLabelWidth: tbMaxLabel,
      })
    } catch { return null }
  }, [layoutSource])
  if (!layout) return <p className="my-4 text-sm text-muted-foreground">This diagram could not be arranged. Try fewer connections.</p>
  return <ChatDiagramFrame title={spec.title} width={layout.width} height={layout.height} direction={layout.direction} viewportRef={region}
    description={<ul>{spec.edges.map((edge, index) => <li key={index}>{spec.nodes.find(node => node.id === edge.from)?.title} {edge.bidirectional ? 'connects both ways to' : 'connects to'} {spec.nodes.find(node => node.id === edge.to)?.title}{edge.label ? `: ${edge.label}` : ''}</li>)}</ul>}>
        <svg className="pointer-events-none absolute inset-0 text-muted-foreground/70" width={layout.width} height={layout.height} aria-hidden="true">
          <defs><marker id={arrow} viewBox="0 0 8 8" refX="8" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L8 4L0 8Z" fill="currentColor" /></marker></defs>
          {layout.edges.map(({ edge, points }, index) => (
            <path key={index} d={roundedPath(points)} fill="none" stroke="currentColor" strokeWidth="1.5" markerEnd={`url(#${arrow})`} markerStart={edge.bidirectional ? `url(#${arrow})` : undefined} />
          ))}
        </svg>
        {layout.edges.map(({ edge, x, y, width, height }, index) => edge.label && (
          <div key={index} data-diagram-edge-label={index} title={edge.label}
            className="absolute flex items-center justify-center rounded-md border border-border bg-background px-2 text-center text-xs leading-4 text-muted-foreground"
            style={{ left: x - width / 2, top: y - height / 2, width, height }}>
            <span className="line-clamp-3 break-words">{edge.label}</span>
          </div>
        ))}
        {layout.nodes.map(({ node, x, y, width, height, columns, tileHeight }) => <section key={node.id} data-diagram-node={node.id} aria-label={node.title} title={[node.title, node.description].filter(Boolean).join(' — ')}
          className={`absolute flex flex-col overflow-hidden rounded-2xl border p-4 shadow-sm ${node.emphasis ? 'border-primary/30 bg-primary/10 text-foreground' : 'border-border bg-card text-card-foreground'}`}
          style={{ left: x - width / 2, top: y - height / 2, width, height }}>
          <div className="line-clamp-2 break-words text-sm font-semibold leading-5">{node.title}</div>
          {node.description && <div className="mt-2 line-clamp-3 break-words text-xs leading-4 text-muted-foreground">{node.description}</div>}
          {node.items && <div className="mt-auto grid gap-3 pt-4" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0,1fr))` }}>
            {node.items.map((item, index) => <div key={index} className="flex min-w-0 flex-col justify-center rounded-xl border border-border bg-background/75 px-3 py-2" style={{ height: tileHeight }}>
              <div className="line-clamp-2 break-words text-xs font-medium leading-4">{item.title}</div>
              {item.description && <div className="mt-1 line-clamp-2 break-words text-[11px] leading-4 text-muted-foreground">{item.description}</div>}
            </div>)}
          </div>}
        </section>)}
  </ChatDiagramFrame>
}
