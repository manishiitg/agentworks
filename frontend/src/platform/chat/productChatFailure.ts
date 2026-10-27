export type ProductChatFailureCode =
  | 'quota_exhausted'
  | 'billing_error'
  | 'authentication_failed'
  | 'rate_limited'
  | 'provider_unavailable'
  | 'configuration_error'
  | 'access_denied'
  | 'project_unavailable'
  | 'cancelled'
  | 'internal_error'
  | 'delivery_uncertain'
  | 'input_required'

export type ProductChatFailure = {
  code: ProductChatFailureCode
  title: string
  message: string
  provider?: string
  retryAt?: string
  resetLabel?: string
  actionUrl?: string
  actionLabel?: string
  retryable: boolean
  technicalDetails?: string
}

export type FailureHints = {
  code?: unknown
  provider?: unknown
  retryAt?: unknown
  technicalDetails?: unknown
  providerMessage?: unknown
  nextStep?: unknown
  retryable?: unknown
}

// One metadata reader for live events, restored history and HTTP refusals.
export function failureHints(payload: Record<string, unknown>): FailureHints {
  const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}
  const metadata = record(payload.metadata)
  const error = record(payload.error)
  return {
    code: payload.code ?? payload.error_code ?? payload.error_kind ?? payload.kind ?? error.code ?? error.kind ?? metadata.error_code ?? metadata.code,
    provider: payload.provider ?? payload.llm_provider ?? error.provider ?? metadata.provider,
    retryAt: payload.retry_at ?? payload.retryAt ?? payload.resume_after ?? error.retry_at ?? error.retryAt ?? metadata.retry_at ?? metadata.retryAt ?? metadata.resume_after,
    technicalDetails: payload.technical_details ?? payload.technicalDetails ?? error.technical_details ?? error.technicalDetails ?? metadata.technical_details ?? metadata.technicalDetails,
    providerMessage: payload.provider_message ?? payload.providerMessage ?? metadata.provider_message,
    nextStep: payload.next_step ?? payload.nextStep ?? metadata.next_step,
    retryable: payload.retryable ?? metadata.retryable,
  }
}

const QUOTA_MARKERS = [
  /\[quota_exhausted\]/i,
  /usage limit (?:has been )?(?:reached|exhausted)/i,
  /(?:hit|reached|exceeded) (?:your|the) (?:weekly |daily |usage )?limit/i,
  /all (?:models|llms) (?:are )?quota[- ]exhausted/i,
]

const STRONG_FAILURE_MARKERS = [
  /^all LLMs failed\b/i,
  /\[(?:quota_exhausted|billing_error|authentication_failed|rate_limited|provider_unavailable)\]/i,
  /^usage limit reached\s*·\s*\/upgrade/i,
  /^failed to (?:start|continue|complete) (?:streaming|the request|the conversation)/i,
]

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function providerFrom(raw: string, hint?: unknown): string | undefined {
  const explicit = text(hint)
  if (explicit) {
    if (/^muse(?:-cli)?$/i.test(explicit)) return 'Muse'
    return explicit
  }
  if (/claude(?: code|code)?/i.test(raw) || /claudecode/i.test(raw)) return 'Claude Code'
  if (/codex(?: cli)?/i.test(raw)) return 'Codex'
  if (/cursor(?: cli)?/i.test(raw)) return 'Cursor'
  if (/\bpi(?: cli)?\b/i.test(raw)) return 'Pi'
  if (/\bmuse(?:-cli| code)?\b/i.test(raw) || /accountscenter\.meta\.com\/muse_code/i.test(raw)) return 'Muse'
  return undefined
}

function museResetLabelFrom(raw: string): string | undefined {
  return raw.match(/wait for usage to reset at\s+([a-z]{3}\s+\d{1,2}\s+at\s+\d{1,2}:\d{2}\s+(?:am|pm))/i)?.[1]
}

function museUpgradeUrlFrom(raw: string): string | undefined {
  return /https:\/\/accountscenter\.meta\.com\/muse_code\/?\?ep=xgrade/i.test(raw)
    ? 'https://accountscenter.meta.com/muse_code/?ep=xgrade'
    : undefined
}

function retryAtFrom(hint?: unknown): string | undefined {
  const explicit = text(hint)
  if (explicit && Number.isFinite(Date.parse(explicit))) return new Date(explicit).toISOString()
  if (typeof hint === 'number' && Number.isFinite(hint)) {
    const milliseconds = hint < 10_000_000_000 ? hint * 1000 : hint
    if (Number.isFinite(new Date(milliseconds).getTime())) return new Date(milliseconds).toISOString()
  }
  return undefined
}

function safeTechnicalDetails(raw: string): string {
  return raw
    .replace(/("(?:api[_-]?key|token|authorization|password|secret)"\s*:\s*")[^"]*(")/gi, '$1[redacted]$2')
    .replace(/\b(Bearer\s+)[^\s"']+/gi, '$1[redacted]')
    .replace(/\b([A-Z][A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD))\s*[=:]\s*[^\s"']+/g, '$1=[redacted]')
    .replace(/\bsk-[A-Za-z0-9_-]{12,}\b/g, '[redacted]')
}

export function looksLikeProductChatFailure(raw: string): boolean {
  const value = raw.trim()
  return value !== '' && STRONG_FAILURE_MARKERS.some((pattern) => pattern.test(value))
}

export function normalizeProductChatFailure(rawError: string, hints: FailureHints = {}): ProductChatFailure {
  const raw = rawError.trim() || 'The request could not be completed.'
  const technicalDetails = safeTechnicalDetails(text(hints.technicalDetails) || raw)
  const evidence = technicalDetails === raw ? raw : `${raw}\n${technicalDetails}`
  const normalizedCode = text(hints.code)?.toLowerCase().replace(/-/g, '_')
  const provider = providerFrom(evidence, hints.provider)
  const retryAt = retryAtFrom(hints.retryAt)
  const resetLabel = museResetLabelFrom(evidence)
  const actionUrl = museUpgradeUrlFrom(evidence)
  const providerLabel = provider || 'The AI provider'

  // A durable submission may already be running: the journal reconciles it.
  if (normalizedCode === 'delivery_uncertain') {
    return { code: 'delivery_uncertain', title: 'Delivery is still being checked',
      message: 'Your message was saved, but delivery has not been confirmed. Check the conversation before sending it again.',
      retryable: false, technicalDetails }
  }

  // Failure events carry the server's classified, masked explanation.
  // The remaining parsing handles transport errors without server metadata.
  const providerMessage = text(hints.providerMessage)
  if (providerMessage && normalizedCode) {
    return {
      code: normalizedCode as ProductChatFailureCode,
      title: normalizedCode === 'internal_error' ? 'The response could not be completed' : 'The provider could not complete this response',
      message: [providerMessage,
        (normalizedCode === 'quota_exhausted' || normalizedCode === 'rate_limited') && (retryAt || resetLabel)
          ? `You can retry after ${retryAt ? new Date(retryAt).toLocaleString() : resetLabel}.`
          : text(hints.nextStep),
      ].filter(Boolean).join(' '),
      provider,
      ...((normalizedCode === 'quota_exhausted' || normalizedCode === 'rate_limited') ? {
        retryAt,
        resetLabel,
        actionUrl,
        actionLabel: actionUrl ? 'Upgrade Muse' : undefined,
      } : {}),
      retryable: hints.retryable === true,
      technicalDetails,
    }
  }

  if (normalizedCode === 'access_denied' || normalizedCode === 'project_unavailable') {
    return {
      code: normalizedCode,
      title: 'This project is unavailable',
      message: raw,
      retryable: false,
    }
  }

  if (normalizedCode === 'billing_error' || /\[billing_error\]/i.test(evidence)) {
    return {
      code: 'billing_error',
      title: 'AI account plan or billing needs attention',
      message: "Check this account's plan and billing, or choose another account. For a shared account, ask your admin to check it.",
      provider,
      retryable: false,
      technicalDetails,
    }
  }

  if (normalizedCode === 'quota_exhausted' || QUOTA_MARKERS.some((pattern) => pattern.test(evidence))) {
    return {
      code: 'quota_exhausted',
      title: `${providerLabel} usage limit reached`,
      message: resetLabel
        ? `${providerLabel} is temporarily unavailable because its usage limit has been reached. Retry after ${resetLabel}.`
        : retryAt
        ? `${providerLabel} is temporarily unavailable. You can retry after ${new Date(retryAt).toLocaleString()}.`
        : `${providerLabel} is temporarily unavailable because its usage limit has been reached. Retry after the provider limit resets.`,
      provider,
      retryAt,
      resetLabel,
      actionUrl,
      actionLabel: actionUrl ? 'Upgrade Muse' : undefined,
      retryable: true,
      technicalDetails,
    }
  }

  // Before the timeout check: a CLI stuck on its own login screen times out
  // too, but the fix is signing in, not retrying (issue #252).
  if (/API_KEY_INVALID|API key not valid|invalid API key/i.test(raw)) {
    return {
      code: 'authentication_failed',
      title: `${providerLabel} API key was rejected`,
      message: 'Update this account’s API key under Providers, or choose another configured account. Ask an admin to update the key if you use the shared account.',
      provider,
      retryable: false,
      technicalDetails,
    }
  }
  if (normalizedCode === 'authentication_failed' || /\[auth\]|(?:authentication|authorization) (?:failed|required)|invalid (?:api )?(?:key|token)|login required|not (?:authenticated|logged in|signed in)|setup token (?:missing|invalid|required|expired)|account with subscription/i.test(raw)) {
    return {
      code: 'authentication_failed',
      title: `${providerLabel} is not signed in`,
      message: `${providerLabel} has no working login here. Pick a provider that is set up in Setup → Models, add your own account under Providers → Add my account, or ask an admin to sign in the shared account.`,
      provider,
      retryable: false,
      technicalDetails,
    }
  }

  if (normalizedCode === 'rate_limited' || /\brate[- ]limit(?:ed|ing)?\b|too many requests|status (?:code )?429/i.test(raw)) {
    return {
      code: 'rate_limited',
      title: `${providerLabel} is busy`,
      message: retryAt
        ? `The provider asked us to wait. You can retry after ${new Date(retryAt).toLocaleString()}.`
        : 'The provider is temporarily rate-limiting requests. Wait briefly, then retry.',
      provider,
      retryAt,
      retryable: true,
      technicalDetails,
    }
  }

  // A proxy in front of the server (CloudFront, the gateway) gave up waiting.
  // Its HTML error page mentions "a configuration error", so this must come
  // before the configuration check. The message may still have been delivered.
  if (/status code 50[234]\b|\b50[234] (?:Gateway Time-?out|Bad Gateway|Service Unavailable)\b|request could not be satisfied|Generated by cloudfront/i.test(raw)) {
    return {
      code: 'provider_unavailable',
      title: 'The server took too long to respond',
      message: 'Your message may still have been delivered. Check the conversation before sending it again.',
      provider,
      retryable: false,
      technicalDetails,
    }
  }

  if (normalizedCode === 'configuration_error' || /not configured|configuration (?:error|missing)|MCP_API_URL|MCP_API_TOKEN|MCP_BRIDGE_BINARY|mcpbridge binary not found|missing required/i.test(raw)) {
    return {
      code: 'configuration_error',
      title: 'This product is not configured correctly',
      message: 'A required server configuration is missing. Ask an administrator to check this product deployment.',
      provider,
      retryable: false,
      technicalDetails,
    }
  }

  if (normalizedCode === 'provider_unavailable' || /provider unavailable|service unavailable|connection (?:refused|reset)|timed? out|timeout|failed to fetch|no such host|name or service not known|could not resolve|network is unreachable/i.test(raw)) {
    return {
      code: 'provider_unavailable',
      title: `${providerLabel} is unavailable`,
      message: 'The AI provider could not complete this request. Check the connection and retry.',
      provider,
      retryable: true,
      technicalDetails,
    }
  }

  if (normalizedCode === 'cancelled' || /\b(?:cancelled|canceled)\b/i.test(raw)) {
    return {
      code: 'cancelled',
      title: 'Response cancelled',
      message: 'The current response was cancelled.',
      retryable: true,
    }
  }

  return {
    code: 'internal_error',
    title: 'The response could not be completed',
    message: 'Something went wrong while generating the response. Retry, or open technical details if the problem continues.',
    provider,
    retryable: true,
    technicalDetails,
  }
}
