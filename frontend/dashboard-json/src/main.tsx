import React from 'react'
import { createRoot } from 'react-dom/client'
import { flushSync } from 'react-dom'
import { createStateStore, type Spec } from '@json-render/core'
import { DashboardProviders, DashboardUI, components } from './components'
import './style.css'

type DataBinding = { statePath: string; method: 'query' | 'get' | 'run'; sql?: string; path?: string; params?: unknown[]; args?: unknown }
type DashboardSpec = Spec & { data?: DataBinding[]; styles?: string; motion?: { duration: number; statePath: string } }
declare global { interface Window { report: any; __dashboardJSON?: { version: string; ready: boolean; componentCount: number } } }
const spec = JSON.parse(document.getElementById('__dashboard_spec')?.textContent || '{}') as DashboardSpec
const store = createStateStore(spec.state ?? {})
let clock: any
let refreshSequence = 0
let refreshData: () => Promise<void>


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
  const render = () => flushSync(() => root.render(<DashboardError><DashboardProviders store={store} refresh={() => refreshData()} clock={clock}><DashboardUI spec={spec} /></DashboardProviders></DashboardError>))
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
    window.__dashboardJSON = { version: '0.21.1', ready: true, componentCount: Object.keys(components).length }
  }
  // Return the promise to the shared host so preview and frozen export wait for
  // real data and committed React DOM. Ordinary parent rerenders do not reload.
  window.report.ready(refreshData)
}
void main().catch(error => { document.documentElement.dataset.reportState = 'error'; document.getElementById('dashboard-root')!.textContent = `Dashboard unavailable: ${String(error)}`; throw error })
