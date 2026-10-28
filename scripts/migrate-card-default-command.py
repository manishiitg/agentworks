#!/usr/bin/env python3
"""Add the default reply-card command "Check this change" to existing Crews, once.

Skips a Crew that has the marker (a deleted default stays deleted) or already
has a command of that name; writes the marker either way it adds. New Crews get
the same files at creation. Only adds files; keep until every server has run it.
"""
import argparse
import json
from pathlib import Path

NAME = 'check-this-change'
MARKER = '.card-default-seeded'
MESSAGE = ('Look over the files you changed in this reply. Point out anything that looks wrong or risky, '
           'anything you missed, and what I should test.')
COMMAND = ('---\nname: check-this-change\ndescription: Check this change\nicon: play\nmodes:\n  - multi-agent\n'
           'show_on_card: true\n---\n\n' + MESSAGE + '\n\n{{context}}\n')


def crews(root):
    for base in [root] + [p for p in sorted((root / '_users').glob('*')) if p.is_dir() and not p.is_symlink()]:
        folder = base / 'Crew'
        if folder.is_symlink() or not folder.is_dir():
            continue
        for crew in sorted(folder.glob('*')):
            if crew.is_dir() and not crew.is_symlink() and not crew.name.startswith('.'):
                yield crew


def migrate(root, check=False):
    root = Path(root).resolve()
    changed = skipped = 0
    for crew in crews(root):
        custom = crew / 'commands/custom'
        if (custom / MARKER).exists() or (custom / NAME).exists():
            continue
        if check:
            changed += 1
            continue
        # One unreadable Crew must not fail the deploy: skip it, report it, and add the command next time.
        try:
            (custom / NAME).mkdir(parents=True)
            (custom / NAME / 'COMMAND.md').write_text(COMMAND)
            (custom / MARKER).write_text('seeded\n')
            changed += 1
        except OSError:
            skipped += 1
    return {'event': 'card_default_command', 'changed_crews': changed, 'skipped_crews': skipped, 'check': check}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-dir', type=Path, required=True)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    print(json.dumps(migrate(args.workspace_dir, args.check)))
