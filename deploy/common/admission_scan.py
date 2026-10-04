#!/usr/bin/env python3
"""Read-only secret admission scan (PLAT-478 / PLAT-471), the WARN section of the deploy self-test.

Reports, by NAME only (never a value, never a token):
  - a manifest (workflow.json / product.json) that selects a secret which exists nowhere: not in the shared stores
    (managed `_users/_system_global_secrets/secrets.json`, environment `GLOBAL_SECRET_*`) and not in the project's
    own stores (`_users/<owner>/workflow_secrets/<sha256(path)>.json`) -- the same rule as
    `agentworks server migrate-secret-selections`;
  - a shared secret that manifests select but the Vault Platform group has no grant for (only when Vault is
    configured: CAPLAYER_SERVICE_URL and CAPLAYER_SERVICE_TOKEN[_FILE] in the agent's environment). Admission refuses
    those selections ("Your groups do not have access to secret ...").

It never writes, never decrypts, and always exits 0 (it warns; the slot checks decide the deploy). User ids in
manifest paths are shown as <user>.

  admission_scan.py --docs <docs root> [--agent-pid <pid>] [--env-file <app>/.env]
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

USERS = "_users"
SHARED_OWNER = "_shared"
MANAGED_GLOBALS_OWNER = "_system_global_secrets"
PROJECT_ROOTS = ["Chats/Work/projects", "Chats/Code/projects", "Chats/Relays/projects", "Chats/Goals/projects", "Chats/Video Studio/projects"]
FIELDS = ("selected_secrets", "selected_global_secret_names")
GATEWAY_WORKSPACE = "w1"  # mcp-gateway/cmd/server/main.go


def platform_group_id(workspace: str = GATEWAY_WORKSPACE) -> str:
    """store.PlatformGroupID: "everyone-" + hex(sha256(workspace)[:12])."""
    return "everyone-" + hashlib.sha256(workspace.encode()).digest()[:12].hex()


def read_env(agent_pid: str | None, env_file: str | None) -> dict[str, str]:
    """The agent's environment (the running process first, else its .env). Kept in memory only."""
    if agent_pid:
        try:
            raw = Path(f"/proc/{agent_pid}/environ").read_bytes()
            return dict(entry.split("=", 1) for entry in raw.decode(errors="replace").split("\0") if "=" in entry)
        except OSError:
            pass
    env: dict[str, str] = {}
    if env_file:
        try:
            for line in Path(env_file).read_text().splitlines():
                key, sep, value = line.partition("=")
                if sep and key.strip() and not key.lstrip().startswith("#"):
                    env[key.strip()] = value.strip().strip('"').strip("'")
        except OSError:
            pass
    return env


def child_dirs(path: Path) -> list[Path]:
    try:
        return sorted(p for p in path.iterdir() if p.is_dir() and not p.is_symlink() and not p.name.startswith("."))
    except OSError:
        return []


def manifests(docs: Path) -> list[Path]:
    roots = [docs / "Workflow", docs / "Crew", docs / "Relays"] + [docs / r for r in PROJECT_ROOTS]
    for user in child_dirs(docs / USERS):
        if user.name in (SHARED_OWNER, MANAGED_GLOBALS_OWNER):
            continue
        roots += [user / "Workflow"] + [user / r for r in PROJECT_ROOTS]
    found = []
    for root in roots:
        for child in child_dirs(root):
            for name in ("workflow.json", "product.json"):
                path = child / name
                if path.is_file() and not path.is_symlink():
                    found.append(path)
    return sorted(found)


def store_names(path: Path, workspace: str | None = None) -> set[str]:
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError):
        return set()
    if not isinstance(data, dict):
        return set()
    if workspace is not None:
        if data.get("workflow_path") != workspace or not isinstance(data.get("secrets"), dict):
            return set()
        data = data["secrets"]
    return set(data)


def display(docs: Path, path: Path) -> str:
    parts = path.relative_to(docs).parts
    if len(parts) >= 2 and parts[0] == USERS:
        parts = (USERS, "<user>") + parts[2:]
    return "/".join(parts)


def platform_grants(env: dict[str, str]) -> tuple[set[str] | None, str]:
    url = env.get("CAPLAYER_SERVICE_URL", "").strip()
    token = env.get("CAPLAYER_SERVICE_TOKEN", "").strip()
    token_file = env.get("CAPLAYER_SERVICE_TOKEN_FILE", "").strip()
    if not url:
        return None, "Vault is not configured on this server (no CAPLAYER_SERVICE_URL): grants not checked"
    if token_file:
        try:
            token = Path(token_file).read_text().strip()
        except OSError:
            return None, "the Vault service credential file is unreadable: grants not checked"
    if len(token) < 32:
        return None, "no Vault service credential in the agent's environment: grants not checked"
    request = urllib.request.Request(url.rstrip("/") + f"/api/admin/groups/{platform_group_id()}/secrets",
                                     headers={"Authorization": "Bearer " + token, "X-CapLayer-Actor": "deploy-self-test"})
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            body = json.loads(response.read().decode())
    except urllib.error.HTTPError as error:
        return None, f"Vault answered HTTP {error.code} for the Platform group's secrets: grants not checked"
    except (urllib.error.URLError, OSError, ValueError) as error:
        return None, f"Vault is unreachable ({type(error).__name__}): grants not checked"
    return {row.get("name", "") for row in body.get("secrets", []) if isinstance(row, dict)}, ""


def scan(docs: Path, env: dict[str, str], grants: set[str] | None) -> tuple[list[str], dict[str, int]]:
    warnings: list[str] = []
    globals_ = store_names(docs / USERS / MANAGED_GLOBALS_OWNER / "secrets.json")
    globals_ |= {key[len("GLOBAL_SECRET_"):] for key in env if key.startswith("GLOBAL_SECRET_")}
    owners = [SHARED_OWNER] + [u.name for u in child_dirs(docs / USERS) if u.name not in (SHARED_OWNER, MANAGED_GLOBALS_OWNER)]
    selected_shared: dict[str, int] = {}
    paths = manifests(docs)
    for path in paths:
        try:
            manifest = json.loads(path.read_text())
        except (OSError, ValueError):
            warnings.append(f"WARN secret-manifest {display(docs, path)}: unreadable or not JSON")
            continue
        caps = manifest.get("capabilities") if isinstance(manifest, dict) else None
        if not isinstance(caps, dict):
            continue
        workspace = "/".join(path.parent.relative_to(docs).parts)
        digest = hashlib.sha256(workspace.encode()).hexdigest()
        available = set(globals_)
        for owner in owners:
            available |= store_names(docs / USERS / owner / "workflow_secrets" / f"{digest}.json", workspace)
        for field in FIELDS:
            names = caps.get(field)
            if not isinstance(names, list):
                continue
            for name in names:
                if not isinstance(name, str):
                    continue
                if name not in available:
                    warnings.append(f"WARN secret-missing {display(docs, path)}: {field} names {name}, which exists nowhere (agentworks server migrate-secret-selections --dry-run lists it; add the secret or remove the selection)")
                elif field == "selected_global_secret_names" or name in globals_:
                    selected_shared[name] = selected_shared.get(name, 0) + 1
    if grants is not None:
        for name in sorted(selected_shared):
            if name not in grants:
                warnings.append(f"WARN secret-no-platform-grant {name}: selected by {selected_shared[name]} manifest(s), no Platform grant (Vault > Access > Platform > Secrets)")
    counts = {"manifests": len(paths), "shared_secrets": len(globals_), "selected_shared": len(selected_shared)}
    return warnings, counts


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--docs", required=True)
    parser.add_argument("--agent-pid")
    parser.add_argument("--env-file")
    args = parser.parse_args(argv)
    docs = Path(args.docs).resolve()
    env = read_env(args.agent_pid, args.env_file)
    grants, note = platform_grants(env)
    warnings, counts = scan(docs, env, grants)
    print(f"secret admission scan: {counts['manifests']} manifest(s), {counts['shared_secrets']} shared secret name(s), {counts['selected_shared']} selected shared name(s)")
    if note:
        print("INFO " + note)
    for line in warnings:
        print(line)
    print(f"secret admission scan: {len(warnings)} warning(s)")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as error:  # a scan problem never fails the deploy and never prints a value
        print(f"WARN secret admission scan could not run: {type(error).__name__}")
        sys.exit(0)
