#!/usr/bin/env python3
"""Install reviewed Cursor project hooks; leave existing hooks intact."""
import argparse
import json
import os
from pathlib import Path
import shlex
import stat
import sys
import tempfile


def read_config(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    with os.fdopen(fd, "rb") as source:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise ValueError("The existing hooks.json is not a regular file")
        raw = source.read((1 << 20) + 1)
        if len(raw) > 1 << 20:
            raise ValueError("The existing hook configuration is too large")
        return raw


def configure(project, package):
    project = Path(project).resolve(strict=True)
    if not project.is_dir():
        raise ValueError("Choose an existing project directory")
    folder = project / ".cursor"
    if folder.is_symlink():
        raise ValueError("The project's .cursor directory is a symlink")
    folder.mkdir(exist_ok=True)
    target = folder / "hooks.json"
    if target.is_symlink():
        raise ValueError("The project's hooks.json is a symlink")
    prior = read_config(target)
    data = json.loads(prior) if prior else {"version": 1, "hooks": {}}
    if not isinstance(data, dict) or data.get("version", 1) != 1:
        raise ValueError("Unsupported hook configuration")
    hooks = data.setdefault("hooks", {})
    if not isinstance(hooks, dict):
        raise ValueError("The hooks field must be an object")
    template = json.loads((package / "hooks" / "hooks.json").read_text())
    command = "agentworks plugin hook cursor"
    repaired = []
    def legacy_managed(value):
        try:
            words = shlex.split(value)
            if len(words) != 2 or Path(words[0]).name not in ("python", "python3"):
                return False
            script = Path(words[1])
            if not script.is_absolute() or script.name != "cursor_hook.py" or script.parent.name != "scripts":
                return False
            manifest = json.loads(read_config(script.parent.parent / ".cursor-plugin/plugin.json"))
            return isinstance(manifest, dict) and manifest.get("name") == "agentworks"
        except (OSError, ValueError, TypeError):
            return False
    for event, entries in template["hooks"].items():
        existing = hooks.setdefault(event, [])
        if not isinstance(existing, list) or any(not isinstance(item, dict) for item in existing):
            raise ValueError("Each hook event must contain a list of hook objects")
        if any(not isinstance(item.get("command", ""), str) for item in existing):
            raise ValueError("Hook commands must be strings")
        managed = [item for item in existing if item.get("command") == command or "cursor_hook.py" in item.get("command", "")]
        if len(managed) > 1:
            raise ValueError("Duplicate AgentWorks hooks are registered; review them before retrying")
        if managed and managed[0].get("command") != command:
            if not legacy_managed(managed[0]["command"]):
                raise ValueError("An unknown Cursor hook path is registered; review it before retrying")
            managed[0]["command"] = command
            repaired.append(event)
        if not managed:
            existing.append({**entries[0], "command": command})
    data["version"] = 1
    encoded = (json.dumps(data, indent=2) + "\n").encode()
    if encoded == prior:
        return target, False
    fd, name = tempfile.mkstemp(prefix=".agentworks-hooks-", dir=folder)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as out:
            out.write(encoded)
            out.flush()
            os.fsync(out.fileno())
        # Do not overwrite a person's edit made while this command prepared it.
        if target.is_symlink() or read_config(target) != prior:
            raise ValueError("The hook configuration changed; review it and retry")
        os.replace(name, target)
    finally:
        if os.path.exists(name):
            os.unlink(name)
    if repaired:
        print("Replaced old AgentWorks package paths with the CLI on PATH for: " + ", ".join(repaired))
    return target, True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True, help="Exact project whose hooks you reviewed")
    args = parser.parse_args()
    try:
        target, changed = configure(args.project, Path(__file__).resolve().parents[1])
        print(f"AgentWorks project hooks {'installed' if changed else 'already configured'}: {target}")
        print("Review this configuration in Cursor. No workspace/MCP trust, login or attachment was approved.")
        return 0
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as error:
        print(f"AgentWorks project setup refused: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
