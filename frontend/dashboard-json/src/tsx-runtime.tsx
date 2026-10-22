import React from 'react'
import * as ReactDOM from 'react-dom'
import * as ReactDOMClient from 'react-dom/client'
import * as JSXRuntime from 'react/jsx-runtime'
import * as JSXDevRuntime from 'react/jsx-dev-runtime'
import './style.css'

type DashboardModule = {
  default: React.ComponentType<{ data: unknown; report: any }>
  loadData?: (report: any) => unknown | Promise<unknown>
}

function showError(error: unknown) {
  document.documentElement.dataset.reportState = 'error'
  const message = error instanceof Error ? error.message : String(error)
  document.getElementById('dashboard-root')!.textContent = `Dashboard unavailable: ${message}`
  // The shared host records this error for preview and export too.
  throw error instanceof Error ? error : new Error(message)
}

class DashboardError extends React.Component<{ children: React.ReactNode }, { message: string }> {
  state = { message: '' }
  static getDerivedStateFromError(error: Error) { return { message: error.message } }
  componentDidCatch(error: Error) {
    document.documentElement.dataset.reportState = 'error'
    window.dispatchEvent(new ErrorEvent('error', { error, message: error.message }))
  }
  render() { return this.state.message ? <p role="alert">This dashboard could not render: {this.state.message}</p> : this.props.children }
}

// Never retain a bootstrap API: the host replaces it with the current authorized
// adapter and reuses the document when refreshing data or changing theme.
const report = new Proxy({}, {
  get: (_target, name) => {
    const value = window.report[name]
    return typeof value === 'function' ? value.bind(window.report) : value
  },
})

function mount(module: DashboardModule) {
  if (!module.default) { showError(new Error('index.tsx must default-export a React component.')); return }
  const root = ReactDOMClient.createRoot(document.getElementById('dashboard-root')!)
  let sequence = 0
  window.report.ready(async () => {
    const current = ++sequence
    try {
      const data = module.loadData ? await module.loadData(report) : undefined
      if (current !== sequence) return
      ReactDOM.flushSync(() => root.render(<DashboardError key={current}><module.default data={data} report={report} /></DashboardError>))
    } catch (error) { if (current === sequence) showError(error) }
  })
}

// Compiled dashboard imports resolve only to this pinned runtime or files in
// that dashboard's immutable bundle. There is a single React instance per page.
Object.assign(window, { __agentworksDashboard: { modules: {
  react: React,
  'react-dom': ReactDOM,
  'react-dom/client': ReactDOMClient,
  'react/jsx-runtime': JSXRuntime,
  'react/jsx-dev-runtime': JSXDevRuntime,
  '@agentworks/dashboard': { report, mount },
} } })
