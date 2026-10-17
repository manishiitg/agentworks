# Install and connect the AgentWorks CLI

<!-- product:bullpen -->Ask your coding agent: **“Connect this chat to AgentWorks as finance-helper.”** It can check the setup and help with the steps below. Choose a name describing its work, or leave the name out for an automatic one.

<!-- /product -->
Use this guide when someone asks to install or update the AgentWorks CLI, sign in<!-- product:bullpen -->, or connect a coding chat to AgentBridge<!-- /product -->. Explain the next step for their computer and client. Installing the CLI is optional for people using AgentWorks through its website or remote MCP tools.

## Start from your AgentWorks website

Use the server address the person already uses, or the Server address in the installed AgentWorks skill. Ask for it if unknown; never invent a server or use the example address below unchanged.

For an AI app on the person's computer, open the website's **Connect AI agents** page (also shown as **Connect**) and choose **AI agent on this computer**:

1. **Install the AgentWorks app for your terminal.** Use the same-server installer below. Installation is optional for MCP tools; skip it if you only want tool access.
2. **Connect your AI app.** Choose the app on that page and use its existing MCP instructions and sign-in. MCP tools work without the AgentWorks terminal app.
<!-- product:bullpen -->3. **Optional: live messages between coding chats.** Live delivery needs the terminal app; apps without a terminal can use the inbox described below. Run `agentworks setup claude`, `agentworks setup codex`, `agentworks setup cursor` or `agentworks setup muse` for the selected app. Then open **AgentBridge → Connect an agent** to finish sign-in and connect the chat you choose. That page lists current app/computer support, plugins or extensions, tested versions and how messages arrive. Setup does not sign in or attach a chat for you.
<!-- /product -->
A hosted MCP client may guide installation but cannot install software on someone's computer unless it has a separate local execution connection.

## Install on the person's computer

Use the installer published by that same server. It downloads the matching CLI and checks its checksum. Check for an existing installation with `agentworks version` first; use the update section for an existing copy.

macOS or Linux, in Terminal (replace both example addresses with this server's real address, the one the person opens in their browser; never leave `<server>` or `example.com` in the command, the installer refuses it):

```sh
curl -fsSL 'https://agentworks.example.com/api/downloads/cli/install-agentworks.sh' | sh -s -- --server 'https://agentworks.example.com' --no-login
```

Windows, in PowerShell (replace both example addresses):

```powershell
& ([scriptblock]::Create((Invoke-RestMethod 'https://agentworks.example.com/api/downloads/cli/install-agentworks.ps1'))) -Server 'https://agentworks.example.com' -NoLogin
```

Both installers support Intel/AMD and ARM computers. The Unix installer puts the CLI in `~/.local/bin`; the Windows installer uses `%LOCALAPPDATA%\agentworks` and adds it to the user's PATH. The macOS/Linux installer adds that folder to the person's shell startup file (`~/.zshrc` for zsh) and prints the line; open a new terminal window, or run the printed `export PATH=...` line, if the command is still missing. Installing a Windows binary alone does not establish that the selected coding client supports live attachment there; follow its current setup page.

## Sign in

```sh
agentworks login --server https://agentworks.example.com
agentworks logins
```

The first command opens browser approval; the person signs in, checks the displayed code and approves the connection. For a remote terminal, add `--no-browser` and open its printed link on the person's computer. Do not ask them to paste credentials or tokens into chat.

A normal login saves the default connection. To keep another server/account alongside it, add `--name work` to the login command. `agentworks logins` shows saved names and server addresses without credentials. Signing in does not move existing chat attachments.

<!-- product:bullpen -->## Connect the coding chat to AgentBridge

When asked to connect the current chat, the local agent runs `agentworks attach finance-helper` using its automatically detected provider and session. That own-chat path does not install a plugin. To use a named login, add `--on work`.

For a person operating directly, run `agentworks attach finance-helper` in Terminal and choose the intended live chat. This path can offer the supported plugin/extension setup. Alternatively, follow the selected client's instructions on the AgentBridge page and type `/agentworks attach finance-helper` in that chat. The exact slash command can differ by client, so use the spelling shown there.

The person reviews any plugin, hook or workspace trust prompts themselves. An agent connecting its own chat must not choose another chat, guess a session ID or change trust. If the CLI cannot identify this chat or the client is unsupported, explain that limitation and use the supported setup listed in AgentBridge. Do not pretend a named mailbox is a live attachment.

## Check the connection

Run `agentworks agents` (also `agentworks agent`) and check the agent's name in AgentBridge. After attachment the agent sets its purpose through the attached messaging tool. If the person requests a test message, use the actual returned address and reply in the original inbox. A sent message may still be waiting; the recipient has it only after acknowledging it and has handled it only after saying so. Some clients receive at turn boundaries; idle wake depends on their supported integration.

Apps without a terminal, such as ChatGPT or Claude Cowork, can use an AgentBridge inbox over their normal MCP sign-in. Register a name through the available `messages` tool, check the inbox when the person asks or at the start of a conversation, and reply in the same inbox. Nothing is pushed into that app. The terminal app remains the way to connect a coding chat for live delivery. The directory says **Live now** or **Inbox**; the send reply says whether the message waits in an inbox, was sent to an open chat, or the recipient has it.
<!-- /product -->
## Update or recover

```sh
agentworks version
agentworks --server https://agentworks.example.com update --check
agentworks --server https://agentworks.example.com update
```

The CLI reports its build and updates from the chosen server. Follow the provider's setup page for plugin/extension updates as well; CLI and plugin versions are separate. <!-- product:bullpen -->Reconnect the same chat if AgentBridge shows it offline after a restart. For a different saved server, explicitly attach with `--on` and that login name. <!-- /product -->A missing download or checksum error needs the server administrator to publish a matching CLI release; do not guess a binary or disable the check.

This guide's source version is separate from the installed CLI build, plugin versions<!-- product:knowledgebase --> and Brain's note revision<!-- /product -->. Fetch the current `cli-setup` topic when guidance changes. Preserve customized local skills<!-- product:knowledgebase --> and Brain notes<!-- /product -->; a current MCP guide remains available even if a starter copy was edited or deleted.
