import { describe, expect, it } from 'vitest'
import { formatRunFolderLabel, getDefaultRunFolder, getStepLatestError, getStepStatus, getStepTypeBadgeStyle, getStepTypeDescription, getStepTypeLabel, isWebhookRunFolder } from './helpers'

describe('isWebhookRunFolder', () => {
  it('recognizes webhook iteration roots and group folders', () => {
    expect(isWebhookRunFolder('iteration-6-hook')).toBe(true)
    expect(isWebhookRunFolder('iteration-6-hook/default')).toBe(true)
  })

  it('does not expose payload UI for manual or scheduled runs', () => {
    expect(isWebhookRunFolder('iteration-6/default')).toBe(false)
    expect(isWebhookRunFolder('iteration-6-sched/default')).toBe(false)
    expect(isWebhookRunFolder(null)).toBe(false)
  })
})

describe('execution log run selection', () => {
  it('keeps an explicitly opened run even when another grouped run is available', () => {
    expect(getDefaultRunFolder('iteration-85-slack-one', ['iteration-84/default'])).toBe('iteration-85-slack-one')
  })
})

describe('crew step log presentation', () => {
  it('labels crew steps as Crew, not Scripted', () => {
    expect(getStepTypeLabel('crew')).toBe('Crew')
    expect(getStepTypeDescription('crew')).toContain('Crew step')
    expect(getStepTypeBadgeStyle('crew')).toContain('sky')
  })
})

describe('formatRunFolderLabel', () => {
  it('reads iteration folders as runs', () => {
    expect(formatRunFolderLabel('iteration-84-sched/default')).toBe('Run 84 · sched · default')
    expect(formatRunFolderLabel('iteration-0/default')).toBe('Run 0 · default')
    expect(formatRunFolderLabel('iteration-6-hook')).toBe('Run 6 · Webhook')
    expect(formatRunFolderLabel('iteration-6-hook/default')).toBe('Run 6 · Webhook · default')
    expect(formatRunFolderLabel('iteration-9')).toBe('Run 9')
  })

  it('leaves anything else untouched', () => {
    expect(formatRunFolderLabel('manual/debug')).toBe('manual/debug')
  })
})

describe('getStepLatestError', () => {
  it('prefers the newest execution error, then validations', () => {
    expect(getStepLatestError({ executions: [{ content: { error: 'old  failure' } }, { content: {} }] } as never)).toBe('old failure')
    expect(getStepLatestError({ executions: [], validations: [{ content: { errors: [{ Message: 'bad gate' }] } }] } as never)).toBe('bad gate')
    expect(getStepLatestError({ executions: [], validations: [] } as never)).toBe('')
  })
})

describe('saved script execution status', () => {
  it('shows a failed script as failed even when it stops before validation', () => {
    expect(getStepStatus({ type: 'regular', executions: [{ fast_path: true, content: { success: false, exit_code: 1, error: 'script failed' } }], validations: [] } as never)).toBe('failed')
  })

  it('does not turn a recovered agent attempt into a failed step', () => {
    expect(getStepStatus({ type: 'regular', executions: [{ content: { success: false } }, { content: { success: true } }], validations: [{ content: { execution_status: 'COMPLETED' } }] } as never)).toBe('completed')
  })
})
