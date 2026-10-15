"""Real provisioning/discovery check for the AWS managed command folder."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[2]
with tempfile.TemporaryDirectory() as fixture:
    root = Path(fixture)
    managed_bin = root / "managed/bin"
    env = dict(os.environ, REMOTE_APP=str(root / "app"))
    command = ["python3", str(repo / "deploy/rootless-linux/install-ffprobe.py"), "--bin-dir", str(managed_bin)]
    subprocess.run(command, env=env, check=True)
    assert shutil.which("ffprobe", path=str(managed_bin) + os.pathsep + os.environ["PATH"]) == str(managed_bin / "ffprobe")
    subprocess.run([str(managed_bin / "ffprobe"), "-version"], check=True)
    repeat = subprocess.check_output(command, env=env, text=True)
    assert repeat.strip() == "ffprobe: pinned tool unchanged", repeat
    subprocess.run(["bash", "-n", str(repo / "deploy/aws-ec2/server/build-and-activate.sh")], check=True)
print("Pinned FFprobe installed in the managed command folder, discovered, launched and reused.")
