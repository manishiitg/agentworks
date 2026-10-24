import React, { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { createDashboardStore, DashboardStateProvider, useDashboardStore } from './state'
import { shadcnComponents } from './shadcn/components'
import { MotionToolbar } from './MotionToolbar'
import { BarChart as RechartsBarChart, LineChart as RechartsLineChart, Bar, Line, CartesianGrid, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
const Environment = createContext<{ refresh: () => Promise<void>; clock?: any }>({ refresh: async () => {} })
export function DashboardProviders({ children, store: supplied, refresh, clock }: { children: ReactNode; store?: ReturnType<typeof createDashboardStore>; refresh: () => Promise<void>; clock?: any }) {
  const [local] = useState(() => createDashboardStore({}))
  return <Environment.Provider value={{ refresh, clock }}><DashboardStateProvider store={supplied ?? local}>{children}</DashboardStateProvider></Environment.Provider>
}

// Literal utility names are compiled with the shared runtime. Authored classes
// follow these defaults so the upstream class merger can override them.
const gridColumns = ['sm:grid-cols-1', 'sm:grid-cols-2', 'sm:grid-cols-3', 'sm:grid-cols-4', 'sm:grid-cols-5', 'sm:grid-cols-6']
function GridComponent({ props, ...context }: any) {
  const columns = Math.max(1, Math.min(6, Math.floor(Number(props.columns) || 1)))
  return <shadcnComponents.Grid {...context} props={{ ...props, className: `dashboard-grid grid-cols-1 ${gridColumns[columns - 1]} ${props.className ?? ''}` }} />
}

function ImageComponent({ props, ...context }: any) {
  const [url, setURL] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    const path = props.src
    if (typeof path !== 'string' || !path) { setURL(null); return }
    if (path.startsWith('data:image/')) { setURL(path); return }
    if (!/^(db|knowledgebase|docs)\//.test(path)) { setURL(null); return }
    void window.report.fileUrl(path).then((value: string | null) => { if (live) setURL(value) }).catch(() => { if (live) setURL(null) })
    return () => { live = false }
  }, [props.src])
  return <shadcnComponents.Image {...context} props={{ ...props, src: url }} />
}

function AvatarComponent({ props, ...context }: any) {
  // Avatar also accepts a source URL. Resolve it through the same project file
  // API rather than allowing a state binding to issue an external request.
  const [url, setURL] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    if (typeof props.src !== 'string' || !/^(db|knowledgebase|docs)\//.test(props.src)) { setURL(null); return }
    void window.report.fileUrl(props.src).then((v: string | null) => { if (live) setURL(v) }).catch(() => { if (live) setURL(null) })
    return () => { live = false }
  }, [props.src])
  return <shadcnComponents.Avatar {...context} props={{ ...props, src: url }} />
}

function ChatPanelRenderer({ props }: any) {
  const available = Boolean(window.report.chat?.available)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!available) return
    const selector = '#' + CSS.escape(props.id)
    void window.report.chat.mount(selector).catch((e: Error) => setError(e.message))
    return () => window.report.chat.close(selector)
  }, [props.id, available])
  if (!available) return null
  return <section data-report-chat-panel id={props.id} className={props.className ?? ''} style={{ height: props.height }}>{error && <p role="status">{error}</p>}</section>
}

function AskButtonRenderer({ props }: any) {
  const [error, setError] = useState('')
  if (!window.report.chat?.available) return null
  return <><button data-report-chat-button data-report-chat-target={props.target || undefined} type="button" className={props.className ?? 'dashboard-button'}
    onClick={event => { event.stopPropagation(); void window.report.chat.open(props.target || undefined).catch((e: Error) => setError(e.message)) }}>{props.label}</button>{error && <p role="alert">{error}</p>}</>
}

function Chart({ props, line = false }: any) {
  const rows = Array.isArray(props.data) ? props.data.filter((r: any) => typeof r?.value === 'number' && Number.isFinite(r.value)) : []
  const progress = Math.max(0, Math.min(1, Number(props.progress ?? 1)))
  const visible = line ? rows.slice(0, Math.max(1, Math.ceil(rows.length * progress))) : rows
  if (!rows.length) return <p role="status">No recorded values available.</p>
  const ChartComponent = line ? RechartsLineChart : RechartsBarChart
  return <figure className={'dashboard-chart ' + (props.className ?? '')} aria-label={props.label || (line ? 'Line chart' : 'Bar chart')}>
    {props.label && <figcaption>{props.label}</figcaption>}
    <div style={{ width: '100%', height: props.height ?? 240, minWidth: 0 }}>
      <ResponsiveContainer width="100%" height="100%"><ChartComponent accessibilityLayer data={line ? visible : rows.map((row: any) => ({ ...row, value: row.value * progress }))}>
        <CartesianGrid vertical={false} stroke="hsl(var(--border))"/>
        <XAxis dataKey="label" stroke="hsl(var(--muted-foreground))"/><YAxis stroke="hsl(var(--muted-foreground))"/>
        <Tooltip contentStyle={{ background: 'hsl(var(--popover))', color: 'hsl(var(--popover-foreground))', border: '1px solid hsl(var(--border))', borderRadius: 'var(--radius)' }}/>
        {line ? <Line dataKey="value" type="monotone" stroke="hsl(var(--primary))" strokeWidth={2} dot={false} isAnimationActive={false}/> : <Bar dataKey="value" fill="hsl(var(--primary))" radius={[4, 4, 0, 0]} isAnimationActive={false}/>}
      </ChartComponent></ResponsiveContainer>
    </div>
    <details><summary>Values</summary><table><tbody>{visible.map((r: any, i: number) => <tr key={i}><th>{r.label}</th><td>{r.value}</td></tr>)}</tbody></table></details>
  </figure>
}

function MotionControlsRenderer({ props }: any) {
  const { clock: environmentClock } = useContext(Environment)
  const supplied = Object.fromEntries(Object.entries(props).filter(([, value]) => value != null))
  return <MotionToolbar {...supplied} clock={props.clock ?? environmentClock}/>
}

export const componentRenderers = {
    ...shadcnComponents,
    Image: ImageComponent, Avatar: AvatarComponent, Grid: GridComponent,
    // Keep ordinary authored link navigation; the host handles destinations.
    Link: ({ props }: any) => <a href={props.href} target="_blank" rel="noopener noreferrer">{props.label}</a>,
    Metric: ({ props }: any) => <section className={`dashboard-stat ${props.className ?? ''}`}><div className="dashboard-stat-label">{props.label}</div><strong className="dashboard-metric">{props.value}</strong>{props.detail && <p className="dashboard-stat-detail">{props.detail}</p>}</section>,
    BarChart: (context: any) => <Chart {...context} />,
    LineChart: (context: any) => <Chart {...context} line />,
    AskButton: AskButtonRenderer, ChatPanel: ChatPanelRenderer, MotionControls: MotionControlsRenderer,
    FunctionButton: ({ props }: any) => <button type="button" className={props.className ?? 'dashboard-button'} data-report-action={props.name} data-report-action-label={props.label} data-report-action-defaults={JSON.stringify(props.defaults ?? {})}>{props.label}</button>,
}
// These wrappers expose the same shared shadcn primitives to authored TSX.
type DirectProps = Record<string, any> & { children?: ReactNode; slots?: Record<string, ReactNode>; on?: Record<string, () => void>; onPress?: () => void; bindings?: Record<string, string> }
function direct(name: keyof typeof componentRenderers) {
  const Component = componentRenderers[name] as React.ComponentType<any>
  return function PlatformComponent({ children, slots, on: events = {}, onPress, bindings, ...props }: DirectProps) {
    const store = useDashboardStore()
    for (const [key, path] of Object.entries(bindings ?? {})) props[key] = store.get(path)
    const emit = (event: string) => (events[event] ?? (event === 'press' ? onPress : undefined))?.()
    return <Component props={props} children={children} slots={slots} bindings={bindings} emit={emit} on={(event: string) => ({ emit: () => emit(event), bound: Boolean(events[event] || (event === 'press' && onPress)), shouldPreventDefault: false })} />
  }
}
export const components = Object.fromEntries(Object.keys(componentRenderers).map(name => [name, direct(name as keyof typeof componentRenderers)])) as Record<keyof typeof componentRenderers, React.ComponentType<DirectProps>>
export const { Card, Stack, Grid, Heading, Text, Metric, Table, BarChart, LineChart, Button, Tabs, Accordion, Collapsible, Alert, Badge, Input, Select, Checkbox, AskButton: DashboardAskButton, ChatPanel: DashboardChatPanel, FunctionButton, MotionControls: DashboardMotionControls } = components
export { DashboardAskButton as AskButton, DashboardChatPanel as ChatPanel, DashboardMotionControls as MotionControls }
export function RefreshButton({ label = 'Refresh data', ...props }: DirectProps) {
  const { refresh } = useContext(Environment)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return <><Button {...props} label={busy ? 'Refreshing…' : label} disabled={busy} onPress={async () => {
    setBusy(true); setError('')
    try { await refresh() } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setBusy(false) }
  }} />{error && <p role="alert">{error}</p>}</>
}
export { createDashboardStore, useDashboardStore, useDashboardBinding } from './state'
export { UploadZone, MediaGallery, MediaPreview } from './media'
export type { DashboardAsset, DashboardPresentation } from './media'

/** Refresh saved data after a local filter or project selection changes. */
export function useDashboardRefresh() { return useContext(Environment).refresh }
