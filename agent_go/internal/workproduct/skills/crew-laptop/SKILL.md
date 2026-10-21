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
- Commands are a separate choice: the "Run terminal commands" switch (`manage_laptop_access` set with `commands: true`) plus a Read & write folder. Offer it only when the work needs to run programs, builds or git on the laptop; files alone need no commands.
- `set` saves the whole list: include every folder to keep, or the omitted ones lose access.
- Never offer folders that hold sign-ins: `.aws`, `.ssh`, `.config`, keychains, browser profiles, files with keys.

## When it is refused

- "Operation not permitted" on a folder outside the shared one comes from the computer's own privacy rules, not the Crew. Say so once. The person can add the folder in the Crew's laptop settings; if it still fails, the AgentWorks app or terminal needs access in the computer's privacy settings. Do not retry in a loop or look for a way around it.
- For cloud sign-ins (AWS and similar): prefer doing the check from the server. If it has to be the laptop, the person signs in themselves, for example `aws sso login`. You never read their key or config files.
- A command that timed out or lost the connection may have run. Look at the result before trying again.

## Care

- Server secrets are never sent to the laptop. What you read there reaches the server and the chat history, so say so before opening anything personal.
- Change the saved folders only when the person asks.
