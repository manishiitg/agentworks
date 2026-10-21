#!/usr/bin/env python3
"""Find code nothing refers to, without compiling anything.

Usage:
  scripts/check-unused.py go                 fail if a Go function is never referenced
  scripts/check-unused.py ts                 fail if an exported frontend name is never referenced
                                             and is not already in scripts/unused-exports-baseline.txt
  scripts/check-unused.py ts --update-baseline   rewrite the baseline from the current tree

How: every identifier is counted once across the tree. A name that appears only at its own
declaration (and nowhere in tests, scripts, docs or config) is unused. This is deliberately
simple: it can miss dead code (a name that is also used for something else) but should not flag
live code. Dynamic uses such as reflection or a name built from strings are the exception, so
the Go check skips common interface-method names, and anything it flags is reviewed, not
trusted blindly.

Why it exists: retired backend fields and helpers stayed in the frontend and elsewhere for
months because eslint's no-unused-vars does not look at exports or interface fields.
"""
import collections
import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SKIP = ("node_modules", "/logs/", "/vendor/", "/dist/", "/.git/", "/build/")
TOKEN = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
IS_TEST = re.compile(r"(_test\.go|\.test\.tsx?|\.spec\.tsx?|__tests__)")
BASELINE = os.path.join(ROOT, "scripts", "unused-exports-baseline.txt")

GO_SKIP_NAMES = {
    "main", "init", "String", "Error", "Len", "Less", "Swap", "Write", "Read", "Close", "Unwrap",
    "MarshalJSON", "UnmarshalJSON", "ServeHTTP", "Is", "As", "Format", "Scan", "Value", "Get", "Set",
    "Run", "Name", "Start", "Stop", "Execute", "Handle", "Describe", "Reset", "Seek", "Flush",
}


def files_under(roots, exts):
    out = []
    for root in roots:
        base = os.path.join(ROOT, root)
        for dirpath, _, names in os.walk(base):
            if any(s in dirpath + "/" for s in SKIP):
                continue
            for name in names:
                if name.endswith(exts):
                    out.append(os.path.join(dirpath, name))
    return out


def read(path):
    with open(path, encoding="utf-8", errors="replace") as handle:
        return handle.read()


def count_words(files):
    counter = collections.Counter()
    for path in files:
        if os.path.abspath(path) == BASELINE:
            continue  # the baseline lists names; it must not count as a use of them
        counter.update(TOKEN.findall(read(path)))
    return counter


def other_text_words():
    exts = (".sh", ".py", ".yaml", ".yml", ".json", ".md", ".ts", ".tsx", ".js", ".mjs", ".tmpl", ".txt", ".html")
    return count_words(files_under(["agent_go", "workspace", "mcp-gateway", "scripts", "deploy", "frontend/src", "plugins", "docs"], exts))


def go_unused():
    roots = ["agent_go", "workspace", "mcp-gateway"]
    files = files_under(roots, (".go",))
    prod, test = collections.Counter(), collections.Counter()
    decl = re.compile(r"^func\s+(?:\([^)]*\)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*[\[(]", re.M)
    declared = []
    for path in files:
        text = read(path)
        words = collections.Counter(TOKEN.findall(text))
        (test if IS_TEST.search(path) else prod).update(words)
        if not IS_TEST.search(path):
            for match in decl.finditer(text):
                declared.append((os.path.relpath(path, ROOT), match.group(1)))
    other = other_text_words()
    result = []
    for path, name in declared:
        if name in GO_SKIP_NAMES or len(name) <= 4:
            continue
        if name.startswith(("Test", "Benchmark", "Example", "Fuzz")):
            continue
        if prod[name] == 1 and test[name] == 0 and other[name] == 0:
            result.append((path, name))
    return sorted(result)


def ts_unused():
    files = files_under(["frontend/src"], (".ts", ".tsx"))
    prod, test = collections.Counter(), collections.Counter()
    decl = re.compile(
        r"^export\s+(?:default\s+)?(?:async\s+)?(?:function\*?|const|let|class|interface|type|enum)\s+([A-Za-z_][A-Za-z0-9_]*)",
        re.M,
    )
    declared = []
    for path in files:
        text = read(path)
        words = collections.Counter(TOKEN.findall(text))
        (test if IS_TEST.search(path) else prod).update(words)
        if not IS_TEST.search(path) and "/generated/" not in path:
            for match in decl.finditer(text):
                declared.append((os.path.relpath(path, ROOT), match.group(1)))
    other = other_text_words()
    result = []
    for path, name in declared:
        if len(name) <= 3:
            continue
        # other[name] counts every file, including the declaring one, so a name used only at
        # its declaration appears exactly once in the combined text.
        if prod[name] == 1 and test[name] == 0 and other[name] <= 1:
            result.append((path, name))
    return sorted(result)


def load_baseline():
    if not os.path.exists(BASELINE):
        return set()
    return {line.strip() for line in read(BASELINE).splitlines() if line.strip() and not line.startswith("#")}


def main(argv):
    if len(argv) < 2 or argv[1] not in ("go", "ts"):
        print(__doc__)
        return 2
    if argv[1] == "go":
        found = go_unused()
        if not found:
            print("No unreferenced Go functions.")
            return 0
        print(f"{len(found)} Go function(s) are never referenced (not even in tests). Delete them or use them:")
        for path, name in found:
            print(f"  {path}: {name}")
        return 1
    found = ["%s:%s" % item for item in ts_unused()]
    if "--update-baseline" in argv:
        with open(BASELINE, "w", encoding="utf-8") as handle:
            handle.write("# Exported frontend names nothing refers to. Shrink this list; never add to it.\n")
            handle.write("# Regenerate: scripts/check-unused.py ts --update-baseline\n")
            for line in found:
                handle.write(line + "\n")
        print(f"Wrote {len(found)} entries to {os.path.relpath(BASELINE, ROOT)}")
        return 0
    baseline = load_baseline()
    new = [line for line in found if line not in baseline]
    gone = sorted(baseline - set(found))
    if gone:
        print(f"{len(gone)} baseline entr{'y is' if len(gone) == 1 else 'ies are'} fixed: remove from the baseline with --update-baseline.")
    if new:
        print(f"{len(new)} new exported name(s) are never referenced. Delete them or use them:")
        for line in new:
            print(f"  {line}")
        return 1
    print(f"No new unused frontend exports ({len(baseline)} older ones are in the baseline).")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
