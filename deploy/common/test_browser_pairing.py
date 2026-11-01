#!/usr/bin/env python3
"""Run the extension pairing boundary check on the remote verification host."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run(["node", "scripts/test-chrome-extension-pairing.mjs"], cwd=root, check=True)
for source in ("extensions/agentworks-chrome/background.js", "extensions/agentworks-chrome/popup.js", "desktop/main.js", "desktop/lib/externalNav.js"):
    subprocess.run(["node", "--check", source], cwd=root, check=True)
