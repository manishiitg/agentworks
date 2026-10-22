type SessionStream = { lastActivityAt: number; isClosed: boolean }
type Attempt = { nextAt: number; failures: number; inFlight: boolean }

/** One recovery reader per session; live delivery stays with the event stream. */
export class SessionEventRecovery {
  private attempts = new Map<string, Attempt>()

  retain(sessionIds: Set<string>) {
    for (const sessionId of this.attempts.keys()) {
      if (!sessionIds.has(sessionId)) this.attempts.delete(sessionId)
    }
  }

  async poll<T>(sessionId: string, stream: SessionStream | undefined, read: () => Promise<T>): Promise<T | undefined> {
    // Status frames arrive every two seconds. Allow three missed ticks (and
    // time for a new connection) before recovering a silent/queued stream.
    if (stream && !stream.isClosed && Date.now() - stream.lastActivityAt < 6000) {
      const attempt = this.attempts.get(sessionId)
      if (!attempt?.inFlight) this.attempts.delete(sessionId)
      return undefined
    }
    let attempt = this.attempts.get(sessionId)
    if (!attempt) {
      attempt = { nextAt: 0, failures: 0, inFlight: false }
      this.attempts.set(sessionId, attempt)
    }
    if (attempt.inFlight || Date.now() < attempt.nextAt) return undefined

    attempt.inFlight = true
    try {
      const response = await read()
      attempt.failures = 0
      return response
    } catch (error) {
      attempt.failures++
      throw error
    } finally {
      attempt.inFlight = false
      // A failed server must not receive a new request every scheduler tick.
      attempt.nextAt = Date.now() + Math.min(30_000, 2000 * 2 ** Math.min(attempt.failures, 4))
    }
  }
}
