# Per-user Linux accounts

When several people share one AgentWorks server, each person gets their own
Linux account on that machine — a "slot". Your commands and coding assistants
run as *you*, so another person's work is simply out of reach.

## How one command runs

<div class="sec-flow">
<div class="sec-box"><b>1. Who's asking?</b><span>The service looks up your slot in the admin-owned table. No slot, no run — never a silent fallback.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>2. Switch user</b><span>One fixed launcher runs as your account. It can do nothing else.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>3. Check the request</b><span>Program on the allow-list? Folder inside the allowed roots? Your own terminal?</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box hi"><b>4. Run confined</b><span>As you, in exactly the allowed folders, with a minimal fixed environment.</span></div>
</div>

The launcher adds no privilege beyond your own account — there is nothing in
it for a compromised account to gain. The program list, folder roots, and
user-to-slot table are all owned by the administrator, so neither the service
nor any user can widen them.

## Who can reach what

<div class="sec-lanes">
<div class="sec-lane server"><h4>Service</h4>Verifies every caller.<br>Belongs to every group, so it can serve files and start launches for anyone.<br>Holds the credential store — no user account can read it.</div>
<div class="sec-lane"><h4>Alice's slot</h4><span class="sec-yes">✓</span> Her own files and runs<br><span class="sec-yes">✓</span> Folders explicitly shared with her<br><span class="sec-no">✗</span> Bob's files, terminals, launches<br><span class="sec-no">✗</span> Service credentials and config</div>
<div class="sec-lane"><h4>Bob's slot</h4><span class="sec-yes">✓</span> His own files and runs<br><span class="sec-yes">✓</span> Folders explicitly shared with him<br><span class="sec-no">✗</span> Alice's files, terminals, launches<br><span class="sec-no">✗</span> Service credentials and config</div>
</div>

Shared folders are granted to a slot at launch, mirroring exactly what the
launch allows — read or read/write, per path. Everything a slot creates stays
inside its group; nothing is world-readable.

## Terminals stay apart too

Each slot gets its **own terminal server** with its own socket, so one user
can never see or type into another's panes. A router sends each terminal
command to the server owning that session; anything else goes to the
platform's own server unchanged. Terminals start with a minimal fixed
environment — no service secrets — and pasted content travels only with the
session being pasted into.

## For administrators

- Provision once as root, then add people (account + slot in one step).
  Signing in never provisions anything.
- After initial provisioning adds service-account groups, verify the running
  agent and workspace processes' `Groups:` in `/proc/<pid>/status`. `id <account>`
  shows the account database, which can differ from running processes. With
  systemd user services, wait until active work finishes and refresh the user
  manager as root (`systemctl restart user@<service-uid>.service`), then verify
  services and run `./deploy.sh slotcheck <server>`. Restarting individual user
  services alone retains stale groups. Do not repair this by opening private
  folders, weakening the sandbox or running user commands as the service account.
  Incident: [PLAT-650](../bugs/pulse_platform/dominion/deployment/plat-650.md).
- With slots on, shell commands require a folder guard — unguarded commands
  are refused rather than run as the shared account.
- Freeing a slot releases the table entry; clear the slot's runtime files
  before someone else reuses it.

Switches: `AGENTWORKS_SLOTS=on` (shell commands),
`AGENTWORKS_SLOT_CLI=on` (coding assistants, optionally limited to listed
people while rolling out). Hosts without slots behave exactly as before.

Related: [Provider credentials](provider_credentials.md),
[Security overview](README.md).
