import { expect, it } from 'vitest'
import { allowedModelOrFirst, allowedModelsSummary, filterAllowedModels, isModelAllowed } from './allowedModels'

const models = [{ model_id: 'gpt-5.3-codex' }, { model_id: 'gpt-5.5' }, { model_id: 'gpt-5.4' }]

it('offers every model when the account has no list', () => {
  expect(filterAllowedModels(models, undefined)).toBe(models)
  expect(filterAllowedModels(models, [])).toBe(models)
  expect(isModelAllowed('anything', [])).toBe(true)
  expect(allowedModelsSummary(undefined)).toBe('All models')
})

it('offers only the allowed models, in catalog order, ignoring case', () => {
  expect(filterAllowedModels(models, ['GPT-5.4', 'gpt-5.3-codex']).map(model => model.model_id)).toEqual(['gpt-5.3-codex', 'gpt-5.4'])
  expect(allowedModelsSummary(['a'])).toBe('1 model')
  expect(allowedModelsSummary(['a', 'b'])).toBe('2 models')
})

it('shows the first allowed model selected when the current one is not allowed', () => {
  expect(allowedModelOrFirst('gpt-5.5', ['gpt-5.3-codex'])).toBe('gpt-5.3-codex')
  expect(allowedModelOrFirst('gpt-5.3-codex', ['gpt-5.3-codex', 'gpt-5.4'])).toBe('gpt-5.3-codex')
  expect(allowedModelOrFirst('gpt-5.5', undefined)).toBe('gpt-5.5')
  expect(allowedModelOrFirst(undefined, ['only-one'])).toBe('only-one')
})
