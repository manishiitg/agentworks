import { describe, expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({
  agentApi: {},
  getApiBaseUrl: () => 'http://localhost:8000',
  getAuthToken: () => null,
}))
import { parseProductCommands } from './workData'
import { toProductCommandDefinitions } from './productCommands'

describe('crew product commands', () => {
  it('parses profile commands and drops entries without a prompt', () => {
    expect(parseProductCommands({
      commands: [
        { name: 'deep work', description: 'Plan work', icon: 'brain', prompt: 'Do it {{context}}' },
        { name: 'broken', description: 'No prompt' },
      ],
    })).toEqual([
      { name: 'deep work', description: 'Plan work', icon: 'brain', prompt: 'Do it {{context}}' },
    ])
  })

  it('submits the prompt with slash-prefixed text substituted for {{context}}', () => {
    const [definition] = toProductCommandDefinitions([
      { name: 'deep work', description: '', icon: 'brain', prompt: 'Review {{context}} now' },
    ])
    expect(definition.modes).toEqual(['multi-agent'])
    const onSubmit = vi.fn()
    definition.execute?.({ beforeSlash: 'this project', onSubmit } as never)
    expect(onSubmit).toHaveBeenCalledWith('Review this project now')
  })
})
