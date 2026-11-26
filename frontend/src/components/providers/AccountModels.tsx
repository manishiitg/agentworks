import { useEffect, useState, type ReactNode } from 'react'
import { llmConfigService, type DynamicModelEntry } from '../../services/llm-config-api'
import { filterAllowedModels, hasModelLimit } from '../../utils/allowedModels'

/** Programs that list the models of the signed-in account itself (its plan decides); the rest use the app's catalog. */
const ACCOUNT_LISTED = new Set(['cursor-cli', 'agy-cli'])
const PSEUDO_MODELS = new Set(['high', 'medium', 'low', 'default', 'auto'])

/** One model per row: "Gemini 4 Neon (High)" and "Gemini 4 Neon (Low)" are one model with two levels. */
const familyOf = (model: DynamicModelEntry) => model.family || model.model_name.replace(/\s*\([^)]*\)\s*$/, '') || model.model_id

/**
 * The "Models:" line of an account card: which models this account can run, read from the account itself for
 * Cursor and Antigravity (what its plan offers) and from the app's catalog for the others, within any limit set on
 * the account. "Show" lists them by maker.
 */
export function AccountModelsLine({ provider, accountId, allowed, action }: {
  provider: string
  accountId: string
  allowed?: string[] | null
  action?: ReactNode
}) {
  const [models, setModels] = useState<DynamicModelEntry[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [open, setOpen] = useState(false)
  const live = ACCOUNT_LISTED.has(provider)
  useEffect(() => {
    let cancelled = false
    setModels(null); setFailed(false)
    const request = live
      ? llmConfigService.getProviderModels(provider, false, false, { connection_id: accountId, workspace_path: '', product: '' })
      : llmConfigService.getProviderModels(provider, true)
    request.then(result => {
      if (cancelled) return
      setModels((result.models || []).filter(model => model.model_id !== provider && !PSEUDO_MODELS.has(model.model_id.toLowerCase())))
    }).catch(() => { if (!cancelled) setFailed(true) })
    return () => { cancelled = true }
  }, [provider, accountId, live])

  // The account picker already applies the account's limit; the catalog does not.
  const runnable = models && !live ? filterAllowedModels(models, allowed) : models
  const families = new Map<string, Set<string>>()
  for (const model of runnable || []) {
    const group = model.group && model.group !== 'Default' ? model.group : ''
    if (!families.has(group)) families.set(group, new Set())
    families.get(group)!.add(familyOf(model))
  }
  const count = [...families.values()].reduce((sum, names) => sum + names.size, 0)
  const limited = hasModelLimit(allowed) && !allowed!.includes('*')
  const summary = failed ? 'could not be listed' : runnable === null ? '…' : count === 0 ? 'none listed'
    : `${count} ${count === 1 ? 'model' : 'models'}${limited ? ' (limited)' : live ? ' from this account' : ''}`

  return <div className="mt-0.5 text-xs text-muted-foreground">
    <p>
      Models: {summary}
      {count > 0 && <button type="button" className="ml-1.5 text-primary hover:underline" onClick={() => setOpen(value => !value)}>{open ? 'Hide' : 'Show'}</button>}
      {action}
    </p>
    {open && <ul className="mt-1 space-y-0.5">
      {[...families.entries()].map(([group, names]) => <li key={group || 'models'}>
        {group && <span className="font-medium text-foreground">{group}: </span>}{[...names].join(', ')}
      </li>)}
    </ul>}
  </div>
}
