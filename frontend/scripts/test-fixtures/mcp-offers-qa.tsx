import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { McpConnectionsPanel } from '../../src/components/integrations/McpConnectionsPanel'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import '../../src/index.css'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') === 'dark')
function Fixture() {
  const [added, setAdded] = useState(0)
  const catalog = [
    { name: 'Apify', description: 'Scrapers, crawlers and automation actors' },
    { name: 'Browserbase', description: 'Managed browser sessions for agents' },
    { name: 'Exa', description: 'Search the web and retrieve page content' },
    { name: 'GitHub', description: 'Repositories, issues and pull requests' },
    { name: 'Unstructured', description: 'Convert documents into structured content' },
  ].map(item => ({ ...item, id: item.name, category: 'Developer tools', connect: {
    label: 'Add connection', ariaLabel: `Add ${item.name} connection`, run: () => setAdded(value => value + 1),
  } }))
  return <TooltipProvider><main className="mx-auto max-w-4xl p-4 text-foreground sm:p-8">
    <h1 className="mb-4 text-lg font-medium">MCPs</h1>
    <McpConnectionsPanel catalog={catalog} servers={[{ id: 'connected-exa', name: 'My Exa account', status: 'Connected' }]} showTools={false} />
    <output hidden aria-label="Connection setup opened">{added}</output>
  </main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
