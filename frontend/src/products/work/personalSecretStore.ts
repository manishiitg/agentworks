import type { SecretStore } from '../../components/secrets/SecretSelectionSection'
import { personalMcpApi } from '../../api/personalMcp'

// A Code's secrets are the signed-in person's own (docs/design/code_private_mcp.md):
// the same secrets UI as a Crew or workflow, backed by the personal store.
// They reach only that person's Code chats (as $SECRET_<NAME>) and their own
// MCP servers; nobody else with access to the Code sees or uses them.
export const personalSecretStore: SecretStore = {
  list: async () => (await personalMcpApi.list()).secrets,
  save: (name, value) => personalMcpApi.saveSecret(name, value),
  remove: name => personalMcpApi.deleteSecret(name),
  description: 'Only you: used in your own chats of every Code and by your MCP servers. Nobody else sees them, and values cannot be read back.',
}
