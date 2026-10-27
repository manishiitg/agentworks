import { describe, expect, it } from 'vitest'
import { quickCommandsFromProfile } from './commands'

describe('quickCommandsFromProfile', () => {
  it('keeps command identity separate from the label and drops incomplete entries', () => {
    expect(quickCommandsFromProfile({ commands: [
      { name: 'create-test', description: 'Create a practice test', icon: 'x', prompt: 'Create a practice test for my child.' },
      { name: 'backup', prompt: 'Back up my workspace now.' },
      { name: 'broken', description: 'No prompt' },
      { description: 'No name', prompt: 'Do something' },
      { name: 'invalid', prompt: { text: 'not a prompt' } },
    ] })).toEqual([
      { name: 'create-test', label: 'Create a practice test', message: 'Create a practice test for my child.' },
      { name: 'backup', label: 'backup', message: 'Back up my workspace now.' },
    ])
    expect(quickCommandsFromProfile(null)).toEqual([])
  })

  it('preserves platform aliases and hidden menu shortcuts', () => {
    expect(quickCommandsFromProfile({ commands: [{
      name: ' check-in ', description: ' Check progress ', prompt: ' Review learning ',
      aliases: [' pulse ', '', 42], menu_hidden: true,
    }] })).toEqual([{
      name: 'check-in', label: 'Check progress', message: 'Review learning',
      aliases: ['pulse'], menuHidden: true,
    }])
  })
})
