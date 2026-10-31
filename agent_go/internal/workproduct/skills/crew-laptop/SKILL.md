---
name: crew-laptop
description: Read for laptop folders and commands, local Computer use (desktop apps, click/type/screenshot), or permission errors. Covers setup and the separate file, terminal and desktop capabilities.
---

# Folders and laptop connections

## Computer use: desktop apps on this computer

Local Crew owners have `computer_use`, a managed `agent-desktop` integration separate from files, terminal and the managed `agent_browser`. Hosted server agents, shared Run users and file-only device connections have no desktop authority. The person's phone/website messages can reach the local agent through the existing paired Crew chat relay; execution stays on their computer.

Enabling grants full control of the computer, including Terminal. Clipboard reads may contain passwords or other private content; read it only for the person’s task. Personal MCP connections manage setup only; app actions require the interactive Crew chat, so a saved personal MCP login cannot give a scheduled turn desktop control.

Start with `computer_use(action="status")`. When asked to set it up, use `install` (pinned runtime, no global npm change), `permissions` (the OS permission guide) and `enable`. Do not enable again after a person stops it unless they ask. The panel is Integrations → Computer use. On macOS, explain that the app named by the guide and its child processes may inherit the OS permissions; screenshots require Screen Recording too.

Before desktop work, call `guide`, then `guide` with `command="platform"` for the installed version's instructions. Use `guide` with a CLI command name for syntax. `run` takes one `command`, literal `args` and optional `headed`; do not launch the CLI from the sandbox or a script. Prefer semantic accessibility actions. Physical input (`headed=true`) needs the person's request. First inspect, act on that snapshot's qualified ref, then inspect again; use skeleton/compact snapshots and drill into regions to keep responses small. Check delivery/retry in results; never repeat uncertain clicks or typing blindly. A busy response means another desktop command is running; wait or let the person choose Stop. Desktop references and images are private to this chat. Screenshots return a private path for the native image reader; keep them out of shared Crew files, dashboards and uploads. UI content sent to the model goes to the selected provider. Stop turns Computer use off and cancels pending work; an already dispatched action may finish.

For website tasks, keep using the existing Browser panel and `agent_browser`; this integration operates native desktop apps.

## Start a local Crew in an existing folder

On a local AgentWorks installation, a new Crew or Studio can work directly in a folder already on that computer. In Create, use the optional **Folder** field or **Browse**. Leave it blank for a new AgentWorks folder. Existing files are used in place, without importing, copying or adding a linked folder.

Over MCP, discover `workspace action=create kind=crew` and pass the requested existing absolute folder in `spec.root_path`, with the name and the existing creation key. This is a creation choice; do not move an existing Crew or change its root. The server checks that local-folder creation is allowed. Crew resources live in `.agentworks` inside that folder; chats, credentials and generated runtime remain in private AgentWorks storage. Keep existing project instructions intact. Removing the Crew preserves the selected folder and the person's files.

The returned `files_path` is the logical file root; `workspace_path` stays the stable resource/settings namespace. Native commands and the project terminal use the selected folder. For shared-file MCP calls, keep the project's normal namespace and address user files with the returned relative `_files/...` path; never invent a different project root or use an absolute host path in a file API.

## Connect a laptop to a hosted Crew

For a hosted Crew, the agent runs on the server. Every current owner shares its selected laptop, folders and commands switch; each keeps their own chat tabs. People only shared with use the dashboard and do not get laptop tools. A connected laptop adds two separate things. Files: `list_local_files` and `copy_local_file_to_project` for a Read folder, and also `write_local_file` for a Read & write folder. Commands: `execute_local_shell_command` runs a command in a Read & write folder as the person, only when the person has turned on "Run terminal commands" for this Crew. Nothing syncs.

## Check first (read-only)

Everything the Local laptop panel does you can do here with `manage_laptop_access`; the person never has to open the panel.

1. `manage_laptop_access` with `devices` lists the person's computers plus the computer already selected for this Crew. `get` shows the computer, folders and whether commands are on for this Crew.
2. Tell the person plainly what you found: connected or not, which folder, Read or Read & write, and whether terminal commands are on.
3. To set up or change: `browse` to find the folder, then `set` with the complete choice (device, every folder to keep with Read or Read & write, and `commands`). It takes effect from the next message. `list_local_files` with no folder shows the folders you can use now.

## If no computer shows

`devices` then also returns the install and connect commands for this server (`setup`). Give the person the one for their system, in plain steps: run the install command in a terminal on that computer, run the connect command (`agentworks start --server <this server's address>`), sign in with the same account in the browser window it opens, and leave it running. Ask them to say when it is running, then check `devices` again. Never ask for passwords or keys.

## Choosing a folder

- One project folder, as small as works. Use `browse` to list folder names.
- Read lists and copies files in. Read & write also lets you write files there. Suggest Read first and ask when unsure.
- Commands are a separate choice: the "Run terminal commands" switch (`manage_laptop_access` set with `commands: true`) plus a Read & write folder to start in. Offer it only when the work needs to run programs, builds, git or a cloud CLI on the laptop; files alone need no commands.
- With the switch on, a command is the person's own terminal: their login shell, their environment, their files, keys and cloud logins, not limited to the shared folders. Say this plainly when you offer it; it is the person's call.
- A big folder such as Downloads is listed a page at a time: `list_local_files` returns `total` and, when more follow, `truncated` and `next_offset`. To find a file, pass `query` (part of its name) instead of reading every page. If the tool returns an error, tell the person what it says; an error is never an empty folder.
- `set` saves the whole list: include every folder to keep, or the omitted ones lose access.
- Do not offer folders that hold sign-ins (`.aws`, `.ssh`, `.config`, keychains, browser profiles) as shared file folders; commands can use the person's tools without that.

## When it is refused

- "Operation not permitted" on a folder outside the shared one comes from the computer's own privacy rules, not the Crew. Say so once. The person can add the folder in the Crew's laptop settings; if it still fails, the AgentWorks app or terminal needs access in the computer's privacy settings. Do not retry in a loop or look for a way around it.
- For cloud CLIs (AWS and similar) on the laptop: with the commands switch on, run them as the person would, for example `aws sts get-caller-identity`. If the person's login has expired, ask them to sign in themselves (`aws sso login`). Do not print, copy or send their key or config files.
- A command that timed out or lost the connection may have run. Look at the result before trying again.

## Care

- Server secrets are never sent to the laptop. What you read there reaches the server and the chat history, so say so before opening anything personal.
- Change the saved folders only when the person asks.

Connect the computer once in the project's Local laptop panel or in chat (manage_my_vaults operation=laptops lists your computers and setup steps), then choose it in this project. Goals use the same optional connection for Builder chats; their steps and schedules stay on the server. copy_local_file_to_project takes local_path and project_path.

## Recognize an existing local project

When the person asks what the project contains, use
`sessions action=project_context` for a names-only scan of instruction files
and skills. This is discovery, not proof that a model loaded every file.
Keep existing instructions and configuration in place.

On the local Mac app, `sessions action=native_list` finds the person's own
Claude/Codex conversations for this exact folder and shows the original account.
When the person asks to continue one, pass its returned opaque `native_id` to
`sessions action=native_resume`. It opens a separate private chat tab; the next
message resumes the native conversation with the Crew's current tools and
permissions. Never copy native history between accounts, select a different
folder's conversation or imply that opening a tab ran the agent. Other people's
or shared-account owners' private chats are not listed.
