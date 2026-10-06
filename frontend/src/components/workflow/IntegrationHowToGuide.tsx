import { HowToAnswers, type HowToQuestion } from './HowToAnswers'
import { GmailHowToGuide } from './bots/GmailHowToGuide'
import type { IntegrationHowToTopic } from './integrationHowToTopics'

function questionsFor(topic: Exclude<IntegrationHowToTopic, 'Gmail'>, scopeNoun: 'project' | 'workflow'): HowToQuestion[] {
  switch (topic) {
    case 'MCPs': return [
      {
        title: 'How do I let this work use an app that is already connected?',
        answer: <>Find it under <b>Platform connected</b> and select it. It then appears under <b>This {scopeNoun}</b>; choose individual tools there if shown. The selection saves immediately.</>,
      },
      {
        title: 'How do I connect a new app?',
        answer: <>Use <b>Search apps</b> and look under <b>Connect a new app</b>. Click <b>Ask AI</b> on the app card, or use the help button above the list, to start setup. New app accounts are shared across AgentWorks, so a platform admin must authorize them on multi-user installations.</>,
      },
      {
        title: 'How do I add an app that is missing from the list?',
        answer: <>Search for its name first. If it is absent, use <b>Ask AI</b> above the list and describe the service you need. The assistant can look for a compatible MCP server and help an admin connect it.</>,
      },
      {
        title: 'Why can I select an app but not connect a new one?',
        answer: <>Selecting an already connected app changes this {scopeNoun}. Creating or replacing the underlying connection changes a shared platform account and requires an admin on multi-user installations. Ask an admin to connect it, then select it here.</>,
      },
      {
        title: 'Why is a selected app not working?',
        answer: <>Check the status dot beside its name. A connected app may still be unreachable or waiting for tools to load. Click <b>Refresh connected integrations</b>, then use the app card’s <b>Ask AI</b> button to inspect its connection and selected tools.</>,
      },
      {
        title: 'How do I stop this work from using an app?',
        answer: <>Remove its selection under <b>This {scopeNoun}</b>. That removes access here; it does not disconnect the shared platform account for everyone else.</>,
      },
      {
        title: 'How are MCPs different from Skills and Connect?',
        answer: <>MCPs provide tools from external apps. Skills provide reusable task instructions. The <b>Connect</b> tab lets an outside AI app or terminal use AgentWorks.</>,
      },
    ]
    case 'Skills': return [
      { title: 'How do I add a skill?', answer: <>Ask the agent to find, install or create a skill in this {scopeNoun}. Skills belong to this workspace. Select an installed skill for the main chat, or ask the Builder to attach it to the steps that need it.</> },
      { title: 'Where is a skill used?', answer: <>The list shows each skill’s folder and its main chat or step attachments. Steps use their own attachments; selecting a skill for the main chat does not attach it to steps.</> },
      { title: 'How do I uninstall a skill?', answer: <>Expand the skill and click <b>Uninstall</b>. Confirm to remove its files and chat/step attachments from this workspace. Platform-managed skills cannot be uninstalled.</> },
      { title: 'Why is a skill unavailable?', answer: <>An attachment whose files are missing stays visible. Refresh the list, ask the agent to reinstall it in this workspace, or remove the attachment.</> },
    ]
    case 'Slack': return [
      {
        title: 'How do I choose between its own bot and the shared bot?',
        answer: <>Choose <b>Its own bot</b> if it should answer only for this {scopeNoun} in any channel you invite it to. Choose <b>Shared bot</b> if one platform bot should answer for several projects or workflows, with specific channel IDs routed here.</>,
      },
      {
        title: 'How do I create this work’s own bot?',
        answer: <>Select <b>Its own bot</b>. Copy the manifest under <b>Create from manifest</b> into Slack (Create New App → From a manifest), or open <b>Where to get Slack tokens and set up the app by hand</b> and follow the <b>Permissions and events</b> checklist; then enter a bot name and both tokens, then click <b>Save &amp; test</b>.</>,
      },
      {
        title: 'Where do I get the two Slack tokens?',
        answer: <>Open your <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="underline">Slack app settings</a>, install the app, and copy the <b>Bot User OAuth Token</b> from <b>OAuth &amp; Permissions</b>; it starts with <code>xoxb-</code>. Under <b>Basic Information → App-Level Tokens</b>, create a token with <code>connections:write</code>; it starts with <code>xapp-</code>. Enable <b>Socket Mode</b>.</>,
      },
      {
        title: 'Which permissions and events does the bot need?',
        answer: <>Under <b>Bot Token Scopes</b>, add <code>app_mentions:read</code>, <code>channels:history</code>, <code>groups:history</code>, <code>channels:read</code>, <code>groups:read</code>, <code>chat:write</code>, <code>reactions:write</code>, <code>users:read</code>, and <code>users:read.email</code>. Subscribe to <code>app_mention</code>, <code>message.channels</code>, and <code>message.groups</code>. For direct messages with the bot, also add <code>im:history</code>, <code>im:read</code> and the <code>message.im</code> event, and turn on <b>App Home → Messages Tab</b>. The manifest sets all of this. The full checklist is in this tab.</>,
      },
      {
        title: 'How do I make the bot answer in a channel?',
        answer: <>For its own bot, invite it with <code>/invite @YourBot</code> and @mention it; no channel route is needed. For the shared bot, invite it, find the channel ID in Slack’s channel details, then enter that ID under <b>Shared bot</b> and click <b>Add channel</b>.</>,
      },
      {
        title: 'How do I test or fix a silent bot?',
        answer: <>Click <b>Test bot</b> or <b>Save &amp; test</b> and read <b>Setup checks</b>. Confirm the bot is enabled, Socket Mode and the three events are on, and it is invited to the channel. Reinstall the Slack app after changing scopes, then try an @mention and a plain thread reply.</>,
      },
      {
        title: 'How do I limit who can use a shared-bot route?',
        answer: <>Open the route’s <b>Options</b>, enter <b>Blocked email addresses</b>, and click <b>Save</b>. Everyone in the channel is allowed unless blocked.</>,
      },
      {
        title: 'How do I switch fully to the shared bot?',
        answer: <>Select <b>Shared bot</b> and add its channel route. If this {scopeNoun} still has its own bot, remove that bot too; otherwise it continues answering wherever it was invited.</>,
      },
    ]
    case 'WhatsApp': return [
      {
        title: 'How do I pair WhatsApp?',
        answer: <>Click <b>Open</b> on the WhatsApp row. Scan the <b>Pair device</b> QR in WhatsApp under <b>Linked Devices → Link a device</b>. Wait for the status to become <b>Connected</b>. Pairing is account-wide and shared across projects and workflows. Each account links one WhatsApp; to use another number, unpair this one first.</>,
      },
      {
        title: 'What if the pairing QR expires?',
        answer: <>Click <b>Refresh</b> beside the QR and scan the new code. If there is no QR or the connector says disabled, check the status message; a server operator may need to enable or restart the WhatsApp connector.</>,
      },
      {
        title: 'How do I link a direct-message chat?',
        answer: <>After pairing, open <b>How to chat</b> and copy its <code>link</code> command. Send that command in a WhatsApp direct message to the paired number. The link code expires, so use the current one shown in the panel.</>,
      },
      {
        title: 'How do I route a message to this work?',
        answer: <>Use the default slug or enter another in the WhatsApp row and click <b>Add</b>. Begin the WhatsApp message with <code>@slug</code> to send it to this {scopeNoun}. Review the route under <b>{scopeNoun === 'project' ? 'Routes for this project' : 'Workflow routes'}</b>.</>,
      },
      {
        title: 'How do I include progress updates?',
        answer: <>Open a WhatsApp route’s <b>Options</b> and turn on <b>Show workflow progress</b>. Replies, requests for input, and errors can still arrive when this option is off.</>,
      },
      {
        title: 'How do I stop routing or unpair a number?',
        answer: <>Remove a route with its trash button if only this {scopeNoun} should stop answering. To disconnect the account's WhatsApp, open <b>Linked number</b>, click <b>Unpair</b>, then <b>Confirm</b>.</>,
      },
    ]
    case 'Connect': return [
      {
        title: 'How do I choose a connection path?',
        answer: <>Choose <b>AI agent on this computer</b> for Claude Code, Codex, or another local MCP client. Choose <b>Hosted AI app</b> for ChatGPT or Claude Cowork.</>,
      },
      {
        title: 'How do I connect Codex or Claude Code on this computer?',
        answer: <>Select <b>AI agent on this computer</b>, choose <b>Codex</b> or <b>Claude Code</b>, and run the shown MCP command. Codex also shows a login command. Approve access in your browser. No AgentWorks binary is needed.</>,
      },
      {
        title: 'How do I connect ChatGPT?',
        answer: <>Select <b>Hosted AI app → ChatGPT</b>. Copy the <b>Remote MCP URL</b> into a custom MCP connector in ChatGPT, choose OAuth, then approve AgentWorks access in your browser. The AgentWorks server must have a public HTTPS address.</>,
      },
      {
        title: 'How do I connect Claude Cowork?',
        answer: <>Select <b>Hosted AI app → Claude Cowork</b>, click <b>Download Cowork plugin</b>, import it under <b>Customize → Plugins</b> in Cowork, and approve access in your browser. The tab also offers a manual connector URL.</>,
      },
      {
        title: 'Why does a hosted app reject the local URL?',
        answer: <>A hosted app cannot reach a <code>localhost</code> AgentWorks server. Open <b>Connect</b> on a public HTTPS AgentWorks server and use its <b>Remote MCP URL</b>.</>,
      },
      {
        title: 'How do I remove an outside app’s access?',
        answer: <>Under <b>Connected apps</b>, find the client and click <b>Revoke</b>. You can sign in again later if needed.</>,
      },
      {
        title: 'How is Connect different from MCPs?',
        answer: <>Connect lets an outside AI agent use AgentWorks tools. The <b>MCPs</b> tab gives AgentWorks tools from outside apps and services.</>,
      },
    ]
  }
}

export function IntegrationHowToGuide({ topic, scopeNoun }: {
  topic: IntegrationHowToTopic
  scopeNoun: 'project' | 'workflow'
}) {
  if (topic === 'Gmail') return <GmailHowToGuide scopeNoun={scopeNoun} />
  return <HowToAnswers
    topic={topic}
    description="Open a question for the steps you need. The related controls are in this tab."
    questions={questionsFor(topic, scopeNoun)}
  />
}
