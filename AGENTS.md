# Rules for agents working on AgentWorks

Read this before designing or changing anything in this repository. These are
the product and engineering rules every contributor's agent follows. The
repository is public: nothing here, or in anything you commit, may name a
company, customer, server, person or internal endpoint.

## How to work

1. Work in your own git worktree, one per task, started from `origin/main`.
   Never edit, build or test in someone's main checkout.
2. Every change belongs to a GitHub issue on `manishiitg/agentworks`. Open one
   if none fits, comment progress on it, and link the pull request with
   `Fixes #N`.
3. Before pushing, fetch and rebase onto `origin/main`. Commit only your own
   task's files. Open a pull request into `main` (`gh pr create --base main`)
   and merge it right away (`gh pr merge --rebase`). Review happens after the
   merge: read the comments on your pull requests before your next task and
   fix real findings forward in a new pull request.
4. Run the checks in the README ("Checks before a pull request") for what you
   changed. Do not deploy to servers; maintainers do that.
5. Before changing behaviour, read `git log` / `git blame` for the lines you
   touch and the issues and pull requests they mention, so you know why the
   code is the way it is. Do not undo an earlier decision without asking.
6. Public-repo hygiene: use `example.com` and roles (owner, teammate) in tests,
   docs and comments. Keep commit messages generic. Never commit secret
   values, server addresses or per-server configuration.

## Simpler beats complex

Prefer the smallest model that does the job: fewer modes, settings, states and
concepts people must learn. Before adding a new concept (a group, a level, a
mode, a setting), check whether an existing one already covers it; if two do
the same job, keep one. Remove unused or rarely used options rather than
hiding them.

## Current phase: move fast, breaking is fine

Assume nobody depends on the platform yet; we are getting the design patterns
right.

1. Do not spend time on backward compatibility, legacy migrations, keeping old
   saved data working, in-flight safety (leases, busy checks, atomic
   cutovers), rollback machinery or scale hardening. A plain backup before a
   destructive data change is enough.
2. Do spend time on the design: stable identities, the backend decides, one
   concept per job, plain wording, public-repo hygiene, and real security
   (other people's private chats, secret values).
3. No backward-compatibility code. When a design changes, migrate the data and
   delete the old paths, aliases, legacy names and fallbacks; keep only a
   one-off migration tool until every server has moved.
4. Ship and fix forward. If your change breaks the build on `main`, fix it at
   once: a broken `main` stops everyone else.

## Product focus: a whole team on one project

We build for everyone on a team: developers, product managers, QA and business
people. The main focus is bringing a team onto one project, making it easy for
people and their agents to talk and hand over work, and running workflows
together as a team.

1. Rank features by how much they help a team work together on one project.
   Prefer features that let one role hand work to another.
2. Every role matters equally. Developers get developer-grade tools
   (worktrees, PRs, the editor, terminals, code-first workflows), not hidden or
   watered down. Everyone else reaches the same work through chat, the website
   and plain-language screens.
3. Write screens, prompts, skills and tool replies in plain words, with
   technical detail where the person or the context is technical. No terminal
   or CLI as the only way to do something; a refusal always says what the
   person can do instead. No internal IDs or jargon unless asked.

## The backend decides

The server owns behaviour (queueing, permissions, turn state, delivery). The
browser shows what the server says and sends what the person typed. Do not add
browser-side queues, holds or policy copies; remove existing ones when you
touch that code.

## Real work and speed over extra safety layers

Make the product easy to use first. When ease of use and an extra protection
conflict, choose ease of use and keep only the real protections in point 5.

1. Runs are real. Running a step or a workflow, from the Builder, MCP or a
   schedule, does real work with real effects. No simulation, stub or dry-run
   mode unless the person asks for one.
2. Do not add safety layers on your own: no extra gates, fail-closed modes,
   confirmations, holds or approval steps beyond the existing access rules
   (sharing, Builder/Run, Vault, token scopes). A person asking for something
   is the approval.
3. A change to how runs, steps or permissions behave is the owner's decision.
   Bring it to them first; never build it as a side effect of other work.
4. Workflow step file access: a step may read everything in its workflow
   folder. It may write its current run folder and the shared areas
   (db/assets, reports, tool_output_folder), but not past runs, the plan and
   settings (planning/, workflow.json, config, step settings) or code/.
5. Real security still holds: secret values, people's private chats and
   accounts, and other companies' data stay protected.

## One company per server

We optimise for people and agents sharing data and working on the same things
fast, not for privacy between colleagues.

1. Inside a product someone can access, everything about it is visible to
   them: runs and results, step logs, function calls, token usage, dashboards
   and their data. Sharing a Crew, Goal or workflow includes reading its files
   over MCP (skills, memory, knowledge, instructions, runs and logs); secret
   values and private logs stay excluded.
2. Sharing is called "Share" (Only me / Chosen people / Everyone). Sharing a
   product means sharing its dashboard; every shared product has one (a
   default when none was built). Code and Relays have one owner. A Crew has
   one main owner; a Crew on the server also has co-owners who build it with
   them and change everything, and Users who work in it through chat with its
   setup but cannot change that setup (only the main owner adds or removes
   either); a Crew on a Mac has its Team instead.
3. Still private: secret values, personal provider accounts and logins, and a
   person's own chats and Code projects until they share them. Being in the
   company is not access.
4. Costs: record who spent what, on which product and activity. Do not build
   budgets, payer switching or other walls between colleagues.
5. Two modes only: Builder (the owner) and Run (everyone else). Never write
   "read-only", "viewer", "reader" or "guest" in anything people see.
6. SparkQuill and Video Studio are personal products, switched on per server.
   Never mention them in shared guides, onboarding, core prompts, tips or
   skills, and core work must not depend on them.

## Secrets

This is the whole secret model; do not build more on top unless asked.

1. Secret values are stored encrypted and reach work only as environment
   variables (`SECRET_<NAME>`) in the platform terminal and in scripts.
2. Agents are trusted not to print, paste, log, commit or send secret values
   (chat, files, code, issues, messages or URLs). Use `$SECRET_<NAME>` in code,
   never a literal value. Secret names are not sensitive.
3. Which secrets a workflow, Crew or Code project may use comes from the
   existing selection and Vault grants. No proxies, redaction pipelines or
   per-use approvals on top.
4. A person may give a secret value in chat; the agent stores it with the
   secret tools and from then on refers to it by name only. Tools never return
   values, and servers never log tool arguments that carry one.

## Build -> Share -> Use

Every product (Goals/workflows, Crew, Relays, Code) follows one model. Judge
every feature by whether it shortens the path from Build to Share to Use.

1. Build. Someone builds the thing through either of two equal surfaces: the
   website Builder, or MCP (Claude Code, Codex, Cursor and other agents using
   AgentWorks tools). Anything you can build in one must be possible in the
   other, with the same Builder/Run rules and server checks. A new feature
   ships with its MCP tool, not only a screen, and every side-panel or
   settings action also works from the product's own chat, reusing the same
   native tool and server checks.
2. Share. The built thing exposes entry points once: functions, routes,
   ask/chat and dashboards. Share channels (Slack, WhatsApp, shared
   dashboards, the app, the MCP network, webhooks) are adapters onto those
   same entry points, never separate product code. Every channel uses two
   entry points: `ask` (conversational, runs as the caller in Run mode) and
   functions/routes (typed, for machines). A channel only formats and
   delivers; it never decides what is allowed. Products talk to each other
   the same way: one product calls another's `ask` or functions as the person
   its run acts for, never by reading its files or internals.
3. Use. People use what was shared through those channels and through callers
   they own: their own agents over MCP, schedules and Watchers. A caller runs
   with its person's current access and selected secrets, can call only what
   is shared with them, and its cost is attributed to them. Access is
   rechecked at run time; using a caller never adds authority.

Older per-product schedules, webhooks and Slack wiring predate this model.
Converge them onto the shared entry points rather than adding new variants.

## Prompts and skills stay small, and reuse the cache

Keep system prompts small. Put detailed procedures and feature-specific
guidance in the relevant skills, and keep those skills current.

Everything that goes to a model must reuse the prompt cache and spend as few
tokens as possible:

1. Keep the system prompt and tool list the same from turn to turn. Text that
   changes every turn (times, counters, IDs, live status, lists in a random
   order) does not belong there; put it in the person's message or a tool
   result. Text from saved settings may be in it, since it changes only when
   the person changes them.
2. Put stable parts first and parts that change rarely after them, so a change
   invalidates as little of the cached prefix as possible.
3. Prefer a short pointer to a skill or a tool over pasting its content. Keep
   tool results short: page or filter large outputs instead of returning
   everything.
4. When you add to a prompt, say in the pull request what it costs per turn and
   when it changes.

## Tests

1. Keep unit tests minimal and focused. Add one only to protect a concrete
   access or secret rule, reproduce a real bug, or pin a meaningful parser or
   data edge. Say the rule or the real failure in the test name or a short
   comment.
2. Prefer the real path: a server request, chat, shell or deploy self-test,
   asserting what the person would see. Unit tests are quick checks of
   specific rules, not proof that a live flow works.
3. Extend an existing focused test before creating another: one test per
   distinct behaviour, table cases only for genuinely different outcomes.
4. Avoid tests that copy implementation details: exact prompt or UI wording,
   model names, tool or catalog counts, internal call sequences. Mock only
   necessary external boundaries.
5. When you touch a heavily tested area, remove tests that others or real-path
   checks already cover. Keep access/secret rules and real regressions. A
   failing test is not a reason to delete it: find its cause first.
6. Run the smallest relevant checks, name only the tests you changed, and
   report separately what tests passed and what was checked live. A passing
   build or mocked test is not proof that the feature works.
