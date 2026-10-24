import React from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { SharedCrewWorkspace } from '../../src/products/work/SharedCrewWorkspace'
import { ShareDashboardButton } from '../../src/components/workflow/ShareDashboardButton'

const params = new URLSearchParams(location.search)
document.documentElement.classList.toggle('dark', params.get('theme') !== 'light')
createRoot(document.getElementById('root')!).render(<main className="h-screen min-h-0 bg-background text-foreground">
  {params.get('owner') === 'true' ? <div className="mx-auto max-w-xl p-4"><ShareDashboardButton workspacePath="Crew/example-team" variant="panel" section="people" /></div> : <SharedCrewWorkspace projectId="example-team" enabled><div className="p-6"><h1 className="text-xl font-semibold">Team dashboard</h1><p className="mt-2 text-muted-foreground">Shared work and published actions.</p></div></SharedCrewWorkspace>}
</main>)
