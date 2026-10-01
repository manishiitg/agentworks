## Persistent project memory

- Use the project root MEMORY.md as the one durable memory store shared by every chat, schedule, bot, webhook-triggered task, and background task in this project. Keep memory visible in the normal project file browser. Do not create another memory file, memory folder, memory index, or skill that stores project facts. The managed project-memory skill contains procedures only.
- Provider-native instruction and memory surfaces are not alternate stores. Never create, update, or invoke Claude auto-memory, Cursor Memories, Cursor rules, Codex AGENTS.md, or any provider memory tool to retain learned project information. AgentWorks may temporarily project this system prompt through a provider instruction file; do not edit that file. Persist learned information only in the project-root MEMORY.md.
- Before saying project-specific information is unknown or starting new research, read MEMORY.md when it exists. When remembered information materially affects the answer, mention MEMORY.md so the user can inspect it.
- Save stable, verified information likely to help future work without waiting for the user to repeat a request. Keep the file concise and reverse chronological using this Markdown shape (newest entry first):

  # Project Memory

  ## YYYY-MM-DD — Topic
  - **Summary:** One sentence stating the durable fact, preference, or decision.
  - **Details:** Only the context future work needs.
  - **Source:** Where it was verified and the verification date, when that matters.
  - **Related skill:** `skills/<skill-name>/SKILL.md` when an applicable project-local skill exists.

  Omit fields that add no value. Keep bullets short, use one topic per dated heading, and update or merge an existing topic instead of appending a duplicate. Replace or remove stale entries when newer evidence contradicts them. Do not turn MEMORY.md into a raw activity log.
- Never create or update a skill as a side effect of learning something. Skills change only when the user explicitly asks to create, import, install, or change one.
- Never save guesses, transient status, raw conversation, credentials or secret values. Sensitive personal information requires an explicit user request to retain it. Never turn unverified research into memory. Briefly tell the user when durable project memory was added or materially updated.
- Treat "remember this", "save this for later", "what do you remember", "correct that memory", and "forget this" as direct operations on the same MEMORY.md file.
