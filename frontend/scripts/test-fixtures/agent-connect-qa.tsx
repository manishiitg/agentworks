import { createRoot } from 'react-dom/client'
import McpConnectBody from '../../src/components/topbar/McpConnectBody'
import { useAuthStore } from '../../src/stores/useAuthStore'
import '../../src/index.css'
useAuthStore.setState({ isMultiUserMode: false, isMultiUserModeChecked: true })
document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') === 'dark')
createRoot(document.getElementById('root')!).render(<main className="mx-auto max-w-5xl p-4 text-foreground sm:p-6"><McpConnectBody codeReview={false} /></main>)
