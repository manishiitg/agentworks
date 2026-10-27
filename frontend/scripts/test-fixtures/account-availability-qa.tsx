import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import '../../src/App.css'
import ProviderAccounts from '../../src/components/providers/ProviderAccounts'
import { useAuthStore } from '../../src/stores/useAuthStore'

// The production account component; the browser fixture supplies synthetic API data.
useAuthStore.setState({ user: { id: 'admin-example', username: 'Example admin', is_admin: true, can_create: true } as never, isMultiUserMode: true })
createRoot(document.getElementById('root')!).render(<main className="mx-auto max-w-3xl p-4 sm:p-8"><h1 className="mb-6 text-xl font-semibold">AI models &amp; accounts</h1><ProviderAccounts provider="codex-cli" providerLabel="Codex" /></main>)
