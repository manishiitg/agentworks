[← coding-agents / accounts](index.md)

# PLAT-615: Private provider setup linked user credentials to the shared server login

| Field | Value |
|---|---|
| State | fixed on main |
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

## Left

Deploy to Excellence, detach legacy links, preserve the verified owner's login
in that owner's account only, quarantine the contaminated service login, and
verify admission from both users. Other affected private accounts need their
owners to sign in again. A valid shared server login requires an admin sign-in.
