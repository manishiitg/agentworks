---
name: crew-laptop
description: Read when the person mentions their laptop or computer, a folder on it, running a command on their machine, or an "Operation not permitted" error; it guides connecting and choosing folders.
---

# Laptop connection

The Crew runs on the server. A connected laptop adds two separate things. Files: `list_local_files` and `copy_local_file_to_crew` for a Read folder, and also `write_local_file` for a Read & write folder. Commands: `execute_local_shell_command` runs a command in a Read & write folder as the person, only when the person has turned on "Run terminal commands" for this Crew. Nothing syncs.

## Check first (read-only)

1. `manage_laptop_access` with `devices` lists the person's connected computers. `get` shows the folders saved for this Crew and whether commands are on.
2. Tell the person plainly what you found: connected or not, which folder, Read or Read & write, and whether terminal commands are on.

## If no computer shows

On the laptop, the person installs AgentWorks and signs in to this server, then leaves it running. With the command line: `agentworks start --server <this server's address>`. In the app, the Crew's Connections → Local laptop shows the download and sign-in steps. Ask them to say when it is running, then check `devices` again. Never ask for passwords or keys.

## Choosing a folder

- One project folder, as small as works. Use `browse` to list folder names.
- Read lists and copies files in. Read & write also lets you write files there. Suggest Read first and ask when unsure.
- Commands are a separate choice: the "Run terminal commands" switch (`manage_laptop_access` set with `commands: true`) plus a Read & write folder to start in. Offer it only when the work needs to run programs, builds, git or a cloud CLI on the laptop; files alone need no commands.
- With the switch on, a command is the person's own terminal: their login shell, their environment, their files, keys and cloud logins, not limited to the shared folders. Say this plainly when you offer it; it is the person's call.
- `set` saves the whole list: include every folder to keep, or the omitted ones lose access.
- Do not offer folders that hold sign-ins (`.aws`, `.ssh`, `.config`, keychains, browser profiles) as shared file folders; commands can use the person's tools without that.

## When it is refused

- "Operation not permitted" on a folder outside the shared one comes from the computer's own privacy rules, not the Crew. Say so once. The person can add the folder in the Crew's laptop settings; if it still fails, the AgentWorks app or terminal needs access in the computer's privacy settings. Do not retry in a loop or look for a way around it.
- For cloud CLIs (AWS and similar) on the laptop: with the commands switch on, run them as the person would, for example `aws sts get-caller-identity`. If the person's login has expired, ask them to sign in themselves (`aws sso login`). Do not print, copy or send their key or config files.
- A command that timed out or lost the connection may have run. Look at the result before trying again.

## Care

- Server secrets are never sent to the laptop. What you read there reaches the server and the chat history, so say so before opening anything personal.
- Change the saved folders only when the person asks.
