import { useState } from 'react'
import { ArrowLeft, CheckCircle2, CircleAlert, ExternalLink, KeyRound, Loader2, X } from 'lucide-react'
import ByokModelBrowser from './ByokModelBrowser'
import { llmConfigService, providerApiErrorText, type ByokCheck, type ProviderConnection } from '../../services/llm-config-api'
import { BYOK_SERVICES, type ByokService } from '../../utils/byok'

type Step = 'service' | 'key' | 'models'

/**
 * "Use your own model key": pick a service, paste and test the key, choose
 * models. Saves a private Pi account (Pi runs the turns; nobody has to know
 * Pi provider ids). Sharing is set later from the account's menu.
 */
export default function ByokSetup({ onSaved, onCancel, disabled = false, initialService }: {
  onSaved: (record: ProviderConnection, picks: string[]) => void
  onCancel: () => void
  disabled?: boolean
  initialService?: string
}) {
  const initial = BYOK_SERVICES.find(service => service.id === initialService)
  const [step, setStep] = useState<Step>(initial ? 'key' : 'service')
  const [service, setService] = useState<ByokService | undefined>(initial)
  const [name, setName] = useState(initial ? `${initial.label} key` : '')
  const [credential, setCredential] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [piProvider, setPiProvider] = useState('')
  const [check, setCheck] = useState<ByokCheck | null>(null)
  const [testing, setTesting] = useState(false)
  const [picks, setPicks] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const choose = (next: ByokService) => {
    setService(next); setName(`${next.label} key`); setCheck(null); setError(null); setStep('key'); setPicks([])
  }
  const keyRequest = service ? { service: service.id, credential, ...(service.needs === 'base_url' ? { base_url: baseUrl } : {}) } : {}
  const test = async () => {
    setTesting(true); setCheck(null)
    try { setCheck(await llmConfigService.byokTestKey(keyRequest)) }
    catch (testError) { setCheck({ state: 'error', detail: providerApiErrorText(testError, 'The check failed.') }) }
    finally { setTesting(false) }
  }
  const save = async () => {
    if (!service) return
    setBusy(true); setError(null)
    try {
      const other = service.needs === 'pi_provider'
      const record = await llmConfigService.addProviderConnection({
        provider: 'pi-cli', display_name: name.trim() || `${service.label} key`, credential,
        underlying_provider: other ? piProvider.trim() : service.id,
        ...(service.needs === 'base_url' ? { base_url: baseUrl.trim() } : {}),
        ...(!other && picks.length > 0 ? { allowed_models: picks } : {}),
      })
      onSaved({ relation: 'own', kind: 'user', can_manage: true, ...record, allowed_models: other ? record.allowed_models : picks }, other ? [] : picks)
    } catch (saveError) { setError(providerApiErrorText(saveError, 'Could not save the key.')) }
    finally { setBusy(false) }
  }

  const inputClass = 'mt-1 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-500 focus:ring-2 focus:ring-violet-500/20 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100'
  const secondary = 'inline-flex items-center justify-center gap-1.5 rounded-lg border border-gray-300 bg-white px-3 py-2 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700'
  const primary = 'inline-flex items-center gap-2 rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-white hover:bg-violet-500 disabled:cursor-not-allowed disabled:opacity-50'
  const keyReady = credential.trim() !== '' && (service?.needs !== 'base_url' || baseUrl.trim() !== '') && (service?.needs !== 'pi_provider' || piProvider.trim() !== '')
  const steps: Step[] = service?.needs === 'pi_provider' ? ['service', 'key'] : ['service', 'key', 'models']

  return (
    <section aria-label="Use your own model key" className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-2">
          {step !== 'service' && <button type="button" aria-label="Back" className="mt-0.5 rounded p-1 text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={() => setStep(step === 'models' ? 'key' : 'service')}><ArrowLeft className="h-4 w-4" /></button>}
          <div>
            <h4 className="flex items-center gap-1.5 text-sm font-semibold text-gray-900 dark:text-gray-100"><KeyRound className="h-4 w-4 text-violet-600 dark:text-violet-300" />{step === 'service' ? 'Use your own model key' : service?.label}</h4>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">Step {steps.indexOf(step) + 1} of {steps.length}: {step === 'service' ? 'pick a service' : step === 'key' ? 'paste your key' : 'choose models'}. Your key stays private to you.</p>
          </div>
        </div>
        <button type="button" aria-label="Close" className="rounded-lg p-1 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={onCancel}><X className="h-4 w-4" /></button>
      </div>

      {step === 'service' && (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {BYOK_SERVICES.map(option => (
            <div key={option.id} className={`rounded-lg border transition-colors hover:border-violet-300 hover:bg-violet-50/40 dark:hover:border-violet-500/50 dark:hover:bg-violet-500/5 ${option.recommended ? 'border-violet-300 dark:border-violet-500/40' : 'border-gray-200 dark:border-gray-700'}`}>
              <button type="button" disabled={disabled} onClick={() => choose(option)} className="block w-full px-3 pt-3 text-left disabled:opacity-50">
                <span className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-100">{option.label}{option.recommended && <span className="rounded bg-violet-100 px-1.5 py-0.5 text-[10px] font-medium text-violet-700 dark:bg-violet-500/15 dark:text-violet-300">Recommended</span>}</span>
                <span className="mt-0.5 block text-xs text-gray-600 dark:text-gray-300">{option.blurb}</span>
                <span className="mt-1 block text-[11px] text-gray-500 dark:text-gray-400">Free tier: {option.freeTier}</span>
              </button>
              <div className="px-3 pb-3 pt-1">{option.keyUrl
                ? <a href={option.keyUrl} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[11px] font-medium text-violet-600 hover:underline dark:text-violet-400">Get a key <ExternalLink className="h-3 w-3" /></a>
                : <span className="text-[11px] text-gray-400">Use the key your service gave you</span>}</div>
            </div>
          ))}
        </div>
      )}

      {step === 'key' && service && (
        <form className="space-y-3" onSubmit={event => { event.preventDefault(); if (!keyReady) return; if (service.needs === 'pi_provider') void save(); else setStep('models') }}>
          {service.keyUrl && <p className="text-xs text-gray-600 dark:text-gray-300">Create a key at <a href={service.keyUrl} target="_blank" rel="noreferrer" className="font-medium text-violet-600 hover:underline dark:text-violet-400">{service.keyLabel}</a>{service.keyHint ? <> (it looks like <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">{service.keyHint}</code>)</> : null} and paste it here.</p>}
          {service.needs === 'base_url' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Base URL<input required value={baseUrl} onChange={event => { setBaseUrl(event.target.value); setCheck(null) }} placeholder="https://api.example.com/v1" className={inputClass} /></label>}
          {service.needs === 'pi_provider' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Pi provider id<input required value={piProvider} onChange={event => setPiProvider(event.target.value)} placeholder="e.g. zai, deepseek, mistral, xai" className={inputClass} /></label>}
          <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">API key<input required type="password" autoComplete="new-password" value={credential} onChange={event => { setCredential(event.target.value); setCheck(null) }} placeholder={service.keyHint || 'Paste your key'} className={inputClass} /></label>
          <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Name <span className="font-normal text-gray-400">(optional)</span><input maxLength={120} value={name} onChange={event => setName(event.target.value)} className={inputClass} /></label>
          {service.needs !== 'pi_provider' && <div className="flex flex-wrap items-center gap-2">
            <button type="button" disabled={!keyReady || testing} className={secondary} onClick={() => void test()}>{testing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}Test key</button>
            {check && <span role="status" className={`flex items-center gap-1 text-xs ${check.state === 'ok' ? 'text-emerald-700 dark:text-emerald-300' : check.state === 'rate_limited' ? 'text-amber-700 dark:text-amber-300' : 'text-red-700 dark:text-red-300'}`}>
              {check.state === 'ok' ? <CheckCircle2 className="h-3.5 w-3.5" /> : <CircleAlert className="h-3.5 w-3.5" />}
              {check.state === 'ok' ? `Key works${check.identity ? ` · ${check.identity}` : ''}${check.detail ? `. ${check.detail}` : ''}` : check.detail}
            </span>}
          </div>}
          <div className="flex gap-2">
            <button type="submit" disabled={!keyReady || busy || disabled || check?.state === 'rejected'} className={primary}>{busy && <Loader2 className="h-4 w-4 animate-spin" />}{service.needs === 'pi_provider' ? 'Save key' : 'Next: choose models'}</button>
            <button type="button" className={secondary} onClick={onCancel}>Cancel</button>
          </div>
        </form>
      )}

      {step === 'models' && service && (
        <div className="space-y-3">
          <ByokModelBrowser request={keyRequest} service={service.id} value={picks} onChange={setPicks} manualIds={service.needs === 'base_url'}
            onLoaded={(_, defaultModel) => setPicks(current => current.length > 0 || !defaultModel ? current : [defaultModel])} />
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" disabled={picks.length === 0 || busy || disabled} className={primary} onClick={() => void save()}>{busy && <Loader2 className="h-4 w-4 animate-spin" />}Save key and {picks.length === 1 ? '1 model' : `${picks.length} models`}</button>
            <button type="button" className={secondary} onClick={onCancel}>Cancel</button>
            {picks.length === 0 && <span className="text-xs text-amber-700 dark:text-amber-300">Star at least one model.</span>}
          </div>
        </div>
      )}
      {error && <p role="alert" className="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>}
    </section>
  )
}
