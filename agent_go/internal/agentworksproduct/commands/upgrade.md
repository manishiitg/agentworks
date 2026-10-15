{{context}}

Bring this workflow up to date with the current platform.
- Call get_contract_upgrades. If nothing is owed, say "already up to date" and stop.
- If upgrades are listed, do them in the order given, one at a time. After each, say what changed in one line.
- Then run the plan drift check (/run-plan-drift) and tell me what it found and fixed.
- Do not change what the workflow does. If an upgrade would change behaviour, stop and ask me first.
