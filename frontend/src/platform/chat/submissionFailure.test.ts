import { describe, expect, it } from 'vitest'
import { normalizeProductChatFailure } from './productChatFailure'
import { isAccessRefusal, submissionFailure } from './submissionFailure'

describe('submissionFailure', () => {
  it('preserves a diagnostic 409 response for the Technical details card', () => {
    const failure = submissionFailure({
      message: 'Request failed with status code 409',
      response: {
        status: 409,
        data: {
          error: 'live_input_unavailable',
          message: 'Live input unavailable: context deadline exceeded',
          provider: 'pi-cli',
          technical_details: 'Request failed with status code 409\ntmux: mlp-pi-cli-test\nTerminal snapshot:\nπ • ✅ api-bridge_execute_shell_command',
        },
      },
    })

    expect(failure).toEqual({
      message: 'Live input unavailable: context deadline exceeded',
      code: 'live_input_unavailable',
      provider: 'pi-cli',
      technicalDetails: 'Request failed with status code 409\ntmux: mlp-pi-cli-test\nTerminal snapshot:\nπ • ✅ api-bridge_execute_shell_command',
    })
    const productFailure = normalizeProductChatFailure(failure.message, {
      code: failure.code,
      provider: failure.provider,
      technicalDetails: failure.technicalDetails,
    })
    expect(productFailure.title).toBe('The response could not be completed')
    expect(productFailure.technicalDetails).toContain('tmux: mlp-pi-cli-test')
  })

  it('keeps plain transport errors useful', () => {
    const failure = submissionFailure(new Error('Network Error'))
    expect(failure.message).toBe('Network Error')
    expect(failure.technicalDetails).toBe('Network Error')
  })

  it('shows a server error string once instead of repeating the Axios status', () => {
    const failure = submissionFailure({
      message: 'Request failed with status code 422',
      response: {
        status: 422,
        data: { error: 'initialize product code folder: folder already exists' },
      },
    })

    expect(failure.message).toBe('initialize product code folder: folder already exists')
    expect(failure.code).toBeUndefined()
    expect(failure.technicalDetails).toBe('Request failed with status code 422\ninitialize product code folder: folder already exists')
  })
})

it('shows a revoked project refusal once, without retrying or restoring a duplicate draft', () => {
  const message = 'This Crew is unavailable. Ask its owner to share it with you, or open another Crew.'
  for (const data of [{ error: { code: 'project_unavailable', message } }, message]) {
    const failure = submissionFailure({ response: { status: 403, data } })
    expect(isAccessRefusal(failure)).toBe(true)
    expect(normalizeProductChatFailure(failure.message, failure)).toMatchObject({ message, retryable: false })
  }
  expect(isAccessRefusal(submissionFailure(new Error('Network Error')))).toBe(false)
})

// PLAT-746-158 A4: a durable 409 must be reconciled, never blindly resent.
it('keeps uncertain delivery out of the retry path', () => {
  const failure = submissionFailure({ response: { status: 409, data: {
    error: 'delivery_uncertain', message: 'Submission saved but delivery uncertain.',
  } } })
  expect(normalizeProductChatFailure(failure.message, failure)).toMatchObject({ code: 'delivery_uncertain', retryable: false })
})

it('preserves server failure metadata from an HTTP refusal', () => {
  const failure = submissionFailure({ response: { status: 422, data: {
    error: 'billing_error', message: 'raw diagnostic', metadata: {
      provider_message: 'Plan does not include this model.', next_step: 'Choose another model.', retryable: false,
    },
  } } })
  expect(normalizeProductChatFailure(failure.message, failure)).toMatchObject({
    code: 'billing_error', message: 'Plan does not include this model. Choose another model.', retryable: false,
  })
})
