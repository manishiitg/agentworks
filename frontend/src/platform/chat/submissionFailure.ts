export type SubmissionFailure = {
  message: string
  code?: string
  provider?: string
  technicalDetails?: string
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

function firstText(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return undefined
}

function statusText(value: unknown): string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return firstText(value)
}

function machineErrorCode(value: unknown): string | undefined {
  const text = firstText(value)
  return text && /^[a-z][a-z0-9_]*$/.test(text) ? text : undefined
}

// Axios rejects non-2xx responses and puts the useful server payload under
// response.data. Convert that transport shape into the same event hints used by
// restored/SSE failures so the product error card can show safe diagnostics.
export function submissionFailure(error: unknown): SubmissionFailure {
  const errorRecord = asRecord(error)
  const response = asRecord(errorRecord?.response)
  const responseData = response?.data
  const data = asRecord(responseData)
  const nestedError = asRecord(data?.error)
  const responseErrorText = firstText(typeof data?.error === 'string' ? data.error : undefined)
  const responseMessage = firstText(
    data?.message,
    nestedError?.message,
    typeof responseData === 'string' ? responseData : undefined,
    responseErrorText,
  )
  const fallbackMessage = firstText(
    typeof error === 'string' ? error : undefined,
    error instanceof Error ? error.message : undefined,
    errorRecord?.message,
    'The request could not be started.',
  ) as string
  const status = statusText(response?.status)
  const technicalDetails = firstText(data?.technical_details, data?.technicalDetails)
    || [status ? `Request failed with status code ${status}` : undefined, responseMessage || fallbackMessage]
      .filter((value): value is string => Boolean(value))
      .join('\n')

  return {
    message: responseMessage || fallbackMessage,
    code: firstText(data?.code, data?.error_code, machineErrorCode(data?.error), nestedError?.code, response?.status === 403 ? 'access_denied' : undefined),
    provider: firstText(data?.provider, nestedError?.provider),
    technicalDetails,
  }
}

/** The server refused this target; retrying the same message cannot restore access. */
export function isAccessRefusal(failure: SubmissionFailure): boolean {
  return failure.code === 'access_denied' || failure.code === 'project_unavailable'
}
