import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { CrewEntryPage } from '../../src/products/work/CrewEntryPage'
import type { WorkSession } from '../../src/products/work/workSessions'

const projects = [
  { id: 'research', product: 'work', title: 'Research', description: 'Compare useful sources', updatedAt: '2026-10-26T08:00:00Z' },
  { id: 'reports', product: 'work', title: 'Team reports', description: 'Weekly updates', updatedAt: '2026-10-25T08:00:00Z', shared: { canBuild: false, ownerId: 'another-person' } },
] as WorkSession[]
function Fixture() {
  const params = new URLSearchParams(location.search)
  const [result, setResult] = useState('')
  return <div className="h-dvh bg-background text-foreground"><CrewEntryPage projects={params.has('empty') ? [] : projects} loading={false} error={null} canCreate={!params.has('run')}
    onCreate={prompt => setResult(`Setup request: ${prompt}`)} onOpen={project => setResult(`Open Crew: ${project.id}`)} onRefresh={async () => {}} />
    <output className="sr-only" aria-label="Navigation result">{result}</output>
  </div>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
