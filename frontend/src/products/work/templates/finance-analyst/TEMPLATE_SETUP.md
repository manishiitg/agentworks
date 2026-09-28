# Finance Analyst setup

Template: `finance-analyst` version 1. This file is reusable setup guidance, not a customer data record.

The ordered setup checks and their progress live in `TEMPLATE_SETUP.json`. The Crew reads that file during setup and records a check only after it verifies the result with the owner. The chat header shows pending or complete from that saved progress.

## First brief

1. Upload a bank statement, accounting export, invoice export, or similar authorized record.
2. Say which period and currency to analyze, and define revenue and expense measures if they differ from the source labels.
3. Ask: “Prepare a finance brief for this period. Show calculations and cite the source records. Separate cash received from invoiced revenue.”

The first brief needs no MCP connection. A spreadsheet skill or accounting MCP can help with larger or connected datasets; select a suitable one in this Crew only after reviewing its access. Do not copy an account, token, secret, or channel from another Crew.

The skill contains a fictional worked brief that distinguishes invoices, customer collections, processor fees, bank deposits and unknown recognized revenue. Use it to check the output format, then reproduce one calculation from the customer's own records. A fictional example never completes a setup check.

## Choose deeper analyses only when requested

The Finance Analyst skill also covers read-only processor account checks, subscription metrics, cash planning, and spend/reconciliation questions. Each requires its own source and definition check at the time of use. For a processor check, confirm provider account, live/test mode, payout timezone and currency, balance transaction/fee/refund/dispute export, and bank evidence before calling funds deposited. For MRR or churn, confirm subscription state rules, cohort window, discounts, credits, and customer identity. For runway, confirm dated bank balance, obligations, collection timing, and scenario assumptions. When an independent billing, close, or payables owner exists, use the corresponding Crew template and propose a reviewed Finance Operations Review route only if a cross-Crew handoff is needed.

For a cloud-savings verification, confirm the exact cloud account, service and resource, currency, billing and allocation basis, two equal complete periods, workload and health evidence, and the approved deployment receipt. A projected optimization range or IaC plan is not a billed saving. If any source is absent, return a pending readout with the next evidence needed. Cost Anomaly to Verified Savings uses a separate Cost Analyst and Delivery Coordinator before this Finance check.

## Optional recurring capabilities

These are suggestions. They are not created or enabled by this template.

| Capability | Suggested definition | Before enabling |
| --- | --- | --- |
| Crew schedule | “Prepare the weekly finance brief for the last complete business week from the authorized data. Show sources, calculations, changes, and open questions. If data is missing, report the gap instead of inventing values.” | Choose a cadence and timezone, verify a current data source, test one brief, and review any delivery route. |
| Authenticated trigger | On a `new_statement` event, analyze the new authorized statement and report changes for its period. Expected payload: `statement_ref` and `period`; optionally `currency`. | Choose an event source and authentication, create the trigger, test with a real accessible statement, then enable. Do not place raw financial data or credentials in the event payload. |
| Crew function | `analyze_finances` accepts an explicit period and optional authorized source reference. It returns a summary, metric calculations, source references, and open questions. The proposed typed contract is below. | Review the contract and source permissions, define the function in Crew, and test an authorized call. This template does not expose an additional function. |
| Goal-chasing Automation | Weekly Business Report: deliver an accurate, on-time finance brief and track coverage, delivery, and unresolved exceptions. | Create separately in Automations after agreeing on the goal, data source, metric definitions, and delivery policy. |

Slack or email delivery is optional and needs its own connection, destination, and approval policy. Never assume the template has access to a customer's accounts.

### Suggested `analyze_finances` contract

Input schema:

```json
{"type":"object","additionalProperties":false,"required":["period"],"properties":{"period":{"type":"string","description":"Explicit reporting period or date range"},"source_ref":{"type":"string","description":"Optional reference to an authorized source"}}}
```

Result schema:

```json
{"type":"object","additionalProperties":false,"required":["period","summary","metrics","source_refs","open_questions"],"properties":{"period":{"type":"string"},"summary":{"type":"string"},"metrics":{"type":"array","items":{"type":"object","required":["name","value","unit","calculation","source_refs"],"properties":{"name":{"type":"string"},"value":{"type":"number"},"unit":{"type":"string"},"calculation":{"type":"string"},"source_refs":{"type":"array","items":{"type":"string"}}}}},"source_refs":{"type":"array","items":{"type":"string"}},"open_questions":{"type":"array","items":{"type":"string"}}}}
```
