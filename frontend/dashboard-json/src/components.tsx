import React, { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { createStateStore, type Spec } from '@json-render/core'
import { Renderer, StateProvider, VisibilityProvider, ActionProvider, ValidationProvider, defineRegistry, useStateStore } from '@json-render/react'
import { shadcnComponents } from '@json-render/shadcn'
import { catalog } from '../catalog.mjs'

const Environment = createContext<{ refresh: () => Promise<void>; clock?: any }>({ refresh: async () => {} })
export function DashboardProviders({ children, store: supplied, refresh, clock }: { children: ReactNode; store?: ReturnType<typeof createStateStore>; refresh: () => Promise<void>; clock?: any }) {
  const [local] = useState(() => createStateStore({}))
  return <Environment.Provider value={{ refresh, clock }}><StateProvider store={supplied ?? local}><VisibilityProvider><ValidationProvider><ActionProvider handlers={{ refreshData: refresh }}>{children}</ActionProvider></ValidationProvider></VisibilityProvider></StateProvider></Environment.Provider>
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
  const max = Math.max(1, ...rows.map((r: any) => Math.abs(Number(r.value))))
  const low = Math.min(0, ...rows.map((r: any) => r.value))
  const high = Math.max(0, ...rows.map((r: any) => r.value))
  const y = (value: number) => 180 - (value - low) * 150 / (high - low || 1)
  const points = visible.map((r: any, i: number) => `${20 + i * 560 / Math.max(1, rows.length - 1)},${y(r.value)}`).join(' ')
  return <figure className={'dashboard-chart ' + (props.className ?? '')} aria-label={props.label || (line ? 'Line chart' : 'Bar chart')}>
    {props.label && <figcaption>{props.label}</figcaption>}
    {line ? <svg viewBox="0 0 600 210" width="600" height="210" role="img" aria-label={props.label || 'Recorded values'}><polyline points={points} fill="none" stroke="currentColor" strokeWidth="3" />{visible.map((r: any, i: number) => <circle key={i} cx={20 + i * 560 / Math.max(1, rows.length - 1)} cy={y(r.value)} r="3" fill="currentColor"><title>{r.label}: {r.value}</title></circle>)}</svg>
      : <div>{rows.map((r: any, i: number) => <div key={i} className="dashboard-bar-row"><span>{r.label}</span><div className="dashboard-bar" style={{ width: `${Math.abs(Number(r.value)) * 100 / max * progress}%` }} /><span>{r.value}</span></div>)}</div>}
    <details><summary>Values</summary><table><tbody>{visible.map((r: any, i: number) => <tr key={i}><th>{r.label}</th><td>{r.value}</td></tr>)}</tbody></table></details>
  </figure>
}

function MotionControlsRenderer({ props }: any) {
  useStateStore()
  const { clock } = useContext(Environment)
  const state = clock?.getState()
  return <div className={props.className ?? 'dashboard-motion'} aria-label={props.label || 'Playback'}>
    <button type="button" className="dashboard-button" disabled={!clock} onClick={() => state?.playing ? clock.pause() : clock.play()}>{state?.playing ? 'Pause' : 'Play'}</button>
    <input aria-label="Playback position" type="range" min="0" max={state?.duration ?? 1} step="0.01" value={state?.time ?? 0} disabled={!clock} onChange={e => clock.seek(Number(e.target.value))} />
    <span>{(state?.time ?? 0).toFixed(1)} / {state?.duration ?? 0}s</span>
  </div>
}

export const componentRenderers = {
    ...shadcnComponents,
    Image: ImageComponent, Avatar: AvatarComponent, Grid: GridComponent,
    // Link URLs were validated as static safe links by the server. Opening is
    // always from a click; the existing host handles external destinations.
    Link: ({ props }: any) => <a href={props.href} target="_blank" rel="noopener noreferrer">{props.label}</a>,
    Metric: ({ props }: any) => <section className={`dashboard-stat ${props.className ?? ''}`}><div className="dashboard-stat-label">{props.label}</div><strong className="dashboard-metric">{props.value}</strong>{props.detail && <p className="dashboard-stat-detail">{props.detail}</p>}</section>,
    BarChart: (context: any) => <Chart {...context} />,
    LineChart: (context: any) => <Chart {...context} line />,
    AskButton: AskButtonRenderer, ChatPanel: ChatPanelRenderer, MotionControls: MotionControlsRenderer,
    FunctionButton: ({ props }: any) => <button type="button" className={props.className ?? 'dashboard-button'} data-report-action={props.name} data-report-action-label={props.label} data-report-action-defaults={JSON.stringify(props.defaults ?? {})}>{props.label}</button>,
}
export const { registry } = defineRegistry(catalog, { components: componentRenderers })

// Direct TSX props use the very same renderers as JSON. `on` maps catalog event
// names (press/change/submit/...) to callbacks; bindings and slots remain usable.
type DirectProps = Record<string, any> & { children?: ReactNode; slots?: Record<string, ReactNode>; on?: Record<string, () => void>; onPress?: () => void; bindings?: Record<string, string> }
function direct(name: keyof typeof componentRenderers) {
  const Component = componentRenderers[name] as React.ComponentType<any>
  return function PlatformComponent({ children, slots, on: events = {}, onPress, bindings, ...props }: DirectProps) {
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
// Optional JSON composition inside a TSX page; this is the platform registry,
// never an authored switch statement or a second set of charts and controls.
export function DashboardUI({ spec }: { spec: Spec }) { return <Renderer spec={spec} registry={registry} /> }
export { useStateStore as useDashboardStore, useStateBinding as useDashboardBinding } from '@json-render/react'
