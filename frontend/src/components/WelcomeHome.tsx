import { useEffect, useMemo, useState } from 'react'
import { Check, Copy, KeyRound, Plug, X } from 'lucide-react'
import { Button } from './ui/Button'
import { PRODUCT_CARDS } from './ProductSurfaceSwitcher'
import { useAuthStore } from '../stores/useAuthStore'
import { useLLMStore } from '../stores/useLLMStore'
import { deploymentDefaultProductSurface, visibleProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { isGuideRemembered, rememberGuide } from '../utils/onboarding'
import { getRuntimeAppName, runtimeBrandAsset, runtimeBrandingConfig } from '../runtime-branding'
import { copyToClipboard } from '../utils/textUtils'

// The first page a person sees: what this server offers them, how to reach it from their own AI tools, and where
// the coding agents are set up. Shown once (then from the event below), instead of an empty product surface.
export const WELCOME_HOME_DISMISSED_KEY = 'agentworks_welcome_home_v1_dismissed'
export const OPEN_WELCOME_HOME_EVENT = 'open-welcome-home'

export function WelcomeHome() {
  // Someone whose account opens exactly one product (a Code-only account) goes straight to it: the welcome page is for choosing
  // between products and connecting tools, and they have nothing to choose. The menu can still open it.
  const [open, setOpen] = useState(() => !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && visibleProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).length !== 1)
  const [copied, setCopied] = useState('')
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const products = useMemo(() => {
    const visible = new Set(visibleProductSurfaceIDs(allowedProducts))
    return PRODUCT_CARDS.filter(product => visible.has(product.id))
  }, [allowedProducts])

  useEffect(() => {
    const show = () => setOpen(true)
    window.addEventListener(OPEN_WELCOME_HOME_EVENT, show)
    return () => window.removeEventListener(OPEN_WELCOME_HOME_EVENT, show)
  }, [])

  if (!open) return null

  const branding = runtimeBrandingConfig()
  const appName = getRuntimeAppName(branding) || 'AgentWorks'
  const mark = runtimeBrandAsset('markUrl', branding)
  const mcpUrl = `${window.location.origin}/api/external/v1/mcp`
  const claudeCommand = `claude mcp add --transport http agentworks ${mcpUrl}`

  const close = () => { rememberGuide(WELCOME_HOME_DISMISSED_KEY); setOpen(false) }
  const openProduct = (surface: ProductSurface) => { close(); openProductWorkspace(surface) }
  const openProviders = () => { close(); useLLMStore.getState().setShowLLMModal(true) }
  const copy = async (label: string, text: string) => {
    if (await copyToClipboard(text)) { setCopied(label); window.setTimeout(() => setCopied(''), 1500) }
  }
  const startSurface = products.some(product => product.id === deploymentDefaultProductSurface()) ? deploymentDefaultProductSurface() : products[0]?.id

  return (
    <div role="dialog" aria-modal="true" aria-label={`Welcome to ${appName}`} className="fixed inset-0 z-[60] overflow-y-auto bg-background">
      <div className="mx-auto flex max-w-4xl flex-col gap-8 px-6 py-10">
        <header className="flex items-start justify-between gap-4">
          <div className="flex items-center gap-3">
            {mark && <img src={mark} alt="" className="h-10 w-10 rounded-lg" />}
            <div>
              <h1 className="text-2xl font-semibold text-foreground">Welcome to {appName}</h1>
              <p className="mt-1 text-sm text-muted-foreground">Agents that work for you: pick where to start, connect your own AI tools, and set up your coding agents.</p>
            </div>
          </div>
          <button type="button" onClick={close} aria-label="Close welcome" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"><X className="h-5 w-5" /></button>
        </header>

        <section aria-labelledby="welcome-products">
          <h2 id="welcome-products" className="text-sm font-semibold text-foreground">Your products</h2>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {products.map(product => {
              const Icon = product.icon
              return (
                <button key={product.id} type="button" onClick={() => openProduct(product.id)}
                  className="flex items-start gap-3 rounded-xl border border-border bg-card p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted/40">
                  <Icon className="mt-0.5 h-6 w-6 shrink-0" />
                  <span>
                    <span className="block text-sm font-semibold text-foreground">{product.label}</span>
                    <span className="mt-0.5 block text-xs leading-5 text-muted-foreground">{product.description}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </section>

        <section aria-labelledby="welcome-mcp" className="rounded-xl border border-border p-4">
          <h2 id="welcome-mcp" className="flex items-center gap-2 text-sm font-semibold text-foreground"><Plug className="h-4 w-4" />Use it from your own AI tools (MCP)</h2>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">Claude Code, Codex, Cursor or any MCP client can list and run your workflows and Crews. Add this server as an HTTP MCP server; you sign in in the browser the first time.</p>
          <div className="mt-3 flex flex-col gap-2">
            {[{ label: 'url', text: mcpUrl }, { label: 'claude', text: claudeCommand }].map(item => (
              <div key={item.label} className="flex items-center gap-2 rounded-md bg-muted px-3 py-2">
                <code className="min-w-0 flex-1 truncate text-xs text-foreground">{item.text}</code>
                <button type="button" onClick={() => void copy(item.label, item.text)} aria-label={`Copy ${item.label === 'url' ? 'MCP address' : 'Claude Code command'}`} className="shrink-0 text-muted-foreground hover:text-foreground">
                  {copied === item.label ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
            ))}
          </div>
        </section>

        <section aria-labelledby="welcome-providers" className="rounded-xl border border-border p-4">
          <h2 id="welcome-providers" className="flex items-center gap-2 text-sm font-semibold text-foreground"><KeyRound className="h-4 w-4" />Coding agents and model keys</h2>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">Agents run on coding CLIs (Claude Code, Codex, Cursor, Muse, Pi). See which ones are ready for you, check usage, or add your own model key (OpenRouter, NVIDIA NIM and more, many free models).</p>
          <Button type="button" variant="outline" size="sm" className="mt-3" onClick={openProviders}>Open Providers</Button>
        </section>

        <footer className="flex flex-wrap items-center gap-3">
          {startSurface && <Button type="button" onClick={() => openProduct(startSurface)}>Start in {PRODUCT_CARDS.find(product => product.id === startSurface)?.label}</Button>}
          <button type="button" onClick={close} className="text-sm text-muted-foreground hover:text-foreground">Skip for now</button>
        </footer>
      </div>
    </div>
  )
}
