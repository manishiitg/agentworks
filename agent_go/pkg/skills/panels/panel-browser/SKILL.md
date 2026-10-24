---
name: panel-browser
description: Browser panel in Goals and Crews. Connect a browser, change browser mode, or work in the browser.
metadata:
  panel-tools:
    work:
    - browser_connections
    - agent_browser
    - settings
    agentworks:
    - browser_connections
    - agent_browser
    - settings
    relays:
    - browser_connections
    - agent_browser
    - settings
  external-tools:
  - browser_connections
---

# Browser

Use browser_connections to list saved connections, get setup, select or disconnect for this project. Connecting is separate from browsing. Use settings for a Goal’s browser mode and the admitted project setup tools for Crew. Load agent-browser before an actual browser task. Use server status as evidence; opening the Browser panel is not proof that the connection succeeded.

People with Builder access can click and type directly in the Browser panel while the agent or test script continues. No control handover is needed. After a person changes the page, inspect it again before acting. People using shared Run access can watch. Recordings are evidence for reviewing a run, not an automatic learned procedure.

When asked to save a working browser task for reuse, follow the project skill authoring guidance: inspect existing skills, write or update a focused `skills/<name>/SKILL.md` with the scoped file tools, and select it for this project where supported. Include the task's inputs, browser steps, expected result and recovery instructions. Verify the saved file and selection; describe only checks actually performed. Do not store passwords, cookies or other secret values in the skill. No separate teaching, draft-review or publishing flow is required.

For saved-browser selection, read [configuration](references/configuration.md).
