// Optional metadata only. Never read or render the native conversation here.
// Anthropic mods: session/turn lifecycle + AbovePrompt, no interval or render I/O.
export function registerStatus(on) {
  let cached = { session: '', line: '' };
  let revision = 0;
  const refresh = async ($, e, next) => {
    if (e.agentId !== undefined) return next(e);
    const request = ++revision;
    cached = { session: '', line: '' };
    try {
      if (await $.env.get('AGENTWORKS_TERMINAL_STATUS') !== '0') {
        const session = await $.session.id();
        const result = await $.process.run(['agentworks', 'plugin', 'terminal-status', 'claude'], { timeoutMs: 2500 });
        const line = result.exitCode === 0 ? result.stdout.trim() : '';
        // The CLI emits one sanitized metadata line; never display diagnostics.
        if (request === revision && line.startsWith('AgentWorks · ') && !/[\r\n\x00-\x1f\x7f]/.test(line) && line.length < 600) cached = { session, line };
      }
    } catch { /* Failure clears stale Crew/account metadata; normal work continues. */ }
    $.ui.invalidate('ui.render');
    return next(e);
  };
  on('session.start', refresh);
  on('turn.start', refresh);
  on('turn.complete', refresh);
  on('session.end', async ($, e, next) => { ++revision; cached = { session: '', line: '' }; return next(e); });
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (!cached.line || cached.session !== await $.session.id()) return next(e);
    const { Text } = $.ui.resolve(e);
    return Text({ dimColor: true, children: [cached.line] });
  });
}
