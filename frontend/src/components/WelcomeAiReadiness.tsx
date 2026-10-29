import { codingProviderName } from '../utils/codingProviderName'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowRight, CheckCircle2, KeyRound, Loader2, LogIn, Terminal } from 'lucide-react'
import { llmConfigService, type ProviderConnection, type ProviderManifestEntry } from '../services/llm-config-api'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { PRODUCT_SURFACE_LABELS, type ProductSurface } from '../products/productSurfaceConfig'
import { Button } from './ui/Button'

type Inventory = { providers: ProviderManifestEntry[]; accounts: { product: ProductSurface; connections: ProviderConnection[] }[] }
type Check = { scope: string; status: 'loading' | 'loaded' | 'error'; inventory?: Inventory }

/** Check actual caller-scoped accounts, not the saved model defaults (which may exist before sign-in).
 *  When a Crew account is ready, crewReadyView replaces the card (AgentWorks is a Crew). */
export default function WelcomeAiReadiness({ products, onSetup, crewReadyView }: {
  products: ProductSurface[]
  onSetup: () => void
  crewReadyView?: ReactNode
}) {
  const user = useAuthStore(state => state.user)
  const profile = useWorkspaceConnectionStore(state => state.profiles.find(item => item.id === state.activeWorkspaceId))
  const workspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const productKey = products.join('|')
  const scope = JSON.stringify([workspaceId, profile?.apiBaseUrl, user?.id, productKey])
  const [check, setCheck] = useState<Check>({ scope, status: 'loading' })
  const [revision, setRevision] = useState(0)
  const refresh = useCallback(() => setRevision(value => value + 1), [])
  const readyRef = useRef(false)

  useEffect(() => {
    let current = true
    const permittedProducts = productKey.split('|').filter(Boolean) as ProductSurface[]
    // A recheck (focus, account change) keeps showing the last result, so a draft in crewReadyView is not lost.
    setCheck(previous => previous.scope === scope && previous.status === 'loaded' ? previous : { scope, status: 'loading' })
    void Promise.all([
      llmConfigService.getProviderManifest(),
      Promise.all(permittedProducts.map(async product => ({ product, connections: await llmConfigService.getProviderConnections({ product }) }))),
    ]).then(([manifest, accounts]) => {
      if (current) setCheck({ scope, status: 'loaded', inventory: {
        providers: manifest.providers.filter(provider => provider.integration_kind === 'coding_agent' && !provider.deprecated), accounts,
      } })
    }).catch(() => { if (current) setCheck({ scope, status: 'error' }) })
    return () => { current = false }
  }, [scope, productKey, user, revision])

  useEffect(() => {
    // Coming back to the tab rechecks only while no account is ready (one may have just been connected elsewhere);
    // a ready result stays until the accounts actually change.
    const refreshUntilReady = () => { if (!readyRef.current) refresh() }
    window.addEventListener('provider-connections-changed', refresh)
    window.addEventListener('focus', refreshUntilReady)
    return () => {
      window.removeEventListener('provider-connections-changed', refresh)
      window.removeEventListener('focus', refreshUntilReady)
    }
  }, [refresh])

  const state = check.scope === scope ? check : { scope, status: 'loading' as const }
  const inventory = state.inventory
  const eligible = (account: ProviderConnection) => account.usable !== false && account.relation !== 'admin_view'
  const runtime = (account: ProviderConnection) => inventory?.providers.find(provider => provider.id === account.provider)
  const readyProducts = inventory?.accounts.filter(group => group.connections.some(account => eligible(account) && account.configured === true && runtime(account)?.runtime_available === true)) ?? []
  const ready = readyProducts.length > 0
  readyRef.current = ready
  const accountSummary = [
    { label: 'Personal', relations: ['own'] },
    { label: 'Company', relations: ['server'] },
    { label: 'Shared with you', relations: ['shared_with_you', 'shared_with_workflow', 'shared_with_crew'] },
  ].map(group => {
    const providers = new Map<string, string>()
    for (const { connections } of readyProducts) {
      for (const account of connections) {
        const provider = runtime(account)
        const relation = account.relation ?? (account.scope === 'global' ? 'server' : 'own')
        if (group.relations.includes(relation) && eligible(account) && account.configured === true && provider?.runtime_available === true) {
          providers.set(provider.id, codingProviderName(provider))
        }
      }
    }
    return { label: group.label, providers: [...providers.values()] }
  }).filter(group => group.providers.length > 0)
  const unknown = inventory?.accounts.some(group => group.connections.some(account => eligible(account) && account.configured !== false && runtime(account) && (account.configured === undefined || runtime(account)?.runtime_available === undefined)))
  const connections = inventory?.accounts.flatMap(group => group.connections) ?? []
  const canAddPersonal = (provider: ProviderManifestEntry) => connections.some(account => account.provider === provider.id && account.scope === 'global' && account.personal_accounts_allowed === true)
  const personalAllowed = inventory?.providers.some(canAddPersonal)

  if (crewReadyView && readyProducts.some(group => group.product === 'work')) return <>{crewReadyView}</>
  return <>
    <section aria-label="AI account setup" className={`rounded-2xl border p-4 sm:p-5 ${ready ? 'border-border bg-card' : 'border-primary/30 bg-primary/[0.04]'}`}>
      {state.status === 'loading' ? <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />Checking your AI accounts…</p> : <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div className="flex items-start gap-3">
          <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ${ready ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'bg-primary/10 text-primary'}`}>{ready ? <CheckCircle2 className="h-5 w-5" aria-hidden="true" /> : <KeyRound className="h-5 w-5" aria-hidden="true" />}</span>
          <div>
            <h2 className="text-sm font-semibold">{state.status === 'error' ? 'Check your AI setup' : ready ? 'Your AI account is ready' : unknown ? 'Check your AI account' : 'First, connect an AI account'}</h2>
            <p className="mt-1 max-w-xl text-xs leading-5 text-muted-foreground">{state.status === 'error' ? 'Account status could not be checked. Retry or open setup to review it.' : ready ? `Available for ${readyProducts.map(group => PRODUCT_SURFACE_LABELS[group.product]).join(', ')}. Switch accounts under “Runs on”.` : unknown ? 'Sign-in status is not confirmed yet. Open setup to check your account.' : 'Running agents needs an AI account. No ready account is available to you for new work yet.'}</p>
            {ready && <dl aria-label="Available AI accounts" className="mt-2 flex flex-wrap gap-x-4 gap-y-1.5 text-xs">
              {accountSummary.map(group => <div key={group.label} className="flex flex-wrap items-baseline gap-x-1.5 rounded-md bg-muted/60 px-2 py-1">
                <dt className="text-muted-foreground">{group.label}</dt>
                <dd className="font-medium text-foreground">{group.providers.join(', ')}</dd>
              </div>)}
            </dl>}
            {state.status === 'loaded' && !ready && !unknown && <p className="mt-1 text-xs leading-5 text-muted-foreground">{personalAllowed ? 'Add your own login or model key, or ask your administrator for a shared account.' : 'Ask your administrator to provide a shared account or enable personal accounts.'}</p>}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {state.status === 'error' && <button type="button" onClick={refresh} className="rounded-md text-xs text-muted-foreground underline underline-offset-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Retry</button>}
          <Button type="button" variant={ready ? 'outline' : 'default'} onClick={onSetup}>{ready ? 'Manage AI accounts' : unknown || !personalAllowed ? 'Review AI setup' : 'Set up my AI'}<ArrowRight className="ml-2 h-4 w-4" aria-hidden="true" /></Button>
        </div>
      </div>}
      {state.status === 'loaded' && !ready && !unknown && <ol aria-label="AI setup steps" className="mt-4 grid grid-cols-3 gap-2 border-t border-primary/15 pt-3 text-xs text-muted-foreground">
        {[{ icon: Terminal, label: 'Choose a CLI' }, { icon: LogIn, label: 'Sign in or add a key' }, { icon: CheckCircle2, label: 'Start your work' }].map((step, index) => <li key={step.label} className="flex items-center gap-2"><step.icon className="h-4 w-4 shrink-0 text-primary" aria-hidden="true" /><span><span className="sr-only">{index + 1}. </span>{step.label}</span></li>)}
      </ol>}
    </section>
  </>
}
