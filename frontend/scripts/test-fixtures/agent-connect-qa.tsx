import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeContext } from '../../src/contexts/ThemeContext'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import { BullpenSurface } from '../../src/products/bullpen/BullpenSurface'
import McpConnectBody from '../../src/components/topbar/McpConnectBody'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { useProductSurfaceStore } from '../../src/stores/useProductSurfaceStore'
import '../../src/index.css'
console.info("QA fixture module initialized")
const params = new URLSearchParams(location.search)
useAuthStore.setState({ user: { id: 'qa-owner', username: 'reviewer', allowed_products: params.has('restricted') ? ['agentworks'] : ['agentworks', 'work', 'bullpen'] }, isAuthenticated: true, isMultiUserMode: false, isMultiUserModeChecked: true })
useProductSurfaceStore.setState({ productSurface: 'bullpen' })
document.documentElement.classList.toggle('dark', params.get('theme') === 'dark')
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
const theme = params.get('theme') === 'dark' ? 'dark' : 'light'
createRoot(document.getElementById('root')!).render(<QueryClientProvider client={queryClient}><ThemeContext.Provider value={{ theme, toggleTheme: () => {}, setTheme: () => {} }}><TooltipProvider>{params.has('mcp') ? <main className="mx-auto max-w-5xl p-4 text-foreground sm:p-6"><McpConnectBody codeReview={false} /></main> : <BullpenSurface />}</TooltipProvider></ThemeContext.Provider></QueryClientProvider>)
