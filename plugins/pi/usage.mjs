// The native SDK computes its own /session statistics. Project only numeric
// counters; do not expose its sessionFile, costs, context or conversation data.
export async function nativeUsageStats(ctx, AgentSession) {
  if (ctx.sessionManager.getEntries().length > 65536) throw new Error('Native usage record cap reached');
  if (typeof AgentSession?.prototype?.getSessionStats !== 'function') throw new Error('Native usage unavailable');
  // ExtensionContext exposes the native read-only session manager, not its
  // AgentSession. The SDK's public statistics method consumes that manager;
  // the unrelated context meter is deliberately excluded from this view.
  const nativeView = {
    sessionManager: ctx.sessionManager,
    sessionFile: ctx.sessionManager.getSessionFile(),
    sessionId: ctx.sessionManager.getSessionId(),
    getContextUsage: () => undefined,
  };
  const stats = AgentSession.prototype.getSessionStats.call(nativeView);
  if (stats.sessionId !== nativeView.sessionId) throw new Error('Native usage session mismatch');
  const tokens = Object.fromEntries(['input', 'output', 'cacheRead', 'cacheWrite', 'total']
    .map(key => [key, stats.tokens?.[key]]));
  if (Object.values(tokens).some(n => !Number.isSafeInteger(n) || n < 0) ||
      !Number.isSafeInteger(stats.userMessages) || stats.userMessages < 0) throw new Error('Native usage unavailable');
  return { sessionId: stats.sessionId, tokens, userMessages: stats.userMessages,
    model: typeof ctx.model?.id === 'string' ? ctx.model.id : '' };
}
