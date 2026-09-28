export interface CodingProviderGuide {
  authenticateNote: string
}

export const CODING_PROVIDER_GUIDES: Record<string, CodingProviderGuide> = {
  'claude-code': {
    authenticateNote: 'Use the guided Claude Code window to complete sign-in. No SSH or direct server access is required.',
  },
  'codex-cli': {
    authenticateNote: 'Complete the guided browser sign-in. No SSH or direct server access is required. CODEX_API_KEY is also supported for managed environments.',
  },
  'cursor-cli': {
    authenticateNote: 'Complete the guided Cursor authentication flow. No SSH or direct server access is required.',
  },
  'pi-cli': {
    authenticateNote: 'Open the guided Pi window, type /login, and choose the model provider you want to connect. Type /quit when finished. No SSH or direct server access is required.',
  },
  'muse-cli': {
    authenticateNote: 'Complete the guided Muse sign-in. No SSH or direct server access is required. META_API_KEY is also supported for managed environments.',
  },
  'agy-cli': {
    authenticateNote: 'Launch the guided Antigravity window and complete Google sign-in. No SSH or direct server access is required.',
  },
}

// DEFAULT_CODING_PROVIDER_GUIDE keeps the providers page renderable for a
// newly onboarded CLI that has no tailored guidance yet. Kept outside
// CODING_PROVIDER_GUIDES so the exact-keys test still pins the tailored set.
export const DEFAULT_CODING_PROVIDER_GUIDE: CodingProviderGuide = {
  authenticateNote: 'Complete authentication for this provider in the guided terminal. No SSH or direct server access is required.',
}
