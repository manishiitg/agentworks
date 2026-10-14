import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { RelaySourceGraph, type RelayRecordedCall } from '../../src/components/workflow/RelaySourceGraph'
import { graphFromDBOSHistory } from '../../src/components/workflow/relayDBOSGraph'
import type { RelaySourceGraph as SourceGraph } from '../../src/components/workflow/relayGraphAnnotations'
import '../../src/index.css'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') === 'dark')
const categories = { billing: 'Payments and refunds', technical: 'Errors and outages', sales: 'Plans and demos', other: 'Other requests' }
const overview: SourceGraph = {
  native: true, errors: [],
  nodes: [
    { id: 'input', type: 'input', label: 'Receive input', line: 0 },
    { id: 'classify', type: 'classifier', label: 'Classify request', categories, model: 'clef-flash', line: 10 },
    { id: 'billing-choice', type: 'decision', label: "classification['label'] == 'billing'", line: 20 },
    { id: 'billing', type: 'agent', label: 'Billing agent', line: 21 },
    { id: 'technical-choice', type: 'decision', label: "classification['label'] == 'technical'", line: 22 },
    { id: 'technical', type: 'agent', label: 'Support agent', line: 23 },
    { id: 'sales-choice', type: 'decision', label: "classification['label'] == 'sales'", line: 24 },
    { id: 'sales', type: 'agent', label: 'Sales agent', line: 25 },
    { id: 'other', type: 'agent', label: 'General agent', line: 26 },
    { id: 'output', type: 'output', label: 'Return result', line: 0 },
  ],
  edges: [
    { from: 'input', to: 'classify', line: 0 }, { from: 'classify', to: 'billing-choice', line: 0 },
    { from: 'billing-choice', to: 'billing', label: 'Yes', line: 0 },
    { from: 'billing-choice', to: 'technical-choice', label: 'No', line: 0 },
    { from: 'technical-choice', to: 'technical', label: 'Yes', line: 0 },
    { from: 'technical-choice', to: 'sales-choice', label: 'No', line: 0 },
    { from: 'sales-choice', to: 'sales', label: 'Yes', line: 0 },
    { from: 'sales-choice', to: 'other', label: 'No', line: 0 },
    ...['billing', 'technical', 'sales', 'other'].map(from => ({ from, to: 'output', line: 0 })),
  ],
}
const observed: RelayRecordedCall[] = [
  { id: 'step-1', name: 'classify_request', type: 'classifier', status: 'completed', provider: 'cloudflare', model: 'clef-flash', categories, output: { label: 'billing', scores: { billing: 0.9, other: 0.1 } }, started_at: 100, completed_at: 100.326 },
  { id: 'step-2', name: 'billing_agent', status: 'completed', provider: 'provider', model: 'Example model', output: { reply: 'Example reply' }, started_at: 101, completed_at: 102 },
]

function Fixture() {
  const [scene, setScene] = useState('overview')
  const [wide, setWide] = useState(false)
  const calls = scene === 'overview' ? [] : observed.map((call, index) => scene === 'failed' && index === 1 ? { ...call, status: 'failed', error: 'Reply could not be generated' } : call)
  const graph = scene === 'overview' ? overview : graphFromDBOSHistory(calls, { status: scene === 'failed' ? 'failed' : 'completed', resultRecorded: scene === 'completed' })
  return <main className="p-4 text-foreground">
    <div className="mb-3 flex gap-3">{['overview', 'completed', 'failed'].map(value => <button key={value} onClick={() => setScene(value)} className="rounded border border-border px-3 py-1">{value}</button>)}<button className="rounded border border-border px-3 py-1" onClick={() => setWide(value => !value)}>Resize panel</button></div>
    <div className="flex flex-col rounded border border-border" style={{ width: wide ? '100%' : '60%', height: wide ? 680 : 350 }}>
      <RelaySourceGraph source="" graph={graph} calls={calls} onBuild={() => {}} />
    </div>
  </main>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
