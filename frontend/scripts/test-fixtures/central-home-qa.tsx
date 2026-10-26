import { createRoot } from 'react-dom/client'
import { useEffect, useState } from 'react'
import '../../src/index.css'
import { AppShell } from '../../src/components/workspace/AppShell'
import { WelcomeHome } from '../../src/components/WelcomeHome'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { useChatStore } from '../../src/stores/useChatStore'
import { useGlobalPresetStore } from '../../src/stores/useGlobalPresetStore'
import { CreateCrewWizard } from '../../src/products/work/CreateCrewWizard'
import { useProductCreateRequest } from '../../src/hooks/useProductCreateRequest'
import { useLLMStore } from '../../src/stores/useLLMStore'
import { useProductSurfaceStore } from '../../src/stores/useProductSurfaceStore'
import { observeProductHistory } from '../../src/utils/productUrlNavigation'
import { ModePresetBar } from '../../src/components/ModePresetBar'
import { TooltipProvider } from '../../src/components/ui/tooltip'

useAuthStore.setState({ user: { id: 'example-person', allowed_products: ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen'], can_create: true } as never, isMultiUserMode: true })
useChatStore.setState({ chatTabs: {}, activeSessionsCache: [], getActiveSessions: async () => [] })
useProductSurfaceStore.setState({ productSurface: 'agentworks', selectedWorkProjectId: 'notes' })
useGlobalPresetStore.setState({ workflowPresetsLoaded: true, activePresetIds: { workflow: 'reading', 'multi-agent': null }, workflowPresets: [
  { id: 'reading', label: 'Reading target', createdAt: 1, selectedFolder: { filepath: 'Goals/reading' } },
  { id: 'weekly', label: 'Weekly updates', createdAt: 1, workflowKind: 'relay', selectedFolder: { filepath: 'Relays/weekly' } },
] as never })
history.replaceState({}, '', '/home')
useLLMStore.setState({ providerManifest: [], providerManifestLoaded: true })
function Fixture() {
  const product = useProductSurfaceStore(state => state.productSurface)
  useEffect(() => observeProductHistory(), [])
  const [creating, setCreating] = useState<'studio' | 'crew' | null>(null)
  useProductCreateRequest('work', intent => setCreating(intent === 'studio' ? 'studio' : 'crew'))
  return <TooltipProvider><AppShell><WelcomeHome autoOpen={false} />{(product === 'agentworks' || product === 'relays') && <ModePresetBar />}{creating && <CreateCrewWizard studio={creating === 'studio'} initialMode={creating === 'studio' ? 'cowork' : 'dev'} onClose={() => setCreating(null)} onCreate={() => {}} submitting={false} error={null} />}</AppShell></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
