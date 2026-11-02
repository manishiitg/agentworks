---
name: software-work
description: Investigate engineering incidents, quality assurance and security findings.
---

# Software work

Use this skill to investigate engineering incidents, quality assurance and security findings. Read only the reference that matches the current request. Ask for missing source data or access in the current chat, then produce a useful result; there is no separate setup checklist. Use the project's existing tools, selections and sharing rules.

- [Incident Investigator](references/incident-investigator.md): Connect alerts, telemetry, deploys, and owner updates into an evidence-linked incident timeline and next-action queue.
- [Engineering Delivery Coordinator](references/engineering-delivery-coordinator.md): Trace blocked work across issues, PRs, CI, and deployments; chase the next owner decision and verify release state.
- [Engineering Operations Analyst](references/engineering-operations-analyst.md): Reconcile authorized delivery, quality or reliability records into reproducible team metrics and bounded improvement questions.
- [Performance Investigator](references/performance-investigator.md): Compare a reported regression with traces, measurements, and recent changes to produce a reproducible diagnosis and owner action.
- [Cloud Cost Analyst](references/cloud-cost-analyst.md): Explain cloud cost changes across billing and usage systems, route ownership, and prepare risk-checked savings decisions.
- [Post-Incident Reviewer](references/post-incident-reviewer.md): Reconstruct a stabilized incident from frozen evidence, separate observed impact from hypotheses, and propose owner-reviewable improvements.
- [Improvement Follow-Through Coordinator](references/improvement-follow-through-coordinator.md): Turn owner-approved incident actions into an exact register, reconcile work-item receipts, and verify the intended control rather than issue status alone.
- [Browser Journey QA Analyst](references/browser-journey-qa-analyst.md): Run approved user journeys against an exact build, investigate failures, and return durable evidence and reproduction steps.
- [Flaky Test Investigator](references/flaky-test-investigator.md): Separate inconsistent application behavior from test or environment instability and prepare a reviewed stabilization decision.
- [Release Quality Assistant](references/release-quality-assistant.md): Join exact change and build identity to required QA suites, classify missing evidence, and prepare an auditable gate decision for the release owner.
- [Security Findings Analyst](references/security-findings-analyst.md): Validate in-scope scanner or reported findings against the actual asset, classify risk with evidence, and prepare owner-reviewed remediation criteria.
- [Access Review Analyst](references/access-review-analyst.md): Compare approved permission policy with observed UI and server behavior using authorized actors, isolated fixtures, and exact matrix evidence.
- [Security Remediation Coordinator](references/security-remediation-coordinator.md): Track one approved finding through owner assignment, reviewed change, deployment, independent retest, and evidence-backed closure.

## Running an app others can open

To keep a web app or API running for people to open, start it detached so it outlives this chat and server restarts: `pm2 start …`, `setsid nohup … &`, or `docker compose up -d`. A plain `cmd &` stops with the chat. Bind it to `0.0.0.0` on a free port from `$AGENTWORKS_APP_PORTS` (the ports this server opens to the internet) and give the person `http://<server address>:<port>`. If that variable is empty, say the server has no open app ports and ask which port to use. Checking from the server itself (`curl` to 127.0.0.1 or the server's own address) does not show that people outside can open it; never claim that. If `id -un` prints the app account (`agents`) instead of a `slot` name, the app stops at the next platform restart: say so plainly. When you start one, tell the person once: it keeps running, its address, that anyone with the address can open it (add a password in the app if it shows private data), and that it shares their limit. Your apps share a per-person limit of $AGENTWORKS_APP_LIMITS (your AI turns use it too). If an app is slow or stops, that's why; stop old apps you no longer need. Stop it when asked (`pm2 delete`, `kill`, `docker compose down`).

