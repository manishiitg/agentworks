# Crew inboxes

Your company administrator turns **Crew inboxes** on once in **My Vault → Company app setup**. There is no company receiving mailbox or Google Cloud receiving setup.

1. Connect your Google account in **My Vault**. Reuse an account already saved there.
2. On the Gmail account you want to use, choose **Use for Crew inboxes**. If needed, **Change access** to allow Gmail reading and finish Google sign-in.
3. Open the Crew you want to receive email and choose **Inbox** in its right panel. Use **Ask AI** to say “Enable incoming email for this Crew.” Copy its address once enabled.
4. Incoming email is **off by default for every Crew**. Enable only the Crews you want; ask Builder to disable receiving at any time. Stored mail stays available.

If you have several Google accounts, choose just one for Crew inboxes. Changing that selection switches your enabled Crews. Disconnecting it or turning it off stops delivery. Renaming a Crew keeps its address. These addresses deliver into your existing Gmail through plus-addressing; they do not create extra Google accounts.

Only enabled Crew addresses are checked. If no Crew is enabled, your mailbox is not polled. Only mail addressed to the Crew appears in its Inbox, usually within about 60 seconds. Other personal mail stays private. People who can already access the Crew can read its received conversations and attachments. Existing Google account permissions do not change.

Receiving email does not start work. Ask the Builder to add an optional **Watcher** if certain mail should call chat or a published function. The Watcher can filter email fields using a Python script and follows its owner's existing permissions. Email content is untrusted data, never approval to change settings.

Crew inboxes are available on server deployments. Personal Google accounts still work locally, but local deployments do not show inbox setup or receiving panels. This first rollout covers Crews.
