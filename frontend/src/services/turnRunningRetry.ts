import { randomUUID } from '../utils/randomUUID'
import type { AxiosError, InternalAxiosRequestConfig } from 'axios'

// A 409 with this marker means the previous turn still held the session input
// lane when this send arrived. Nothing was sent or queued, so the retry is a
// fresh submission: it must use a fresh Idempotency-Key (the 409 outcome is
// journaled as rejected under the old key) and it cannot duplicate.
const TURN_RUNNING_MARKER = 'turn_running'
const TURN_RUNNING_STATUS = 409

const MAX_TURN_RUNNING_RETRIES = 10
const TURN_RUNNING_BACKOFF_MS = [2000, 5000, 10000, 15000, 20000, 30000, 30000, 30000, 30000, 30000]

export type TurnRunningRetriableRequestConfig = InternalAxiosRequestConfig & {
  __turnRunningRetries?: number
}

export function isTurnRunningConflict(error: unknown): boolean {
  if (!error || typeof error !== 'object' || !('response' in error)) return false
  const response = (error as { response?: { status?: number; data?: unknown } }).response
  if (response?.status !== TURN_RUNNING_STATUS) return false
  const data = (response.data ?? {}) as { error?: unknown; status?: unknown }
  return data.error === TURN_RUNNING_MARKER || data.status === TURN_RUNNING_MARKER
}

export function turnRunningRetryDelayMs(attempt: number): number {
  const index = Math.min(Math.max(attempt, 0), TURN_RUNNING_BACKOFF_MS.length - 1)
  return TURN_RUNNING_BACKOFF_MS[index]
}

function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms)
  })
}

// The message keeps its first key in X-Client-Message-ID, so the server stores
// the retried send under the id of the bubble already on screen (#3795).
function rotateIdempotencyKey(headers: unknown): void {
  const holder = headers as { get?: (name: string) => unknown; set?: (name: string, value: string) => void; [name: string]: unknown } | undefined
  if (!holder) return
  const read = (name: string): unknown => typeof holder.get === 'function' ? holder.get(name) : holder[name]
  const write = (name: string, value: string) => {
    if (typeof holder.set === 'function') holder.set(name, value)
    else holder[name] = value
  }
  const first = read('Idempotency-Key')
  if (!read('X-Client-Message-ID') && typeof first === 'string' && first) write('X-Client-Message-ID', first)
  write('Idempotency-Key', randomUUID())
}

export async function retryTurnRunningSubmission(
  instance: (config: TurnRunningRetriableRequestConfig) => Promise<unknown>,
  error: unknown,
  sleep: (ms: number) => Promise<void> = defaultSleep,
): Promise<unknown> {
  if (!isTurnRunningConflict(error)) return Promise.reject(error)
  const config = (error as AxiosError).config as TurnRunningRetriableRequestConfig | undefined
  if (!config) return Promise.reject(error)
  const attempt = config.__turnRunningRetries ?? 0
  if (attempt >= MAX_TURN_RUNNING_RETRIES) return Promise.reject(error)
  config.__turnRunningRetries = attempt + 1
  rotateIdempotencyKey(config.headers)
  await sleep(turnRunningRetryDelayMs(attempt))
  return instance(config)
}
