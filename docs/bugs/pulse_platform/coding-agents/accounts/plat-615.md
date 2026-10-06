[← coding-agents / accounts](index.md)

# PLAT-615: Private provider setup linked user credentials to the shared server login

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | Private account setup omitted the credential source, causing the confinement launcher to link a personal login to the service login. |

## What happened

On Excellence, Vaibhav is a creator and his Claude connection `Void` is private.
Its `.claude/.credentials.json`, and another private Claude connection's file,
were symlinks to the service HOME's Claude credential file. Ashutosh's
`Crew/browser-test-769c45bc` selected the server Claude account (no private
connection ID), whose account metadata now named Vaibhav's Claude email.
The private authenticate action on October 6 preceded the shared credential
update. No credential contents are included here.

`confineProviderSetup` set `PrivateHome` but omitted `CredentialHome`.
`linkCredentialFiles` consequently used the service HOME, installed a link in
the private account HOME, and allowed private authentication/refresh to update
that shared file. Registry sharing checks correctly denied private IDs but could
not protect a personal login accidentally installed as the server account.

## Fix

- Bind private setup's credential source and path environment to its own HOME.
- Reject private runtime admission when a credential file is a symlink.
- Explicit private reauthentication detaches only the bad link; it does not
  copy, overwrite, or remove the other account's credential.
- Regression drives the setup wrapper through a real Linux Landlock shell,
  checking private reads/refreshes and an unchanged server login.
- Separate regression rejects an existing server link and verifies reconnect
  leaves its target untouched.

## Validation

Focused Mac account/admission and prompt checks pass. Three older interactive
account tests cannot run on this multi-user Mac without Linux Landlock; their
failures report the pre-existing refusal to launch an unconfined setup terminal.
Linux regression ran on Excellence through its real deployed Landlock launcher:
private setup read/refreshed its own dummy login, the server dummy login stayed
unchanged, and legacy-link rejection/reconnect passed. No real tokens were used
in the regression. Live repair remains below.

## Excellence repair and live checks

On October 6, shared Claude availability was restricted to admins through the
account API. The affected browser-test conversation was stopped through its
session API, closing the cached Claude terminal without deleting its history.

SSH repair confirmed the shared login identity matched Vaibhav before preserving
it as a new independent `0600` file in his private account HOME. The contaminated
shared credential was quarantined in private operator state and removed from
service HOME; shared and unrelated runtime identity caches were cleared. Private
Cursor/Codex links to service login files were detached without copying those
logins. The other affected Claude connection had already been removed by the
live registry; its runtime link/cache was removed too. No remaining private
credential-file symlinks were found.

Authenticated HTTP checks after repair:

- Vaibhav's private Claude status: 200, signed in as his Claude email.
- Ashutosh requesting Vaibhav's private Claude status: 404.
- Ashutosh requesting shared Claude status: 404; account picker reports unusable.
- Admin checking shared Claude: 200, signed out.

These are CLI login status/admission checks, not a paid model request. Encrypted
connection records and credential values were never included in logs or tickets.

The fix is deployed in Excellence release
`agents-bd97744f-20261006143646`. The same authenticated admission/status checks
passed again after restart, with no private credential-file symlinks. Deployment
health and the Linux slot self-test passed (156 checks; zero failures).

## Account setup follow-up

The affected private Cursor login needs its owner to sign in again. A later audit found Ankita's Codex key already in shared auth despite the
private link removal; its shared file and runtime links were subsequently
removed. That exposure and required owner key rotation are tracked in
[PLAT-623](plat-623.md). A valid shared Claude login requires an
admin sign-in; keep it unavailable to creators until then. Existing historical
provider/account metadata in chat replies is not rewritten.

## Wider account audit, October 6

The other affected private Claude account was Utkarsh's deleted `Utkarsh`
connection. His private Cursor `aaaa` account also had links to the shared Cursor
login; those links were detached and its current status is signed out. The
shared Cursor identity is Aayush's, so the link proves private/shared isolation
was broken, not that Utkarsh's own Cursor credential was exposed. He needs to
sign in to his own account again. Abhicodes' private Cursor account is signed
out with no remaining credential links; available evidence does not establish
that his credential was exposed. Shared Muse login was configured by the admin,
and no private Muse accounts remain in the registry.

Vaibhav's independent private Claude login passed an actual isolated CLI model
verification. A separate Code token-preflight error still blocked his Hosati
chat and is fixed in [PLAT-625](plat-625.md).
