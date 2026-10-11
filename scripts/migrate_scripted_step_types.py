#!/usr/bin/env python3
"""PLAT-859: normalize saved regular step types while the server is drained.

Dry-run by default. Pass workspace document roots, then --apply after review.
Only live **/planning/plan.json files are touched, never run snapshots or
contract versions. The runtime also accepts the old spelling without this job.
"""
import argparse
import copy
import json
import os
from pathlib import Path
import shutil
import tempfile


def normalize(plan):
    plan = copy.deepcopy(plan)
    count = 0

    def visit(step):
        nonlocal count
        if not isinstance(step, dict):
            raise ValueError("step must be an object")
        if step.get("type") == "regular":
            step["type"] = "scripted"
            count += 1
        for route in step.get("predefined_routes") or []:
            if route.get("sub_agent_step") is not None:
                visit(route["sub_agent_step"])

    for key in ("steps", "orphan_steps"):
        for step in plan.get(key) or []:
            visit(step)
    return plan, count


def migrate(roots, apply=False):
    # Validate all candidates before writing any. Backups are never overwritten.
    changes = []
    seen = set()
    for root in roots:
        root = Path(root).resolve(strict=True)
        for path in sorted(root.glob("**/planning/plan.json")):
            if path.is_symlink() or any(p.is_symlink() for p in path.parents if p != root):
                raise ValueError(f"refusing symlink path: {path}")
            if path in seen:
                continue
            seen.add(path)
            # Historical execution plans must retain their original revision.
            if "runs" in path.relative_to(root).parts:
                continue
            before = path.read_bytes()
            plan, count = normalize(json.loads(before))
            if count:
                changes.append((path, before, plan, count))
    for path, before, plan, count in changes:
        print(f"{'apply' if apply else 'dry-run'}: {path}: {count} regular → scripted")
        if not apply:
            continue
        if path.read_bytes() != before:
            raise ValueError(f"plan changed during migration: {path}; drain the server first")
        backup = path.with_name("plan.json.pre-scripted-type.bak")
        if not backup.exists():
            with backup.open("xb") as out:
                out.write(before)
            shutil.copystat(path, backup)
        fd, temporary = tempfile.mkstemp(prefix=".plan-scripted-", dir=path.parent)
        try:
            with os.fdopen(fd, "w") as out:
                json.dump(plan, out, ensure_ascii=False, indent=2)
                out.write("\n")
                out.flush()
                os.fsync(out.fileno())
            # Retain file mode and platform metadata/ACL extended attributes.
            shutil.copystat(path, temporary)
            original = path.stat()
            current = os.stat(temporary)
            if (original.st_uid, original.st_gid) != (current.st_uid, current.st_gid):
                os.chown(temporary, original.st_uid, original.st_gid)
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    return len(changes)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("roots", nargs="+", help="workspace document roots")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    print(f"{migrate(args.roots, args.apply)} plan(s) {'updated' if args.apply else 'would change'}")
