[← ops / repo](index.md)

# PLAT-719: Company and customer specifics in the public repo

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | ops |
| Area | repo |
| Summary | The public repo carries company and customer specifics (server IPs/ports, customer names and a customer's internal endpoint, people's emails, Slack IDs); move them to a private ops repo |

## What happened

## Fix

## Left

## Found (2026-10-08)

`manishiitg/agentworks` is public (4 forks). Tracked files carry: server IPs and SSH ports (Hetzner, Citymall, RTS); customer names and domains; Citymall's internal AI gateway endpoint (`products/citymall/pi-agent/models.json`, `citymall.md`); people's email addresses in tickets, docs, a live product config (`products/sparkquill/product.env`) and test fixtures; a real Slack channel ID in a test. gitleaks has found no secrets.

## Owner decision

A private ops repo for company- and customer-specific material; the public repo keeps generic tooling and an example. History is not rewritten (forks keep it); SSH stays IP-restricted.

## Step 1 (this commit)

People's emails removed from tickets (PLAT-323, 616, 623, 710) and `citymall.md`; test fixtures use example addresses; the Slack channel ID in a test is a dummy.

## Left

- Step 2: private `agentworks-ops` repo with `deploy/rootless-linux/products/*` (incl. SparkQuill's admin emails and Citymall's gateway endpoint), per-server docs, the server list and `./deploy.sh check`; `deploy.sh` reads products from it; public keeps one example product.
- Step 3: decide where tickets and DECISIONS entries that name customers/people live.
