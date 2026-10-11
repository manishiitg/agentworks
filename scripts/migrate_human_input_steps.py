#!/usr/bin/env python3
"""PLAT-864: replace retired human_input plans during a drained deployment.

Dry-run by default. Each affected live plan needs a reviewed JSON recipe under
--recipes at its workspace-relative planning/plan.json path. A recipe contains
source_sha256, plan, optional step_config, and optional variables_add definitions
(name/description only). Values and secrets are never supplied by a recipe.
Historical runs, releases, versions and backups are not rewritten.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile


HISTORY = {"runs", "releases", "versions", "backup", "backups", "moved-workflows", ".trash", ".calls", ".relay_releases"}
TYPES = {"agent", "scripted", "routing", "branch", "crew", "regular", "message_sequence", "orchestrator", "todo_task"}


def steps(plan):
    def visit(step):
        if not isinstance(step, dict):
            raise ValueError("step must be an object")
        yield step
        for route in step.get("predefined_routes") or []:
            child = route.get("sub_agent_step")
            if child is not None:
                yield from visit(child)
    for key in ("steps", "orphan_steps"):
        for step in plan.get(key) or []:
            yield from visit(step)


def validate(plan):
    records = list(steps(plan))
    ids = [s.get("id") for s in records]
    if any(not isinstance(i, str) or not i.strip() or i == "end" for i in ids) or len(ids) != len(set(ids)):
        raise ValueError("replacement needs unique nonempty step IDs")
    known = set(ids) | {"end"}
    for step in records:
        if step.get("type") not in TYPES:
            raise ValueError(f"unsupported replacement step type: {step.get('type')}")
        targets = [step.get("next_step_id")]
        targets += [r.get("next_step_id") for r in step.get("routes") or []]
        for target in targets:
            if target and target not in known:
                raise ValueError(f"dangling next-step reference in {step['id']}: {target}")
        for retired in ("question", "response_type", "variable_name", "if_yes_next_step_id", "if_no_next_step_id", "option_routes"):
            if retired in step:
                raise ValueError(f"retired human-input field in {step['id']}: {retired}")


def read(path, root, missing=False):
    relative = path.relative_to(root)
    current = root
    for part in relative.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError(f"refusing symlink: {current}")
    return None if missing and not path.exists() else path.read_bytes()


def prepare(root, recipes):
    changes = []
    for path in sorted(root.glob("**/planning/plan.json")):
        relative = path.relative_to(root)
        if any(part in HISTORY for part in relative.parts):
            continue
        before = read(path, root)
        original = json.loads(before)
        if not any(s.get("type") == "human_input" for s in steps(original)):
            continue
        recipe_path = recipes / relative
        if not recipe_path.is_file():
            raise ValueError(f"reviewed replacement missing: {relative}")
        recipe = json.loads(read(recipe_path, recipes))
        if recipe.get("source_sha256") != hashlib.sha256(before).hexdigest():
            raise ValueError(f"plan changed since review: {relative}")
        replacement = recipe["plan"]
        validate(replacement)
        changes.append((path, before, replacement))
        workspace = path.parent.parent
        if "step_config" in recipe:
            config_path = workspace / "planning/step_config.json"
            config_before = read(config_path, root, missing=True)
            digest = hashlib.sha256(config_before).hexdigest() if config_before is not None else None
            if recipe.get("step_config_sha256") != digest:
                raise ValueError(f"step configuration changed since review: {relative}")
            config = recipe["step_config"]
            if not isinstance(config, list) and not (isinstance(config, dict) and isinstance(config.get("steps"), list)):
                raise ValueError("step_config replacement must be an array or an object with steps")
            changes.append((config_path, config_before, recipe["step_config"]))
        if recipe.get("variables_add"):
            variable_path = workspace / "variables/variables.json"
            variable_before = read(variable_path, root, missing=True)
            manifest = json.loads(variable_before) if variable_before is not None else {"variables": []}
            definitions = manifest.setdefault("variables", [])
            known = {v["name"] for v in definitions}
            for addition in recipe["variables_add"]:
                if set(addition) - {"name", "description"} or not addition.get("name"):
                    raise ValueError("variable additions accept only name and description, never values or secrets")
                if addition["name"] not in known:
                    definitions.append({**addition, "value": ""})
                    known.add(addition["name"])
            if variable_before is None or manifest != json.loads(variable_before):
                changes.append((variable_path, variable_before, manifest))
    return changes


def migrate(root, recipes, apply=False):
    root = Path(root).resolve(strict=True)
    recipes = Path(recipes).resolve(strict=True)
    changes = prepare(root, recipes)  # Validate every plan before the first write.
    for path, before, _ in changes:
        current = read(path, root, missing=True)
        if current != before:
            raise ValueError(f"file changed during migration; drain the server: {path}")
    for path, before, value in changes:
        print(f"{'apply' if apply else 'dry-run'}: {path.relative_to(root)}")
        if not apply:
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        if before is not None:
            backup = path.with_name(path.name + ".pre-human-input-retirement.bak")
            if not backup.exists():
                with backup.open("xb") as out:
                    out.write(before)
                shutil.copystat(path, backup)
        fd, temporary = tempfile.mkstemp(prefix=".retire-human-input-", dir=path.parent)
        try:
            with os.fdopen(fd, "w") as out:
                json.dump(value, out, ensure_ascii=False, indent=2)
                out.write("\n")
                out.flush()
                os.fsync(out.fileno())
            if before is not None:
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
    parser.add_argument("root", help="workspace document root")
    parser.add_argument("--recipes", required=True, help="reviewed replacement recipe root")
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--server-drained", action="store_true", help="confirm all writers and executions are drained")
    args = parser.parse_args()
    if args.apply and not args.server_drained:
        parser.error("--apply requires --server-drained")
    print(f"{migrate(args.root, args.recipes, args.apply)} file(s) {'updated' if args.apply else 'would change'}")
