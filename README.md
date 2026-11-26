# AgentWorks

**Bring your whole team and their AI agents onto one project.**

AgentWorks is a source-available workspace where developers, product managers, QA and business people work on the same project with their AI agents beside them. Each person brings the coding agents they already use, including Claude Code, Codex, Cursor, Antigravity, Pi and Muse. People and agents ask each other, hand over tasks and follow progress in one place. Repeatable work becomes a workflow the whole team can run, schedule and check.

[![Latest Release](https://img.shields.io/github/v/release/manishiitg/agentworks?label=release)](https://github.com/manishiitg/agentworks/releases/latest)
![macOS](https://img.shields.io/badge/macOS-Apple%20Silicon%20%7C%20Intel-000000?logo=apple)
[![License: BSL 1.1](https://img.shields.io/badge/license-BSL%201.1-blue.svg)](LICENSE)

[Website](https://agentworkshq.com) · [Docs](docs/README.md) · [User guides](https://agentworkshq.com/docs/) · [First workflow](docs/getting-started/first-workflow.md) · [Releases](https://github.com/manishiitg/agentworks/releases/latest) · [Book a call](https://calendly.com/manishiitg/15min)

## How it works

| | |
|---|---|
| **Project** | A folder with its files, memory, skills, tools and browser connections. Chat with as many agents as you need, each in its own tab. Choose the agent and model for each chat in its message box. |
| **Team** | Teammates join a project from their own Macs, each with their own agents and chats; only the Team folder and Team keys are shared. People and agents ask each other, hand over tasks and see who is working on what. A project on a server can join a Team too: its agents become AI teammates that work around the clock. |
| **Workflows** | Save repeatable work as Python, with typed inputs and named steps. Run it from chat, on a schedule or from another app, and inspect each run's result, files, logs and cost. [First workflow](docs/getting-started/first-workflow.md) |
| **Your own agents** | Connect a Claude Code, Codex or other agent chat you already run in a terminal with `agentworks attach`. Your agents across laptops and servers can then message each other and your teammates' agents. [CLI and MCP](docs/getting-started/agentworks-cli-mcp.md) |

Everyone works at their own level: developers get worktrees, pull requests, terminals and the editor; everyone else reaches the same work through chat and plain-language screens.

## Install (macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/manishiitg/agentworks/main/install.sh | bash
```

Already have Node.js? `npx --yes @manishiitg/agentworks` does the same.

It installs the app (Apple Silicon or Intel) and the `agentworks` command, then opens the app. Nothing else to set up.

Then open a project folder and start chatting. The app finds the AI programs you already have. If your company runs an AgentWorks server, connect it to use the company's AI accounts and join its Teams.

To update, use **Check for Updates** in the app, or run the same command again.

<details>
<summary>Manual install, and the "AgentWorks is damaged and can't be opened" message</summary>

Download `AgentWorks-<version>-arm64.dmg` (Apple Silicon) or `AgentWorks-<version>-x64.dmg` (Intel) from the [latest release](https://github.com/manishiitg/agentworks/releases/latest) and drag the app to Applications.

The build is not yet signed or notarized, so macOS Gatekeeper flags it after download. For a copy downloaded from this release, clear the quarantine flag:

```bash
xattr -cr /Applications/AgentWorks.app
```

If macOS still complains, also run `xattr -cr ~/Downloads/AgentWorks-*.dmg`. No `sudo` is needed. Signing and notarization are on the roadmap.
</details>

## Works with

- **AI programs:** Claude Code, OpenAI Codex CLI, Cursor CLI, Antigravity, Pi CLI (Gemini, OpenRouter and other Pi providers) and Muse. Use the subscriptions you already pay for, or the company accounts your server lends to your Mac.
- **Channels:** Slack and WhatsApp for two-way conversations; Gmail for outbound updates.
- **Tools:** any MCP server, project files, and a persistent, isolated browser per project or workflow ([browser docs](docs/core/browser.md)).
- **Your AI app:** AgentWorks is also an MCP server. Connect ChatGPT, Claude (Desktop, Code, Cowork) or any MCP client to work with your projects and start workflows, with the same permissions as in the app ([CLI and MCP](docs/getting-started/agentworks-cli-mcp.md)).

AgentWorks runs each vendor's own agent (model plus harness), not a bare model API, so you get everything the agent can do. Pick the agent and model that fit each chat or step: your strongest for judgment, a cheaper one for routine work.

## Control and security

- **Sharing:** share a project as Only me, Chosen people or Everyone. On a server, the owner adds co-owners who build with them and Users who work in it through chat; on a Mac, the project's Team. Being in the company is not access ([sharing](docs/security/sharing.md)).
- **Secrets vault:** credentials are encrypted and reach work only as environment variables at run time, never shown in chat or logs ([secrets](docs/design/vault-secrets.md)).
- **Sandboxing:** OS-enforced per agent, Landlock on Linux and sandbox-exec on macOS, limited to the folders and tools each project or workflow is granted ([FolderGuard](docs/core/folder_guard_system.md)).
- **Run logs and cost:** every step, tool call and model cost is recorded, with who spent what on which project ([cost and logs](docs/workflow/cost_and_log_measurement.md)).
- **Decisions:** work that needs a person happens in chat. Workflows use their saved inputs and return a clear outcome; they do not wait for questions mid-run ([questions and inputs](docs/workflow/human_feedback_system.md)).

## Run from source

Prerequisites: Go 1.26+, Node.js 22.18+ (22.x) or 24+, [GitHub CLI](https://cli.github.com/) (`gh auth login`), and whichever coding-agent CLIs you want to use.

Dashboard preparation imports TypeScript directly using [Node’s built-in type stripping](https://nodejs.org/api/typescript.html#type-stripping); Node 20 cannot run it. The launcher checks the Node version before installing dependencies.

You can ask your own coding agent to do all of this for you: give it this section and tell it to clone the repository, check out `main`, install dependencies and start the app.

The two engine libraries, `mcpagent/` and `multi-llm-provider-go/`, are folders in this repository; `agent_go/go.mod` builds them from there. Clone it and work on the `main` branch:

```bash
git clone https://github.com/manishiitg/agentworks.git mcp-agent-builder-go
cd mcp-agent-builder-go
git checkout main
(cd frontend && npm ci)
(cd agent_go && go mod download)
./run_agentworks
```

`./run_agentworks` starts the agent API, workspace API, frontend, and Electron shell. No `.env` is needed for a local run; the launcher creates `agent_go/.env` with a persistent `AUTH_SECRET`. Don't copy `agent_go/env.example` for local use, since it is for managed deployments. An explicit `MULTI_USER_MODE` in your environment or `agent_go/.env` is preserved; otherwise it defaults to `false`. This is a local development launcher, not a hosted-server setup; use the [rootless deployment guide](deploy/README.md) for a Linux server.

| Service | Default URL |
| --- | --- |
| Agent API | `http://localhost:18743` |
| Workspace API | `http://localhost:18744` |
| Frontend | `http://127.0.0.1:51733` |

The first start can take several minutes on a small machine while Go downloads its toolchain and compiles the services. The launcher waits for those builds to finish before starting the service health timers; a failed build stops startup.

If a port is busy, the runner picks the next free one and prints it. Logs go to `agent_go/logs/`. Run `./run_agentworks --help` for options such as `--only-frontend` and `--build`.

Checks before a pull request:

```bash
(cd agent_go && go test ./cmd/server -run '^$')   # backend compiles
(cd frontend && ./node_modules/.bin/tsc -b)       # frontend type-checks
./scripts/scan-secrets.sh                         # no secrets committed
```

Install the git hooks once with `./scripts/install-git-hooks.sh`.

Make changes in your own git worktree per task, started from `origin/main`, then open a pull request into `main` (`gh pr create --base main`).

## Self-hosting

Run AgentWorks on your Mac, or on your own Linux server with the rootless deployer. See the [deployment overview](deploy/README.md). For a managed or private-cloud deployment with SSO, audit logs, and support, [talk to us](https://calendly.com/manishiitg/15min).

## Contributing

Issues are tracked on GitHub: open an [issue](https://github.com/manishiitg/agentworks/issues) for every bug or feature, and link your pull request to it with `Fixes #<number>`. This repository is public, so never put customer, server, IP address, person or internal-endpoint details in an issue, pull request or commit.

Pull requests are welcome. Start with the [docs index](docs/README.md) and the [workflow system overview](docs/workflow/README.md). Keep changes focused and run the checks above.

## License

AgentWorks is source-available under the [Business Source License 1.1](LICENSE), from 2026-10-09.

- **Free:** read the code, modify it, and run it locally for personal projects, development, testing, evaluation, education and research.
- **Commercial license required:** using AgentWorks in the operation of a business or organization, or offering it or a service based on it to others. Contact us through [agentworkshq.com/about](https://agentworkshq.com/about/).
- **Later:** each version converts to the Apache License 2.0 on the Change Date stated in the license.

Versions published before 2026-10-09 were released under the MIT License and remain available under it.

Built on the [Model Context Protocol](https://modelcontextprotocol.io/), our engine libraries [mcpagent](mcpagent/) and [multi-llm-provider-go](multi-llm-provider-go/), and [React Flow](https://reactflow.dev/) for the workflow canvas.
