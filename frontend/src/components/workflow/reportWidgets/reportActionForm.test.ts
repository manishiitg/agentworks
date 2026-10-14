import { describe, expect, it } from 'vitest'
import { actionArgs, buildActionFields } from './reportActionForm'

describe('dashboard function form', () => {
  const schema = {
    type: 'object',
    properties: {
      ticket: { type: 'string', description: 'Ticket id' },
      days: { type: 'integer' },
      env: { type: 'string', enum: ['dev', 'prod'] },
      dry_run: { type: 'boolean' },
      ids: { type: 'array' },
    },
    required: ['ticket'],
  }

  it('builds fields from the schema, prefills page defaults and sends only typed entries', () => {
    const fields = buildActionFields(schema, { ticket: 'WEB-1', ids: ['a'] })
    expect(fields.map(f => [f.name, f.kind, f.required])).toEqual([
      ['ticket', 'text', true], ['days', 'integer', false], ['env', 'choice', false], ['dry_run', 'boolean', false], ['ids', 'json', false],
    ])
    expect(fields[0].value).toBe('WEB-1')
    fields[1].value = '3'
    expect(actionArgs(fields)).toEqual({ ok: true, args: { ticket: 'WEB-1', days: 3, dry_run: false, ids: ['a'] } })
  })

  it('refuses a missing required value, a fractional integer and broken JSON', () => {
    const fields = buildActionFields(schema)
    expect(actionArgs(fields)).toEqual({ ok: false, error: 'ticket is required.' })
    fields[0].value = 'WEB-1'
    fields[1].value = '2.5'
    expect(actionArgs(fields)).toEqual({ ok: false, error: 'days must be a whole number.' })
    fields[1].value = ''
    fields[4].value = '[1,'
    expect(actionArgs(fields)).toEqual({ ok: false, error: 'ids must be valid JSON.' })
  })
})
