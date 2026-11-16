"""Build-host fixture preparation for the explicitly opted-in Chrome E2E.
Installs only the locked test driver in this disposable checkout, never a
browser, extension or configuration into a person's profile or app.
"""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run(['npm', 'ci', '--prefix', str(root/'packages/playwright'), '--no-audit', '--no-fund'], check=True)

# Branded Chrome no longer accepts the unpacked-extension launch flags. Use
# the pinned Playwright Chromium fixture, not a person's installed browser.
subprocess.run(['node', str(root/'packages/playwright/node_modules/playwright/cli.js'), 'install', 'chromium'], check=True)
