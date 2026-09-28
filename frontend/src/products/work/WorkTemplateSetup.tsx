import { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, CircleAlert, Loader2, RefreshCw, Sparkles } from 'lucide-react'
import { agentApi } from '../../services/api'
import { responseContent } from '../../utils/plannerFiles'
import { parseCrewTemplateSetupState, type CrewTemplate, type CrewTemplateSetupState } from './crewTemplates'

function migrateSetupState(content: string | undefined, template: CrewTemplate): CrewTemplateSetupState {
  const initial = parseCrewTemplateSetupState(template.files[template.setupPath] || '', template)
  if (!initial) throw new Error('This template has no valid setup checklist.')
  let completed: string[] = []
  try {
    const old = JSON.parse(content || '') as { template_id?: unknown; checks?: unknown; completed_steps?: unknown }
    if (old.checks !== undefined || (old.template_id !== undefined && old.template_id !== template.id)) {
      throw new Error('The saved setup checklist is invalid. Ask Crew to inspect it before changing progress.')
    }
    if (Array.isArray(old.completed_steps)) {
      const known = new Set(initial.checks.map(check => check.id))
      completed = old.completed_steps.filter((id): id is string => typeof id === 'string' && known.has(id))
    }
  } catch (cause) {
    if (content?.trim()) throw new Error('The saved setup checklist is invalid. Ask Crew to inspect it before changing progress.', { cause })
    // A missing progress file starts with no completed checks.
  }
  return { ...initial, completed_steps: [...new Set(completed)] }
}

export function WorkTemplateSetup({ template, workspacePath, chatReady = true, onStartSetup }: {
  template: CrewTemplate
  workspacePath: string
  chatReady?: boolean
  onStartSetup: () => Promise<void>
}) {
  const [setup, setSetup] = useState<CrewTemplateSetupState | null>(null)
  const [loading, setLoading] = useState(true)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    const path = `${workspacePath}/${template.setupPath}`
    try {
      let content: string | undefined
      try {
        const response = await agentApi.getPlannerFileContent(path)
        content = responseContent(response)?.content
      } catch (cause) {
        const status = (cause as { response?: { status?: number } })?.response?.status
        if (status !== 404) throw cause
      }
      let current = content?.trim() ? parseCrewTemplateSetupState(content, template) : null
      if (!current) {
        current = migrateSetupState(content, template)
        await agentApi.updatePlannerFile(path, `${JSON.stringify(current, null, 2)}\n`, `Initialize ${template.name} setup checklist`)
      }
      setSetup(current)
      setError('')
    } catch (cause) {
      setSetup(null)
      setError(cause instanceof Error ? cause.message : 'Could not load template setup.')
    } finally {
      setLoading(false)
    }
  }, [template, workspacePath])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 10000)
    return () => window.clearInterval(timer)
  }, [refresh])

  const complete = Boolean(setup && setup.checks.every(check => setup.completed_steps.includes(check.id)))
  const start = async () => {
    if (sending) return
    setSending(true)
    setError('')
    try {
      await onStartSetup()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not start setup chat.')
    } finally {
      setSending(false)
    }
  }

  return (
    <section className="shrink-0 border-b border-border bg-muted/30 px-4 py-2.5" aria-label={`${template.name} setup status`}>
      <div className="mx-auto flex max-w-2xl items-center gap-2">
        {complete ? <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" /> : <Sparkles className="h-4 w-4 shrink-0 text-primary" />}
        <span className="min-w-0 flex-1 text-sm font-semibold text-foreground">{template.name} · {error && !setup ? 'Setup needs attention' : complete ? 'Setup complete' : 'Setup pending'}</span>
        <button type="button" onClick={() => { void refresh() }} disabled={loading} aria-label="Refresh setup status" className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
        <button type="button" onClick={() => { void start() }} disabled={sending || !chatReady} className="shrink-0 rounded-md bg-primary px-2.5 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
          {sending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : !chatReady ? 'Opening chat…' : complete ? 'Review in chat' : 'Set up in chat'}
        </button>
      </div>
      {error ? <p className="mx-auto mt-1 flex max-w-2xl items-center gap-1 text-xs text-destructive"><CircleAlert className="h-3.5 w-3.5" />{error}</p> : null}
    </section>
  )
}
