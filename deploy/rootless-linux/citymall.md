# Citymall host preparation

Dedicated host: `ubuntu@52.66.201.227`, Ubuntu 26.04 LTS, Linux x86_64,
2 CPUs, approximately 4 GB RAM and a 20 GB root volume.
The operator's SSH key is `~/Downloads/manish.pem` (mode 0600).
Never copy that private key into the repository or onto the server.

Run the base preparation from an owned checkout:

```bash
ssh -i "$HOME/Downloads/manish.pem" -o BatchMode=yes ubuntu@52.66.201.227 \
  'sudo -n bash -s' < deploy/rootless-linux/setup-citymall-host.sh
```

The script installs native build, Python/venv and browser prerequisites;
creates the unprivileged `citymall` account with home `/srv/citymall`;
enables its persistent systemd user manager; creates persistent workspace,
release, tool and log directories; and installs checksum-verified Node 24.21.0.
The account uses the same public authorized SSH keys as `ubuntu`, so deployment
can later connect directly as `citymall` using the operator's existing key.
It has no sudo or privileged Docker group membership.

Fresh secrets are generated once in the owner-only `/srv/citymall/.env`.
Repeated preparation preserves them. No other customer's credentials,
workflow data or provider logins are copied.

This is host preparation only. No application binaries, application services,
public site, sign-in provider or provider login are installed or activated yet.
Citymall is not yet a target in `deploy.sh`.

Before application deployment, choose the domain, enabled product surfaces,
sign-in configuration and initial administrator. Then add a Citymall product
configuration to the shared rootless pipeline and install its service units,
Caddy site, managed Chrome and coding CLIs. The shared bootstrap installs its
pinned Go toolchain on the server. Use ports 25000 (agent), 25001 (workspace)
and 25080 (loopback gateway), as reserved in the prepared environment.
Keep credentials and all persisted data outside release directories.

Validate host-specific namespace confinement, running service environments,
public HTTPS/authentication and an authenticated app flow before calling the
application deployed. The existing shared build defaults target a larger host;
set an appropriate build memory/parallelism budget for this 4 GB machine before
its first build.

## Citymall AI gateway

The operator's key is stored in the local macOS Keychain service
`cm-ai-key-manish`. On the host, keep it as `CITYMALL_API_KEY` in the existing
mode-0600 `/srv/citymall/.env`; never put it in the product configuration.
The non-secret Pi template is `products/citymall/pi-models.json`, also staged
at `/srv/citymall/provider-config/pi-models.json` on the host.

Live checks on 2026-10-01 passed chat, streaming, inline-image vision and image
generation. The supplied Wikimedia URL could not be downloaded by the gateway;
the same vision API accepted an inline PNG and described it correctly.
Isolated invocations of the installed Pi CLI passed a Hindi greeting and a
native read-tool call that returned the random contents of a test file.
Use `citymall/gpt-5.6-luna` with thinking off: the gateway rejects function tools
with its default reasoning setting, but accepts `reasoning_effort: "none"`.
The template explicitly maps Pi's off level to `none`; simply selecting off
without that mapping omits the field and still fails tool calls.

Pi can use this OpenAI Chat Completions endpoint with the `api-key` header.
The existing Azure adapter routes any GPT-5 model through `/responses`; that
gateway request returned HTTP 500 during the check, so Azure is not yet a
working configuration for this endpoint. The image-generation endpoint is
separate (`/images/v1/mai-image-2.6-flash/generations`) and needs its own application
adapter/tool integration.

This template is not automatically loaded by the application. Pi's AgentWorks
adapter creates private per-session `PI_CODING_AGENT_DIR` directories. Application
integration must stage the non-secret model configuration in those directories,
pass the selected connection's credential through the existing scoped provider
key path, and qualify a real authenticated app turn before activation. Model
pricing and exact context/output limits are not established by these checks.
