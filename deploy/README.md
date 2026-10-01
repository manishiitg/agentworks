# Deploy

Deployment configs and scripts for Runloop. `./deploy.sh <server>` at the
repository root is the only local deployment entry point; each server's logic
is a function inside it. Scripts under `deploy/` are the server-side halves it
ships to the box (bootstrap, build-and-activate, release pruning, checks).

Incoming Gmail is configured per deployment and remains off until configured.
See [Gmail incoming conversations](../docs/gmail-inbound.md) for shared OAuth
topics, deployment-specific push subscriptions, and the RTS-first test steps.

Every frontend release must pass `node frontend/scripts/check-release-assets.mjs <packaged-static-directory>`.
`npm run build` includes this gate. Repeat it against uploaded assets before
activation and configure the agent's `STATIC_DIR` to the checked directory.
See the [Linux release gate](ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md#release-gate-report-preview-assets-every-release), including split-container requirements.

| Target | Path | Description |
|--------|------|-------------|
| **Dominion** (dedicated VM) | [deploy/dedicated-vm/dominion-hetzner.md](dedicated-vm/dominion-hetzner.md) | Isolated Hetzner VM, rootless `systemd --user`. Live at https://trader.tectonicmarkets.com |
| **Video Studio** (AWS EC2) | [deploy/aws-ec2/](aws-ec2/) | Isolated EC2 host, rootless `systemd --user`. Live at https://video.realtrainingsys.com |
| **Rootless Linux products** | [deploy/rootless-linux/](rootless-linux/) | Shared deterministic deployer used by Confida and SparkQuill |

- **Dominion**: `./deploy.sh dominion [--activate]` (SSHes in and runs the on-box `deploy-dominion.sh`). See [dedicated-vm/dominion-hetzner.md](dedicated-vm/dominion-hetzner.md).
- **Video Studio**: `./deploy.sh rts`. See [aws-ec2/README.md](aws-ec2/README.md). Every RTS deploy ends with a CloudFront usage report (month-to-date requests and GB against the free tier, last-24h error rates); it never fails the deploy.
- **Confida**: `./deploy.sh confida`.
- **SparkQuill**: `./deploy.sh sparkquill`.

Dominion and Video Studio share the same rootless `systemd --user` +
host-level-Caddy + Landlock-sandboxed architecture. Before standing up a new
deployment on that pattern — or after any change to the shared `workspace`/
`agent_go` sandboxing or Caddy config — run through
[`ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md`](ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md).
It exists because Dominion, which then had no deploy script, independently
rediscovered every item on it as a live production incident.
