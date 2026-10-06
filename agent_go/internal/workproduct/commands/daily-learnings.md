Do this Crew's end-of-day learning review: read today's conversations, then save what is new and worth keeping into memory and skills.

{{context}}

## 1. Read today's conversations

- Today's date is in the context above; if not, use the system date.
- Read every conversation this Crew had today, not just the current chat:
  - `builder/conversation/<YYYY-MM-DD>/` — chats with this Crew's owner;
  - `builder/crew-chats/users/*/` — other users' chats with this Crew (use each file's modification time or message timestamps to find today's).
- Files are JSON: `conversation_history[].Role` and `.Parts[].Text`. They can be large; skim, search (e.g. `jq`, `grep`) and focus on user requests, corrections, decisions, outcomes and repeated procedures. Skip tool output noise.
- If a date other than today is given in the context (e.g. "yesterday" or a specific date), review that day instead.

## 2. Decide what is a learning

Keep only things that are new today and would change how this Crew works next time:
- facts, preferences, decisions, constraints and corrections — test each with "Crew should remember that…";
- procedures that worked (or failed and were fixed) and will recur — test each with "When asked to do X, Crew should…";
- mistakes the user had to correct, with the correct approach.

Leave out transient status, one-off details, raw chat, guesses, unverified research and credentials. If nothing new qualifies, say so and change nothing.

## 3. Update memory

- Read the project-root MEMORY.md first (create it if missing).
- Follow the project memory contract: concise reverse-chronological entries, one topic per dated heading, merge into an existing topic instead of duplicating, and replace or remove entries that today's evidence contradicts.

## 4. Update skills

Invoking this command is my explicit ask to preserve reusable procedures:
- Call `list_skills` first and read the names and descriptions of existing project skills.
- Create or update focused project-local skills at `skills/<skill-name>/SKILL.md` (with `name` and `description` frontmatter) — keep skills inside the current workspace. Update an existing skill only for the same topic and trigger; otherwise create a new, clearly named one; split catch-all skills.
- Select each created or updated skill with `update_project_skill_selection`.

## 5. Report

Give a short end-of-day summary:
- conversations reviewed (count and whose);
- what was added or changed in MEMORY.md and in which skills;
- what you deliberately left out, so I can correct you.
