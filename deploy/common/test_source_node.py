#!/usr/bin/env python3
"""#2908: old Node must fail before npm installs or dashboard preparation."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
subprocess.run(["node", "-e", r'''
const assert = require('node:assert/strict');
const { supportedSourceNode } = require(process.argv[1]);
for (const v of ['20.20.2', '22.0.0', '22.17.0', '23.0.0']) assert.equal(supportedSourceNode(v), false, v);
for (const v of ['22.18.0', '22.23.4', '24.0.0', '26.0.0']) assert.equal(supportedSourceNode(v), true, v);
console.log('Source Node prerequisites regression passed.');
''', str(root / 'scripts/check-source-node.cjs')], check=True)
subprocess.run(['bash', '-n', str(root / 'agent_go/run_server_with_logging.sh')], check=True)
