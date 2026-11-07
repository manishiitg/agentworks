# Getting Started

AgentWorks runs coding agents, model providers, browsers, and MCP tools as repeatable business workflows. This guide takes you from installation to a reviewed first run.

## 1. Install the macOS App

```bash
curl -fsSL https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/install.sh | bash
```

The installer downloads the latest release, installs the app, installs the local MCP bridge, clears the macOS quarantine flag, and launches AgentWorks. See the [latest release](https://github.com/manishiitg/coding-agent-loop/releases/latest) for manual installation artifacts.

## 2. Complete First-Launch Setup

On first launch:

1. Choose the workspace folder that will hold `workspace-docs/`.
2. Set `AUTH_SECRET`. Reuse the existing value when opening an existing workspace because it encrypts stored provider credentials.
3. Configure at least one coding agent or model provider from the app's provider settings.

Provider keys are encrypted in `<workspace-docs>/config/provider-api-keys.json`.

## 3. Build the First Workflow

Follow [Build Your First Workflow](first-workflow.md) to create an automation, define its operating contract, run it, and review the result.

## 4. Operate and Improve

- [Schedule recurring runs](../workflow/workflow_scheduling.md)
- [Monitor runs, reports, and costs](../workflow/workflow_monitoring.md)
- [Understand reporting](../workflow/self_improvement_and_reporting.md)
- [Configure evidence-based Auto Improve](../workflow/auto_improvement_framework.md)

## 5. Connect the Runtime

- [Configure model providers](../core/llm_configuration_and_resilience.md)
- [Connect MCP tools](../core/mcp_bridge_layer.md)
- [Configure browser sessions](../core/browser.md)
- [Manage global and workflow secrets](../core/secrets.md)
- [Connect Slack, WhatsApp, and other channels](../core/bot_connector_system.md)

## 6. Use AgentWorks from Another AI Agent

- [Connect an AI agent with MCP](agentworks-cli-mcp.md)
