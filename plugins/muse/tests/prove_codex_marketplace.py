"""Run the native Codex catalog/install regression in a disposable GitHub runner.

Do not run this against a person's normal plugin profile. No model session starts.
"""

import json
import os
from pathlib import Path
import subprocess
import sys


REPO = Path(__file__).resolve().parents[3]


def main():
    if os.environ.get("GITHUB_ACTIONS") != "true":
        raise SystemExit("Use the disposable GitHub runner; this installs a Codex plugin.")
    binary = sys.argv[1]

    def codex(*args):
        result = subprocess.run([binary, "plugin", *args, "--json"], cwd=REPO,
                                capture_output=True, text=True, timeout=90)
        assert result.returncode == 0, result.stdout + result.stderr
        return json.loads(result.stdout)

    codex("marketplace", "add", str(REPO))
    catalog = codex("list", "--available", "--marketplace", "agentworks")
    entries = catalog["installed"] + catalog["available"]
    assert any(item["name"] == "agentworks-codex" for item in entries), catalog
    codex("add", "agentworks-codex@agentworks")
    catalog = codex("list", "--marketplace", "agentworks")
    installed = [item for item in catalog["installed"] if item["name"] == "agentworks-codex"]
    assert len(installed) == 1 and installed[0]["installed"], catalog
    source = REPO / "plugins/codex"
    manifest = json.loads((source / ".codex-plugin/plugin.json").read_text())
    assert installed[0]["version"] == manifest["version"], installed
    cache = Path.home() / ".codex/plugins/cache/agentworks/agentworks-codex" / manifest["version"]
    # Pin actual unchanged hooks, MCP, skills and assets, not just the catalog row.
    for path in source.rglob("*"):
        if path.is_file():
            target = cache / path.relative_to(source)
            assert target.read_bytes() == path.read_bytes(), target
    print("PASSED: native Codex marketplace add/list/install preserves the Codex "
          + manifest["version"] + " package alongside the Muse entry")


if __name__ == "__main__":
    main()
