import { useCallback, useEffect, useMemo, useState } from 'react'
import { CheckCircle2, CircleAlert } from 'lucide-react'
import { llmConfigService, type ProviderConnection } from '../../services/llm-config-api'
import { useLLMStore } from '../../stores/useLLMStore'
import { accountConfigured, accountRelation, accountUsable } from '../../components/providers/ProviderAccounts'
import { loadAgentProfileProviderOptions, type AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'

/** What a new Crew or Code runs on: the coding CLI, its model, and the account. */
export type RunsOnSelection = {
  provider: string
  modelId: string
  reasoningEffort?: string
  /** A person's own or shared account; empty means the server account. */
  connectionId?: string
}

type Choice = {
  option: AgentProfileProviderOption
  label: string
  /** Signed in and usable here; false means the person has to sign in first. */
  ready: boolean
  accountLabel: string
  connectionId?: string
}

const lastChoiceKey = (profileId: string) => `agentworks.runsOn.${profileId}`

const readLastChoice = (profileId: string) => {
  try { return window.localStorage.getItem(lastChoiceKey(profileId)) || '' } catch { return '' }
}

/** Remembers the provider picked for a new project, so the next one starts with it. */
export function rememberRunsOn(profileId: string, provider: string) {
  try { window.localStorage.setItem(lastChoiceKey(profileId), provider) } catch { /* private window */ }
}

const newestFirst = (a: ProviderConnection, b: ProviderConnection) => String(b.updated_at || '').localeCompare(String(a.updated_at || ''))

// The account a new project would run on for one CLI: your own signed-in account first (the server
// uses it by default too), then one shared with you, then the server account. None: sign in.
function choiceFor(option: AgentProfileProviderOption, records: ProviderConnection[]): Choice {
  const provider = option.provider || option.id
  const label = option.label || provider
  const usable = records.filter(record => record.provider === provider && accountUsable(record) && accountConfigured(record))
  const own = usable.filter(record => accountRelation(record) === 'own').sort(newestFirst)[0]
  if (own) return { option, label, ready: true, accountLabel: `your account${own.identity ? ` (${own.identity})` : ''}`, connectionId: own.id }
  const shared = usable.filter(record => accountRelation(record).startsWith('shared')).sort(newestFirst)[0]
  if (shared) return { option, label, ready: true, accountLabel: `shared by ${shared.owner_name || 'a teammate'}`, connectionId: shared.id }
  const server = usable.find(record => record.scope === 'global')
  if (server) return { option, label, ready: true, accountLabel: 'shared account' }
  return { option, label, ready: false, accountLabel: 'not signed in' }
}

const optionEffort = (option: AgentProfileProviderOption) => {
  const effort = option.options?.reasoning_effort
  return typeof effort === 'string' ? effort : option.reasoning_efforts?.[0]
}

/**
 * The "Runs on" row of the create dialogs: which coding CLI and account a new Crew or Code uses.
 * It starts on the CLI used last, else one you are signed in to, so most people just click Create;
 * a CLI you are not signed in to offers Sign in right here. The choice is saved with the project,
 * account included, so the first message works without visiting the Models tab.
 */
export function RunsOnPicker({ profileId, onChange, disabled, options: givenOptions, accountsProduct = profileId }: {
  /** Crew/Code product id; also the key for remembering the last choice ("workflow" for workflows). */
  profileId: string
  onChange: (selection: RunsOnSelection | undefined) => void
  disabled?: boolean
  /** The CLIs to offer; when absent they are the product profile's provider options. */
  options?: AgentProfileProviderOption[]
  /** Product the account list is asked for; empty asks without one (workflows). */
  accountsProduct?: string
}) {
  const [options, setOptions] = useState<AgentProfileProviderOption[]>([])
  const [records, setRecords] = useState<ProviderConnection[]>([])
  const [loaded, setLoaded] = useState(false)
  const [picked, setPicked] = useState('')

  const refreshAccounts = useCallback(() => {
    void llmConfigService.getProviderConnections(accountsProduct ? { product: accountsProduct } : undefined).then(setRecords).catch(() => undefined)
  }, [accountsProduct])

  useEffect(() => {
    let cancelled = false
    void Promise.all([
      givenOptions ? Promise.resolve(givenOptions) : loadAgentProfileProviderOptions(profileId),
      llmConfigService.getProviderConnections(accountsProduct ? { product: accountsProduct } : undefined).catch(() => [] as ProviderConnection[]),
    ]).then(([nextOptions, nextRecords]) => {
      if (cancelled) return
      setOptions(nextOptions.filter(option => option.provider))
      setRecords(nextRecords)
      setLoaded(true)
    })
    return () => { cancelled = true }
  }, [profileId, givenOptions, accountsProduct])

  // Signing in happens on the Providers screen; pick up the new account when the person is back.
  useEffect(() => {
    window.addEventListener('provider-connections-changed', refreshAccounts)
    window.addEventListener('focus', refreshAccounts)
    return () => {
      window.removeEventListener('provider-connections-changed', refreshAccounts)
      window.removeEventListener('focus', refreshAccounts)
    }
  }, [refreshAccounts])

  const choices = useMemo(() => options.map(option => choiceFor(option, records)), [options, records])

  // Start on the CLI used last, else one you are signed in to (your own account first), else the
  // product default, so the default is right for most people.
  const initial = useMemo(() => {
    if (choices.length === 0) return undefined
    const last = readLastChoice(profileId)
    return choices.find(choice => choice.option.id === last && choice.ready)
      || choices.find(choice => choice.ready && choice.accountLabel.startsWith('your account'))
      || choices.find(choice => choice.ready && choice.option.default)
      || choices.find(choice => choice.ready)
      || choices.find(choice => choice.option.default)
      || choices[0]
  }, [choices, profileId])

  const current = choices.find(choice => choice.option.id === picked) || initial

  useEffect(() => {
    if (!current) { onChange(undefined); return }
    const option = current.option
    onChange({
      provider: option.provider || option.id,
      modelId: option.model_id || '',
      reasoningEffort: optionEffort(option),
      connectionId: current.connectionId,
    })
  }, [current, onChange])

  if (!loaded || choices.length === 0 || !current) return null

  return (
    <div className="mt-4" data-testid="runs-on-picker">
      <label className="block text-xs font-semibold text-foreground" htmlFor={`runs-on-${profileId}`}>Runs on</label>
      <select
        id={`runs-on-${profileId}`}
        value={current.option.id}
        disabled={disabled}
        onChange={event => setPicked(event.target.value)}
        className="mt-2 h-10 w-full rounded-lg border border-border bg-background px-3 text-sm outline-none focus:border-primary focus:ring-2 focus:ring-primary/15"
      >
        {choices.map(choice => (
          <option key={choice.option.id} value={choice.option.id}>
            {choice.label} · {choice.ready ? choice.accountLabel : 'sign in needed'}
          </option>
        ))}
      </select>
      {current.ready ? (
        <p className="mt-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />
          Uses {current.accountLabel}. You can change it later in Models.
        </p>
      ) : (
        <p className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[11px] text-amber-700 dark:text-amber-300">
          <CircleAlert className="h-3.5 w-3.5" />
          You are not signed in to {current.label}.
          <button type="button" disabled={disabled} onClick={() => useLLMStore.getState().setShowLLMModal(true)} className="font-semibold text-primary hover:underline">Sign in</button>
          <span className="text-muted-foreground">or create now and sign in later.</span>
        </p>
      )}
    </div>
  )
}
