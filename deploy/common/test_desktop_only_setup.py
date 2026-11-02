#!/usr/bin/env python3
"""PLAT-856-496: real CLI setup with no provider, Node, npm or git on PATH.

Run on the remote verification host, never as a person's installed profile.
"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib


repo = Path(__file__).resolve().parents[2]
with tempfile.TemporaryDirectory(prefix="agentworks-desktop-setup-") as temp:
    root = Path(temp)
    cli = root / "agentworks"
    subprocess.run(["go", "build", "-o", str(cli), "./cmd/agentworks"], cwd=repo / "agent_go", check=True)
    for arch in ("amd64", "arm64"):
        env = dict(os.environ, GOOS="windows", GOARCH=arch, CGO_ENABLED="0")
        subprocess.run(["go", "build", "-o", str(root / f"agentworks-windows-{arch}.exe"), "./cmd/agentworks"],
                       cwd=repo / "agent_go", env=env, check=True)
        print(f"Windows CLI cross-build ({arch}): PASS", flush=True)

    profile = root / "codex-profile"
    env = dict(os.environ, PATH="", HOME=str(root), USERPROFILE=str(root),
               CODEX_HOME=str(profile), XDG_CONFIG_HOME=str(root / "config"), APPDATA=str(root / "config"))
    config = root / "no-agentworks-login.json"
    for flags, plugin in ((["--hooks"], "agentworks-codex"), (["--no-hooks"], "agentworks-codex-tools")):
        result = subprocess.run([str(cli), "--config", str(config), "setup", "codex", *flags],
                                cwd=root, env=env, capture_output=True, text=True, timeout=100)
        if result.returncode:
            raise AssertionError(f"app-only setup failed: {result.stdout}\n{result.stderr}")
        doc = tomllib.loads((profile / "config.toml").read_text())
        assert doc["marketplaces"]["agentworks"]["source_type"] == "local"
        assert plugin + "@agentworks" in doc["plugins"]
        installed = list((profile / "plugins/cache/agentworks" / plugin).glob("*/.codex-plugin/plugin.json"))
        assert len(installed) == 1, installed
        assert json.loads(installed[0].read_text())["name"] == plugin
        mcp = json.loads((installed[0].parent.parent / ".mcp.json").read_text())
        assert mcp["mcpServers"]["agentworks"]["args"] == ["plugin", "mcp", "codex"]
        assert "hooks" not in doc, "setup must not approve hooks"
        print(f"Real setup codex {' '.join(flags)}, empty PATH, no saved login: PASS", flush=True)
        if plugin == "agentworks-codex":
            # Repeat setup after a person disables the plugin; keep it disabled.
            file = profile / "config.toml"
            file.write_text(file.read_text().replace("enabled = true", "enabled = false"))
            again = subprocess.run([str(cli), "--config", str(config), "setup", "codex", "--hooks"],
                                   cwd=root, env=env, capture_output=True, text=True, timeout=100)
            assert again.returncode == 0, again.stderr
            doc = tomllib.loads(file.read_text())
            assert doc["plugins"][plugin + "@agentworks"]["enabled"] is False
            print("Real repeated setup preserves disabled plugin: PASS", flush=True)
