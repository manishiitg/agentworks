"""Build-host fixture preparation for the explicitly opted-in Chrome E2E.
Installs only the locked test driver in this disposable checkout, never a
browser, extension or configuration into a person's profile or app.
"""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run(['npm', 'ci', '--prefix', str(root/'packages/playwright'), '--no-audit', '--no-fund'], check=True)
