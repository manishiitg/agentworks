export type ProjectChannel = 'slack' | 'whatsapp' | 'cli'
export const projectChannelLabel = (channel: ProjectChannel) => channel === 'cli' ? 'MCP access' : channel === 'slack' ? 'Slack' : 'WhatsApp'
