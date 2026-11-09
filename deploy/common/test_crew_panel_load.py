"""PLAT-746-513: same real HTTP Crew before/after the panel-load fix.

Run only inside the normal capped, unprivileged product verifier. No model,
client objects, broad test suite or altered permission guard. Original sources
are restored from this commit's saved parent, then put back byte-for-byte.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

repo = Path(__file__).resolve().parents[2]
module = repo / "agent_go"
if os.geteuid() == 0:
    raise SystemExit("Use the normal unprivileged verifier")
base = "bac24e79ad6a0cf08cae7e7f43f2311996f39f78"
files = ["crew_panel.go", "crew_panel_sources.go", "crew_scripts.go", "python_source_inspection.go"]
paths = [module / "cmd/server" / name for name in files]
fixed = {p: p.read_bytes() for p in paths}
command = ["go", "test", "./cmd/server", "-run", "^TestCrewPanelTwentyWorkflowsAvoidsInspectionAndDeepHistory$", "-count=1", "-v", "-timeout=90s"]
report = {"base": base, "candidate": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()}

def run(label):
    start = time.monotonic()
    result = subprocess.run(command, cwd=module, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    print(label, "seconds", round(time.monotonic()-start, 3), "exit", result.returncode)
    samples = [line for line in result.stdout.splitlines() if "PANEL_LOAD " in line]
    print("\n".join(samples))
    report[label] = {"exit": result.returncode, "samples": samples}
    if result.returncode:
        print(result.stdout[-14000:])
    return result

try:
    for p in paths:
        relative = p.relative_to(repo).as_posix()
        p.write_bytes(subprocess.check_output(["git", "show", base+":"+relative], cwd=repo))
    old = run("before")
    if old.returncode == 0 or "panel performed expensive per-poll work" not in old.stdout:
        raise SystemExit("Old path did not reproduce the panel-load regression; inspect setup, not a PASS")
finally:
    for p, content in fixed.items():
        p.write_bytes(content)

if any(p.read_bytes() != content for p, content in fixed.items()):
    raise SystemExit("source restoration mismatch")
report["restored_hashes"] = {str(p.relative_to(repo)): hashlib.sha256(content).hexdigest() for p, content in fixed.items()}
new = run("fixed")
print(json.dumps(report, indent=2))
if new.returncode:
    raise SystemExit("Fixed HTTP panel check failed")
