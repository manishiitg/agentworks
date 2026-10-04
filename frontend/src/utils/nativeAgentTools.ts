/**
 * "Native agent tools" is always on for every workflow, Relay, Crew and Code (owner, 2026-10-03): a value an older manifest saved as false
 * is ignored, because there is no switch left to turn it back on. Mirrors nativeAgentToolsEnabled in the server.
 */
export function nativeAgentToolsEnabled(_setting: unknown): boolean {
  return true
}
