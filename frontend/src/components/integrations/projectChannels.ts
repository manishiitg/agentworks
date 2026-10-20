export type ProjectChannel = 'slack' | 'whatsapp' | 'cli' | 'share'
export const projectChannelLabel = (channel: ProjectChannel) => channel === 'cli' ? 'MCP access' : channel === 'share' ? 'Share' : channel === 'slack' ? 'Slack' : 'WhatsApp'
