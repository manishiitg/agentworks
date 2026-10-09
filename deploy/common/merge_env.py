#!/usr/bin/env python3
"""Merge a product's managed environment into the host .env, and drop keys it no longer manages.

The deploy writes each EXTRA_ENV entry (and PATH) into <app>/.env. It remembers the keys it wrote in a small file so
that a key deleted from EXTRA_ENV later is removed from .env too; before, a deleted line stayed on the host and kept
its old value (a temporary WHATSAPP_DEBUG=true stayed on after its line was removed, 2026-10-09, PLAT-789).
Only keys this tool wrote itself are ever removed: a line someone added by hand stays.

usage: merge_env.py SOURCE DESTINATION KEYS_FILE PATH_VALUE [KEY=value ...]
"""
import sys
from pathlib import Path


def parse_entries(runtime_path, entries):
    managed = {"PATH": runtime_path}
    for entry in entries:
        if not entry:
            continue
        key, separator, value = entry.partition("=")
        if not separator or not key:
            raise SystemExit("EXTRA_ENV entries must use KEY=value")
        managed[key] = value
    return managed


def merge(text, managed, previously_managed):
    """Return the new .env text. Keys in `previously_managed` but not in `managed` are dropped."""
    gone = set(previously_managed) - set(managed)
    output, written = [], set()
    for line in text.splitlines():
        key, separator, _ = line.partition("=")
        if separator and key in managed:
            if key not in written:
                output.append(f"{key}={managed[key]}")
                written.add(key)
            continue
        if separator and key in gone:
            continue
        output.append(line)
    for key, value in managed.items():
        if key not in written:
            output.append(f"{key}={value}")
    return "\n".join(output) + "\n"


def main(argv):
    source, destination, keys_file, runtime_path, *entries = argv
    managed = parse_entries(runtime_path, entries)
    keys_path = Path(keys_file)
    previous = set(keys_path.read_text().split()) if keys_path.exists() else set()
    Path(destination).write_text(merge(Path(source).read_text(), managed, previous))
    keys_path.write_text("\n".join(sorted(managed)) + "\n")
    keys_path.chmod(0o600)


if __name__ == "__main__":
    main(sys.argv[1:])
