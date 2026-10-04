// The ONE switch for whether tool calls are shown in the chat transcript.
//
// Tool calls are a debugging aid, not a supported product feature, and may be
// removed from the UI. Everything that decides whether they render reads this
// helper, so hiding them everywhere later is a one-line change. It is
// deliberately not a user-facing setting. The server's compact restore (older
// turns as messages only) does not depend on it.
const SHOW_TOOL_CALLS = true

export function toolCallsVisible(): boolean {
  return SHOW_TOOL_CALLS
}

export function withToolCallVisibility<T extends { kind: string }>(items: T[]): T[] {
  return toolCallsVisible() ? items : items.filter(item => item.kind !== 'tools')
}
