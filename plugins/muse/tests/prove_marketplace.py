"""Check real Muse marketplace discovery/install without approving runtime tools.

Usage: python3 .../prove_marketplace.py /absolute/muse-binary [GitHub-source]
Without a source, create an isolated Git fixture from this repository's packages.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


REPO = Path(__file__).resolve().parents[3]


def main():
    binary = Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="agentworks-muse-marketplace-") as directory:
        scratch = Path(directory).resolve()
        env = dict(os.environ, XDG_CONFIG_HOME=str(scratch / "config"),
                   XDG_DATA_HOME=str(scratch / "data"), XDG_CACHE_HOME=str(scratch / "cache"),
                   PYTHONDONTWRITEBYTECODE="1", GIT_TERMINAL_PROMPT="0")
        if len(sys.argv) > 2:
            source = sys.argv[2]
        else:
            fixture = scratch / "repository"
            for package in ("muse", "codex", "claude"):
                shutil.copytree(REPO / "plugins" / package, fixture / "plugins" / package,
                                ignore=shutil.ignore_patterns("__pycache__"))
            for catalog in (".agents/plugins/marketplace.json", ".claude-plugin/marketplace.json"):
                target = fixture / catalog
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(REPO / catalog, target)
            subprocess.run(["git", "init", "-q", "-b", "main", str(fixture)], check=True)
            subprocess.run(["git", "-C", str(fixture), "add", "."], check=True)
            subprocess.run(["git", "-C", str(fixture), "-c", "core.hooksPath=/dev/null",
                            "-c", "commit.gpgsign=false", "-c", "user.name=Plugin fixture",
                            "-c", "user.email=fixture@example.com", "commit", "-qm",
                            "Marketplace fixture"], check=True)
            source = fixture.as_uri() + "#main"

        def muse(*args):
            result = subprocess.run([str(binary), "plugins", *args, "--json"],
                                    cwd=scratch, env=env, capture_output=True,
                                    text=True, timeout=90)
            assert result.returncode == 0, result.stdout + result.stderr
            return json.loads(result.stdout)

        marketplace = muse("marketplace", "add", "agentworks", source)["marketplace"]
        assert marketplace["source"]["kind"] == "git", marketplace
        available = muse("list", "--available")["available"]
        assert {"agentworks-muse", "agentworks-codex"}.issubset(
            {item["name"] for item in available}), available
        installation = muse("install", "agentworks-muse@agentworks")
        installed = installation["installed"]
        assert installed["id"] == "agentworks-muse" and installed["manifest_family"] == "native"
        assert installed["source"]["provenance"] == "marketplace-user-added"
        assert "require review" in installation["warning"]
        assert not installation["diagnostics"]
        cache = Path(installed["cache_path"]).resolve()
        assert cache.is_relative_to(scratch)
        capabilities = muse("inspect", "agentworks-muse")["plugin"]["capabilities"]
        assert {hook["id"] for hook in capabilities["hooks"]} == {
            "prompt-submit", "stop", "session-end"}
        for hook in capabilities["hooks"]:
            assert Path(hook["source_path"]).is_relative_to(cache), hook
        assert capabilities["mcp_servers"][0]["command"] == [
            "agentworks", "plugin", "mcp", "muse"]
        assert [item["id"] for item in capabilities["commands"]] == ["agentworks"]
        assert [item["id"] for item in capabilities["skills"]] == ["messaging", "monitor"]
        if len(sys.argv) == 2:
            with (fixture / "plugins/muse/README.md").open("a") as readme:
                readme.write("\nMarketplace update fixture.\n")
            subprocess.run(["git", "-C", str(fixture), "add", "."], check=True)
            subprocess.run(["git", "-C", str(fixture), "-c", "core.hooksPath=/dev/null",
                            "-c", "commit.gpgsign=false", "-c", "user.name=Plugin fixture",
                            "-c", "user.email=fixture@example.com", "commit", "-qm",
                            "Marketplace update fixture"], check=True)
            # Muse 1.4.4 retains the installed source's old generation. A direct
            # reinstall refuses the new source; update reads the old generation.
            muse("marketplace", "update", "agentworks")
            muse("remove", "agentworks-muse")
            refreshed = muse("install", "agentworks-muse@agentworks")
            assert refreshed["installed"]["id"] == installed["id"]
            assert "require review" in refreshed["warning"]
            assert refreshed["installed"]["package_sha256"] != installed["package_sha256"]
        print("PASSED: Git marketplace installs the native Muse bundle with three cached hooks "
              "and the messaging child; runtime approval remains the person's")


if __name__ == "__main__":
    main()
