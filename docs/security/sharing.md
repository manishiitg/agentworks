# Sharing and slots

Workflows and Crews can be shared with other people; Code is always private
to its owner. Sharing is decided in one place and enforced in two layers:
the server authorizes *who may*, and the person's isolated account decides
*what their processes can touch*.

## Who may do what

<div class="sec-flow">
<div class="sec-box"><b>Owner</b><span>Full control: run, edit, share, delete.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>Editor</b><span>Run and edit. Can't share or delete.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>Reader</b><span>Run and view. Can't change anything.</span></div>
</div>

Each workflow carries its own list (owners, editors, readers); admins keep
owner-level access everywhere. Every access is re-checked when it happens —
including scheduled runs that fire while you are away. Removing someone stops
all their new access immediately.

## What shared access can touch

When Bob runs Alice's shared workflow, his agent steps work and his shell
steps don't — by design, in this order:

<div class="sec-lanes">
<div class="sec-lane server"><h4>Server decides</h4>Bob is a reader of Invoices.<br>Launch policy lists exactly the shared folders, read or read/write.</div>
<div class="sec-lane"><h4>Agent steps ✓</h4>Bob's account is granted just those folders for that launch.<br>Nothing else on the host opens up.</div>
<div class="sec-lane"><h4>Shell steps ✗</h4>No grant step exists yet: Bob's account meets someone else's folders and is refused.<br>Fails closed, loudly.</div>
</div>

Shell access to shared folders is the known gap: it fails with "permission
denied" rather than leaking, and extending the grant step there is planned
work. Agent runs — the common case — are unaffected.

## Code stays out of this

A Code workspace has no readers, editors, or shares: only its owner opens,
runs, or edits it. One Code can call explicitly declared functions on another
Code its *owner* also owns, and audited read-only inspection exists
separately for admins and reviewers. There is no cross-user Code folder to
mount and no Code credential to share.

Related: [Per-user Linux accounts](per_user_linux_accounts.md),
[Provider credentials](provider_credentials.md),
[Security overview](README.md).
