import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { flushSync } from 'react-dom'
import { createStateStore, type Spec } from '@json-render/core'
import { Renderer, StateProvider, VisibilityProvider, ActionProvider, ValidationProvider, defineRegistry, useStateStore } from '@json-render/react'
import { shadcnComponents } from '@json-render/shadcn'
import { catalog } from '../catalog.mjs'
import './style.css'

type DataBinding = { statePath: string; method: 'query' | 'get' | 'run'; sql?: string; path?: string; params?: unknown[]; args?: unknown }
type DashboardSpec = Spec & { data?: DataBinding[]; styles?: string; motion?: { duration: number; statePath: string } }
declare global { interface Window { report: any; __dashboardJSON?: { version: string; ready: boolean; componentCount: number } } }
const spec = JSON.parse(document.getElementById('__dashboard_spec')?.textContent || '{}') as DashboardSpec
const store = createStateStore(spec.state ?? {})
let clock: any
let refreshSequence = 0
let refreshData: () => Promise<void>

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

function ChatPanel({ props }: any) {
  const [error, setError] = useState('')
  useEffect(() => {
    if (!window.report.chat?.available) { setError('Chat is available when you open this shared project dashboard.'); return }
    const selector = '#' + CSS.escape(props.id)
    void window.report.chat.mount(selector).catch((e: Error) => setError(e.message))
    return () => window.report.chat.close(selector)
  }, [props.id])
  return <section id={props.id} className={props.className ?? ''} style={{ height: props.height }}>{error && <p role="status">{error}</p>}</section>
}

function AskButton({ props }: any) {
  const [error, setError] = useState('')
  return <><button type="button" className={props.className ?? 'dashboard-button'} disabled={!window.report.chat?.available}
    onClick={() => { void window.report.chat.open(props.target || undefined).catch((e: Error) => setError(e.message)) }}>{props.label}</button>{error && <p role="alert">{error}</p>}</>
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

function MotionControls({ props }: any) {
  useStateStore()
  const state = clock?.getState()
  return <div className={props.className ?? 'dashboard-motion'} aria-label={props.label || 'Playback'}>
    <button type="button" disabled={!clock} onClick={() => state?.playing ? clock.pause() : clock.play()}>{state?.playing ? 'Pause' : 'Play'}</button>
    <input aria-label="Playback position" type="range" min="0" max={state?.duration ?? 1} step="0.01" value={state?.time ?? 0} disabled={!clock} onChange={e => clock.seek(Number(e.target.value))} />
    <span>{(state?.time ?? 0).toFixed(1)} / {state?.duration ?? 0}s</span>
  </div>
}

const { registry } = defineRegistry(catalog, {
  components: {
    ...shadcnComponents,
    Image: ImageComponent, Avatar: AvatarComponent,
    // Link URLs were validated as static safe links by the server. Opening is
    // always from a click; the existing host handles external destinations.
    Link: ({ props }: any) => <a href={props.href} target="_blank" rel="noopener noreferrer">{props.label}</a>,
    Metric: ({ props }: any) => <section className={props.className ?? ''}><div>{props.label}</div><strong className="dashboard-metric">{props.value}</strong>{props.detail && <p>{props.detail}</p>}</section>,
    BarChart: (context: any) => <Chart {...context} />,
    LineChart: (context: any) => <Chart {...context} line />,
    AskButton, ChatPanel, MotionControls,
    FunctionButton: ({ props }: any) => <button type="button" className={props.className ?? 'dashboard-button'} data-report-action={props.name} data-report-action-label={props.label} data-report-action-defaults={JSON.stringify(props.defaults ?? {})}>{props.label}</button>,
  },
  actions: { refreshData: async () => refreshData() },
})

class DashboardError extends React.Component<{ children: React.ReactNode }, { message: string }> {
  state = { message: '' }
  static getDerivedStateFromError(e: Error) { return { message: e.message } }
  componentDidCatch() { document.documentElement.dataset.reportState = 'error' }
  render() { return this.state.message ? <p role="alert">This dashboard could not render: {this.state.message}</p> : this.props.children }
}

function resolve(value: any): any {
  if (Array.isArray(value)) return value.map(resolve)
  if (value && typeof value === 'object') {
    if (typeof value.$state === 'string') return store.get(value.$state)
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, resolve(v)]))
  }
  return value
}

async function main() {
  const root = createRoot(document.getElementById('dashboard-root')!)
  const authored = document.createElement('style'); authored.textContent = spec.styles ?? ''; document.head.append(authored)
  const render = () => flushSync(() => root.render(<DashboardError><StateProvider store={store}><VisibilityProvider><ValidationProvider><ActionProvider handlers={{ refreshData: async () => refreshData() }}><Renderer spec={spec} registry={registry} /></ActionProvider></ValidationProvider></VisibilityProvider></StateProvider></DashboardError>))
  refreshData = async () => {
    const sequence = ++refreshSequence
    const values = await Promise.all((spec.data ?? []).map(async binding => {
      let value: unknown
      if (binding.method === 'query') value = await window.report.query(binding.sql, resolve(binding.params ?? []))
      else if (binding.method === 'get') value = await window.report.get(binding.path)
      else value = await window.report.run(binding.path, resolve(binding.args ?? {}))
      return [binding.statePath, value] as const
    }))
    if (sequence !== refreshSequence) return
    flushSync(() => store.update(Object.fromEntries(values)))
    if (spec.motion && !clock) {
      clock = window.report.motion.create({ id: 'dashboard-json', duration: spec.motion.duration,
        render: (state: any) => flushSync(() => store.set(spec.motion!.statePath, state)),
      })
    }
    render()
    window.__dashboardJSON = { version: '0.21.1', ready: true, componentCount: Object.keys(registry).length }
  }
  // Return the promise to the shared host so preview and frozen export wait for
  // real data and committed React DOM. Ordinary parent rerenders do not reload.
  window.report.ready(refreshData)
}
void main().catch(error => { document.documentElement.dataset.reportState = 'error'; document.getElementById('dashboard-root')!.textContent = `Dashboard unavailable: ${String(error)}`; throw error })
