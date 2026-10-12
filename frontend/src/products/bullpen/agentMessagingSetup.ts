export type MessagingClient = 'codex' | 'claude' | 'muse' | 'pi' | 'cursor' | 'agy'
type SetupCommand = { label: string; text: string }
export type MessagingGuide = {
  id: MessagingClient; name: string; status: string; available: boolean
  desktop?: Partial<MessagingGuide>
  requirements: string[]; versions: string; install: SetupCommand[]; update: SetupCommand[]
  installNote: string; approval: string; attach: string; ownRequest: string; delivery: string
  menus: { entry: string; who: string; description: string }[]
  troubleshooting: { title: string; text: string }[]
}
export const messagingGuides: MessagingGuide[] = [
  {
    id: 'codex', name: 'Codex', status: 'Live messaging · desktop idle wake', available: true,
    requirements: ['A compatible Codex desktop app or a Codex CLI with plugin hooks, and the AgentWorks CLI on PATH.', 'Desktop native delivery can wake an idle chat. A normal CLI session uses turn-boundary hooks unless its exact shared-daemon queue route is verified.'],
    versions: 'Current plugin: agentworks-codex 1.0.7. Use the updated AgentWorks CLI with own-session support, available from 12 Oct 2026. A compatible desktop version is required; no universal minimum is pinned.',
    install: [{ label: 'Install Codex plugin', text: 'codex plugin marketplace add manishiitg/agentworks-plugins\ncodex plugin add agentworks-codex@agentworks' }],
    update: [{ label: 'Update Codex plugin', text: 'codex plugin marketplace upgrade agentworks\ncodex plugin add agentworks-codex@agentworks' }],
    installNote: 'The marketplace supplies updates; the installed plugin is a separate entry. Enable AgentWorks and reload Codex after installation or update.',
    approval: 'Review the exact plugin hooks yourself. In the Codex CLI, use /hooks. Installing the plugin never approves its hooks.',
    attach: '/agentworks attach codex-review', ownRequest: 'Attach this chat to AgentWorks as codex-review.',
    delivery: 'Desktop native delivery reaches the attached chat, including while it is idle. Hooks deliver at turn boundaries. A notification or a queue write alone is not a processed receipt.',
    menus: [
      { entry: '/agentworks attach, detach, agents, share GROUP, unshare, groups, group NAME', who: 'You', description: 'Type the command in this chat; selecting a skill alone does not attach it.' },
      { entry: '/ menu → Agentworks Codex: Agentworks', who: 'You', description: 'Person-command skill shown in the slash picker. Selecting it explains commands; type /agentworks attach to connect.' },
      { entry: '/ menu → Agentworks Codex: Agent Messages', who: 'The agent', description: 'Messaging skill shown in the slash picker; you do not run it to attach. The hooks accept /agentworks, not an invented namespaced attach spelling.' },
    ],
    troubleshooting: [
      { title: 'Only two skills are visible', text: 'Two skills are expected, together with the messaging MCP and four hooks. Check plugin loading and hook trust; a skills-only installation is incomplete.' },
      { title: 'Hook blocked this message', text: 'Codex can show this badge when AgentWorks consumes a control command. Read the result: an attached identity or agent list is success; an error needs attention.' },
      { title: 'Attach reaches the model', text: 'Reload the enabled plugin and review its hooks. With the current CLI, you may instead ask the agent to attach its own detected chat.' },
    ],
  },
  {
    id: 'claude', name: 'Claude Code', status: 'Live messaging · idle wake', available: true,
    requirements: ['Claude Code with native session messaging enabled, the AgentWorks CLI on PATH, and Python 3 for plugin hooks.', 'Without native session messaging, the plugin hooks deliver at turn boundaries. The optional development MCP channel has separate provider approval requirements.'],
    versions: 'Current plugin: agentworks 1.0.5. The optional channel was tested with Claude Code 2.1.296. Use the updated AgentWorks CLI with own-session support, available from 12 Oct 2026.',
    install: [{ label: 'Install Claude plugin', text: 'claude plugin marketplace add manishiitg/agentworks-plugins --scope project\nclaude plugin install agentworks@agentworks --scope project' }],
    update: [{ label: 'Update Claude plugin', text: 'claude plugin marketplace update agentworks\nclaude plugin update agentworks@agentworks' }],
    installNote: 'Run project-scoped installation in the project your Claude chat uses. Update the existing installation rather than adding a second scope, then restart Claude as prompted.',
    approval: 'Review plugin and workspace trust prompts yourself. Enable native session messaging for idle wake; channel approval and session messaging are different routes.',
    attach: '/agentworks attach claude-review', ownRequest: 'Attach this chat to AgentWorks as claude-review.',
    delivery: 'Native session messaging wakes an idle chat and offers messages to a running session. Hooks deliver at turn boundaries when that route is unavailable. Claude may add its own permission notice to each message.',
    menus: [
      { entry: '/agentworks:agentworks attach, detach, agents, share GROUP, unshare, groups, group NAME', who: 'You', description: 'Namespaced person command. The short /agentworks spelling is also accepted by the hooks.' },
      { entry: 'Agent Messages', who: 'The agent', description: 'Messaging skill and restricted tools; you do not run it to attach.' },
    ],
    troubleshooting: [
      { title: 'Messages wait until my next turn', text: 'Check native session messaging and the agent’s route. Hooks alone do not wake an idle chat.' },
      { title: 'A repeated permission notice', text: 'Claude adds its own notice to native peer messages. AgentWorks guidance is bounded separately; updating AgentWorks cannot suppress the provider notice.' },
    ],
  },
  {
    id: 'muse', name: 'Muse', status: 'Live messaging · Monitor idle wake', available: true,
    requirements: ['Muse 1.4.6 or a compatible release, Python 3, and a current AgentWorks CLI on PATH.', 'The CLI must support Muse hooks, agentworks plugin mcp muse and agentworks watch. Approve the plugin MCP and the native Monitor tool when Muse asks.'],
    versions: 'Tested idle Monitor: Muse 1.4.6-R6000.1 and plugin 0.1.6; plugin/hook setup was also tested on 1.4.4-R5419.1. The Monitor requires the current AgentWorks CLI with Muse watch support, available from 12 Oct 2026. Older plugins use turn-boundary hooks only.',
    install: [
      { label: 'Add Muse marketplace', text: 'muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks-plugins.git' },
      { label: 'Install Muse plugin', text: 'muse plugins install agentworks-muse@agentworks' },
      { label: 'Inspect Muse plugin', text: 'muse plugins inspect agentworks-muse --json' },
      { label: 'Approve Muse plugin', text: 'muse plugins approve agentworks-muse --json' },
    ],
    update: [{ label: 'Update Muse plugin', text: 'muse plugins marketplace update agentworks\nmuse plugins remove agentworks-muse\nmuse plugins install agentworks-muse@agentworks' }],
    installNote: 'Muse can retain an older marketplace snapshot. Refresh, remove without --delete-data, reinstall, then inspect and approve the updated plugin. Removing this way preserves plugin data.',
    approval: 'Inspect and approve the updated plugin yourself. Review its hooks and messaging MCP. Restart Muse, review workspace trust, and review any native tool approval for the Monitor. Installation alone does not attach a chat.',
    attach: '/agentworks-muse:agentworks attach muse-review', ownRequest: 'Attach this chat to AgentWorks as muse-review.',
    delivery: 'The native Monitor wakes this attached chat without polling. Wait until Muse reports that its Monitor is listening, then leave the chat idle. Hooks deliver at turn boundaries if the Monitor is unavailable. A wake hint is not a receipt.',
    menus: [
      { entry: '/agentworks-muse:agentworks', who: 'You', description: 'Person command: attach [NAME], detach, agents, share GROUP, unshare, groups, group NAME. This is the entry to use.' },
      { entry: '/agentworks-muse:messaging', who: 'The agent', description: 'Skill for finding peers, reading, sending, replying and acknowledging messages.' },
      { entry: '/agentworks-muse:monitor', who: 'The agent', description: 'Skill for starting idle wake. You do not run it manually.' },
      { entry: '/agentworks (if a user skill is installed)', who: 'Other tools', description: 'The general AgentWorks tools guide can have this name. It is not the namespaced attach command.' },
    ],
    troubleshooting: [
      { title: 'Required MCP server agentworks failed during startup', text: 'Disable with muse plugins disable agentworks-muse. Update the AgentWorks CLI, then inspect and approve the plugin again before restarting Muse.' },
      { title: 'Attach did not connect', text: 'Use /agentworks-muse:agentworks attach. The short /agentworks entry may open a general tools skill instead.' },
      { title: 'No idle wake or sandbox socket refusal', text: 'Update the CLI and plugin, and review the approved messaging MCP and Monitor. Use the plugin’s approved MCP connection; do not disable the sandbox to make direct socket access work. Submit a turn for hook delivery if the Monitor is unavailable.' },
    ],
  },
  {
    id: 'pi', name: 'Pi', status: 'Live messaging', available: true,
    requirements: ['Pi 1.1.0 interactive TUI and the AgentWorks Pi extension. Use the native AgentWorks binary on PATH.', 'The loaded extension must publish its exact session. RPC/print mode is not the interactive attachment path; unsupported Windows IPC fails closed.'],
    versions: 'Tested: Pi 1.1.0 with extension 0.1.1. Current package: @agentworks/pi 0.1.2. Asking the agent to attach itself needs the current CLI and skill, available from 12 Oct 2026; older installations use the typed command.',
    install: [], update: [{ label: 'Check installed Pi packages', text: 'pi list' }],
    installNote: 'After signing in, run agentworks attach in a terminal and accept the Pi extension install if it is missing. It uses the CLI’s bundled package or a clone pinned to the CLI build, then asks you to reload Pi and attach again. @agentworks/pi is not advertised as a published npm package.',
    approval: 'For an older package, inspect its source with pi list, refresh that same retained package directory, then /reload. If moving directories, remove and install the package at the original scope. Installing the repository root is not supported; retain a referenced local package directory.',
    attach: '/agentworks attach pi-review', ownRequest: 'Attach this chat to AgentWorks as pi-review.',
    delivery: 'The native extension offers messages to this session; busy tools finish before queued follow-ups. The agent explicitly acknowledges admitted and processed. Native display alone is not admission.',
    menus: [
      { entry: '/agentworks attach, detach, agents, share GROUP, unshare, groups, group NAME, help', who: 'You', description: 'Literal interactive input handled by the extension. These deliberately have no registered autocomplete/slash-menu entry.' },
      { entry: '/reload', who: 'You', description: 'Pi’s built-in command reloads extensions.' },
      { entry: 'Six agent_message_* tools', who: 'The agent', description: 'Peers, send, read, reply, ack and purpose. There is no attach/detach model tool.' },
    ],
    troubleshooting: [
      { title: 'No AgentWorks slash-menu item', text: 'This is expected: type the literal /agentworks attach NAME in the interactive TUI. Do not use RPC, print or extension-generated input as person approval.' },
      { title: 'No exact Pi TUI SDK session', text: 'Install the extension from the CLI’s bundled or pinned source, reload Pi, send one message in the chat, then attach again.' },
      { title: 'Older package or CLI', text: 'Update the CLI and retained extension source. A terminal attach installs a missing package; it is not an automatic updater for an older package.' },
    ],
  },
  {
    id: 'cursor', name: 'Cursor', status: 'CLI preview · desktop later', available: true,
    requirements: ['Cursor CLI on macOS or Linux, Python 3, and an AgentWorks CLI build with Cursor messaging support.', 'Start in the exact project whose hooks you reviewed. This setup is for cursor-agent; choose Cursor desktop below for its current limits.'],
    versions: 'Package: agentworks 0.2.0. Investigated CLI: 2026.10.01-e373342; desktop: 3.21.18. Kernel/session checks passed on macOS. Real Cursor acceptance remains pending; this preview delivers at completed-turn boundaries and has no idle input route.',
    install: [
      { label: 'Get Cursor plugin', text: 'git clone --depth 1 https://github.com/manishiitg/agentworks-plugins.git "$HOME/.local/share/agentworks-plugins"' },
      { label: 'Configure Cursor project hooks', text: 'python3 "$HOME/.local/share/agentworks-plugins/plugins/cursor/scripts/setup_project.py" --project "/absolute/project/path"' },
      { label: 'Start Cursor CLI', text: 'cursor-agent --plugin-dir "$HOME/.local/share/agentworks-plugins/plugins/cursor" --workspace "/absolute/project/path"' },
    ],
    update: [{ label: 'Update Cursor plugin', text: 'git -C "$HOME/.local/share/agentworks-plugins" pull --ff-only\npython3 "$HOME/.local/share/agentworks-plugins/plugins/cursor/scripts/setup_project.py" --project "/absolute/project/path"' }],
    installNote: 'Use the same retained plugin directory when updating. Project setup preserves other hooks and refuses a second AgentWorks hook path. The package registers project hooks once; its plugin hook set is empty so the CLI cannot deliver twice.',
    approval: 'Review the project hook file and messaging MCP yourself in Cursor. Setup does not approve workspace/MCP trust, sign in, attach a chat, or enable terminal delivery. Remove an older AgentWorks hook entry explicitly before switching directories.',
    attach: '/agentworks attach cursor-review', ownRequest: 'Attach this Cursor CLI chat to AgentWorks as cursor-review.',
    delivery: 'Completed-turn hooks offer messages after busy tools finish. Idle persist/tmux delivery is not built in this milestone; an idle CLI receives pending messages on its next completed turn. A stored message or terminal write is not admission.',
    menus: [
      { entry: '/agentworks attach, detach, agents, share GROUP, unshare, groups, group NAME', who: 'You', description: 'Type in this Cursor CLI conversation after reviewing the project hooks.' },
      { entry: 'AgentWorks messaging MCP tools', who: 'The agent', description: 'Restricted to this verified CLI session; a separate HTTP MCP connection supplies workflow and Crew tools.' },
    ],
    troubleshooting: [
      { title: 'The attach command reaches the model', text: 'Check the selected project and its .cursor/hooks.json. This CLI version may skip plugin-only prompt and stop hooks; rerun the reviewed project setup, then restart Cursor CLI.' },
      { title: 'Messaging MCP cannot start', text: 'Update AgentWorks from a server publishing the Cursor-capable CLI. A preview-era CLI does not expose plugin mcp cursor. Keep workspace and MCP approval as your own choices.' },
      { title: 'No idle receipt', text: 'This milestone has no idle input route. Complete a turn to receive pending messages through its hook. Explicit mailbox recovery is last; do not start an inbox polling loop.' },
    ],
    desktop: {
      status: 'Desktop · not supported yet', available: false,
      requirements: ['Cursor desktop 3.21.18 was inspected. This release does not claim end-to-end desktop messaging.', 'A shared desktop MCP process needs an exact per-call conversation fence and an idle input route before messaging helpers can be enabled.'],
      versions: 'Desktop remains a separate development stage; CLI results do not prove desktop delivery.',
      install: [], update: [],
      installNote: 'Use the supported Cursor CLI setup. Do not install or reconfigure a desktop messaging MCP expecting it to create a live attachment.',
      approval: 'Workflow/Crew HTTP MCP tools can be configured separately; their access does not attach this desktop chat.',
      attach: '', ownRequest: '',
      delivery: 'Native hooks can continue a completed turn. No supported exact-chat idle wake or desktop typing path is claimed.',
      menus: [{ entry: 'Cursor desktop messaging', who: 'Later stage', description: 'Per-call identity and native delivery still need proof.' }],
      troubleshooting: [{ title: 'HTTP MCP works but the chat is absent', text: 'Tool access is separate from Bullpen chat attachment. Use Cursor CLI for this setup; desktop support remains pending.' }],
    },
  },
  {
    id: 'agy', name: 'agy', status: 'Tools only · no live messaging setup', available: false,
    requirements: ['A compatible MCP client may use AgentWorks workflow and Crew tools. No supported AgentWorks live-messaging plugin or native attachment setup is published for agy.'],
    versions: 'No tested agy live-attachment version is claimed. Shared provider names in code do not establish a working integration.',
    install: [], update: [], installNote: 'Use the separate MCP tools setup if your client supports it. There is no AgentWorks messaging plugin install/update command to run for agy today.', approval: 'Review your client’s MCP connection permissions. Tool access and a registered mailbox do not attach a running chat or provide idle wake.',
    attach: '', ownRequest: '', delivery: 'A registered mailbox needs an explicit read. No native live route or idle-wake claim is made for agy.',
    menus: [{ entry: 'AgentWorks MCP tools', who: 'The agent', description: 'Available only after a separate approved tools connection. No supported attach slash command is advertised.' }],
    troubleshooting: [{ title: 'A mailbox name is offline', text: 'Registration alone does not create a live recipient. Do not assume a provider enum or a copied Claude plugin makes agy reachable.' }],
  },
]
