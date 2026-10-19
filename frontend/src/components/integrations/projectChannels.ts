export type ProjectChannel = 'slack-app' | 'whatsapp' | 'cli'
export const projectChannelLabel = (channel: ProjectChannel) => channel === 'cli' ? 'MCP access' : channel === 'slack-app' ? 'Slack app' : 'WhatsApp'
