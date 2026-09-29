#!/usr/bin/env python3
"""Build a deployment's MCP catalog: the one shared catalog
(agent_go/configs/mcp_servers_clean.json) plus an optional per-deployment
override holding only what that deployment does differently. An override
entry replaces the shared entry of the same name; null removes one (a
deployment that keeps an older name for the same server).

Usage: build-mcp-catalog.py <shared.json> <out.json> [override.json]
"""
import json
import sys


def build(shared_path, override_path=None):
    with open(shared_path) as f:
        servers = json.load(f)["mcpServers"]
    if override_path:
        with open(override_path) as f:
            for name, entry in json.load(f)["mcpServers"].items():
                if entry is None:
                    servers.pop(name, None)
                else:
                    servers[name] = entry
    return {"mcpServers": servers}


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    catalog = build(sys.argv[1], sys.argv[3] if len(sys.argv) == 4 else None)
    with open(sys.argv[2], "w") as f:
        json.dump(catalog, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"MCP catalog: {len(catalog['mcpServers'])} servers -> {sys.argv[2]}")
