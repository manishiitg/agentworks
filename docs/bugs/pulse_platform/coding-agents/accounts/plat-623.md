[← coding-agents / accounts](index.md)

# PLAT-623: Ankita private Codex API key remained in the shared server login

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | A private API key had been written through a legacy credential link into shared Codex auth; shared use is contained and repaired, owner key rotation remains. |

## What happened

Ankita Manna (`ankitamanna2003@gmail.com`) is a creator, not an admin. Her Codex
connection `Client API key` is private (`api_key`, user scope, no sharing grants).
A server-side equality check confirmed that `/srv/agents/home/.codex/auth.json`
contained the same key as that private encrypted connection. No key values or
fingerprints were printed or recorded.

Three isolated runtime homes and seven Code project runtime homes had links to
that shared file. One active Codex terminal belonged to another user's Code
workspace. It could reach the shared auth file despite the private connection
ID being correctly protected by account admission.

This is the Codex consequence of the setup credential-source bug in
[PLAT-615](plat-615.md). That repair removed the private Codex link but initially
missed the key already written into shared Codex auth. Deleting the link alone
did not remove that existing shared copy. The shared encrypted provider-key
registry was absent and no service env variable matched Ankita's key.

## Server containment and repair completed

- Restricted shared Codex to admins through the account API (204), before repair.
- Stopped the affected Code session through its session API (200), closing the
  cached Codex terminal without deleting its chat history.
- Reconstituted only Ankita's own Codex auth file from her encrypted private
  connection, as an independent `0600` file in her account's private HOME.
- Quarantined the contaminated shared file in private operator state and removed
  it plus all ten runtime links. Other users' homes received no credential copies.
- Kept shared Codex signed out and unavailable to creators. An intended server
  account needs an admin sign-in before it can be used again.

The generic private setup/admission fix is already on main (`9b345dc75`) and
live in Excellence release `agents-bd97744f-20261006143646`; this repair did not
change binaries or require another deployment. It sets both setup HOME and
credential source to the private account and rejects legacy private links.

## Live verification

- Ankita's private Codex status: 200, signed in using an API key.
- Utkarsh, Ashutosh and Vaibhav requesting her private Codex status: 404 each.
- Those users requesting shared Codex status: 404 each; account pickers mark it
  unusable and do not expose Ankita's private connection.
- Admin shared Codex status: 200, signed out.
- Auth-file audit across service HOME, isolated runtimes, project runtimes and
  slot state found no live copy of her key outside her authorized account/runtime.
- Her invoicing Code runtime points to her own account auth file, not shared auth.

These are actual server API, CLI login-status and filesystem checks. No paid
model request was issued for verification. The Linux confinement regression and
private-account admission tests for the underlying fix are recorded in PLAT-615.

## Wider exposure audit, October 6

The seven affected Code runtime homes map to six users: Utkarsh
(`ubarnwal0802@gmail.com`, two workspaces), Vishwas (`vishwascharan11@gmail.com`),
Abhishek (`abhisheks33537@gmail.com`), Manish (`manish@excellencetechnologies.in`),
Nitish (`nitish000000kushwaha@gmail.com`) and Vaibhav
(`vaibhavpatel122003@gmail.com`). Three additional isolated runtime homes were
also linked. These runtimes could reach Ankita's shared key; this does not prove
provider usage or exposure of those users' own private credentials.

All five current private provider connections were checked against live auth
files in the service HOME, state runtimes, project runtimes and slot state.
No current private key/token remained in another owner's live auth file, and
no private account credential-file symlinks remained. Private Codex/Claude
source files and rightful account runtime links remain available to Ankita.
Deleted accounts and older rotated logs limit historical attribution; this is
an audit of the current registry and available server evidence, not a claim
that no earlier account was ever exposed. Credential values were not recorded.

After the PLAT-625 Excellence release restart, Ankita's private Codex remained
signed in; Utkarsh, Ashutosh and Vaibhav still received 404 for both her private
account and the restricted shared account. The repeated live-file scan again
found zero outside copies of her key. Shared Codex remained signed out.

## Left

Ankita must revoke the exposed old key with its provider, generate a replacement,
and reconnect/update her private Codex connection. Server containment cannot
invalidate a key that was previously readable by other users' runtimes. Keep this
incident in progress until that rotation is confirmed. Shared Codex stays signed
out/admin-only until an admin supplies an intended shared login.
