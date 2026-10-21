#!/bin/bash
# Fail on code nothing refers to: Go functions with no reference anywhere, and exported
# frontend names with no reference that are not already in the baseline.
# Fast (a few seconds) and needs nothing but python3. Details: scripts/check-unused.py.
# Usage: scripts/check-unused.sh
set -e
cd "$(dirname "$0")/.."
status=0
python3 scripts/check-unused.py go || status=1
python3 scripts/check-unused.py ts || status=1
exit $status
