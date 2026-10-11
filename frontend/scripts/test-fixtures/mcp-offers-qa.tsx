import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { McpConnectionsPanel } from '../../src/components/integrations/McpConnectionsPanel'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import '../../src/index.css'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') === 'dark')
function Fixture() {
  const [added, setAdded] = useState<Record<string, number>>({})
  const catalog = [
    { name: 'Apify', description: 'Scrapers, crawlers and automation actors' },
    { name: 'Browserbase', description: 'Managed browser sessions for agents' },
    { name: 'Exa', description: 'Search the web and retrieve page content' },
    { name: 'GitHub', description: 'Repositories, issues and pull requests' },
    { name: 'Unstructured', description: 'Convert documents into structured content' },
  ].map(item => ({ ...item, id: item.name, category: 'Developer tools', addedCount: added[item.name] ?? 0, connect: {
    label: 'Add connection', ariaLabel: `Add ${item.name} connection`, run: () => setAdded(value => ({ ...value, [item.name]: (value[item.name] ?? 0) + 1 })),
  } }))
  const servers = [{ id: 'connected-github', name: 'Repository account', provider: 'GitHub', status: 'Connected' },
    ...Object.keys(added).map(provider => ({ id: provider, name: `${provider} account`, provider, status: 'Connected' }))]
  return <TooltipProvider><main className="mx-auto max-w-4xl p-4 text-foreground sm:p-8">
    <h1 className="mb-4 text-lg font-medium">MCPs</h1>
    <McpConnectionsPanel catalog={catalog} servers={servers} showTools={false} />
    <output hidden aria-label="Connection setup opened">{Object.values(added).reduce((sum, count) => sum + count, 0)}</output>
  </main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
