# Your agent's email inbox

When an administrator enables company agent inboxes on this server, every Crew, Code project, Goal and Relay automatically gets an address. Open the agent's **Inbox** to copy it and read email conversations and attachments. Renaming an agent keeps the same address. People with access to the agent can see its inbox.

Mail appears here without starting work. To act on it, ask Builder, for example:

> Watch this Crew's inbox. When an invoice arrives with a PDF attachment, send it to chat and ask for the amount and due date.

> Watch this Goal's inbox. Send training enquiries to the enquiry route and cancellations to the cancellation route.

Builder creates a normal Python Watcher, discovers the actual chat or route and shows it under **Watchers**. You can ask it to change or remove the Watcher. Mail stays visible even when it doesn't match. Several Watchers can independently act on the same inbox.

Personal Gmail is separate: connect it in **My Vault** and choose its permissions, then choose **Use here** in a product. Agents use that account through gog. Connecting it creates no automatic email intake.

An administrator sets up receiving once under **My Vault → Company agent inboxes**, with the company Google app and one authorized company receiving mailbox. The OAuth JSON alone is not a mailbox login. This feature is available on servers; local deployments have personal Google connections but no agent inboxes.
