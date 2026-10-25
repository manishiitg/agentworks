import { describe, expect, it } from 'vitest'
import {
  formatToolCallArguments,
  formatToolCallResult,
  normalizeToolCallResultValue,
  toolCallValueToText,
} from './toolCallFormatting'

describe('formatToolCallArguments', () => {
  it('pretty-prints JSON arguments', () => {
    expect(formatToolCallArguments('{"command":"echo ok","timeout":30}')).toEqual({
      format: 'json',
      text: '{\n  "command": "echo ok",\n  "timeout": 30\n}',
      isError: false,
    })
  })

  it('leaves non-JSON arguments unchanged', () => {
    expect(formatToolCallArguments('plain text')).toEqual({
      format: 'text',
      text: 'plain text',
      isError: false,
    })
  })
})

describe('formatToolCallResult', () => {
  it('accepts an object-valued MCP shell result without assuming it is a string', () => {
    const result = {
      content: [{
        type: 'text',
        text: JSON.stringify({ stdout: 'ok\n', stderr: '', exit_code: 0 }),
      }],
      structured_content: null,
    }

    expect(normalizeToolCallResultValue(result)).toEqual({
      stdout: 'ok\n',
      stderr: '',
      exit_code: 0,
    })
    expect(formatToolCallResult(result)).toEqual({
      format: 'shell',
      text: 'exit code 0\n\nstdout\nok\n',
      isError: false,
    })
  })

  it('renders non-text content arrays as JSON instead of leaking an array to string methods', () => {
    const result = { content: [{ type: 'image', data: 'abc' }] }

    expect(toolCallValueToText(normalizeToolCallResultValue(result))).toBe(
      '{\n  "content": [\n    {\n      "type": "image",\n      "data": "abc"\n    }\n  ]\n}',
    )
  })

  it('unwraps an MCP text envelope and formats JSON stdout', () => {
    const shellResult = JSON.stringify({
      stdout: JSON.stringify({
        top_type: 'object',
        length: 2,
        keys: ['result', 'success'],
      }),
      stderr: '',
      exit_code: 0,
      execution_time_ms: 27,
    })
    const mcpEnvelope = JSON.stringify({
      content: [{ type: 'text', text: shellResult }],
      structured_content: null,
    })

    expect(formatToolCallResult(mcpEnvelope)).toEqual({
      format: 'shell',
      text: 'exit code 0 · 27ms\n\nstdout\n{\n  "top_type": "object",\n  "length": 2,\n  "keys": [\n    "result",\n    "success"\n  ]\n}',
      isError: false,
    })
  })

  it('keeps multiline shell stdout readable and omits empty stderr', () => {
    const result = JSON.stringify({
      content: [{
        type: 'text',
        text: JSON.stringify({
          stdout: 'post-run-monitor 880\nassumption-audit 92\nreview-improve-log 447\n',
          stderr: '',
          exit_code: 0,
          execution_time_ms: 72,
        }),
      }],
      structured_content: null,
    })

    expect(formatToolCallResult(result)).toEqual({
      format: 'shell',
      text: 'exit code 0 · 72ms\n\nstdout\npost-run-monitor 880\nassumption-audit 92\nreview-improve-log 447\n',
      isError: false,
    })
  })

  it('expands JSON strings nested inside ordinary JSON results', () => {
    const result = JSON.stringify({
      success: true,
      result: JSON.stringify({ reference: 'PATH DISCIPLINE' }),
    })

    expect(formatToolCallResult(result)).toEqual({
      format: 'json',
      text: '{\n  "success": true,\n  "result": {\n    "reference": "PATH DISCIPLINE"\n  }\n}',
      isError: false,
    })
  })

  it('leaves plain-text results unchanged', () => {
    expect(formatToolCallResult('done')).toEqual({
      format: 'text',
      text: 'done',
      isError: false,
    })
  })

  it('marks a Claude native Bash non-zero exit as an error', () => {
    expect(formatToolCallResult('Exit code 1\nls: missing: No such file or directory')).toEqual({
      format: 'text',
      text: 'Exit code 1\nls: missing: No such file or directory',
      isError: true,
    })
  })

  it('marks a native tool call the policy blocked as an error (QA #218)', () => {
    const blocked = 'tool blocked by hook: Muse internal tools are disabled for this session; use web search or an AgentWorks MCP tool.'
    expect(formatToolCallResult(blocked)).toEqual({ format: 'text', text: blocked, isError: true })
    expect(formatToolCallResult('The hook docs say a tool blocked by hook: is rare').isError).toBe(false)
  })

  it('keeps a Claude native Bash zero exit successful', () => {
    expect(formatToolCallResult('Exit code 0\nfile.txt')).toEqual({
      format: 'text',
      text: 'Exit code 0\nfile.txt',
      isError: false,
    })
  })

  it('marks a non-zero nested shell exit as an error and preserves stderr', () => {
    const result = JSON.stringify({
      content: [{
        type: 'text',
        text: JSON.stringify({
          stdout: '',
          stderr: 'Error: in prepare, unable to open database file (14)\n',
          exit_code: 14,
          execution_time_ms: 35,
        }),
      }],
      structured_content: null,
    })

    expect(formatToolCallResult(result)).toEqual({
      format: 'shell',
      text: 'exit code 14 · 35ms\n\nstderr\nError: in prepare, unable to open database file (14)\n',
      isError: true,
    })
  })
})

describe('bridge tool failures that exit 0', () => {
  // A tool that fails behind the HTTP bridge returns its error as ordinary
  // stdout: the curl succeeded, so exit_code is 0 and every exit-code check
  // reports success. One day of codex rollouts (2026-08-01) held 34 of these,
  // all rendered with a green check — 46 get_api_spec rejections, 14 failed
  // mark_pulse_module_result calls, 8 get_goal_update_result misses.
  it('flags a failed tool call carried in stdout with exit_code 0', () => {
    const result = formatToolCallResult(JSON.stringify({
      stdout: 'ERROR: tool execution failed: layer=custom_tool_handler '
        + 'tool=get_goal_update_result session=abc: sql: no rows in result set',
      stderr: '',
      exit_code: 0,
      execution_time_ms: 25,
    }))

    expect(result.isError).toBe(true)
  })

  it('flags the virtual-tool form too', () => {
    const result = formatToolCallResult(JSON.stringify({
      stdout: 'ERROR: tool execution failed: layer=virtual_tool_handler '
        + 'tool=get_api_spec session=xyz: server "custom" is not available.',
      exit_code: 0,
    }))

    expect(result.isError).toBe(true)
  })

  it('flags canceled and timed-out envelopes', () => {
    for (const kind of ['canceled', 'timed out']) {
      const result = formatToolCallResult(JSON.stringify({
        stdout: `ERROR: tool execution ${kind}: layer=custom_tool_handler tool=t session=s`,
        exit_code: 0,
      }))
      expect(result.isError, kind).toBe(true)
    }
  })

  // Matching keys off `layer=`, not the word "error", so tool output that
  // merely discusses errors stays clean. A findings table or a log query would
  // otherwise light up red on every row.
  it('does not flag output that merely mentions errors', () => {
    const result = formatToolCallResult(JSON.stringify({
      stdout: 'ERROR count: 3\nrecent errors: tool execution was reviewed for failure modes',
      exit_code: 0,
    }))

    expect(result.isError).toBe(false)
  })

  it('finds the failure nested inside an MCP envelope', () => {
    const result = formatToolCallResult(JSON.stringify({
      content: [{
        type: 'text',
        text: JSON.stringify({
          stdout: 'ERROR: tool execution failed: layer=custom_tool_handler '
            + 'tool=mark_pulse_module_result session=s: review_run_id must start with a UTC date-time',
          exit_code: 0,
        }),
      }],
    }))

    expect(result.isError).toBe(true)
  })
})

describe('folder-guard denials that exit 0', () => {
  // A denial may be wrapped in a result carrying exit_code 0, including a
  // pipeline whose final command determines the status. The detector must use
  // the returned stderr rather than assume that exit_code represents the
  // denied inner command.
  it('flags a permission denial on stderr despite exit_code 0', () => {
    const result = formatToolCallResult(JSON.stringify({
      stdout: '',
      stderr: 'find: Workflow/social-media: Operation not permitted',
      exit_code: 0,
      execution_time_ms: 14,
    }))

    expect(result.isError).toBe(true)
  })

  it('flags lowercase permission denied too', () => {
    const result = formatToolCallResult(JSON.stringify({
      stderr: 'ls: /Users/x/db: Permission denied',
      exit_code: 0,
    }))

    expect(result.isError).toBe(true)
  })

  // "No such file or directory" is deliberately not a denial: probing for a
  // path that may not exist is ordinary, and flagging it would train the
  // operator to ignore the marker.
  it('does not flag a missing path', () => {
    const result = formatToolCallResult(JSON.stringify({
      stderr: 'ls: runs/iteration-0/default/inputs: No such file or directory',
      exit_code: 0,
    }))

    expect(result.isError).toBe(false)
  })

  // Only stderr counts. A denial quoted in stdout is usually a log being read.
  it('does not flag a denial quoted in stdout', () => {
    const result = formatToolCallResult(JSON.stringify({
      stdout: '2026-08-02 log line: Operation not permitted (from an earlier run)',
      stderr: '',
      exit_code: 0,
    }))

    expect(result.isError).toBe(false)
  })
})

describe('bridge result envelopes', () => {
  it('unwraps a {success:{content:[{text:{text}}]}} shell result to its stdout', () => {
    // The exact shape that reached a user's transcript as raw JSON.
    const raw = JSON.stringify({
      success: {
        content: [{ text: { text: JSON.stringify({ stdout: 'Daily Ops — Latency (Dev)\n  routes ok', stderr: '', exit_code: 0 }) } }],
        isError: false,
      },
    })
    const formatted = formatToolCallResult(raw)
    expect(formatted.text).toContain('Daily Ops — Latency (Dev)')
    expect(formatted.text).not.toContain('"success"')
    expect(formatted.isError).toBe(false)
  })
})
