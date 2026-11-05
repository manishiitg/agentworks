export function humanizeStreamingToolName(toolName: string): string {
  const compactName = toolName.split('__').filter(Boolean).pop() || toolName
  const normalized = compactName.replace(/[-\s.]+/g, '_').toLowerCase()

  if (normalized === 'api_bridge') return 'Using api-bridge'
  if (normalized === 'execute_shell_command') return 'Running shell command'
  if (normalized === 'query_step') return 'Checking step progress'
  if (normalized === 'execute_step') return 'Running automation step'
  if (normalized.includes('browser') || normalized.includes('agent_browser')) return 'Using browser'
  if (normalized.includes('search')) return 'Searching web'
  if (normalized.includes('vision')) return 'Reading image'
  if (normalized.includes('generate_image') || normalized.includes('image_gen')) return 'Generating image'
  if (normalized.includes('read') && normalized.includes('file')) return 'Reading file'
  if ((normalized.includes('write') || normalized.includes('update')) && normalized.includes('file')) return 'Updating file'
  if (normalized.includes('list') && normalized.includes('file')) return 'Listing files'

  return compactName
    .replace(/[_.-]+/g, ' ')
    .replace(/\b\w/g, char => char.toUpperCase())
}

export function normalizeStreamingProviderName(providerName: string): string {
  return providerName
    .replace(/^mcp__/, '')
    .replace(/__$/, '')
    .replace(/_/g, '-')
}

export function getStreamingStatusText(chunk: string): string | null {
  const trimmed = chunk.trim()
  if (!trimmed) return null

  if (chunk.includes('⏳')) return trimmed
  if (chunk.includes('⚠️ Gemini')) return trimmed
  if (trimmed === 'Claude Code is working...') return 'Claude Code is working'

  const providerToolMatch = trimmed.match(/^([A-Za-z0-9_.-]+)\s+-\s+([A-Za-z0-9_.:-]+)\s+\((?:MCP|tool)\)/i)
  if (providerToolMatch) {
    const provider = normalizeStreamingProviderName(providerToolMatch[1])
    const tool = humanizeStreamingToolName(providerToolMatch[2])
    return `${tool} via ${provider}`
  }

  const directMCPToolMatch = trimmed.match(/^([A-Za-z0-9_.:-]+)\((?:\s*[A-Za-z0-9_]+:|\{)/)
  if (directMCPToolMatch && trimmed.includes('(MCP)')) {
    return humanizeStreamingToolName(directMCPToolMatch[1])
  }

  const apiBridgeCallMatch = trimmed.match(/^(Calling|Called)\s+api-bridge(?:\s+(\d+)\s+times?)?(?:…|\.\.\.)?(?:\s+\(.*\))?$/i)
  if (apiBridgeCallMatch) {
    const count = apiBridgeCallMatch[2]
    return count ? `Using api-bridge (${count} tool calls)` : 'Using api-bridge'
  }

  return null
}

export function splitStreamingStatusAndText(chunk: string): { statusText: string | null; text: string } {
  if (!chunk) return { statusText: null, text: '' }

  const hasLineBreak = /\r?\n/.test(chunk)
  if (!hasLineBreak) {
    const statusText = getStreamingStatusText(chunk)
    return statusText ? { statusText, text: '' } : { statusText: null, text: chunk }
  }

  const lines = chunk.split(/(\r?\n)/)
  let statusText: string | null = null
  let text = ''

  for (let i = 0; i < lines.length; i += 2) {
    const line = lines[i] || ''
    const separator = lines[i + 1] || ''
    const candidateStatus = getStreamingStatusText(line)
    if (candidateStatus) {
      statusText = candidateStatus
      continue
    }
    text += line + separator
  }

  return { statusText, text }
}

const TERMINAL_SCREEN_MARKERS = [
  'shift+tab to accept edits',
  'type your message or @path/to/file',
  'authenticated with gemini-api-key',
  'thinking... (esc to cancel',
  'workspace (/directory) sandbox',
  '? for shortcuts',
]

const stripAnsiControlCodes = (value: string): string =>
  // eslint-disable-next-line no-control-regex -- ANSI escape bytes are the protocol being stripped.
  value.replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')

export function looksLikeTerminalScreenText(value: string): boolean {
  if (!value) return false
  const normalized = stripAnsiControlCodes(value).toLowerCase()
  let markerCount = 0

  for (const marker of TERMINAL_SCREEN_MARKERS) {
    if (normalized.includes(marker)) markerCount += 1
  }

  return markerCount >= 2 || normalized.includes('shift+tab to accept edits')
}

/**
 * Appends one streamed chunk to the text accumulated so far, honouring the
 * backend's per-chunk `is_delta` marker.
 *
 * Providers stream in two shapes and the marker is the only reliable way to
 * tell them apart:
 *  - delta chunks are fragments of ONE message (pi splits mid-word: "workspace_"
 *    + "advanced` server.") and must be concatenated verbatim.
 *  - block chunks are each a COMPLETE message (claude-code, codex, cursor all
 *    report delta_content_count = 0) and must be newline-separated.
 *
 * Concatenating everything verbatim — what this store did previously — is why
 * Claude Code output rendered as one run-on blob with the markdown table glued
 * onto the end of a sentence, which stops it being parsed as a table at all.
 *
 * This mirrors the server-side assembly in agent_go/internal/terminals/store.go
 * (structuredChunkIsDelta) so both paths reconstruct a message identically,
 * including its guard against a repeated trailing chunk.
 *
 * When `isDelta` is undefined the chunk came from an emitter that predates the
 * marker; we keep the old verbatim behaviour there rather than guessing, so
 * this can only improve streams that actually carry the field.
 */
export function appendStreamingText(
  currentText: string,
  incoming: string,
  isDelta?: boolean
): string {
  if (!incoming) return currentText
  if (!currentText) return incoming
  if (isDelta !== false) return currentText + deltaSeparator(currentText, incoming) + incoming
  // Block chunk: skip an exact repeat of what we already end with, then ensure
  // it starts on its own line so markdown blocks (tables, lists, fences) parse.
  if (currentText.endsWith(incoming)) return currentText
  return currentText + blockSeparator(currentText, incoming) + incoming
}

/**
 * A few structured providers occasionally mark complete narration blocks as
 * deltas. True token deltas must remain verbatim because they may split a
 * word, but a finished sentence followed immediately by a capitalised word is
 * unambiguously a missing whitespace boundary ("generation.No Gemini"), not
 * a word split. Keep that live preview readable until the final event arrives.
 */
function deltaSeparator(currentText: string, incoming: string): string {
  const previous = currentText.at(-1)
  const next = incoming.at(0)
  if (previous && next && /[.!?]/.test(previous) && /[A-Z]/.test(next)) return ' '
  return ''
}

/**
 * Markdown block elements need a BLANK line before them, not merely a newline.
 *
 * With a single "\n", CommonMark/GFM treats a following table row as lazy
 * continuation of the preceding paragraph, so it renders as literal pipes
 * instead of a table — the exact "not formatted or readable" symptom, still
 * present after chunks stopped running together. A plain sentence following a
 * sentence only needs the single newline.
 */
function blockSeparator(currentText: string, incoming: string): string {
  if (currentText.endsWith('\n\n') || incoming.startsWith('\n\n')) return ''
  // A block chunk is a COMPLETE message, so it starts a new paragraph. A single
  // "\n" is only a soft wrap in CommonMark/GFM: separate narration lines
  // collapse into one run-on paragraph, and a following table is swallowed as
  // lazy continuation and rendered as literal pipes. Both were observed on real
  // provider output.
  if (currentText.endsWith('\n')) return '\n'
  return '\n\n'
}

export function sanitizeStreamingDisplayText(value: string): string {
  if (!value) return ''
  return value
    .split(/\r?\n/)
    .filter(line => !getStreamingStatusText(line))
    .join('\n')
    .trimStart()
}

export function formatTerminalToolSummary(summary: string): string {
  const trimmed = summary.trim()
  if (!trimmed) return ''

  const match = trimmed.match(/^(.+?)(?:\s+x(\d+))?$/)
  if (!match) return trimmed

  const toolName = match[1].trim()
  const count = Number(match[2] || 0)
  const label = humanizeStreamingToolName(toolName)
  if (!Number.isFinite(count) || count <= 1) return label

  const countLabel = label.includes('api-bridge') ? 'tool calls' : 'calls'
  return `${label} (${count} ${countLabel})`
}

const lowerFirst = (value: string): string => {
  if (!value) return value
  return value.charAt(0).toLowerCase() + value.slice(1)
}

export function formatTerminalProgressLine(statusText: string, providerLabel: string, toolSummary: string): string {
  const toolText = formatTerminalToolSummary(toolSummary)
  const status = statusText.trim()
  const provider = providerLabel.trim()

  if (!toolText) return status || (provider ? `${provider} is working` : 'Agent is working')

  const workingMatch = status.match(/^(.+?)\s+is working\.?$/i)
  if (workingMatch) {
    return `${workingMatch[1]} is ${lowerFirst(toolText)}`
  }

  if (!status && provider) {
    return `${provider} is ${lowerFirst(toolText)}`
  }

  if (!status) return toolText
  return status
}
