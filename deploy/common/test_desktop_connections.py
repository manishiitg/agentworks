"""Desktop connection release checks. Run on the build host, not a laptop."""
import os
from pathlib import Path
import subprocess
import tempfile
import signal

root = Path(__file__).resolve().parents[2]
subprocess.run(["bash", "-n", str(root / "agent_go/run_server_with_logging.sh")], check=True)
for source in ("desktop/main.js", "desktop/preload.js", "desktop/lib/devAgent.js"):
    subprocess.run(["node", "--check", str(root / source)], check=True)
subprocess.run(["node", "--test", str(root / "desktop/lib/connections.test.js")], check=True)
# Exercise the actual server executable on the build host, through Electron's
# pipe helper. No real accounts, workspaces, agents or model requests are used.
with tempfile.TemporaryDirectory(prefix="desktop-proof-") as scratch:
    binary = str(Path(scratch) / "agent-server")
    subprocess.run(["go", "build", "-o", binary, "."], cwd=root / "agent_go", check=True)
    # The server can spawn SDK helpers; kill this fixture's process group before
    # removing its temporary HOME, even if a startup assertion fails.
    child = subprocess.Popen(["node", str(root / "deploy/common/check_desktop_process.cjs"), binary, scratch], start_new_session=True)
    try:
        if child.wait(timeout=180) != 0:
            raise RuntimeError("Desktop real-process check failed")
    finally:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
for arch in ("arm64", "amd64"):
    subprocess.run(
        ["go", "build", "-o", os.devnull, "."],
        cwd=root / "agent_go",
        env={**os.environ, "GOOS": "darwin", "GOARCH": arch, "CGO_ENABLED": "0"},
        check=True,
    )
print("Mac desktop server builds and connection isolation checks passed.")
