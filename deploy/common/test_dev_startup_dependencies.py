#!/usr/bin/env python3
"""PLAT-746-934: run launcher checks with stub tools, never start the app."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run(["bash", "-n", str(root / "agent_go/run_server_with_logging.sh"),
                str(root / "scripts/dev-startup-dependencies.sh")], check=True)
subprocess.run(["bash", str(root / "scripts/test-dev-startup-dependencies.sh")], check=True)
