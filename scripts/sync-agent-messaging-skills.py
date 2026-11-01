#!/usr/bin/env python3
"""Generate provider messaging skills and Claude message UI; --check detects drift."""

import argparse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "plugins/shared/skills/agent-messages/SKILL.md"
TARGETS = {
    "claude": "plugins/claude/skills/agent-messages/SKILL.md",
    "codex": "plugins/codex/skills/agent-messages/SKILL.md",
    "muse": "plugins/muse/skills/messaging/SKILL.md",
    "pi": "plugins/pi/skills/agent-messages/SKILL.md",
    "cursor": "plugins/cursor/skills/agent-messages/SKILL.md",
    "claude-tools": "plugins/claude-tools/skills/agent-messages/SKILL.md",
    "codex-tools": "plugins/codex-tools/skills/agent-messages/SKILL.md",
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("providers", nargs="*", help="Provider packages (default: all)")
    args = parser.parse_args()
    providers = args.providers or list(TARGETS)
    unknown = set(providers) - TARGETS.keys()
    if unknown:
        parser.error("unknown provider: " + ", ".join(sorted(unknown)))
    text = SOURCE.read_bytes()
    if not text.startswith(b"---\nname: agent-messages\n"):
        parser.error("shared source must retain its agent-messages frontmatter name")
    stale = []
    for provider in providers:
        target = ROOT / TARGETS[provider]
        # Preserve Muse's published skill name/id while sharing the whole body.
        expected = text.replace(b"name: agent-messages\n", b"name: agentworks-messaging\n", 1) if provider == "muse" else text
        if args.check:
            if not target.is_file() or target.read_bytes() != expected:
                stale.append(TARGETS[provider])
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(expected)
        if provider == "claude-tools":
            # The UI is identical in the full and command-hook-free packages.
            for file in ("hooks/messages.js", "tests/messages.test.ts"):
                expected_ui = (ROOT / "plugins/claude" / file).read_bytes()
                if file.endswith(".test.ts"):
                    expected_ui = expected_ui.replace(b"plugin: 'agentworks'", b"plugin: 'agentworks-tools'")
                target_ui = ROOT / "plugins/claude-tools" / file
                if args.check:
                    if not target_ui.is_file() or target_ui.read_bytes() != expected_ui:
                        stale.append(str(target_ui.relative_to(ROOT)))
                else:
                    target_ui.parent.mkdir(parents=True, exist_ok=True)
                    target_ui.write_bytes(expected_ui)
    if stale:
        parser.exit(1, "Messaging skills need regeneration:\n" + "\n".join(stale) + "\n")
    print(f"Messaging skills {'checked' if args.check else 'generated'}: {', '.join(providers)}")


if __name__ == "__main__":
    main()
