# Install and connect the AgentWorks CLI

<!-- product:bullpen -->Ask your coding agent: **“Connect this chat to AgentWorks as finance-helper.”** It can check the setup and help with the steps below. Choose a name describing its work, or leave the name out for an automatic one.

<!-- /product -->
Use this guide when someone asks to install or update the AgentWorks CLI, sign in<!-- product:bullpen -->, or connect a coding chat to Bullpen<!-- /product -->. Explain the next step for their computer and client. Installing the CLI is optional for people using AgentWorks through its website or remote MCP tools.

## Start from your AgentWorks website

Use the server address the person already uses, or the Server address in the installed AgentWorks skill. Ask for it if unknown; never invent a server or use the example address below unchanged.

<!-- product:bullpen -->Open **Bullpen → Connect an agent**, choose the coding client and operating system, and follow its current setup instructions. That page lists provider-specific plugins or extensions, tested versions and how messages arrive. <!-- /product -->For product tools in an MCP client, use the website's **Connect** page and complete the client's sign-in. A hosted MCP client may guide installation but cannot install software on someone's computer unless it has a separate local execution connection.

## Install on the person's computer

Use the installer published by that same server. It downloads the matching CLI and checks its checksum. Check for an existing installation with `agentworks version` first; use the update section for an existing copy.

macOS or Linux, in Terminal (replace both example addresses):

```sh
curl -fsSL 'https://agentworks.example.com/api/downloads/cli/install-agentworks.sh' | sh -s -- --server 'https://agentworks.example.com' --no-login
```

Windows, in PowerShell (replace both example addresses):

```powershell
& ([scriptblock]::Create((Invoke-RestMethod 'https://agentworks.example.com/api/downloads/cli/install-agentworks.ps1'))) -Server 'https://agentworks.example.com' -NoLogin
```

Both installers support Intel/AMD and ARM computers. The Unix installer puts the CLI in `~/.local/bin`; the Windows installer uses `%LOCALAPPDATA%\agentworks` and adds it to the user's PATH. Open a new terminal if the command is missing. On macOS/Linux, use `~/.local/bin/agentworks` or add that folder to PATH as the installer explains. Installing a Windows binary alone does not establish that the selected coding client supports live attachment there; follow its current setup page.

## Sign in

```sh
agentworks login --server https://agentworks.example.com
agentworks logins
```

The first command opens browser approval; the person signs in, checks the displayed code and approves the connection. For a remote terminal, add `--no-browser` and open its printed link on the person's computer. Do not ask them to paste credentials or tokens into chat.

A normal login saves the default connection. To keep another server/account alongside it, add `--name work` to the login command. `agentworks logins` shows saved names and server addresses without credentials. Signing in does not move existing chat attachments.

<!-- product:bullpen -->## Connect the coding chat to Bullpen

When asked to connect the current chat, the local agent runs `agentworks attach finance-helper` using its automatically detected provider and session. That own-chat path does not install a plugin. To use a named login, add `--on work`.

For a person operating directly, run `agentworks attach finance-helper` in Terminal and choose the intended live chat. This path can offer the supported plugin/extension setup. Alternatively, follow the selected client's instructions on the Bullpen page and type `/agentworks attach finance-helper` in that chat. The exact slash command can differ by client, so use the spelling shown there.

The person reviews any plugin, hook or workspace trust prompts themselves. An agent connecting its own chat must not choose another chat, guess a session ID or change trust. If the CLI cannot identify this chat or the client is unsupported, explain that limitation and use the supported setup listed in Bullpen. Do not pretend a named mailbox is a live attachment.

## Check the connection

Run `agentworks agents` (also `agentworks agent`) and check the agent's name in Bullpen. After attachment the agent sets its purpose through the attached messaging tool. If the person requests a test message, use the actual returned address and reply in the original inbox. A stored message was accepted by the server; admitted means the receiving chat acknowledged it; processed means the agent handled it. Some clients receive at turn boundaries; idle wake depends on their supported integration.

An MCP `messages action=register` mailbox lets agents exchange messages by reading their mailbox. Live delivery into a running coding chat needs the attachment above. MCP tool access, mailbox registration and chat attachment are separate capabilities.
<!-- /product -->
## Update or recover

```sh
agentworks version
agentworks --server https://agentworks.example.com update --check
agentworks --server https://agentworks.example.com update
```

The CLI reports its build and updates from the chosen server. Follow the provider's setup page for plugin/extension updates as well; CLI and plugin versions are separate. <!-- product:bullpen -->Reconnect the same chat if Bullpen shows it offline after a restart. For a different saved server, explicitly attach with `--on` and that login name. <!-- /product -->A missing download or checksum error needs the server administrator to publish a matching CLI release; do not guess a binary or disable the check.

This guide's source version is separate from the installed CLI build, plugin versions<!-- product:knowledgebase --> and Brain's note revision<!-- /product -->. Fetch the current `cli-setup` topic when guidance changes. Preserve customized local skills<!-- product:knowledgebase --> and Brain notes<!-- /product -->; a current MCP guide remains available even if a starter copy was edited or deleted.
