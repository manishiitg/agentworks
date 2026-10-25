export type ToolCallValueFormat = 'json' | 'shell' | 'text'

export interface FormattedToolCallValue {
  text: string
  format: ToolCallValueFormat
  isError: boolean
}

type JsonRecord = Record<string, unknown>

const MAX_NESTED_JSON_DEPTH = 5

function isRecord(value: unknown): value is JsonRecord {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function tryParseJson(text: string): unknown | undefined {
  const trimmed = text.trim()
  if (!trimmed || (!trimmed.startsWith('{') && !trimmed.startsWith('['))) return undefined

  try {
    return JSON.parse(trimmed)
  } catch {
    return undefined
  }
}

function expandNestedJson(value: unknown, depth = 0): unknown {
  if (depth >= MAX_NESTED_JSON_DEPTH) return value

  if (typeof value === 'string') {
    const parsed = tryParseJson(value)
    return parsed === undefined ? value : expandNestedJson(parsed, depth + 1)
  }

  if (Array.isArray(value)) {
    return value.map(item => expandNestedJson(item, depth + 1))
  }

  if (isRecord(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, expandNestedJson(item, depth + 1)]),
    )
  }

  return value
}

function unwrapMcpTextEnvelope(value: unknown): unknown {
  let current = value

  for (let depth = 0; depth < MAX_NESTED_JSON_DEPTH; depth += 1) {
    if (typeof current === 'string') {
      const parsed = tryParseJson(current)
      if (parsed === undefined) return current
      current = parsed
      continue
    }

    if (!isRecord(current)) return current
    // Alias to a const: TS won't carry `isRecord` narrowing of a mutable
    // `let` into the closures below, since it can't prove they run before
    // `current` is reassigned. `record` stays narrowed for the rest of
    // this iteration.
    const record = current

    // The envelope is not always at the top level: a bridge result arrives as
    // {success: {content: [...], isError: false}}, and printing that raw is
    // what put `{"success": {"content": [{"text": {"text": "{\"stdout\"...`
    // in front of the user instead of the command's output.
    if (!Array.isArray(record.content)) {
      const nested = ['success', 'result', 'data']
        .map(key => record[key])
        .find((v): v is Record<string, unknown> => isRecord(v) && Array.isArray(v.content))
      if (!nested) return current
      current = nested
      continue
    }

    const textBlocks = (record.content as unknown[])
      .filter(isRecord)
      .map(block => {
        // A text block is normally {type: "text", text: "..."}. Some bridges
        // double-wrap it as {text: {text: "..."}} and omit the type.
        if (typeof block.text === 'string') return block.text
        if (isRecord(block.text) && typeof block.text.text === 'string') return block.text.text
        return null
      })
      .filter((text): text is string => text !== null)

    if (textBlocks.length === 0) return current
    current = textBlocks.join('\n')
  }

  return current
}

function prettyJson(value: unknown): string {
  const rendered = JSON.stringify(expandNestedJson(value), null, 2)
  return rendered ?? String(value)
}

export function normalizeToolCallResultValue(value: unknown): unknown {
  if (typeof value === 'string') {
    const parsed = tryParseJson(value)
    return unwrapMcpTextEnvelope(parsed === undefined ? value : parsed)
  }
  return unwrapMcpTextEnvelope(value)
}

export function toolCallValueToText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === null || value === undefined) return ''
  return prettyJson(value)
}

/**
 * The harness's own tool-failure envelope, emitted by `toolExecutionError` in
 * mcpagent's executor as one of:
 *
 *   tool execution failed:   layer=… tool=… session=…: …
 *   tool execution canceled: layer=… tool=… session=…
 *   tool execution timed out: layer=… tool=… session=… timeout=…
 *
 * A failure delivered through the HTTP bridge arrives as ordinary stdout with
 * `exit_code: 0` — the *curl* succeeded, so every exit-code check says success.
 * On 2026-08-01 a single day of codex rollouts held 34 of these, every one
 * rendered with a green check: 46 get_api_spec rejections, 14 failed
 * mark_pulse_module_result calls, 8 get_goal_update_result misses. An operator
 * reading that transcript sees a clean run.
 *
 * Matched on `layer=` rather than on the word "error", so tool output that
 * merely discusses errors — a log query, a findings table, a review body — is
 * not flagged. This recognises the harness reporting its own failure, nothing
 * else.
 */
const HARNESS_TOOL_ERROR = /tool execution (?:failed|canceled|timed out): layer=/

// Coding-agent native shell tools do not use the AgentWorks JSON shell
// envelope. Claude Code, for example, reports a failed Bash call as plain text
// beginning with "Exit code 1". Treat that provider status line as authoritative
// so a completed tool event is not rendered with a green success mark.
const NATIVE_SHELL_NONZERO_EXIT = /^exit code\s+[1-9]\d*\b/im

/**
 * A folder-guard denial on stderr, which the exit code does not report.
 *
 * Permission denials have been observed inside shell results carrying
 * exit_code 0, including pipelines where the final command determines the
 * status. The underlying `find`/`ls` command normally returns non-zero for a
 * direct denial, but the UI only sees the returned envelope. A CDP test step on
 * 2026-08-02 produced eleven green-looking denial results.
 *
 * Matched on the two denial phrases only. "No such file or directory" is
 * deliberately excluded: probing for a path that may not exist is ordinary
 * behaviour, and flagging it would train the operator to ignore the marker.
 */
const SHELL_PERMISSION_DENIED = /(?:Operation not permitted|[Pp]ermission denied)/

/**
 * A native CLI tool call the AgentWorks tool policy refused. Muse reports it
 * as "tool blocked by hook: …" in an ordinary result; with native agent tools
 * off, a blocked read_file was shown with a green check and read as a policy
 * leak (QA #218).
 */
const NATIVE_TOOL_BLOCKED = /^\s*tool blocked by hook:/i

function textCarriesHarnessError(value: unknown): boolean {
  return typeof value === 'string' && (
    HARNESS_TOOL_ERROR.test(value) || NATIVE_SHELL_NONZERO_EXIT.test(value) || NATIVE_TOOL_BLOCKED.test(value)
  )
}

function formatTextThatMayBeJson(text: string): FormattedToolCallValue {
  const parsed = tryParseJson(text)
  if (parsed === undefined) {
    return { text, format: 'text', isError: textCarriesHarnessError(text) }
  }
  return {
    text: prettyJson(parsed),
    format: 'json',
    isError: jsonValueIsError(parsed) || textCarriesHarnessError(text),
  }
}

function isShellResult(value: unknown): value is JsonRecord {
  if (!isRecord(value)) return false
  return (
    Object.prototype.hasOwnProperty.call(value, 'stdout') ||
    Object.prototype.hasOwnProperty.call(value, 'stderr') ||
    Object.prototype.hasOwnProperty.call(value, 'exit_code') ||
    Object.prototype.hasOwnProperty.call(value, 'execution_time_ms')
  )
}

function jsonValueIsError(value: unknown): boolean {
  if (typeof value === 'string') return textCarriesHarnessError(value)
  if (Array.isArray(value)) return value.some(jsonValueIsError)
  if (!isRecord(value)) return false
  if (value.success === false || value.isError === true || value.is_error === true) return true
  if (typeof value.exit_code === 'number' && value.exit_code !== 0) return true
  if (typeof value.error === 'string' && value.error.trim().length > 0) return true
  // Checked on stderr specifically, not anywhere in the payload: a denial
  // quoted inside stdout is usually a log or a findings table being read, not
  // this command being refused.
  if (typeof value.stderr === 'string' && SHELL_PERMISSION_DENIED.test(value.stderr)) return true
  // A bridge failure rides in stdout with exit_code 0, and nests: the shell
  // result wraps an MCP envelope which wraps the failing tool's own payload.
  // Recursing is what finds it at whatever depth this particular tool landed.
  return Object.values(value).some(jsonValueIsError)
}

function shellResultText(result: JsonRecord): string {
  const sections: string[] = []
  const metadata: string[] = []

  if (typeof result.exit_code === 'number') metadata.push(`exit code ${result.exit_code}`)
  if (typeof result.execution_time_ms === 'number') metadata.push(`${result.execution_time_ms}ms`)
  if (metadata.length > 0) sections.push(metadata.join(' · '))

  if (typeof result.stdout === 'string' && result.stdout.length > 0) {
    sections.push(`stdout\n${formatTextThatMayBeJson(result.stdout).text}`)
  }
  if (typeof result.stderr === 'string' && result.stderr.length > 0) {
    sections.push(`stderr\n${formatTextThatMayBeJson(result.stderr).text}`)
  }
  if (typeof result.error === 'string' && result.error.length > 0) {
    sections.push(`error\n${result.error}`)
  }

  return sections.length > 0 ? sections.join('\n\n') : prettyJson(result)
}

export function formatToolCallArguments(value: string): FormattedToolCallValue {
  return formatTextThatMayBeJson(value)
}

export function formatToolCallResult(value: unknown): FormattedToolCallValue {
  const unwrapped = normalizeToolCallResultValue(value)
  if (isShellResult(unwrapped)) {
    return {
      text: shellResultText(unwrapped),
      format: 'shell',
      isError: jsonValueIsError(unwrapped),
    }
  }
  if (typeof unwrapped === 'string') {
    return formatTextThatMayBeJson(unwrapped)
  }

  return {
    text: prettyJson(unwrapped),
    format: 'json',
    isError: jsonValueIsError(unwrapped),
  }
}
