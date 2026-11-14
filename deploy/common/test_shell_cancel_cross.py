"""Compile and vet the Unix shell controller for both supported Mac targets.

PLAT-746-506: Linux execution tests cannot prove a Mac sandbox wrapper stops;
the actual Mac heartbeat probe remains a separate live acceptance check.
"""
import os
from pathlib import Path
import subprocess

workspace = Path(__file__).resolve().parents[2] / "workspace"
for arch in ("arm64", "amd64"):
    env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="0")
    subprocess.run(["go", "build", "-o", os.devnull, "./handlers"], cwd=workspace, env=env, check=True, timeout=600)
    subprocess.run(["go", "vet", "./handlers"], cwd=workspace, env=env, check=True, timeout=600)
    print(f"Mac shell controller compile/vet: {arch} PASS", flush=True)
