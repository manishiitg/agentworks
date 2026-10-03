import { useEffect, useState } from 'react'
import api from '../../services/api'

export type TeachAction = {
  id: number
  kind: string
  target: { selector?: string; role?: string; name?: string }
  value?: string
  url?: string
  parameter?: string
  warning?: string
}
export type TeachState = {
  id?: string
  status: string
  goal?: string
  directory?: string
  actions?: TeachAction[]
  errors?: string[]
  check?: { kind: string; value: string }
  skill?: string
  guidance?: string
}
export function BrowserTeachingPanel({
  workspacePath,
  session,
  state,
  onState,
  onStart,
  onControl,
  onClose,
  profileId,
}: {
  workspacePath: string
  session: string
  state: TeachState
  onState: (state: TeachState) => void
  onStart: (goal: string) => void
  onControl: (action: string) => void
  onClose: () => void
  profileId?: string
}) {
  const [goal, setGoal] = useState('')
  const [items, setItems] = useState<TeachState[]>([])
  const [actions, setActions] = useState<TeachAction[]>(state.actions || [])
  const [check, setCheck] = useState(state.check || { kind: 'text', value: '' })
  const [guidance, setGuidance] = useState(state.guidance || '')
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const active = state.status === 'recording' || state.status === 'paused'
  const dirty =
    JSON.stringify(actions) !== JSON.stringify(state.actions || []) ||
    JSON.stringify(check) !==
      JSON.stringify(state.check || { kind: 'text', value: '' }) ||
    guidance !== (state.guidance || '')
  useEffect(() => {
    setActions(state.actions || [])
    setCheck(state.check || { kind: 'text', value: '' })
    setGuidance(state.guidance || '')
  }, [state])
  const endpoint = `/api/browser/live/${encodeURIComponent(session)}/teaching`
  const params = { workspace_path: workspacePath, profile_id: profileId }
  useEffect(() => {
    let live = true
    api
      .post<{ demonstrations: TeachState[] }>(
        endpoint,
        { action: 'list' },
        { params },
      )
      .then(({ data }) => {
        if (live) setItems(data.demonstrations || [])
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [workspacePath, session, state.id, state.status]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!active || !state.id) return
    let live = true
    const timer = window.setInterval(() => {
      api
        .post<TeachState>(
          endpoint,
          { action: 'status', id: state.id },
          { params },
        )
        .then(({ data }) => {
          if (live) onState(data)
        })
        .catch(() => {})
    }, 1500)
    return () => {
      live = false
      window.clearInterval(timer)
    }
  }, [active, state.id, endpoint, workspacePath, profileId, onState]) // eslint-disable-line react-hooks/exhaustive-deps
  async function request(action: string, id = state.id) {
    setBusy(true)
    setError('')
    try {
      if (action === 'test')
        await api.post(
          endpoint,
          { action: 'save', id, actions, check, guidance },
          { params },
        )
      const values = { ...inputs }
      for (const a of actions)
        if (a.parameter && !(a.parameter in values))
          values[a.parameter] = a.value || ''
      const { data } = await api.post<TeachState>(
        endpoint,
        { action, id, actions, check, guidance, inputs: values },
        { params, timeout: 105000 },
      )
      onState(data)
    } catch (cause) {
      const detail =
        cause instanceof Error
          ? (cause as { response?: { data?: unknown } }).response?.data
          : undefined
      setError(
        typeof detail === 'string'
          ? detail
          : cause instanceof Error
            ? cause.message
            : 'Teaching request failed',
      )
    } finally {
      setBusy(false)
    }
  }
  return (
    <div
      role="dialog"
      aria-label="Teach a browser task"
      className={
        active
          ? 'absolute bottom-2 right-2 z-20 max-w-sm rounded-lg border bg-background p-3 shadow-xl'
          : 'absolute inset-x-2 top-12 z-20 max-h-[calc(100%-4rem)] overflow-auto rounded-lg border bg-background p-4 shadow-xl'
      }
    >
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-medium">Teach a browser task</h3>
        <button
          type="button"
          disabled={active}
          onClick={onClose}
          className="text-sm disabled:opacity-40"
        >
          Close
        </button>
      </div>
      {!active && (
        <p className="mb-3 text-xs text-muted-foreground">
          Sign in before recording. Demonstrate the task, review its inputs and
          expected result, then test it. Saved procedures belong to this
          workspace.
        </p>
      )}
      {!active && (
        <>
          <label className="block text-sm">
            Result to demonstrate
            <textarea
              aria-label="Task goal"
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              maxLength={1000}
              className="my-2 block w-full rounded border bg-background p-2"
            />
          </label>
          <button
            type="button"
            disabled={busy || !goal.trim()}
            onClick={() => onStart(goal)}
            className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-40"
          >
            Start teaching
          </button>
        </>
      )}
      {active && (
        <div className="flex flex-wrap items-center gap-2">
          <span role="status" className="text-sm font-medium text-red-600">
            {state.status === 'paused' ? 'Paused' : 'Recording actions'}
          </span>
          <button
            type="button"
            onClick={() =>
              onControl(state.status === 'paused' ? 'resume' : 'pause')
            }
            className="rounded border px-3 py-1 text-sm"
          >
            {state.status === 'paused' ? 'Resume' : 'Pause'}
          </button>
          <button
            type="button"
            onClick={() => onControl('finish')}
            className="rounded border px-3 py-1 text-sm"
          >
            Finish
          </button>
          <button
            type="button"
            onClick={() => onControl('cancel')}
            className="rounded border px-3 py-1 text-sm"
          >
            Cancel
          </button>
        </div>
      )}
      {!active && items.length > 0 && (
        <label className="mt-3 block text-sm">
          Demonstrations
          <select
            aria-label="Saved demonstrations"
            value={state.id || ''}
            onChange={(e) => void request('status', e.target.value)}
            className="ml-2 max-w-full rounded border bg-background p-1"
          >
            <option value="">Select a demonstration</option>
            {items.map((item) => (
              <option key={item.id} value={item.id}>
                {item.goal} · {item.status}
              </option>
            ))}
          </select>
        </label>
      )}
      {active && (
        <p className="mt-2 text-xs text-muted-foreground">
          {actions.length} captured steps · Demonstrate in the browser.
        </p>
      )}
      {state.id && !active && (
        <>
          <button
            type="button"
            disabled={busy}
            onClick={() => void request('status')}
            className="mt-2 text-xs underline"
          >
            Reload reviewed draft
          </button>
          <p className="mt-3 text-sm font-medium">
            {state.goal} · {state.status}
          </p>
          <ol className="mt-2 space-y-2 text-xs">
            {actions.map((action, index) => (
              <li key={`${action.id}-${index}`} className="rounded border p-2">
                <span>
                  {index + 1}. {action.kind}{' '}
                  {action.target?.name ||
                    action.target?.selector ||
                    action.url ||
                    ''}
                </span>
                {action.warning && (
                  <p className="my-1 text-destructive">{action.warning}</p>
                )}
                {!active && (
                  <button
                    type="button"
                    aria-label={`Remove step ${index + 1}`}
                    onClick={() =>
                      setActions((current) =>
                        current.filter((_, i) => i !== index),
                      )
                    }
                    className="ml-2 underline"
                  >
                    Remove step
                  </button>
                )}
                {!active &&
                  (action.kind === 'fill' || action.kind === 'select') && (
                    <div className="mt-1 flex flex-wrap gap-2">
                      <label>
                        Input name{' '}
                        <input
                          aria-label={`Input name for step ${index + 1}`}
                          value={action.parameter || ''}
                          onChange={(e) =>
                            setActions((current) =>
                              current.map((a, i) =>
                                i === index
                                  ? { ...a, parameter: e.target.value }
                                  : a,
                              ),
                            )
                          }
                          className="rounded border bg-background p-1"
                          placeholder="Optional variable"
                        />
                      </label>
                      {action.parameter && (
                        <label>
                          Test value{' '}
                          <input
                            aria-label={`Test value for ${action.parameter}`}
                            value={
                              inputs[action.parameter] ?? action.value ?? ''
                            }
                            onChange={(e) =>
                              setInputs((current) => ({
                                ...current,
                                [action.parameter!]: e.target.value,
                              }))
                            }
                            className="rounded border bg-background p-1"
                          />
                        </label>
                      )}
                    </div>
                  )}
              </li>
            ))}
          </ol>
          {!active &&
            state.status !== 'cancelled' &&
            state.status !== 'interrupted' && (
              <>
                <label className="mt-3 block text-sm">
                  Reviewed guidance
                  <textarea
                    aria-label="Reviewed guidance"
                    value={guidance}
                    onChange={(e) => setGuidance(e.target.value)}
                    maxLength={20000}
                    className="mt-2 block min-h-24 w-full rounded border bg-background p-2 text-xs"
                  />
                </label>
                <label className="mt-3 block text-sm">
                  Expected result{' '}
                  <select
                    aria-label="Outcome check type"
                    value={check.kind}
                    onChange={(e) =>
                      setCheck((current) => ({
                        ...current,
                        kind: e.target.value,
                      }))
                    }
                    className="mx-2 rounded border bg-background p-1"
                  >
                    <option value="text">Page contains text</option>
                    <option value="url">URL contains</option>
                  </select>
                  <input
                    aria-label="Expected outcome"
                    value={check.value}
                    onChange={(e) =>
                      setCheck((current) => ({
                        ...current,
                        value: e.target.value,
                      }))
                    }
                    className="mt-2 w-full rounded border bg-background p-2"
                  />
                </label>
                <p className="my-2 text-xs text-muted-foreground">
                  Test repeats these actions in the signed-in browser and may
                  change website data. Use the example inputs you want it to
                  run.
                </p>
                <div className="flex gap-2">
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => void request('save')}
                    className="rounded border px-3 py-1 text-sm"
                  >
                    Save draft
                  </button>
                  <button
                    type="button"
                    disabled={busy || !check.value.trim()}
                    onClick={() => {
                      const values = { ...inputs }
                      for (const a of actions)
                        if (a.parameter && !(a.parameter in values))
                          values[a.parameter] = a.value || ''
                      setInputs(values)
                      void request('test')
                    }}
                    className="rounded border px-3 py-1 text-sm"
                  >
                    {busy ? 'Working…' : 'Test procedure'}
                  </button>
                  {state.status === 'tested' && (
                    <button
                      type="button"
                      disabled={busy || dirty}
                      onClick={() => void request('publish')}
                      className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground"
                    >
                      Save for reuse
                    </button>
                  )}
                </div>
              </>
            )}
        </>
      )}
      {dirty && state.status === 'tested' && (
        <p className="mt-2 text-xs">
          Test the edited procedure again before saving for reuse.
        </p>
      )}
      {state.skill && <p className="mt-2 text-xs">Saved: {state.skill}</p>}
      {state.errors?.map((message, i) => (
        <p key={i} role="alert" className="mt-2 text-xs text-destructive">
          {message}
        </p>
      ))}
      {error && (
        <p role="alert" className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
