"""Desktop connection release checks. Run on the build host, not a laptop."""
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
for source in ("desktop/main.js", "desktop/preload.js"):
    subprocess.run(["node", "--check", str(root / source)], check=True)
subprocess.run(["node", "--test", str(root / "desktop/lib/connections.test.js")], check=True)
# Exercise the actual server executable on the build host, through Electron's
# pipe helper. No real accounts, workspaces, agents or model requests are used.
with tempfile.TemporaryDirectory(prefix="desktop-proof-") as scratch:
    binary = str(Path(scratch) / "agent-server")
    subprocess.run(["go", "build", "-o", binary, "."], cwd=root / "agent_go", check=True)
    subprocess.run(["node", str(root / "deploy/common/check_desktop_process.cjs"), binary, scratch], check=True)
for arch in ("arm64", "amd64"):
    subprocess.run(
        ["go", "build", "-o", os.devnull, "."],
        cwd=root / "agent_go",
        env={**os.environ, "GOOS": "darwin", "GOARCH": arch, "CGO_ENABLED": "0"},
        check=True,
    )
print("Mac desktop server builds and connection isolation checks passed.")
