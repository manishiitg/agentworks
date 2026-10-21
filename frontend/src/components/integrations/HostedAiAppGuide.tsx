import type { ReactNode } from 'react'
import { AI_APPS, type HostedAiApp } from './AiAppPicker'
import { CommandRow } from './LocalAgentSetupSteps'

const guides: Record<HostedAiApp, string> = {
  chatgpt: 'https://help.openai.com/en/articles/12584461-connecting-custom-mcp-servers-in-chatgpt',
  cowork: 'https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp',
  grok: 'https://docs.x.ai/grok/connectors',
  dots: 'https://learn.chatgpt.com/docs/dots/computers-and-apps',
  'muse-personal': 'https://ai.meta.com/muse',
}

/** Hosted apps sign in through the existing OAuth consent flow, never a copied local key. */
export function HostedAiAppGuide({ app, endpoint, serviceName = 'AgentWorks', localServer = false, children }: { app: HostedAiApp; endpoint: string; serviceName?: string; localServer?: boolean; children?: ReactNode }) {
  const name = AI_APPS.find(item => item.id === app)!.name
  return <section aria-label={`Connect ${name}`} className="space-y-4 rounded-xl border border-border p-4 sm:p-5">
    <h3 className="text-sm font-semibold">Connect {name}</h3>
    {localServer ? <div className="space-y-2"><p className="text-sm">Open Connect AI tools on your hosted AgentWorks workspace.</p><p className="text-xs text-muted-foreground">{name} cannot reach this computer’s local address. Use your hosted workspace’s connection and sign in there. No local connection key is needed for that setup.</p></div>
      : app === 'muse-personal' ? <div className="space-y-2"><p className="text-sm">Muse personal-agent setup has not been verified yet.</p><p className="text-xs text-muted-foreground">Muse supports connectors, but its personal-agent setup is different from Muse Code. Check the connector options available in your Muse account before connecting.</p></div>
        : <>
          <p className="text-xs text-muted-foreground">Already connected? {(app === 'chatgpt' || app === 'dots') ? `In ChatGPT, open Plugins → ${serviceName} → Manage → Refresh tools, then start a new chat.` : `Use the existing ${serviceName} connection.`} Sign in again only if your app asks you to reconnect.</p>
          <ol className="space-y-4 text-sm">
            <li><p className="font-medium">1. Open {app === 'dots' ? 'ChatGPT' : name}</p><p className="mt-1 text-xs text-muted-foreground">{app === 'chatgpt' || app === 'dots' ? 'In ChatGPT.com, open Plugins → + → Add custom MCP server. If your workspace uses the older Apps page, choose Apps → Create.' : app === 'grok' ? 'Open grok.com/connectors → New Connector → Custom.' : 'In Claude, open Customize → Connectors → Add custom connector.'}</p></li>
            <li className="space-y-2"><p className="font-medium">2. Add {serviceName} and sign in</p><p className="text-xs text-muted-foreground">Name the connection {serviceName} and paste this address. Choose OAuth for sign-in, then review and approve the access you want to give.</p><CommandRow label="Connection address" command={endpoint} /></li>
            <li><p className="font-medium">3. {app === 'dots' ? 'Enable it for your dot' : 'Use it in a chat'}</p><p className="mt-1 text-xs text-muted-foreground">{app === 'dots' ? 'Your dot can use supported ChatGPT plugins enabled for your account. Review its app access and allowed actions. Availability depends on your account and plugin support.' : `Select ${serviceName} in your app and ask what it can help you do.`}</p></li>
          </ol>
          <p className="text-xs text-muted-foreground">Your app saves the connection. Account, plan and workspace permissions still apply.</p>
          {children}
        </>}
    <a href={guides[app]} target="_blank" rel="noreferrer" className="inline-block text-xs text-primary underline">{name} connection help</a>
  </section>
}
