;(() => {
  if (window.__awTeachInstalled) return
  window.__awTeachInstalled = true
  const binding = '__awTeachEvent'
  let paused = true,
    edit = null,
    enteredAt = 0
  const omitted = new Set()
  window.__awTeachVisualSafe = () => !paused && omitted.size === 0
  const small = (value) => String(value || '').slice(0, 400)
  const secret = (el) =>
    el &&
    (el.type === 'password' ||
      /password|passwd|otp|one.?time|verification.?code|credit.?card|card.?number|token|secret/i.test(
        [el.name, el.id, el.autocomplete].join(' '),
      ))
  const role = (el) =>
    el.getAttribute('role') ||
    { BUTTON: 'button', A: 'link', SELECT: 'combobox', TEXTAREA: 'textbox' }[
      el.tagName
    ] ||
    (['button','submit'].includes(el.type) ? 'button' : el.type === 'checkbox'
      ? 'checkbox'
      : el.type === 'radio'
        ? 'radio'
        : el.tagName === 'INPUT'
          ? 'textbox'
          : '')
  const name = (el) =>
    small(
      el.getAttribute('aria-label') ||
        (el.getAttribute('aria-labelledby') || '')
          .split(' ')
          .map((id) => document.getElementById(id)?.textContent || '')
          .join(' ')
          .trim() ||
        Array.from(el.labels || [])
          .map((x) => x.textContent)
          .join(' ')
          .trim() ||
        el.placeholder ||
      (['button','submit'].includes(el.type) ? el.value : '') ||
        (['BUTTON', 'A'].includes(el.tagName) ? el.textContent : '') ||
        el.title,
    )
  const selector = (el) => {
    const tag = el.tagName.toLowerCase()
    for (const attr of ['data-testid', 'data-test', 'name', 'aria-label']) {
      const value = el.getAttribute(attr)
      if (value) {
        const s = `${tag}[${attr}=${JSON.stringify(value)}]`
        if (document.querySelectorAll(s).length === 1) return s
      }
    }
    if (el.id && !/\d{6}|:r\d|[a-f0-9]{16}/i.test(el.id)) {
      const s = '#' + CSS.escape(el.id)
      if (document.querySelectorAll(s).length === 1) return s
    }
    return ''
  }
  const target = (el) => ({
    selector: selector(el),
    role: role(el),
    name: name(el),
    tag: el.tagName.toLowerCase(),
    context: small(el.closest('tr,[role=row],article')?.textContent),
  })
  const emit = (kind, el, value, warning) => {
    if (paused || secret(el) || omitted.has(el)) return
    const payload = {
      kind,
      target: el ? target(el) : undefined,
      value: value === undefined ? undefined : small(value),
      warning:
        warning ||
        (value && String(value).length > 400
          ? 'Input exceeds recorder limit; review the complete value'
          : undefined),
    }
    try {
      window[binding](JSON.stringify(payload))
    } catch {}
  }
  const flush = () => {
    if (edit && !secret(edit))
      emit(
        edit.tagName === 'SELECT' ? 'select' : 'fill',
        edit,
        edit.value ?? edit.textContent,
      )
    edit = null
  }
  const indicator = document.createElement('div')
  indicator.id = 'aw-browser-teaching-indicator'
  indicator.textContent = 'AgentWorks • Teaching paused'
  indicator.style.cssText =
    'position:fixed;right:12px;top:12px;z-index:2147483647;padding:8px 12px;background:#991b1b;color:white;font:13px system-ui;border-radius:6px;pointer-events:none'
  if (document.documentElement) document.documentElement.append(indicator)
  else
    document.addEventListener(
      'DOMContentLoaded',
      () => document.documentElement?.append(indicator),
      { once: true },
    )
  const on = (type, fn) => document.addEventListener(type, fn, true)
  on('input', (e) => {
    if (!e.isTrusted) return
    const el = e.composedPath()[0]
    if (['checkbox', 'radio'].includes(el?.type)) return
    if (el?.type === 'file') {
      emit(
        'unsupported',
        el,
        undefined,
        'File upload requires a reviewed file input; it cannot be captured from a demonstration',
      )
      return
    }
    if (paused) {
      omitted.add(el)
      edit = null
      return
    }
    if (omitted.has(el)) return
    if (secret(el)) {
      edit = null
      return
    }
    if (edit && edit !== el) flush()
    edit = el
  })
  on('change', (e) => {
    if (!e.isTrusted || paused) return
    const el = e.composedPath()[0]
    if (el.type === 'checkbox' || el.type === 'radio') {
      flush()
      emit(el.checked ? 'check' : 'uncheck', el)
    } else {
      edit = el
      flush()
    }
  })
  on('blur', () => flush())
  on('click', (e) => {
    if (!e.isTrusted || paused) return
    flush()
    const el = e
      .composedPath()
      .find(
        (x) =>
          x instanceof Element &&
          x.matches('button,a,input[type=submit],[role=button],[role=link]'),
      )
    if (el) {
      if (e.detail === 0 && Date.now() - enteredAt < 300) return
      emit('click', el)
    } else {
      const node = e.composedPath()[0]
      if (node instanceof Element) {
        if (node.matches('input,textarea,select,[contenteditable=true]')) return
        const t = target(node)
        emit(
          'click',
          node,
          undefined,
          node.tagName === 'CANVAS'
            ? 'Canvas actions require a separate visual procedure'
            : !t.selector && !t.role
              ? 'Click target has no durable locator; review this step'
              : undefined,
        )
      }
    }
  })
  on('keydown', (e) => {
    if (e.isTrusted && ['Enter', 'Escape'].includes(e.key)) {
      flush()
      if (e.key === 'Enter') enteredAt = Date.now()
      emit('press', e.composedPath()[0], e.key)
    }
  })
  on('dragstart', (e) => {
    if (e.isTrusted)
      emit(
        'unsupported',
        e.composedPath()[0],
        undefined,
        'Drag actions require a separate reviewed procedure',
      )
  })
  on('submit', () => flush())
  on('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flush()
  })
  window.__awTeachControl = (command) => {
    if (command === 'pause') {
      flush()
      paused = true
    }
    if (command === 'resume') {
      edit = null
      paused = false
    }
    if (command === 'reset') {
      omitted.clear()
      edit = null
      paused = false
    }
    if (command === 'flush') flush()
    if (command === 'stop') {
      flush()
      paused = true
    }
    indicator.textContent = paused
      ? 'AgentWorks • Teaching paused'
      : 'AgentWorks • Teaching is recording'
    indicator.style.display = command === 'stop' ? 'none' : 'block'
  }
})()
