import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, RotateCcw } from 'lucide-react'
import type { AgentLLMConfig } from '../../services/api-types'
import type { LLMOption } from '../../types/llm'
import { llmOptionsKey } from '../../utils/llmConfigDisplay'
import { roleModelSummary, effortLabel, modelDisplayName } from '../../utils/roleModelSummary'
import { Button } from '../ui/Button'
import { RoleModelPopover } from './RoleModelPopover'

export type RoleDef = {
  key: string
  label: string
  description: string
  group: string
}

const sameConfig = (a?: AgentLLMConfig, b?: AgentLLMConfig) => {
  const key = (value?: AgentLLMConfig) => value
    ? `${value.published_llm_id || ''}|${value.provider}|${value.model_id}|${llmOptionsKey(value.options)}|${value.connection_id || ''}`
    : ''
  return key(a) === key(b)
}

type Props = {
  roles: RoleDef[]
  /** What each role is saved as now (falls back to the provider default when it has none). */
  values: Record<string, AgentLLMConfig | undefined>
  defaults: Record<string, AgentLLMConfig | undefined>
  /** True when the workflow pins its roles (explicit mode) rather than following provider defaults. */
  pinned: boolean
  readOnly: boolean
  disabledTitle?: string
  available: LLMOption[]
  /** The agent, model, effort and account pickers for one value. */
  renderPickers: (value: AgentLLMConfig, onChange: (next: AgentLLMConfig) => void) => ReactNode
  onApplyAll: (next: AgentLLMConfig) => void
  onUpdateRole: (key: string, next: AgentLLMConfig) => void
  onResetRole: (key: string) => void
  onUseDefaults: () => void
  canUseDefaults: boolean
}

/**
 * Models for a workflow's roles. By default one Model card sets every role to
 * the same agent, model, effort and account; an opt-in switch reveals a
 * compact list with one popover per role.
 */
export function WorkflowRoleModels({
  roles, values, defaults, pinned, readOnly, disabledTitle, available, renderPickers,
  onApplyAll, onUpdateRole, onResetRole, onUseDefaults, canUseDefaults,
}: Props) {
  // The single card represents the chat beside it, whose runtime is Builder.
  const primaryRole = roles.find(role => role.key === 'builder_llm') ?? roles[0]
  const primaryKey = primaryRole.key
  const primary = values[primaryKey]
  const differing = useMemo(() => pinned && roles.some(role => !sameConfig(values[role.key], primary)), [pinned, roles, values, primary])
  const [perRole, setPerRole] = useState(differing)
  const [confirmingOff, setConfirmingOff] = useState(false)
  const [cardOpen, setCardOpen] = useState(true)
  // A saved per-role setup arrives after the first render in some screens.
  useEffect(() => { if (differing) setPerRole(true) }, [differing])
  useEffect(() => { if (!pinned) setPerRole(false) }, [pinned])

  const toggle = () => {
    if (!perRole) { setPerRole(true); return }
    if (differing) { setConfirmingOff(open => !open); return }
    setPerRole(false)
  }
  const collapseToPrimary = () => {
    if (primary) onApplyAll(primary)
    setConfirmingOff(false)
    setPerRole(false)
  }

  const summary = roleModelSummary(available, primary)
  const effort = primary ? effortLabel(primary.options) : ''
  const cardLine = primary
    ? `${modelDisplayName(available, primary) || 'Provider default'}${effort ? ` · ${effort} reasoning` : ''}`
    : 'Select a provider first.'
  const groups = Array.from(new Set(roles.map(role => role.group)))

  return (
    <div className="space-y-3">
      {!perRole && (
        <section className="overflow-hidden rounded-xl border border-border bg-card">
          <button
            type="button"
            aria-expanded={cardOpen}
            onClick={() => setCardOpen(open => !open)}
            className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40"
          >
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">Model</h3>
              <p className="mt-0.5 truncate text-xs text-muted-foreground">{cardLine}</p>
            </div>
            <ChevronDown className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${cardOpen ? 'rotate-180' : ''}`} />
          </button>
          {cardOpen && (
            <div className="space-y-2 border-t border-border px-4 py-3">
              {primary ? renderPickers(primary, onApplyAll) : <span className="text-xs text-muted-foreground">Select a provider first.</span>}
              {!pinned && <p className="text-xs text-muted-foreground">Showing the Builder chat default. Other roles use their provider defaults. Choosing here applies one model and effort to every role.</p>}
            </div>
          )}
        </section>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <button
          type="button"
          role="switch"
          aria-checked={perRole}
          disabled={readOnly}
          title={readOnly ? disabledTitle : undefined}
          onClick={toggle}
          className="inline-flex items-center gap-2 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-50"
        >
          <span className={`relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors ${perRole ? 'bg-primary' : 'bg-muted-foreground/40'}`} aria-hidden>
            <span className={`h-3 w-3 rounded-full bg-background shadow transition-transform ${perRole ? 'translate-x-3.5' : 'translate-x-0.5'}`} />
          </span>
          Use different models for different roles
        </button>
        {pinned && (
          <Button type="button" variant="link" size="xs" onClick={onUseDefaults} disabled={readOnly || !canUseDefaults}
            title={readOnly ? disabledTitle : canUseDefaults ? undefined : 'No provider profile to return to'}>
            Use provider defaults for all roles
          </Button>
        )}
      </div>

      {confirmingOff && (
        <div role="status" className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
          <span className="min-w-0 flex-1">Every role will use {summary} (the {primaryRole.label} setting).</span>
          <Button type="button" size="xs" onClick={collapseToPrimary} disabled={readOnly}>Use for all roles</Button>
          <Button type="button" variant="outline" size="xs" onClick={() => setConfirmingOff(false)}>Keep separate</Button>
        </div>
      )}

      {perRole && groups.map(group => (
        <div key={group}>
          <div className="mb-1.5 px-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{group}</div>
          <ul className="divide-y divide-border rounded-md border border-border bg-background">
            {roles.filter(role => role.group === group).map(role => {
              const value = values[role.key]
              const defaultValue = defaults[role.key]
              const customised = pinned && Boolean(defaultValue) && !sameConfig(value, defaultValue)
              return (
                <li key={role.key} className="flex flex-col gap-1.5 px-3 py-2.5 sm:flex-row sm:items-center sm:gap-3">
                  <div className="min-w-0 sm:w-[42%]">
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs font-medium text-foreground">{role.label}</span>
                      {customised && <span role="img" aria-label="Customised" title="Customised" className="h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />}
                    </div>
                    <div className="text-[11px] text-muted-foreground">{role.description}</div>
                  </div>
                  <div className="flex min-w-0 flex-1 items-center gap-1.5">
                    <div className="min-w-0 flex-1">
                      {value ? (
                        <RoleModelPopover label={role.label} summary={roleModelSummary(available, value)}>
                          {renderPickers(value, next => onUpdateRole(role.key, next))}
                        </RoleModelPopover>
                      ) : <span className="text-xs text-muted-foreground">Select a provider first.</span>}
                    </div>
                    {customised && (
                      <button
                        type="button"
                        aria-label={`Reset ${role.label} to provider default`}
                        title={readOnly ? disabledTitle : 'Reset to provider default'}
                        disabled={readOnly}
                        onClick={() => onResetRole(role.key)}
                        className="shrink-0 rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
                      >
                        <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                      </button>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        </div>
      ))}
    </div>
  )
}
