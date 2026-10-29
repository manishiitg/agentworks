"""Desktop connection release checks. Run on the build host, not a laptop."""
import os
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
for source in ("desktop/main.js", "desktop/preload.js"):
    subprocess.run(["node", "--check", str(root / source)], check=True)
subprocess.run(["node", "--test", str(root / "desktop/lib/connections.test.js")], check=True)
for arch in ("arm64", "amd64"):
    subprocess.run(
        ["go", "build", "-o", os.devnull, "."],
        cwd=root / "agent_go",
        env={**os.environ, "GOOS": "darwin", "GOARCH": arch, "CGO_ENABLED": "0"},
        check=True,
    )
print("Mac desktop server builds and connection isolation checks passed.")
