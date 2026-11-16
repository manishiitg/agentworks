"""#2831: run the desktop top-frame WebSocket regression on the build host."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run([
    "node", "--test", "--test-name-pattern=project Terminal websocket",
    str(root / "desktop/lib/connections.test.js"),
], check=True)
