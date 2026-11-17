import { createContext } from 'react'

// Exact displayed project's server-provided files root, not the globally active tab.
// This only resolves spelling; the authenticated file endpoint decides access.
export const ConversationImageRoot = createContext<string | undefined>(undefined)
