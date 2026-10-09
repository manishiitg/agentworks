import { createContext, useContext } from 'react'

/** True for a Vault reader: every Vault screen shows, nothing can be changed. */
export const VaultReadOnlyContext = createContext(false)
export const useVaultReadOnly = () => useContext(VaultReadOnlyContext)
