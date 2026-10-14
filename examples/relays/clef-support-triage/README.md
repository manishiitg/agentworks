# Clef-flash support triage Relay

A working example of a classifier inside an ordinary Relay: receive a request,
classify it as Billing, Technical, Sales or Other, ask the corresponding platform
agent to draft a reply, and return JSON. All inference is real.

The program uses the existing native DBOS workflow and steps, platform agent
calls, selected secrets, graph inspection, run history and published versions.
Clef-flash is an HTTP call inside `classify_request`; it is not a new platform runtime.
The graph shows **Classify request**, then the four agent branches.

## Set it up in chat

Create a Relay and tell its Builder:

> Build a support triage Relay using Clef-flash. It receives INPUT.message and chooses
> billing, technical, sales or other. Ask the selected agent to write a suggested
> reply, and return category, reply and the classification scores. Use the native
> DBOS example in examples/relays/clef-support-triage/relay.py. Use the Relay's
> configured model for the reply agents. Test the examples before publishing.

The complete source is [relay.py](relay.py). Save it as the Relay's `relay.py`
through Builder or the MCP `files` tool. The standard MCP Relay creation tool
already creates an enabled function with required object `INPUT`.

Create a free Cloudflare account, open Workers AI, and choose **Use REST API**.
Create a Workers AI API token using its prefilled permissions. Copy the account ID.
Add **CLOUDFLARE_AUTH_TOKEN** and **CLOUDFLARE_ACCOUNT_ID** in this Relay's Secrets.
The code reads them with `vault`; keep the token out of source, sample inputs and
logs. Use the website Secrets panel or the authorized secret tools to add it.

Cloudflare offers **10,000 neurons per day** on Workers Free, with no credit card
required. The allowance is shared across the account's Workers AI use; once it
is exhausted, further requests fail until the daily reset. Clef-flash is a
[typed decision model](https://developers.cloudflare.com/workers-ai/models/clef-flash/)
and fits this allowance; it is not unlimited free inference. See
[pricing](https://developers.cloudflare.com/workers-ai/platform/pricing/) and
[REST setup](https://developers.cloudflare.com/workers-ai/get-started/rest-api/).
The Relay calls Cloudflare directly; you do not need to deploy a Cloudflare Worker.
Reply agents use the selected platform model and its normal usage allowance.

## Use it

Example input:

```json
{"message":"My card was charged twice. Please refund the duplicate charge."}
```

The actual result contains `category`, `reply`, and `classification` with the
chosen label, provider confidence and probability scores, resolved model,
classification latency and usage. Scores describe the provider's prediction;
they are not proof that the chosen category is correct.

The reply is a suggested response. This example does not send an email or perform
a refund. The selected agent creates the response through the real platform agent
runtime. A request needing tools can be added in a later authored version.

## Check the POC

[samples.json](samples.json) contains 20 requests with expected labels, including
mixed-topic requests and an instruction embedded in the input. Run each through
the Relay's **Test** action or MCP `relay action=test`, then inspect `get_run`.
Record actual category, correct/incorrect, classifier time, total time, chosen
agent and the final JSON. Expect exactly one reply-agent step after classification.
Also check a missing message: it should report the actionable input error.

Publish the completed draft, call that version once, and inspect the version and
result returned by the platform. The Graph explains possible paths; Runs shows
the steps that actually ran. Live evidence belongs in the platform ticket, not
in this example's source or sample data.
