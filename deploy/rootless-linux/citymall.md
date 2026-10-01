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
