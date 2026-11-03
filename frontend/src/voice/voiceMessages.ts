// Which messages this browser sent by voice, so their bubble shows a small mic (owner, 3 Nov). A display hint only:
// kept per viewer, and a lost entry just shows no mic.
const KEY = 'agentworks:voice-messages'
let pendingVoice = false

export function markNextSubmissionVoice() { pendingVoice = true }

/** Called with the submission id of the message being sent; remembers it if the composer marked it as voice. */
export function rememberIfVoice(submissionId: string) {
  if (!pendingVoice) return
  pendingVoice = false
  try {
    const ids: string[] = JSON.parse(localStorage.getItem(KEY) || '[]')
    localStorage.setItem(KEY, JSON.stringify([...ids.filter(id => id !== submissionId), submissionId].slice(-200)))
  } catch { /* storage unavailable: no mic mark */ }
}

export function isVoiceMessage(id: unknown): boolean {
  if (typeof id !== 'string' || !id) return false
  try { return (JSON.parse(localStorage.getItem(KEY) || '[]') as string[]).includes(id) } catch { return false }
}
