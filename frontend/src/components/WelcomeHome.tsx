import { useEffect, useMemo, useState } from 'react'
import { ArrowUpRight, Check, Copy, KeyRound, Plug, Terminal, X } from 'lucide-react'
import { Button } from './ui/Button'
import { PRODUCT_CARDS } from './ProductSurfaceSwitcher'
import { useAuthStore } from '../stores/useAuthStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { productRoutePath } from '../utils/productRoutes'
import { navigationProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { isGuideRemembered, rememberGuide } from '../utils/onboarding'
import { getRuntimeAppName, runtimeBrandAsset, runtimeBrandingConfig } from '../runtime-branding'
import { copyToClipboard } from '../utils/textUtils'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'

// The first page a person sees: what this server offers them, how to reach it from their own AI tools, and where
// the coding agents are set up. Shown once (then from the event below), instead of an empty product surface.
export const WELCOME_HOME_DISMISSED_KEY = 'agentworks_welcome_home_v1_dismissed'
export const OPEN_WELCOME_HOME_EVENT = 'open-welcome-home'
const PERSONAL_PRODUCTS = new Set<string>(['video-studio', 'sparkquill'])
const WORK_PRODUCTS = new Set<string>(['agentworks', 'work', 'code', 'relays'])

export function WelcomeHome({ autoOpen = true }: { autoOpen?: boolean }) {
  // Someone whose account opens exactly one product (a Code-only account) goes straight to it: the welcome page is for choosing
  // between products and connecting tools, and they have nothing to choose. The menu can still open it.
  const [open, setOpen] = useState(() => autoOpen && !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && navigationProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).length !== 1)
  const [copied, setCopied] = useState('')
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const products = useMemo(() => {
    const visible = new Set(navigationProductSurfaceIDs(allowedProducts))
    // Personal products are never named in the shared welcome, even where enabled; they stay in the product switcher.
    return PRODUCT_CARDS.filter(product => visible.has(product.id) && !PERSONAL_PRODUCTS.has(product.id))
  }, [allowedProducts])

  useEffect(() => {
    const show = () => setOpen(true)
    window.addEventListener(OPEN_WELCOME_HOME_EVENT, show)
    return () => window.removeEventListener(OPEN_WELCOME_HOME_EVENT, show)
  }, [])

  useEffect(() => {
    if (autoOpen && !isGuideRemembered(WELCOME_HOME_DISMISSED_KEY) && products.length > 1) setOpen(true)
  }, [autoOpen, products.length])

  if (!open) return null

  const branding = runtimeBrandingConfig()
  const appName = getRuntimeAppName(branding) || 'AgentWorks'
  const mark = runtimeBrandAsset('markUrl', branding)
  const mcpUrl = `${window.location.origin}/api/external/v1/mcp`
  const agentBridgeEnabled = products.some(product => product.id === 'bullpen')
  const agentBridgeSetupUrl = productRoutePath({ surface: 'bullpen', view: 'setup' })

  const close = () => { rememberGuide(WELCOME_HOME_DISMISSED_KEY); useWorkspaceReturnStore.setState({ firstVisit: false }); setOpen(false) }
  const openProduct = (surface: ProductSurface) => { close(); openProductWorkspace(surface) }
  const openMcpSetup = () => { close(); useAppStore.getState().setAdminPage('mcp') }
  const openProviders = () => { close(); useLLMStore.getState().setShowLLMModal(true) }
  const workProducts = products.filter(product => WORK_PRODUCTS.has(product.id))
  const services = products.filter(product => !WORK_PRODUCTS.has(product.id))
  const productCard = (product: typeof products[number]) => {
    const Icon = product.icon
    return (
      <button key={product.id} type="button" onClick={() => openProduct(product.id)}
        className="group flex items-start gap-4 rounded-xl border border-border bg-card p-5 text-left transition-colors hover:border-primary/50 hover:bg-muted/40">
        <Icon className="mt-0.5 h-7 w-7 shrink-0" />
        <span className="flex-1">
          <span className="block text-sm font-semibold text-foreground">{product.label}</span>
          <span className="mt-0.5 block text-xs leading-5 text-muted-foreground">{product.description}</span>
        </span>
        <ArrowUpRight className="h-4 w-4 shrink-0 text-muted-foreground group-hover:text-foreground" />
      </button>
    )
  }
  const copy = async (label: string, text: string) => {
    if (await copyToClipboard(text)) { setCopied(label); window.setTimeout(() => setCopied(''), 1500) }
  }

  return (
    <div role="dialog" aria-modal="true" aria-label={`Welcome to ${appName}`} className="fixed inset-0 z-[60] overflow-y-auto bg-background">
      <div className="mx-auto flex min-h-screen max-w-4xl flex-col justify-start gap-8 px-6 py-12">
        <header className="flex items-start justify-between gap-4">
          <div className="flex items-center gap-3">
            {mark && <img src={mark} alt="" className="h-10 w-10 rounded-lg" />}
            <div>
              <p className="text-sm text-muted-foreground">Welcome to {appName}</p>
              <h1 className="mt-2 text-3xl font-semibold tracking-tight text-foreground">Put your agents to work.</h1>
              <p className="mt-3 max-w-xl text-sm leading-6 text-muted-foreground">Run work in AgentWorks or use it from the AI tools you already have. When you return, your last workspace and chat will be ready.</p>
            </div>
          </div>
          <button type="button" onClick={close} aria-label="Close welcome" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"><X className="h-5 w-5" /></button>
        </header>

        {workProducts.length > 0 && <section aria-labelledby="welcome-products">
          <h2 id="welcome-products" className="text-sm font-semibold text-foreground">Choose where to work</h2>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {workProducts.map(productCard)}
          </div>
        </section>}

        {services.length > 0 && <details open={workProducts.length === 0} className="rounded-xl border border-border p-4">
          <summary className="cursor-pointer text-sm font-semibold text-foreground">Shared services</summary>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">Manage shared tools, knowledge and agent connections available to your account.</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2" aria-label="Shared services">
            {services.map(productCard)}
          </div>
        </details>}

        <section aria-labelledby="welcome-tools" className="rounded-2xl border border-primary/25 bg-primary/[0.04] p-5 sm:p-6">
          <h2 id="welcome-tools" className="text-xl font-semibold tracking-tight">Use {appName} from the AI you already use</h2>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">Keep working in Claude Cowork, ChatGPT, Claude Code, Codex, Cursor or another compatible AI tool. Choose the connection for what you want to do.</p>
          <div className="mt-5 grid gap-4 md:grid-cols-2">
            <section aria-labelledby="welcome-mcp" className="flex flex-col rounded-xl border border-border bg-background/70 p-4">
              <div className="flex items-center gap-2 text-primary"><Plug className="h-4 w-4" aria-hidden="true" /><span className="text-xs font-medium">MCP tools</span></div>
              <h3 id="welcome-mcp" className="mt-2 text-sm font-semibold">Use your products from your AI app</h3>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">Add {appName} as an MCP server in your compatible AI app or coding tool. Your assistant can then use the product tools your account has permission to access.</p>
              <div className="my-3 flex items-start gap-2 rounded-lg bg-muted/60 p-2.5">
                <code className="min-w-0 flex-1 break-all text-[11px] leading-5" aria-label="MCP server address">{mcpUrl}</code>
                <button type="button" onClick={() => void copy('url', mcpUrl)} aria-label="Copy MCP address" className="shrink-0 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground">
                  {copied === 'url' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                </button>
              </div>
              <p className="mb-3 text-xs leading-5 text-muted-foreground">Use a client that can reach this server address, then sign in through your browser when prompted.</p>
              <Button type="button" variant="outline" size="sm" className="mt-auto self-start" onClick={openMcpSetup}>Open MCP setup</Button>
            </section>
            <section aria-labelledby="welcome-cli" className="flex flex-col rounded-xl border border-border bg-background/70 p-4">
              <div className="flex items-center gap-2 text-primary"><Terminal className="h-4 w-4" aria-hidden="true" /><span className="text-xs font-medium">AgentWorks CLI</span></div>
              <h3 id="welcome-cli" className="mt-2 text-sm font-semibold">Set up from your terminal{agentBridgeEnabled ? ' and connect a chat' : ''}</h3>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">Install the AgentWorks command-line tool to sign in to this server and set up the integration for your AI client.</p>
              {agentBridgeEnabled && <>
                <p className="mt-3 text-xs leading-5 text-muted-foreground">Connect a supported coding chat to <strong className="font-medium text-foreground">AgentBridge</strong> so agents can find each other, send messages and reply while you keep working in your coding tool.</p>
                <p className="my-3 text-xs leading-5 text-muted-foreground">Live chat messaging requires a supported client integration. The setup guide lists the available clients and their delivery behavior.</p>
              </>}
              {agentBridgeEnabled ? <a href={agentBridgeSetupUrl} onClick={close} className="mt-auto inline-flex h-8 self-start items-center gap-2 rounded-md border border-input bg-background px-3 text-xs font-medium hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Set up CLI &amp; messaging<ArrowUpRight className="h-3.5 w-3.5" aria-hidden="true" /></a> : <Button type="button" variant="outline" size="sm" className="mt-4 self-start" onClick={openMcpSetup}>Open client setup</Button>}
            </section>
          </div>
          <section aria-labelledby="welcome-providers" className="mt-4 flex flex-wrap items-center justify-between gap-4 rounded-xl border border-border bg-background/50 p-4">
            <div className="max-w-xl">
              <h3 id="welcome-providers" className="flex items-center gap-2 text-sm font-semibold"><KeyRound className="h-4 w-4 text-primary" aria-hidden="true" />AI models &amp; accounts · run agents inside {appName}</h3>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">Connect a supported coding-agent account or a model API key. {appName} uses it to run agents in your workspaces.</p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={openProviders}>Connect AI accounts</Button>
          </section>
        </section>

      </div>
    </div>
  )
}
