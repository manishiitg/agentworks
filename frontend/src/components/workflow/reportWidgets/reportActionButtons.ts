export const ACTION_BUTTON_ATTR = 'data-report-action'
export const ACTION_DEFAULTS_ATTR = 'data-report-action-defaults'

export type ActionRequest = { name: string; label: string; defaults?: Record<string, unknown> }

type Options = {
  /** False when the viewer cannot use this dashboard's functions. */
  available: boolean
  open: (request: ActionRequest) => void
}

function parseDefaults(raw: string | null): Record<string, unknown> | undefined {
  if (!raw) return undefined
  try {
    const value = JSON.parse(raw)
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
  } catch { return undefined }
}

/** A dashboard marks a button with data-report-action="function_name". The host
 * opens its own form for that function of this dashboard's project. The page
 * can pre-fill defaults, but only the person's click and Submit run anything:
 * there is no script method that runs a function. */
export function installDashboardActionButtons(doc: Document, options: Options) {
  const disabled = new Map<HTMLButtonElement, boolean>()
  // Functions the server says this person may not call (its can_call is false):
  // the button stays visible, disabled, with a plain reason. The server decides.
  let blocked = new Set<string>()
  const mark = () => {
    for (const button of doc.querySelectorAll<HTMLButtonElement>(`button[${ACTION_BUTTON_ATTR}]`)) {
      if (!disabled.has(button)) disabled.set(button, button.disabled)
      const limited = blocked.has((button.getAttribute(ACTION_BUTTON_ATTR) || '').trim())
      if (!options.available || limited) button.disabled = true
      if (limited) button.title = 'This function is limited to certain people.'
    }
  }
  const click = (event: Event) => {
    const button = (event.target as Element | null)?.closest<HTMLButtonElement>(`button[${ACTION_BUTTON_ATTR}]`)
    if (!button || button.disabled) return
    const name = (button.getAttribute(ACTION_BUTTON_ATTR) || '').trim()
    if (!name || name.length > 128) return
    event.preventDefault()
    options.open({ name, label: (button.getAttribute('aria-label') || button.getAttribute('data-report-action-label') || button.firstChild?.textContent || button.textContent || name).trim().slice(0, 120) || name, defaults: parseDefaults(button.getAttribute(ACTION_DEFAULTS_ATTR)) })
  }
  const mutation = new MutationObserver(mark)
  mutation.observe(doc.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: [ACTION_BUTTON_ATTR] })
  doc.addEventListener('click', click)
  mark()
  return {
    setBlocked: (names: string[]) => { blocked = new Set(names); mark() },
    dispose: () => {
      mutation.disconnect()
      doc.removeEventListener('click', click)
      for (const [button, wasDisabled] of disabled) button.disabled = wasDisabled
      disabled.clear()
    },
  }
}
