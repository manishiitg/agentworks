import { describe, expect, it, vi } from 'vitest'
import type { CommandContext } from '../../../commands/types'
import { quickCommandsFromProfile } from '../api/platform/commands'
import { toProductCommandDefinitions } from './productCommands'

function command(prompt = 'Build the activity.\n\nAdditional context from me (optional): {{context}}') {
  return toProductCommandDefinitions(quickCommandsFromProfile({ commands: [{
    name: 'create-study-material-ib', description: 'Study material (IB style)', prompt,
    aliases: ['ib-lesson'],
  }] }))[0]
}
function run(prompt: string | undefined, context: string) {
  const onSubmit = vi.fn()
  command(prompt).execute({ beforeSlash: context, onSubmit } as unknown as CommandContext)
  return onSubmit
}

describe('SparkQuill slash commands', () => {
  it('uses the manifest name independently of its display label', () => {
    expect(command()).toMatchObject({
      command: 'create-study-material-ib', description: 'Study material (IB style)',
      aliases: ['ib-lesson'], source: 'product', modes: ['multi-agent'],
    })
  })

  it('keeps typed topics and preferences in the submitted prompt', () => {
    expect(run(undefined, 'fractions, Grade 4, include visual examples')).toHaveBeenCalledWith(
      'Build the activity.\n\nAdditional context from me (optional): fractions, Grade 4, include visual examples',
    )
  })

  it('runs bare shortcuts without an empty context label', () => {
    expect(run(undefined, '  ')).toHaveBeenCalledWith('Build the activity.')
  })

  it('honours hidden commands without adding redundant aliases', () => {
    const [definition] = toProductCommandDefinitions([{ name: 'recap', label: 'Recap', message: 'Recap it.', menuHidden: true }])
    expect(definition.menuHidden).toBe(true)
    expect(definition.aliases).toBeUndefined()
  })
})
