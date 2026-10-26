import { createRoot } from 'react-dom/client'
import { useState } from 'react'
import '../../src/index.css'
import { AppShell } from '../../src/components/workspace/AppShell'
import { WelcomeHome } from '../../src/components/WelcomeHome'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { useChatStore } from '../../src/stores/useChatStore'
import { useGlobalPresetStore } from '../../src/stores/useGlobalPresetStore'
import { CreateCrewWizard } from '../../src/products/work/CreateCrewWizard'
import { useProductCreateRequest } from '../../src/hooks/useProductCreateRequest'
import { useLLMStore } from '../../src/stores/useLLMStore'

useAuthStore.setState({ user: { id: 'example-person', allowed_products: ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen'], can_create: true } as never, isMultiUserMode: true })
useChatStore.setState({ chatTabs: {}, activeSessionsCache: [], getActiveSessions: async () => [] })
useGlobalPresetStore.setState({ workflowPresets: [
  { id: 'reading', label: 'Reading target', createdAt: 1, selectedFolder: { filepath: 'Goals/reading' } },
  { id: 'weekly', label: 'Weekly updates', createdAt: 1, workflowKind: 'relay', selectedFolder: { filepath: 'Relays/weekly' } },
] as never })
history.replaceState({}, '', '/home')
useLLMStore.setState({ providerManifest: [], providerManifestLoaded: true })
function Fixture() {
  const [creating, setCreating] = useState<'studio' | 'crew' | null>(null)
  useProductCreateRequest('work', intent => setCreating(intent === 'studio' ? 'studio' : 'crew'))
  return <AppShell><WelcomeHome autoOpen={false} />{creating && <CreateCrewWizard studio={creating === 'studio'} initialMode={creating === 'studio' ? 'cowork' : 'dev'} onClose={() => setCreating(null)} onCreate={() => {}} submitting={false} error={null} />}</AppShell>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
