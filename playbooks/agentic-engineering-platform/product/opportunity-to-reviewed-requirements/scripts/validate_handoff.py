#!/usr/bin/env python3
"""Validate this Playbook's typed artifacts and guarded outcome claims."""
import json
import sys
from pathlib import Path

PACKAGE = Path(__file__).resolve().parents[1]
from category_route_contract import validate as validate_contract


def validate(*artifacts):
    validate_contract(PACKAGE, list(artifacts))


if __name__ == '__main__':
    files = [Path(arg) for arg in sys.argv[1:]]
    try:
        validate(*(json.loads(path.read_text()) for path in files))
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        raise SystemExit(f'invalid handoff: {exc}') from exc
    print('valid route contract')
