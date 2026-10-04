#!/usr/bin/env python3
"""Manifest of a prebuilt release (PLAT-426): create it after a build, verify it before every use.

    release_manifest.py create <dir> --name N --rev repo=sha ... [--build-seconds S] [--note k=v ...]
    release_manifest.py verify <dir> [--manifest-sha256 H] [--skip-prefix P ...] [--host-arch A] [--host-glibc G]
    release_manifest.py show <dir>

`verify` refuses (exit 1, one line on stderr naming the reason) when the build was made for another CPU architecture, needs a
newer glibc than this host has, lists a file that is missing or differs (size, sha256, executable bit, symlink target), or contains
a file the manifest does not list. `--skip-prefix` allows listed files under that prefix to be absent (a trimmed copy shipped to a
small host); files that are present are still checked. Nothing is ever repaired or deleted here.
"""
import argparse
import hashlib
import json
import os
import platform
import socket
import sys
import time
from pathlib import Path

MANIFEST = "manifest.json"
SCHEMA = 1


class Refused(Exception):
    pass


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def host_glibc():
    try:
        name, version = os.confstr("CS_GNU_LIBC_VERSION").split()
        if name == "glibc":
            return version
    except (AttributeError, ValueError, OSError):
        pass
    return ""


def version_tuple(text):
    try:
        return tuple(int(part) for part in text.split("."))
    except ValueError:
        raise Refused(f"unreadable glibc version {text!r}")


def walk(root):
    """Yield (relative path, kind) for every file and symlink below root, except the manifest itself."""
    for current, dirs, files in os.walk(root):
        dirs.sort()
        base = Path(current)
        for name in sorted(files) + [d for d in dirs if (base / d).is_symlink()]:
            path = base / name
            relative = path.relative_to(root).as_posix()
            if relative == MANIFEST:
                continue
            yield relative, ("link" if path.is_symlink() else "file")


def create(root, name, revisions, build_seconds=None, notes=None):
    root = Path(root)
    files, links = {}, {}
    for relative, kind in walk(root):
        path = root / relative
        if kind == "link":
            links[relative] = os.readlink(path)
        else:
            info = path.stat()
            files[relative] = {"sha256": sha256_file(path), "size": info.st_size, "exec": bool(info.st_mode & 0o111)}
    manifest = {
        "schema": SCHEMA,
        "name": name,
        "revisions": revisions,
        "os": platform.system().lower(),
        "arch": platform.machine(),
        "glibc": host_glibc(),
        "built_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "build_seconds": build_seconds,
        "built_on": socket.gethostname(),
        "notes": notes or {},
        "files": files,
        "symlinks": links,
    }
    (root / MANIFEST).write_text(json.dumps(manifest, indent=1, sort_keys=True) + "\n")
    return manifest


def load(root):
    path = Path(root) / MANIFEST
    if not path.is_file():
        raise Refused(f"{path} is missing: this is not a complete build")
    try:
        manifest = json.loads(path.read_text())
    except ValueError as error:
        raise Refused(f"{path} is not valid JSON: {error}")
    if manifest.get("schema") != SCHEMA:
        raise Refused(f"unsupported manifest schema {manifest.get('schema')!r}")
    return manifest


def verify(root, expected_manifest_sha256=None, skip_prefixes=(), arch=None, glibc=None):
    root = Path(root)
    if expected_manifest_sha256 and sha256_file(root / MANIFEST) != expected_manifest_sha256:
        raise Refused("manifest.json does not match the hash announced by the build host")
    manifest = load(root)
    arch = arch or platform.machine()
    glibc = glibc if glibc is not None else host_glibc()
    if manifest.get("os") != "linux":
        raise Refused(f"build is for {manifest.get('os')}, not linux")
    if manifest.get("arch") != arch:
        raise Refused(f"build is for {manifest.get('arch')}, this host is {arch}")
    build_glibc = manifest.get("glibc") or ""
    if not build_glibc:
        raise Refused("manifest does not record the glibc version")
    if not glibc:
        raise Refused("cannot read this host's glibc version")
    if version_tuple(glibc) < version_tuple(build_glibc):
        raise Refused(f"build needs glibc {build_glibc} or newer, this host has {glibc}")

    skipped = lambda relative: any(relative.startswith(prefix) for prefix in skip_prefixes)
    listed_files, listed_links = manifest["files"], manifest["symlinks"]
    for relative, entry in sorted(listed_files.items()):
        path = root / relative
        if not path.exists() and not path.is_symlink():
            if skipped(relative):
                continue
            raise Refused(f"missing file {relative}")
        if path.is_symlink() or not path.is_file():
            raise Refused(f"{relative} is not a regular file")
        info = path.stat()
        if info.st_size != entry["size"]:
            raise Refused(f"size mismatch for {relative}")
        if bool(info.st_mode & 0o111) != entry["exec"]:
            raise Refused(f"executable bit mismatch for {relative}")
        if sha256_file(path) != entry["sha256"]:
            raise Refused(f"hash mismatch for {relative}")
    for relative, target in sorted(listed_links.items()):
        path = root / relative
        if not path.is_symlink():
            if skipped(relative) and not path.exists():
                continue
            raise Refused(f"missing symlink {relative}")
        if os.readlink(path) != target:
            raise Refused(f"symlink {relative} points somewhere else")
    for relative, kind in walk(root):
        if relative not in listed_files and relative not in listed_links:
            raise Refused(f"unlisted file {relative}")
    return manifest


REPOS = ("mcp-agent-builder-go", "mcpagent", "multi-llm-provider-go")


def pinned_names(builds_dir):
    pins = Path(builds_dir) / ".pinned"
    return {m.name for m in pins.glob("*")} if pins.is_dir() else set()


def list_builds(builds_dir):
    """Complete builds under builds_dir, newest first: manifest.json present (a build in progress is a .partial folder)."""
    found = []
    for manifest in Path(builds_dir).glob("*/manifest.json"):
        try:
            data = json.loads(manifest.read_text())
        except ValueError:
            continue
        found.append((manifest.parent.name, data, manifest.stat().st_mtime))
    return sorted(found, key=lambda item: item[0], reverse=True)


def find_build(builds_dir, query):
    """The one build whose name starts with query, or whose builder/mcpagent/provider revision starts with it."""
    if len(query) < 6:
        raise Refused("give at least 6 characters of a build name or revision")
    matches = [
        (name, data) for name, data, _ in list_builds(builds_dir)
        if name.startswith(query) or any(sha.startswith(query) for sha in data.get("revisions", {}).values())
    ]
    exact = [m for m in matches if m[0] == query]
    matches = exact or matches
    if not matches:
        raise Refused(f"no build matches {query!r}")
    if len(matches) > 1:
        raise Refused(f"{query!r} matches {len(matches)} builds: " + ", ".join(m[0] for m in matches))
    name, data = matches[0]
    return name, [data["revisions"][repo] for repo in REPOS]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    c = sub.add_parser("create")
    c.add_argument("dir")
    c.add_argument("--name", required=True)
    c.add_argument("--rev", action="append", default=[], metavar="repo=sha")
    c.add_argument("--build-seconds", type=int)
    c.add_argument("--note", action="append", default=[], metavar="key=value")
    v = sub.add_parser("verify")
    v.add_argument("dir")
    v.add_argument("--manifest-sha256")
    v.add_argument("--skip-prefix", action="append", default=[])
    v.add_argument("--host-arch")
    v.add_argument("--host-glibc")
    s = sub.add_parser("show")
    s.add_argument("dir")
    l = sub.add_parser("list", help="list complete builds under a builds folder")
    l.add_argument("dir")
    f = sub.add_parser("find", help="print '<name> <sha> <sha> <sha>' for the one build matching a name or revision prefix")
    f.add_argument("dir")
    f.add_argument("query")
    args = parser.parse_args(argv)
    try:
        if args.command == "create":
            revisions = dict(item.split("=", 1) for item in args.rev)
            notes = dict(item.split("=", 1) for item in args.note)
            manifest = create(args.dir, args.name, revisions, args.build_seconds, notes)
            print(f"manifest: {len(manifest['files'])} files, {len(manifest['symlinks'])} symlinks")
        elif args.command == "verify":
            manifest = verify(args.dir, args.manifest_sha256, tuple(args.skip_prefix), args.host_arch, args.host_glibc)
            print(f"verified {manifest['name']}: {len(manifest['files'])} files, {manifest['arch']}, glibc {manifest['glibc']}")
        elif args.command == "list":
            now = time.time()
            print(f"{'BUILD':24} {'AGE':>8} {'SECS':>5}  " + "  ".join(f"{repo[:20]:20}" for repo in REPOS) + "  ARCH/GLIBC")
            pins = pinned_names(args.dir)
            for name, data, mtime in list_builds(args.dir):
                age = int(now - mtime)
                shown = f"{age // 86400}d" if age >= 86400 else f"{age // 3600}h" if age >= 3600 else f"{age // 60}m"
                shas = "  ".join(f"{data['revisions'].get(repo, '?')[:10]:20}" for repo in REPOS)
                print(f"{name:24} {shown:>8} {str(data.get('build_seconds') or '?'):>5}  {shas}  {data.get('arch')}/{data.get('glibc')}{'  pinned' if name in pins else ''}")
        elif args.command == "find":
            name, shas = find_build(args.dir, args.query)
            print(" ".join([name] + shas))
        else:
            manifest = load(args.dir)
            print(json.dumps({k: v for k, v in manifest.items() if k not in ("files", "symlinks")}, indent=1, sort_keys=True))
    except Refused as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
