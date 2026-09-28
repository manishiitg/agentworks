Review this conversation and persist what is worth keeping as durable project knowledge — both memory and skills.

{{context}}

## Memory

- Read the project-root MEMORY.md first (create it if it does not exist).
- Extract stable, verified project truths from this conversation: facts, preferences, decisions, constraints, and corrections. Test each candidate with "this workspace should remember that…".
- Follow the project memory contract: concise reverse-chronological entries, one topic per dated heading, merge into an existing topic instead of appending a duplicate, and replace or remove entries that newer evidence contradicts.
- Do not save guesses, transient status, raw chat, credentials, or unverified research.

## Skills

Invoking this command is my explicit ask to preserve reusable procedures, so the normal "skills change only on explicit ask" bar is met for what follows:

- Call `list_skills` first and inspect the names and descriptions of every existing project skill before choosing a destination.
- For each genuinely reusable procedure in this conversation — test it with "When asked to do X, the agent should…" — create or update a focused project-local skill at `skills/<skill-name>/SKILL.md` with concise `name` and `description` frontmatter. Never write it into the account-wide `skills/custom/` library.
- Update an existing skill only when the new knowledge has the same topic and would be loaded for the same kind of future request; create a separate, clearly named skill when the topic, trigger, or outcome differs. If an existing skill has become a catch-all, split the relevant material into focused skills. Link memory and skill entries rather than duplicating instructions.
- Select each created or updated skill for this project with `update_project_skill_selection` so the runtime can load it.

## Report

Tell me briefly what you added or changed in MEMORY.md and in skills — and what you deliberately left out, so I can correct you.
